package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// ErrBusy : un passage est déjà en cours pour ce workspace.
var ErrBusy = errors.New("un passage du réconciliateur est déjà en cours pour ce workspace")

// ErrBadRequest : demande invalide (rendue en 400).
type ErrBadRequest struct{ Msg string }

func (e *ErrBadRequest) Error() string { return e.Msg }

// Connector donne la base d'un workspace (domain.WorkspaceRepository la fournit).
type Connector interface {
	GetConnection(ctx context.Context, workspaceID string) (*sql.DB, error)
}

// confirmPause : délai entre deux lectures de confirmation des compteurs en production.
const confirmPause = 1500 * time.Millisecond

// Service : droits, exécution des contrôles, résolution des écarts.
type Service struct {
	Auth  domain.AuthService
	Conn  Connector
	Stats StatsSource
	Log   logger.Logger
	Now   func() time.Time
	// Pause entre deux lectures de confirmation des compteurs.
	Pause time.Duration

	mu      sync.Mutex
	running map[string]bool
}

// ResolveRequest : action sur un écart.
type ResolveRequest struct {
	WorkspaceID string `json:"workspace_id"`
	FindingID   string `json:"finding_id"`
	Action      string `json:"action"` // apply | dismiss | dry_run
	Note        string `json:"note,omitempty"`
}

// ResolveResult : ce qui s'est passé.
type ResolveResult struct {
	FindingID string         `json:"finding_id"`
	Action    string         `json:"action"`
	Applied   bool           `json:"applied"`
	Result    map[string]any `json:"result"`
	// Verified : le contrôle a été rejoué après l'action et l'écart a disparu.
	Verified  bool     `json:"verified"`
	Remaining *Finding `json:"remaining"`
	Message   string   `json:"message"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) authorize(ctx context.Context, workspaceID string, write ...domain.PermissionResource) (context.Context, *domain.User, error) {
	if workspaceID == "" {
		return ctx, nil, &ErrBadRequest{"workspace_id is required"}
	}
	ctx, user, uw, err := s.Auth.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return ctx, nil, fmt.Errorf("failed to authenticate user: %w", err)
	}
	for _, r := range []domain.PermissionResource{domain.PermissionResourceContacts, domain.PermissionResourceMessageHistory, domain.PermissionResourceAutomations} {
		if !uw.HasPermission(r, domain.PermissionTypeRead) {
			return ctx, nil, domain.NewPermissionError(r, domain.PermissionTypeRead, "Insufficient permissions: read access to "+string(r)+" required")
		}
	}
	for _, r := range write {
		if !uw.HasPermission(r, domain.PermissionTypeWrite) {
			return ctx, nil, domain.NewPermissionError(r, domain.PermissionTypeWrite, "Insufficient permissions: write access to "+string(r)+" required")
		}
	}
	return ctx, user, nil
}

func (s *Service) engine(ctx context.Context, workspaceID string) (*Engine, error) {
	db, err := s.Conn.GetConnection(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace connection: %w", err)
	}
	return &Engine{DB: db, Time: s.Now, Env: Env{WorkspaceID: workspaceID, DB: db, Stats: s.Stats, Now: s.Now, Pause: s.Pause}}, nil
}

// Run exécute les contrôles (tous, ou ceux de `checks`) et rend l'état des écarts.
func (s *Service) Run(ctx context.Context, workspaceID string, checks []string) (*Report, error) {
	ctx, _, err := s.authorize(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, c := range checks {
		if _, ok := FindInvariant(c); !ok {
			return nil, &ErrBadRequest{fmt.Sprintf("contrôle inconnu: %q", c)}
		}
	}
	if !s.acquire(workspaceID) {
		return nil, ErrBusy
	}
	defer s.release(workspaceID)
	eng, err := s.engine(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return eng.Run(ctx, checks)
}

// Report lit les écarts sans rien exécuter.
func (s *Service) Report(ctx context.Context, workspaceID, check string, includeResolved bool) (*Report, error) {
	ctx, _, err := s.authorize(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if check != "" {
		inv, ok := FindInvariant(check)
		if !ok {
			return nil, &ErrBadRequest{fmt.Sprintf("contrôle inconnu: %q", check)}
		}
		check = inv.Code()
	}
	eng, err := s.engine(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return eng.Report(ctx, check, includeResolved)
}

func (s *Service) acquire(ws string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	if s.running[ws] {
		return false
	}
	s.running[ws] = true
	return true
}

func (s *Service) release(ws string) {
	s.mu.Lock()
	delete(s.running, ws)
	s.mu.Unlock()
}

// writeResource : la permission d'écriture qu'exige une action.
func writeResource(kind string) domain.PermissionResource {
	if kind == ActionReclassify {
		return domain.PermissionResourceContacts
	}
	return domain.PermissionResourceAutomations
}

// Resolve applique la réparation proposée (apply), la simule (dry_run) ou écarte l'écart (dismiss).
func (s *Service) Resolve(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	if req.FindingID == "" {
		return nil, &ErrBadRequest{"finding_id is required"}
	}
	if req.Action == "" {
		req.Action = "apply"
	}
	if req.Action != "apply" && req.Action != "dismiss" && req.Action != "dry_run" {
		return nil, &ErrBadRequest{"action doit valoir apply, dismiss ou dry_run"}
	}
	ctx, user, err := s.authorize(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	eng, err := s.engine(ctx, req.WorkspaceID)
	if err != nil {
		return nil, err
	}
	f, err := GetFinding(ctx, eng.DB, req.FindingID)
	if errors.Is(err, ErrFindingNotFound) {
		return nil, &ErrBadRequest{"écart introuvable"}
	}
	if err != nil {
		return nil, err
	}
	if f.ResolvedAt != nil {
		return nil, &ErrBadRequest{"cet écart est déjà résolu"}
	}
	res := &ResolveResult{FindingID: f.ID, Action: req.Action}

	by := "inconnu"
	if user != nil {
		by = user.Email
		if by == "" {
			by = user.ID
		}
	}

	switch req.Action {
	case "dry_run":
		if f.ProposedAction == nil {
			res.Message = "Aucune réparation proposée pour cet écart."
			return res, nil
		}
		res.Result = map[string]any{"would_apply": f.ProposedAction.Kind, "impact": f.ProposedAction.Impact, "params": f.ProposedAction.Params}
		res.Message = "Simulation : rien n'a été modifié."
		return res, nil
	case "dismiss":
		if err := CloseFinding(ctx, eng.DB, f.ID, by, map[string]any{"how": "dismissed", "note": req.Note}, s.now()); err != nil {
			return nil, err
		}
		res.Applied = true
		res.Message = fmt.Sprintf("Écart écarté. Il ne reviendra pas avant %d jours.", int(DismissQuietPeriod.Hours()/24))
		return res, nil
	}

	if f.ProposedAction == nil {
		return nil, &ErrBadRequest{"cet écart n'a pas de réparation automatique"}
	}
	if _, _, err := s.authorize(ctx, req.WorkspaceID, writeResource(f.ProposedAction.Kind)); err != nil {
		return nil, err
	}
	if !s.acquire(req.WorkspaceID) {
		return nil, ErrBusy
	}
	defer s.release(req.WorkspaceID)

	ex := &Executor{DB: eng.DB, Time: s.Now}
	out, err := ex.Apply(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("la réparation a échoué (rien n'a été modifié): %w", err)
	}
	if err := CloseFinding(ctx, eng.DB, f.ID, by, map[string]any{"how": "applied", "action": f.ProposedAction.Kind, "result": out}, s.now()); err != nil {
		return nil, err
	}
	res.Applied, res.Result = true, out

	// Preuve : on rejoue le contrôle. L'écart a disparu, ou il reste un résidu.
	reports, found := RunChecks(ctx, eng.Env, []string{f.Check})
	if len(reports) == 1 && reports[0].Status != StatusIncomplet {
		if _, err := SyncFindings(ctx, eng.DB, f.Check, found[f.Check], s.now()); err != nil {
			s.Log.WithField("error", err.Error()).Warn("reconcile: sync after resolve failed")
		}
		res.Verified = true
		for i := range found[f.Check] {
			if found[f.Check][i].SubjectRef == f.SubjectRef {
				res.Verified = false
				r := found[f.Check][i]
				res.Remaining = &r
			}
		}
	}
	switch {
	case res.Verified:
		res.Message = "Réparation appliquée et vérifiée : le contrôle rejoué ne trouve plus cet écart."
	case res.Remaining != nil:
		res.Message = "Réparation appliquée, mais il reste un résidu : " + res.Remaining.Observed + "."
	default:
		res.Message = "Réparation appliquée. Le contrôle n'a pas pu être rejoué : relancez un passage."
	}
	return res, nil
}
