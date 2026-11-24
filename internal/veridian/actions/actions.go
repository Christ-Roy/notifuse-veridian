// Package actions reçoit les actions qu'une autre app Veridian demande au nom d'un utilisateur (nomad-veridian plan/INTEGRATIONS-NATIVES.md,
// §2.2 et 2.3). POST /api/veridian/actions/{action}, jeton d'action du Hub (ES256, 120 s, claim act). « Envoyer vers Mail » :
// contacts.importer, listes.lister, listes.creer. Aucune n'envoie de mail. Les droits restent ceux de l'utilisateur : l'action est rejouée
// sur le routeur de l'app avec une session courte, comme le MCP. Fiche 87.
package actions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/mcp"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const (
	maxBody      = 2 << 20
	maxTokenLife = 120 * time.Second // §2.2
	clockLeeway  = 10 * time.Second
	maxContacts  = 1000
)

// Refus de résolution de l'espace.
var (
	ErrUnknownUser    = errors.New("utilisateur_inconnu")
	ErrUnlinkedTenant = errors.New("espace_non_relie")
	ErrNotMember      = errors.New("non_membre")
	ErrSuspended      = errors.New("espace_suspendu")
	ErrInProgress     = errors.New("en_cours")
	ErrKeyReused      = errors.New("cle_reutilisee")
)

// Origin : qui a demandé, pour le journal.
type Origin struct {
	App       string
	JTI       string
	HubUserID string
}

// Store : identité, jetons vus, gestes faits (base système).
type Store interface {
	// ConsumeJTI rend false si ce jeton a déjà servi.
	ConsumeJTI(ctx context.Context, jti string) (bool, error)
	Resolve(ctx context.Context, hubUserID, hubTenantID string) (*mcp.Member, error)
	// Begin réserve la clé. Résultat non nil : geste déjà fait. ErrInProgress, ErrKeyReused.
	Begin(ctx context.Context, ws, key, action, hash string, o Origin) (json.RawMessage, error)
	Finish(ctx context.Context, ws, app, key string, result json.RawMessage) error
	// Release rend la clé : le geste n'a rien fait, il peut être retenté.
	Release(ctx context.Context, ws, app, key string) error
}

// request : une requête rejouée sur le routeur de l'app.
type request func(method, path string, query map[string][]string, body []byte) (int, []byte, error)

// call : ce qu'une action fait. readOnly : simulation ou lecture, sans clé d'idempotence.
type call struct {
	readOnly bool
	run      func(do request) (int, []byte, error)
}

type action struct {
	scopes []string
	apps   []string
	build  func(ws, app string, raw []byte) (*call, error)
}

// Portée du registre du Hub (lib/delegation/paires.ts), et le nom qu'il a porté le 24/11 (les deux désignent la même chose, seul le Hub
// les délivre).
var scopeContacts = []string{"notifuse:contacts.ecrire", "notifuse:contacts.importer"}

var mailApps = []string{"app:prospection", "app:crm"}

// Catalogue fermé : une action = une portée, des apps autorisées, des requêtes fixes.
var catalogue = map[string]action{
	"contacts.importer": {scopes: scopeContacts, apps: mailApps, build: buildImport},
	"listes.lister":     {scopes: scopeContacts, apps: mailApps, build: buildLists},
	"listes.creer":      {scopes: scopeContacts, apps: mailApps, build: buildCreateList},
}

// Handler : POST /api/veridian/actions/{action}.
type Handler struct {
	Verifier   *mcp.Verifier
	Store      Store
	Dispatcher mcp.Dispatcher
	Log        logger.Logger
	Now        func() time.Time
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func refuse(w http.ResponseWriter, status int, code, msg string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="notifuse-actions"`)
	}
	writeJSON(w, status, map[string]string{"error": code, "reason": msg})
}

// checkToken : ce que le jeton d'action exige en plus de la signature, de l'émetteur, de l'audience et de l'échéance.
func (h *Handler) checkToken(c *mcp.Claims) error {
	switch {
	case c.Act == nil || !strings.HasPrefix(c.Act.Subject, "app:") || len(c.Act.Subject) > 64:
		return errors.New("act absent ou illisible")
	case c.Act.Act != nil:
		return errors.New("délégation en chaîne refusée")
	case c.TenantID == "":
		return errors.New("tenant_id absent")
	case len(c.ID) < 8 || len(c.ID) > 128:
		return errors.New("jti absent ou illisible")
	case c.IssuedAt == nil || c.ExpiresAt.Sub(c.IssuedAt.Time) > maxTokenLife || c.ExpiresAt.After(h.now().Add(maxTokenLife+clockLeeway)):
		return errors.New("durée de vie supérieure à 120 s")
	}
	return nil
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func hasAny(list, wanted []string) bool {
	for _, w := range wanted {
		if has(list, w) {
			return true
		}
	}
	return false
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		refuse(w, http.StatusUnauthorized, "jeton_refuse", "jeton d'action du Hub requis")
		return
	}
	claims, err := h.Verifier.Verify(ctx, token)
	if err != nil {
		refuse(w, http.StatusUnauthorized, "jeton_refuse", "jeton invalide, expiré ou destiné à une autre adresse")
		return
	}
	if err := h.checkToken(claims); err != nil {
		refuse(w, http.StatusUnauthorized, "jeton_refuse", err.Error())
		return
	}
	// Un jeton sert UNE fois, même pour une demande refusée ensuite.
	fresh, err := h.Store.ConsumeJTI(ctx, claims.ID)
	if err != nil {
		h.fail(w, err, "jeton vu")
		return
	}
	if !fresh {
		refuse(w, http.StatusUnauthorized, "jeton_deja_vu", "ce jeton a déjà servi : en demander un neuf au Hub")
		return
	}
	name := r.PathValue("action")
	act, known := catalogue[name]
	if !known {
		refuse(w, http.StatusNotFound, "action_inconnue", "action inconnue")
		return
	}
	app := claims.Act.Subject
	if !has(act.apps, app) {
		refuse(w, http.StatusForbidden, "app_non_autorisee", app+" ne peut pas demander "+name)
		return
	}
	if !hasAny(strings.Fields(claims.Scope), act.scopes) {
		refuse(w, http.StatusForbidden, "portee_insuffisante", "portée requise : "+act.scopes[0])
		return
	}
	member, err := h.Store.Resolve(ctx, claims.Subject, claims.TenantID)
	switch {
	case errors.Is(err, ErrUnlinkedTenant):
		refuse(w, http.StatusConflict, ErrUnlinkedTenant.Error(), "aucun espace Notifuse n'est relié à cette organisation")
		return
	case errors.Is(err, ErrUnknownUser), errors.Is(err, ErrNotMember), errors.Is(err, ErrSuspended):
		refuse(w, http.StatusForbidden, err.Error(), "l'utilisateur ne peut pas agir dans cet espace")
		return
	case err != nil:
		h.fail(w, err, "résolution de l'espace")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		refuse(w, http.StatusRequestEntityTooLarge, "corps_trop_grand", "2 Mo au plus")
		return
	}
	ws := member.Workspace.WorkspaceID
	c, err := act.build(ws, app, raw)
	if err != nil {
		refuse(w, http.StatusBadRequest, "demande_invalide", err.Error())
		return
	}
	origin := Origin{App: app, JTI: claims.ID, HubUserID: claims.Subject}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !c.readOnly {
		if len(key) < 8 || len(key) > 128 {
			refuse(w, http.StatusBadRequest, "cle_requise", "Idempotency-Key requise (8 à 128 caractères), stable pour le même geste")
			return
		}
		sum := sha256.Sum256(append([]byte(name+"\x00"), raw...))
		prev, err := h.Store.Begin(ctx, ws, key, name, hex.EncodeToString(sum[:]), origin)
		switch {
		case errors.Is(err, ErrInProgress):
			refuse(w, http.StatusConflict, "en_cours", "ce geste est déjà en cours")
			return
		case errors.Is(err, ErrKeyReused):
			refuse(w, http.StatusUnprocessableEntity, "cle_reutilisee", "cette clé a servi pour une autre demande")
			return
		case err != nil:
			h.fail(w, err, "clé d'idempotence")
			return
		case prev != nil:
			h.log(origin, name, ws, "rejoue")
			writeJSON(w, http.StatusOK, map[string]any{"action": name, "rejoue": true, "resultat": prev})
			return
		}
	}
	status, out, err := c.run(func(method, path string, query map[string][]string, body []byte) (int, []byte, error) {
		return h.Dispatcher.Do(ctx, member, method, path, query, body)
	})
	if err == nil && status == http.StatusOK && json.Valid(out) && len(bytes.TrimSpace(out)) > 0 {
		if !c.readOnly {
			if ferr := h.Store.Finish(context.WithoutCancel(ctx), ws, app, key, out); ferr != nil {
				// Le geste est fait mais son résultat n'est pas gardé : l'import dédoublonne, un rejeu ne crée rien.
				h.Log.WithField("error", ferr.Error()).Error("actions: résultat non gardé")
			}
		}
		h.log(origin, name, ws, "fait")
		writeJSON(w, http.StatusOK, map[string]any{"action": name, "rejoue": false, "simulation": c.readOnly, "resultat": json.RawMessage(out)})
		return
	}
	if !c.readOnly {
		_ = h.Store.Release(context.WithoutCancel(ctx), ws, app, key)
	}
	if err != nil || status >= 500 || status < 400 {
		if err == nil {
			err = fmt.Errorf("statut %d", status)
		}
		h.fail(w, err, name)
		return
	}
	h.log(origin, name, ws, fmt.Sprintf("refus %d", status))
	writeJSON(w, status, map[string]any{"error": "refus_de_l_app", "reason": reasonOf(out), "detail": rawOrNil(out)})
}

func reasonOf(out []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(out, &e) == nil {
		if s, ok := e.Error.(string); ok && s != "" {
			return s
		}
	}
	return "refusé"
}

func rawOrNil(out []byte) any {
	if json.Valid(out) {
		return json.RawMessage(out)
	}
	return nil
}

func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	h.Log.WithField("error", err.Error()).Error("actions: " + what)
	refuse(w, http.StatusServiceUnavailable, "echec", "échec interne, réessayer")
}

func (h *Handler) log(o Origin, action, ws, issue string) {
	h.Log.WithFields(map[string]interface{}{"app": o.App, "jti": o.JTI, "hub_user_id": o.HubUserID, "action": action,
		"workspace_id": ws, "issue": issue}).Info("actions: demande d'une app")
}

var listID = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)

type importRequest struct {
	ListIDs  []string                     `json:"list_ids"`
	Contacts []map[string]json.RawMessage `json:"contacts"`
	DryRun   bool                         `json:"dry_run"`
}

// buildImport : « Envoyer vers Mail ». Jamais de séquence démarrée (allow_triggers n'existe pas ici), fiche existante intacte, chaque
// contact étiqueté de l'app d'où il vient.
func buildImport(ws, app string, raw []byte) (*call, error) {
	var req importRequest
	if err := strict(raw, &req); err != nil {
		return nil, err
	}
	if len(req.Contacts) == 0 || len(req.Contacts) > maxContacts {
		return nil, fmt.Errorf("1 à %d contacts par appel", maxContacts)
	}
	if len(req.ListIDs) == 0 {
		return nil, errors.New("list_ids requis : le lot entre dans une liste, sans aucun envoi")
	}
	source, _ := json.Marshal(strings.TrimPrefix(app, "app:"))
	for _, c := range req.Contacts {
		if c == nil {
			return nil, errors.New("contact vide")
		}
		c["veridian_source"] = source
	}
	body, err := json.Marshal(map[string]any{"workspace_id": ws, "contacts": req.Contacts, "list_ids": req.ListIDs,
		"existing": "attach", "dry_run": req.DryRun, "allow_triggers": false})
	if err != nil {
		return nil, err
	}
	return &call{readOnly: req.DryRun, run: func(do request) (int, []byte, error) {
		return do(http.MethodPost, "/api/veridian/contacts.intake", nil, body)
	}}, nil
}

// strict : un seul objet, aucun champ inconnu.
func strict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("corps illisible : %v", err)
	}
	if dec.More() {
		return errors.New("corps illisible : plusieurs objets")
	}
	return nil
}

// buildLists : les listes où verser un lot, et celles qui démarrent une séquence active (à griser). Corps {} ou vide.
func buildLists(ws, _ string, raw []byte) (*call, error) {
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := strict(raw, &struct{}{}); err != nil {
			return nil, err
		}
	}
	return &call{readOnly: true, run: func(do request) (int, []byte, error) {
		return do(http.MethodGet, "/api/veridian/reservoir.lists", map[string][]string{"workspace_id": {ws}}, nil)
	}}, nil
}

// buildCreateList : une liste privée, sans double opt-in (donc sans mail de confirmation). Rejouable : la même liste (id et nom) déjà
// là est rendue ; le même id sous un autre nom est refusé.
func buildCreateList(ws, _ string, raw []byte) (*call, error) {
	var req struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := strict(raw, &req); err != nil {
		return nil, err
	}
	req.Name = strings.TrimSpace(req.Name)
	if !listID.MatchString(req.ID) || req.Name == "" || len(req.Name) > 255 {
		return nil, errors.New("id : 1 à 32 lettres ou chiffres ; name : 1 à 255 caractères")
	}
	body, err := json.Marshal(map[string]any{"workspace_id": ws, "id": req.ID, "name": req.Name, "is_public": false, "is_double_optin": false})
	if err != nil {
		return nil, err
	}
	answer := func(existed bool) []byte {
		b, _ := json.Marshal(map[string]any{"id": req.ID, "name": req.Name, "existait": existed})
		return b
	}
	return &call{run: func(do request) (int, []byte, error) {
		status, out, err := do(http.MethodGet, "/api/lists.get", map[string][]string{"workspace_id": {ws}, "id": {req.ID}}, nil)
		if err != nil {
			return 0, nil, err
		}
		if status == http.StatusOK {
			var got struct {
				List struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"list"`
			}
			if json.Unmarshal(out, &got) != nil || got.List.ID != req.ID {
				return 0, nil, errors.New("lists.get : réponse illisible")
			}
			if got.List.Name != req.Name {
				return http.StatusConflict, []byte(`{"error":"cet identifiant de liste est déjà pris par une autre liste"}`), nil
			}
			return http.StatusOK, answer(true), nil
		}
		if status != http.StatusNotFound {
			return status, out, nil
		}
		status, out, err = do(http.MethodPost, "/api/lists.create", nil, body)
		if err != nil || status >= 300 {
			return status, out, err
		}
		return http.StatusOK, answer(false), nil
	}}, nil
}
