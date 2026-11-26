package iafils

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// §5.24.1 : avant d'afficher « Mon IA », l'app demande l'état au Hub (POST {HUB}/api/ia/etat), signé comme /api/organisations/lire.
// Ce n'est PAS la v2 que SignVeridianHubRequest pose (méthode, chemin, query, empreinte du corps) : c'est le schéma à nonce du Hub
// (veridian-hub/lib/delegation/jeton.ts, verifierAppel) : HMAC-SHA256 hex du secret de l'app sur `ts.nonce.corps`, ts en ms (13 chiffres),
// nonce de 16 à 128 caractères [A-Za-z0-9_-], usage unique côté Hub. Les deux sont prouvés par test sur un vecteur tiré de ce code.

// Etat : ce que dit le Hub. EtatIndisponible : le Hub ne répond pas, 5xx, illisible, 404 : l'app se tait.
const (
	EtatPrete        = "prete"
	EtatSansMoteur   = "sans_moteur"
	EtatEteinte      = "eteinte"
	EtatIndisponible = "indisponible"
)

// App : le nom de cette app dans l'en-tête X-Veridian-App.
const App = "notifuse"

const cacheTTL = 60 * time.Second // §5.24.1 : 60 s au plus par personne et par organisation

// SignEtat rend la signature du schéma à nonce : HMAC-SHA256 hex de `ts.nonce.corps`.
func SignEtat(secret, ts, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + nonce + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ApplyEtatHeaders pose les quatre en-têtes que le Hub exige.
func ApplyEtatHeaders(r *http.Request, secret, ts, nonce string, body []byte) {
	r.Header.Set("X-Veridian-App", App)
	r.Header.Set("X-Veridian-Timestamp", ts)
	r.Header.Set("X-Veridian-Nonce", nonce)
	r.Header.Set("X-Veridian-Hub-Signature", SignEtat(secret, ts, nonce, body))
}

func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

type cached struct {
	etat   string
	expire time.Time
}

// EtatClient interroge le Hub et garde la réponse 60 s.
type EtatClient struct {
	BaseURL string // racine du Hub, sans « / » final
	Secret  string
	HTTP    *http.Client
	Now     func() time.Time

	mu   sync.Mutex
	seen map[string]cached
}

func (c *EtatClient) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Etat rend prete, sans_moteur, eteinte ou indisponible. Un 404 (la personne n'est pas de l'organisation) est une réponse du Hub, gardée
// comme les autres ; 401, 429, 5xx, réseau et réponse illisible ne sont jamais gardés.
func (c *EtatClient) Etat(ctx context.Context, hubUserID, hubTenantID string) string {
	if c.Secret == "" || c.BaseURL == "" {
		return EtatIndisponible
	}
	key := hubUserID + ":" + hubTenantID
	now := c.now()
	c.mu.Lock()
	if v, ok := c.seen[key]; ok && v.expire.After(now) {
		c.mu.Unlock()
		return v.etat
	}
	c.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"hub_user_id": hubUserID, "tenant_id": hubTenantID})
	nonce, err := newNonce()
	if err != nil {
		return EtatIndisponible
	}
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/api/ia/etat", bytes.NewReader(body))
	if err != nil {
		return EtatIndisponible
	}
	req.Header.Set("Content-Type", "application/json")
	ApplyEtatHeaders(req, c.Secret, strconv.FormatInt(now.UnixMilli(), 10), nonce, body)
	cl := c.HTTP
	if cl == nil {
		cl = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return EtatIndisponible
	}
	defer func() { _ = resp.Body.Close() }()
	etat := EtatIndisponible
	switch {
	case resp.StatusCode == http.StatusNotFound:
		c.keep(key, EtatIndisponible, now)
		return EtatIndisponible
	case resp.StatusCode != http.StatusOK:
		return EtatIndisponible
	}
	var out struct {
		Etat string `json:"etat"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out) != nil {
		return EtatIndisponible
	}
	switch out.Etat {
	case EtatPrete, EtatSansMoteur, EtatEteinte:
		etat = out.Etat
	default:
		return EtatIndisponible
	}
	c.keep(key, etat, now)
	return etat
}

func (c *EtatClient) keep(key, etat string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil || len(c.seen) > 5000 {
		c.seen = map[string]cached{}
	}
	c.seen[key] = cached{etat: etat, expire: now.Add(cacheTTL)}
}

// EtatStore : ce que la route de la console demande à la base.
type EtatStore interface {
	HubUserOf(ctx context.Context, userID string) (string, error)
	HubTenantOf(ctx context.Context, workspaceID string) (string, error)
}

// EtatAuth : la session de l'utilisateur et son appartenance à l'espace.
type EtatAuth interface {
	AuthenticateUserForWorkspace(ctx context.Context, workspaceID string) (context.Context, *domain.User, *domain.UserWorkspace, error)
}

// EtatHandler sert GET /api/veridian/ia/etat?workspace_id= à la console (session de l'utilisateur).
type EtatHandler struct {
	Store  EtatStore
	Auth   EtatAuth
	Client interface {
		Etat(ctx context.Context, hubUserID, hubTenantID string) string
	}
	HubURL string // racine du Hub servie à la console, pour le cadre et le bouton
	Log    logger.Logger
}

// Get rend { etat, hub_url, tenant_id }. tenant_id est l'organisation du Hub liée à l'espace, jamais l'identifiant d'espace. Tout ce qui
// ne permet pas d'afficher (espace non relié, compte non relié, Hub muet) rend « indisponible » SANS hub_url ni tenant_id : la console se tait.
func (h *EtatHandler) Get(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace_id")
	if ws == "" {
		refuse(w, http.StatusBadRequest, "invalid_payload")
		return
	}
	_, user, _, err := h.Auth.AuthenticateUserForWorkspace(r.Context(), ws)
	if err != nil || user == nil {
		refuse(w, http.StatusForbidden, "forbidden")
		return
	}
	rien := func() { writeJSON(w, http.StatusOK, map[string]any{"etat": EtatIndisponible}) }
	hubUser, err := h.Store.HubUserOf(r.Context(), user.ID)
	if err != nil || hubUser == "" {
		rien()
		return
	}
	tenant, err := h.Store.HubTenantOf(r.Context(), ws)
	if err != nil || tenant == "" {
		rien()
		return
	}
	etat := h.Client.Etat(r.Context(), strings.ToLower(hubUser), strings.ToLower(tenant))
	if etat != EtatPrete && etat != EtatSansMoteur {
		writeJSON(w, http.StatusOK, map[string]any{"etat": etat})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"etat": etat, "hub_url": strings.TrimRight(h.HubURL, "/"), "tenant_id": strings.ToLower(tenant)})
}
