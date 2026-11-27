package iafils

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// Bornes du contrat (§5.24) : 1 Mo de JSON (octets) par fil, 200 messages au plus. Le Hub ne coupe pas, l'app refuse.
const (
	MaxBodyBytes = 1_000_000
	MaxMessages  = 200
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// FilStore : ce que le handler demande à la base.
type FilStore interface {
	Resolve(ctx context.Context, hubTenantID, hubUserID string) (string, error)
	Owner(ctx context.Context, session string) (hubUserID, hubTenantID string, found bool, err error)
	Get(ctx context.Context, session, hubUserID, hubTenantID string) (json.RawMessage, bool, error)
	Put(ctx context.Context, session, workspaceID, hubUserID, hubTenantID string, messages json.RawMessage, count int) (bool, error)
}

// Handler sert GET et PUT /api/hub/ia/fils/{session}.
type Handler struct {
	Store FilStore
	Log   logger.Logger
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func refuse(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"code": code, "error": code})
}

// RefuseBrowser : ces routes ne sont appelées que par le Hub, serveur à serveur. Un navigateur met Origin (hors GET de même origine),
// Sec-Fetch-Site et Sec-Fetch-Dest : leur présence suffit à refuser, même signé. Jamais Sec-Fetch-Mode seul : le fetch de Node (undici),
// dont se sert le Hub, l'ajoute de lui-même (« cors ») ; le 27/11 à 00:59:44Z, le cas 56 du Hub a pris 403 browser_origin_refused sur
// son PUT de fil à cause de lui. La signature v2 à usage unique reste la vraie garde. Pose dehors de la signature.
func RefuseBrowser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" || r.Header.Get("Sec-Fetch-Dest") != "" {
			refuse(w, http.StatusForbidden, "browser_origin_refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	if h.Log != nil {
		h.Log.WithField("error", err.Error()).Error("iafils: " + what)
	}
	refuse(w, http.StatusInternalServerError, "internal_error")
}

// Get : 200 { messages } si le fil existe pour CE triplet, 404 sinon (fil absent, d'une autre personne, d'une autre organisation,
// personne retirée de l'espace, organisation supprimée : une seule réponse, on ne dit pas qu'il existe).
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	q := r.URL.Query()
	user, tenant := strings.ToLower(strings.TrimSpace(q.Get("hub_user_id"))), strings.ToLower(strings.TrimSpace(q.Get("tenant_id")))
	if !uuidRE.MatchString(session) || !uuidRE.MatchString(user) || !uuidRE.MatchString(tenant) {
		refuse(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	session = strings.ToLower(session)
	if _, err := h.Store.Resolve(r.Context(), tenant, user); err != nil {
		if errors.Is(err, ErrUnlinked) || errors.Is(err, ErrNotMember) {
			refuse(w, http.StatusNotFound, "fil_inconnu")
			return
		}
		h.fail(w, err, "résolution de l'espace")
		return
	}
	raw, ok, err := h.Store.Get(r.Context(), session, user, tenant)
	if err != nil {
		h.fail(w, err, "lecture du fil")
		return
	}
	if !ok {
		refuse(w, http.StatusNotFound, "fil_inconnu")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": raw})
}

type putBody struct {
	HubUserID string            `json:"hub_user_id"`
	TenantID  string            `json:"tenant_id"`
	Messages  []json.RawMessage `json:"messages"`
}

// Put : remplace le fil entier, le crée s'il manque. 200 {}. 409 fil_autre_auteur si la session est rangée pour un autre triplet
// (rien n'est écrit). 413 au-delà de 1 Mo ou de 200 messages. 403 member_removed : la personne n'est plus de l'espace.
func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	session := strings.ToLower(r.PathValue("session"))
	if !uuidRE.MatchString(session) {
		refuse(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		refuse(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	if len(raw) > MaxBodyBytes {
		refuse(w, http.StatusRequestEntityTooLarge, "fil_trop_grand")
		return
	}
	var in putBody
	if err := json.Unmarshal(raw, &in); err != nil || in.Messages == nil {
		refuse(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	user, tenant := strings.ToLower(strings.TrimSpace(in.HubUserID)), strings.ToLower(strings.TrimSpace(in.TenantID))
	if !uuidRE.MatchString(user) || !uuidRE.MatchString(tenant) {
		refuse(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	if len(in.Messages) > MaxMessages {
		refuse(w, http.StatusRequestEntityTooLarge, "fil_trop_long")
		return
	}
	for _, m := range in.Messages { // un UIMessage est un objet : stocké tel quel, jamais interprété
		if t := strings.TrimSpace(string(m)); len(t) == 0 || t[0] != '{' {
			refuse(w, http.StatusBadRequest, "invalid_payload")
			return
		}
	}
	// L'auteur d'abord : une session d'un autre triplet est refusée (409) quoi qu'il en soit de la place de l'appelant.
	if u, t, found, err := h.Store.Owner(r.Context(), session); err != nil {
		h.fail(w, err, "lecture de l'auteur")
		return
	} else if found && (u != user || t != tenant) {
		refuse(w, http.StatusConflict, "fil_autre_auteur")
		return
	}
	ws, err := h.Store.Resolve(r.Context(), tenant, user)
	switch {
	case errors.Is(err, ErrUnlinked):
		refuse(w, http.StatusNotFound, "espace_non_relie")
		return
	case errors.Is(err, ErrNotMember):
		refuse(w, http.StatusForbidden, "member_removed")
		return
	case err != nil:
		h.fail(w, err, "résolution de l'espace")
		return
	}
	msgs, err := json.Marshal(in.Messages)
	if err != nil {
		refuse(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	ok, err := h.Store.Put(r.Context(), session, ws, user, tenant, msgs, len(in.Messages))
	if err != nil {
		h.fail(w, err, "écriture du fil")
		return
	}
	if !ok { // course : un autre triplet a pris la session entre-temps
		refuse(w, http.StatusConflict, "fil_autre_auteur")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}
