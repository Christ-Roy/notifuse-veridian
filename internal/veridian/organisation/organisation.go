// Package organisation reçoit l'organisation du Hub (VER-252, Robert le 24/11 : « une seule configuration sur le Hub »). Fiche 88.
//
//	POST /api/hub/organisations/sync          HMAC Hub, version monotone : nom, logo, couleurs, membres additifs
//	GET  /api/hub/organisations/sync          HMAC Hub : la version tenue (relecture)
//	GET  /api/veridian/organisation           membre de l'espace : la marque, pour la console
//	POST /api/hub/evenements/abonnement       HMAC Hub : allume ou éteint l'abonnement message.received du Hub (F3, VER-247)
//
// Aucun réglage d'envoi (profils, plafonds, débits, fenêtres) ne passe par ici.
package organisation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const (
	maxBody    = 256 << 10
	maxMembers = 200
)

// ErrUnlinked : aucun espace vivant n'est relié à ce tenant du Hub.
var ErrUnlinked = errors.New("espace_non_relie")

// Brand : la marque poussée par le Hub. Une valeur vide ne change rien à l'écran.
type Brand struct {
	DisplayName string `json:"nom_affiche,omitempty"`
	LogoURL     string `json:"logo_url,omitempty"`
	LogoDarkURL string `json:"logo_sombre_url,omitempty"`
	FaviconURL  string `json:"favicon_url,omitempty"`
	Primary     string `json:"couleur_primaire,omitempty"`
	Accent      string `json:"couleur_accent,omitempty"`
}

// Member : un membre à ajouter. Additif : jamais un retrait, jamais un changement de rôle.
type Member struct {
	HubUserID string `json:"hub_user_id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
}

// Organisation : le corps du Hub. Les champs inconnus sont ignorés (le contrat peut grandir).
type Organisation struct {
	HubTenantID string          `json:"hub_tenant_id"`
	Version     int64           `json:"version"`
	Name        string          `json:"nom"`
	Type        string          `json:"type"`
	Brand       Brand           `json:"marque"`
	Modules     json.RawMessage `json:"modules,omitempty"`
	Members     []Member        `json:"membres,omitempty"`
	Settings    json.RawMessage `json:"reglages,omitempty"`
}

// Applied : ce que la poussée a fait.
type Applied struct {
	Done        bool   `json:"applied"`
	Reason      string `json:"reason,omitempty"` // unchanged (même version), stale (plus ancienne) : les mots du Hub
	WorkspaceID string `json:"workspace_id"`
	Version     int64  `json:"version"` // celle que l'app tient maintenant
	Members     int    `json:"membres_ajoutes"`
	MembersKO   int    `json:"membres_refuses"`
}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func safeURL(v string) bool {
	if v == "" {
		return true
	}
	u, err := url.Parse(v)
	return err == nil && len(v) <= 2048 && u.Scheme == "https" && u.Host != "" && u.User == nil
}

// Validate refuse un corps dangereux ou illisible : ces valeurs finissent dans des pages.
func (o *Organisation) Validate() error {
	o.HubTenantID, o.Name, o.Brand.DisplayName = strings.TrimSpace(o.HubTenantID), strings.TrimSpace(o.Name), strings.TrimSpace(o.Brand.DisplayName)
	switch {
	case o.HubTenantID == "" || len(o.HubTenantID) > 64:
		return errors.New("hub_tenant_id requis")
	case o.Version <= 0:
		return errors.New("version requise, strictement positive")
	case len(o.Name) > 255 || len(o.Brand.DisplayName) > 255:
		return errors.New("nom trop long (255 au plus)")
	case len(o.Members) > maxMembers:
		return fmt.Errorf("%d membres au plus par poussée", maxMembers)
	}
	for _, v := range []string{o.Brand.LogoURL, o.Brand.LogoDarkURL, o.Brand.FaviconURL} {
		if !safeURL(v) {
			return errors.New("une adresse de marque doit être en https")
		}
	}
	for _, v := range []string{o.Brand.Primary, o.Brand.Accent} {
		if v != "" && !colorRe.MatchString(v) {
			return errors.New("une couleur s'écrit #rrggbb")
		}
	}
	return nil
}

// hubAutoName : nom que le Hub fabrique pour un compte (« robert.brunon's Workspace »). Personne ne l'a choisi.
const hubAutoName = "'s Workspace"

// displayName : le nom montré dans l'app, seulement s'il a été CHOISI (nom_affiche). `nom` est le nom du compte au Hub : cocher Mail ne doit pas
// remplacer « Green Beam Craft » par « arnaud's Workspace ».
func (o *Organisation) displayName() string {
	if strings.HasSuffix(strings.ReplaceAll(o.Brand.DisplayName, "\u2019", "'"), hubAutoName) {
		return ""
	}
	return o.Brand.DisplayName
}

// Store : espaces et organisations (base système).
type Store interface {
	// Apply range l'organisation si sa version est plus récente, et recopie nom et logo dans l'espace. Rend l'espace, la version tenue, et si
	// cette poussée a été appliquée. ErrUnlinked si aucun espace n'est relié.
	Apply(ctx context.Context, o *Organisation) (workspaceID string, version int64, done bool, err error)
	Brand(ctx context.Context, workspaceID string) (*Organisation, error)
	WorkspaceOf(ctx context.Context, hubTenantID string) (string, error)
	IsMember(ctx context.Context, workspaceID, email string) (bool, error)
}

// SQLStore : Store sur la base système.
type SQLStore struct{ DB *sql.DB }

func (s *SQLStore) WorkspaceOf(ctx context.Context, hubTenantID string) (string, error) {
	var ws string
	err := s.DB.QueryRowContext(ctx, `SELECT p.workspace_id FROM veridian_plan p JOIN workspaces w ON w.id = p.workspace_id
		WHERE p.hub_tenant_id = $1 AND p.deleted_at IS NULL ORDER BY p.workspace_id LIMIT 1`, hubTenantID).Scan(&ws)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnlinked
	}
	return ws, err
}

func (s *SQLStore) Apply(ctx context.Context, o *Organisation) (string, int64, bool, error) {
	ws, err := s.WorkspaceOf(ctx, o.HubTenantID)
	if err != nil {
		return "", 0, false, err
	}
	stored := *o
	stored.Members = nil // les membres vivent dans l'espace, pas ici
	body, err := json.Marshal(stored)
	if err != nil {
		return "", 0, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `INSERT INTO veridian_organisation (workspace_id, hub_tenant_id, version, corps) VALUES ($1, $2, $3, $4)
		ON CONFLICT (workspace_id) DO UPDATE SET hub_tenant_id = EXCLUDED.hub_tenant_id, version = EXCLUDED.version, corps = EXCLUDED.corps, updated_at = NOW()
		WHERE veridian_organisation.version < EXCLUDED.version OR veridian_organisation.hub_tenant_id <> EXCLUDED.hub_tenant_id`,
		ws, o.HubTenantID, o.Version, body)
	if err != nil {
		return "", 0, false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return "", 0, false, err
		}
		var v int64
		err = tx.QueryRowContext(ctx, `SELECT version FROM veridian_organisation WHERE workspace_id = $1`, ws).Scan(&v)
		return ws, v, false, err
	}
	// Une valeur absente ne vide jamais ce que le propriétaire a réglé. Écriture ciblée : les autres réglages de l'espace ne sont pas relus.
	if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET
			name = CASE WHEN $2 <> '' THEN $2 ELSE name END,
			settings = CASE WHEN $3 <> '' THEN jsonb_set(settings, '{logo_url}', to_jsonb($3::text)) ELSE settings END,
			updated_at = NOW()
		WHERE id = $1`, ws, o.displayName(), o.Brand.LogoURL); err != nil {
		return "", 0, false, err
	}
	return ws, o.Version, true, tx.Commit()
}

func (s *SQLStore) Brand(ctx context.Context, workspaceID string) (*Organisation, error) {
	var body []byte
	err := s.DB.QueryRowContext(ctx, `SELECT corps FROM veridian_organisation WHERE workspace_id = $1`, workspaceID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o := &Organisation{}
	return o, json.Unmarshal(body, o)
}

func (s *SQLStore) IsMember(ctx context.Context, workspaceID, email string) (bool, error) {
	var ok bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM user_workspaces uw JOIN users u ON u.id = uw.user_id
		WHERE uw.workspace_id = $1 AND lower(u.email) = lower($2))`, workspaceID, email).Scan(&ok)
	return ok, err
}

// Auth : l'appartenance à l'espace (service d'authentification de l'app).
type Auth interface {
	AuthenticateUserForWorkspace(ctx context.Context, workspaceID string) (context.Context, *domain.User, *domain.UserWorkspace, error)
}

// Handler : les routes du module.
type Handler struct {
	Store Store
	Auth  Auth
	Subs  domain.WebhookSubscriptionRepository
	// AddMember ajoute un membre par la route signée existante (sync-member).
	AddMember func(ctx context.Context, workspaceID string, m Member) error
	HubURL    string
	Log       logger.Logger
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func refuse(w http.ResponseWriter, status int, code, reason string) {
	writeJSON(w, status, map[string]string{"error": code, "reason": reason})
}

func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, ErrUnlinked) {
		refuse(w, http.StatusNotFound, ErrUnlinked.Error(), "aucun espace Notifuse n'est relié à cette organisation")
		return
	}
	h.Log.WithField("error", err.Error()).Error("organisation: " + what)
	refuse(w, http.StatusInternalServerError, "internal_error", "échec interne, réessayer")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		refuse(w, http.StatusRequestEntityTooLarge, "corps_trop_grand", "256 Ko au plus")
		return false
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(v); err != nil {
		refuse(w, http.StatusBadRequest, "invalid_payload", "JSON illisible")
		return false
	}
	return true
}

// Sync : POST /api/hub/organisations/sync. Rejouable : la même version rend le même état, une version plus ancienne est ignorée. Les membres
// manquants sont ajoutés à chaque poussée, même déjà vue (un ajout refusé la fois d'avant se rattrape).
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	var o Organisation
	if !decode(w, r, &o) {
		return
	}
	if err := o.Validate(); err != nil {
		refuse(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}
	ws, version, done, err := h.Store.Apply(r.Context(), &o)
	if err != nil {
		h.fail(w, err, "rangement")
		return
	}
	res := Applied{Done: done, WorkspaceID: ws, Version: version}
	if !done && o.Version < version {
		res.Reason = "stale"
		writeJSON(w, http.StatusOK, res)
		return
	}
	if !done {
		res.Reason = "unchanged"
	}
	for _, m := range o.Members {
		in, err := h.Store.IsMember(r.Context(), ws, m.Email)
		if err == nil && in {
			continue
		}
		if err == nil {
			err = h.AddMember(r.Context(), ws, m)
		}
		if err != nil {
			res.MembersKO++
			h.Log.WithFields(map[string]interface{}{"workspace_id": ws, "error": err.Error()}).Warn("organisation: membre non ajouté")
			continue
		}
		res.Members++
	}
	h.Log.WithFields(map[string]interface{}{"workspace_id": ws, "version": version, "applique": done, "membres": res.Members}).Info("organisation: poussée du Hub")
	writeJSON(w, http.StatusOK, res)
}

// Version : GET /api/hub/organisations/sync?hub_tenant_id=. La version tenue, que le Hub relit avant de dire « appliqué ». 0 = rien reçu.
func (h *Handler) Version(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("hub_tenant_id")
	if tenant == "" || len(tenant) > 64 {
		refuse(w, http.StatusBadRequest, "invalid_payload", "hub_tenant_id requis")
		return
	}
	ws, err := h.Store.WorkspaceOf(r.Context(), tenant)
	if err != nil {
		h.fail(w, err, "relecture")
		return
	}
	o, err := h.Store.Brand(r.Context(), ws)
	if err != nil {
		h.fail(w, err, "relecture")
		return
	}
	out := map[string]any{"workspace_id": ws, "version": int64(0)}
	if o != nil && o.HubTenantID == tenant { // une version venue d'un autre tenant ne compte pas
		out["version"], out["name"] = o.Version, o.displayName()
	}
	writeJSON(w, http.StatusOK, out)
}

// Get : GET /api/veridian/organisation?workspace_id=. La marque, pour un membre de l'espace. Vide si le Hub n'a rien poussé.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace_id")
	if ws == "" {
		refuse(w, http.StatusBadRequest, "invalid_payload", "workspace_id requis")
		return
	}
	if _, _, _, err := h.Auth.AuthenticateUserForWorkspace(r.Context(), ws); err != nil {
		refuse(w, http.StatusForbidden, "forbidden", "vous n'êtes pas membre de cet espace")
		return
	}
	o, err := h.Store.Brand(r.Context(), ws)
	if err != nil {
		h.fail(w, err, "lecture")
		return
	}
	// Tenu par le Hub seulement s'il a donné un nom : sinon verrouiller nom et logo dans les réglages les rendrait inchangeables partout.
	out := map[string]any{"managed": o != nil && o.displayName() != ""}
	if o != nil {
		out["name"], out["type"], out["brand"] = o.displayName(), o.Type, o.Brand
	}
	writeJSON(w, http.StatusOK, out)
}
