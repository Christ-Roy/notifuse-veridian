package platformadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
)

const (
	AddRoute  = "POST /api/veridian/admin/platform-admin"
	ListRoute = "GET /api/veridian/admin/platform-admin"
)

// GuardedRoutes : routes amont par lesquelles un propriétaire retire ou restreint quelqu'un.
var GuardedRoutes = []string{"/api/workspaces.removeMember", "/api/workspaces.setUserPermissions"}

func init() {
	platform.Register(platform.Capability{
		Name: "platformadmin",
		Wire: func(h platform.Host) error {
			if h.DB() == nil {
				h.Logger().Warn("platformadmin: capacité inactive, application assemblée sans base")
				return nil
			}
			if h.Mux() == nil {
				return fmt.Errorf("platformadmin: mux absent")
			}
			s := &Store{DB: h.DB()}
			service.VeridianPersonGone, service.VeridianPlatformAdmins = s.Gone, s.Admins
			Mount(h.Mux(), s, h.Config().HubAPISecret, h.Logger())
			return Guard(h.Mux(), s, h.RequireAuth(), h.Logger())
		},
	})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func refuse(w http.ResponseWriter, status int, code, msg string) {
	reply(w, status, map[string]string{"error": msg, "code": code})
}

// Mount pose les routes d'opérateur sous HMAC Hub (503 sans secret).
func Mount(mux *http.ServeMux, s *Store, hubSecret string, log logger.Logger) {
	hmac := middleware.VeridianHMACMiddleware(hubSecret)
	mux.Handle(AddRoute, hmac(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			HubUserID   string `json:"hub_user_id"`
			GrantedBy   string `json:"granted_by"`
			Reason      string `json:"reason"`
			DryRun      bool   `json:"dry_run"`
		}
		dec := json.NewDecoder(io.LimitReader(r.Body, 8<<10))
		dec.DisallowUnknownFields() // une adresse à la place du hub_user_id est une faute, pas un champ ignoré
		if err := dec.Decode(&in); err != nil {
			refuse(w, http.StatusBadRequest, "invalid_payload", "workspace_id, hub_user_id, granted_by, reason, dry_run: "+err.Error())
			return
		}
		g := Grant{WorkspaceID: strings.TrimSpace(in.WorkspaceID), HubUserID: strings.TrimSpace(in.HubUserID), By: strings.TrimSpace(in.GrantedBy), Reason: strings.TrimSpace(in.Reason), DryRun: in.DryRun}
		if _, err := uuid.Parse(g.HubUserID); err != nil || g.WorkspaceID == "" {
			refuse(w, http.StatusBadRequest, "invalid_payload", "workspace_id and hub_user_id (uuid) are required")
			return
		}
		if n, m := utf8.RuneCountInString(g.By), utf8.RuneCountInString(g.Reason); n < 2 || n > 120 || m < 5 || m > 300 {
			refuse(w, http.StatusBadRequest, "invalid_payload", "granted_by (2 to 120) and reason (5 to 300) are required: they go to the journal")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		res, err := s.Add(ctx, g)
		f := log.WithFields(map[string]interface{}{"workspace_id": g.WorkspaceID, "hub_user_id": g.HubUserID, "granted_by": g.By, "reason": g.Reason, "dry_run": g.DryRun})
		switch {
		case errors.Is(err, ErrWorkspaceUnknown):
			refuse(w, http.StatusNotFound, "tenant_not_found", err.Error())
		case errors.Is(err, ErrPersonUnknown):
			refuse(w, http.StatusNotFound, "person_unknown", err.Error())
		case errors.Is(err, ErrRemovedByOwner):
			refuse(w, http.StatusConflict, "removed_by_owner", err.Error())
		case errors.Is(err, ErrNoClientOwner):
			refuse(w, http.StatusConflict, "no_client_owner", err.Error())
		case errors.Is(err, ErrOthersChanged):
			refuse(w, http.StatusConflict, "others_changed", ErrOthersChanged.Error())
		case err != nil:
			f.WithField("error", err.Error()).Error("platformadmin: échec")
			refuse(w, http.StatusInternalServerError, "internal_error", "nothing was changed")
		default:
			f.WithField("state", res.State).WithField("previous_role", res.PreviousRole).Info("platformadmin: geste")
			reply(w, http.StatusOK, res)
			return
		}
		if err != nil {
			f.WithField("refus", err.Error()).Warn("platformadmin: refusé")
		}
	})))
	mux.Handle(ListRoute, hmac(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if ws == "" {
			refuse(w, http.StatusBadRequest, "invalid_payload", "workspace_id is required")
			return
		}
		m, err := s.Members(r.Context(), ws)
		if err != nil {
			refuse(w, http.StatusInternalServerError, "internal_error", "not read")
			return
		}
		reply(w, http.StatusOK, map[string]any{"workspace_id": ws, "members": m})
	})))
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) {
	w.status = c
	w.ResponseWriter.WriteHeader(c)
}

// Guard : un administrateur de plateforme ne retire ni ne restreint un propriétaire du client ; l'inverse passe, et laisse une trace.
// Le fichier amont n'est pas touché : « POST /chemin » est plus précis que « /chemin ».
func Guard(mux *http.ServeMux, s *Store, auth func(http.Handler) http.Handler, log logger.Logger) error {
	for _, path := range GuardedRoutes {
		probe, err := http.NewRequest(http.MethodPost, path, nil)
		if err != nil {
			return err
		}
		upstream, pattern := mux.Handler(probe)
		if pattern != path {
			return fmt.Errorf("platformadmin: route amont %s introuvable (motif %q)", path, pattern)
		}
		removes := strings.HasSuffix(path, "removeMember")
		mux.Handle("POST "+path, auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
			if err != nil {
				refuse(w, http.StatusBadRequest, "invalid_payload", "unreadable body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			var in struct {
				WorkspaceID string `json:"workspace_id"`
				UserID      string `json:"user_id"`
			}
			_ = json.Unmarshal(body, &in) // illisible : l'amont le dit
			me, _ := r.Context().Value(domain.UserIDKey).(string)
			target, err := s.Standing(r.Context(), in.WorkspaceID, in.UserID)
			var mine Standing
			if err == nil {
				mine, err = s.Standing(r.Context(), in.WorkspaceID, me)
			}
			if err != nil {
				log.WithField("error", err.Error()).Error("platformadmin: situation non lue, geste refusé")
				refuse(w, http.StatusServiceUnavailable, "not_read", "try again")
				return
			}
			if mine.PlatformAdmin && target.Owner && !target.PlatformAdmin {
				refuse(w, http.StatusForbidden, "client_owner_protected", "a platform administrator cannot remove or restrict an owner of the workspace")
				return
			}
			rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			upstream.ServeHTTP(rec, r)
			if removes && target.PlatformAdmin && rec.status == http.StatusOK {
				if err := s.MarkRemoved(context.WithoutCancel(r.Context()), in.WorkspaceID, in.UserID, me); err != nil {
					// Sans conséquence sur le droit : l'appartenance n'existe plus, et Add refuse de la remettre.
					log.WithField("workspace_id", in.WorkspaceID).WithField("error", err.Error()).Error("platformadmin: retrait fait, trace non écrite")
				}
			}
		})))
	}
	return nil
}
