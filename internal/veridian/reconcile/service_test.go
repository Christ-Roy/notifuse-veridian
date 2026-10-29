package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeConn struct{ db *sql.DB }

func (f fakeConn) GetConnection(context.Context, string) (*sql.DB, error) { return f.db, nil }

func readPerms() domain.UserPermissions {
	return domain.UserPermissions{
		domain.PermissionResourceContacts:       {Read: true},
		domain.PermissionResourceMessageHistory: {Read: true},
		domain.PermissionResourceAutomations:    {Read: true},
	}
}

func newSvc(t *testing.T, perms domain.UserPermissions, authErr error) (*Service, sqlmock.Sqlmock) {
	ctrl := gomock.NewController(t)
	auth := mocks.NewMockAuthService(ctrl)
	auth.EXPECT().AuthenticateUserForWorkspace(gomock.Any(), "ws").DoAndReturn(
		func(ctx context.Context, _ string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
			if authErr != nil {
				return ctx, nil, nil, authErr
			}
			return ctx, &domain.User{ID: "u1", Email: "agent@x.fr"}, &domain.UserWorkspace{Role: "member", Permissions: perms}, nil
		}).AnyTimes()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &Service{Auth: auth, Conn: fakeConn{db}, Log: logger.NewLoggerWithLevel("error")}, mock
}

func TestService_ReadPermissionsAreAllRequired(t *testing.T) {
	for _, missing := range []domain.PermissionResource{domain.PermissionResourceContacts, domain.PermissionResourceMessageHistory, domain.PermissionResourceAutomations} {
		perms := readPerms()
		delete(perms, missing)
		s, _ := newSvc(t, perms, nil)
		_, err := s.Report(context.Background(), "ws", "", false)
		var pe *domain.PermissionError
		require.True(t, errors.As(err, &pe), "sans %s : %v", missing, err)
		_, err = s.Run(context.Background(), "ws", nil)
		assert.True(t, errors.As(err, &pe), "run sans %s", missing)
	}
}

func TestService_AuthFailureAndBadInput(t *testing.T) {
	s, _ := newSvc(t, readPerms(), errors.New("jeton invalide"))
	_, err := s.Report(context.Background(), "ws", "", false)
	assert.ErrorContains(t, err, "failed to authenticate user")

	s, _ = newSvc(t, readPerms(), nil)
	_, err = s.Run(context.Background(), "", nil)
	var bad *ErrBadRequest
	assert.True(t, errors.As(err, &bad), "workspace_id manquant")
	_, err = s.Run(context.Background(), "ws", []string{"inconnu"})
	assert.True(t, errors.As(err, &bad), "contrôle inconnu")
	_, err = s.Report(context.Background(), "ws", "inconnu", false)
	assert.True(t, errors.As(err, &bad))
	_, err = s.Resolve(context.Background(), ResolveRequest{WorkspaceID: "ws"})
	assert.True(t, errors.As(err, &bad), "finding_id manquant")
	_, err = s.Resolve(context.Background(), ResolveRequest{WorkspaceID: "ws", FindingID: "x", Action: "supprimer"})
	assert.True(t, errors.As(err, &bad), "action inconnue")
}

var testNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

var findingRowCols = []string{"id", "check", "severity", "subject_ref", "title", "expected", "observed", "explanation",
	"evidence", "proposed_action", "first_seen_at", "last_seen_at", "resolved_at", "resolved_by", "resolution"}

func expectFinding(mock sqlmock.Sqlmock, kind string) {
	act, _ := json.Marshal(Action{Kind: kind, Label: "x", Params: map[string]any{"from": "freemail_fr", "to": "orange"}})
	mock.ExpectQuery("FROM veridian_reconcile_finding WHERE id").WithArgs("f1").WillReturnRows(
		sqlmock.NewRows(findingRowCols).AddRow("f1", "classes", "warning", "classe:freemail_fr->orange", "t", "e", "o", "x",
			[]byte(`{}`), act, testNow, testNow, nil, "", nil))
}

func TestService_ResolveApplyNeedsTheWritePermissionOfTheAction(t *testing.T) {
	// Lecture seule : reclasser (écriture contacts) est refusé AVANT toute modification.
	s, mock := newSvc(t, readPerms(), nil)
	expectFinding(mock, ActionReclassify)
	_, err := s.Resolve(context.Background(), ResolveRequest{WorkspaceID: "ws", FindingID: "f1", Action: "apply"})
	var pe *domain.PermissionError
	require.True(t, errors.As(err, &pe), "%v", err)
	assert.Contains(t, err.Error(), "contacts")
	assert.NoError(t, mock.ExpectationsWereMet(), "aucune écriture n'a été tentée")

	// Ecriture automations ne suffit pas pour reclasser des contacts.
	perms := readPerms()
	perms[domain.PermissionResourceAutomations] = domain.ResourcePermissions{Read: true, Write: true}
	s, mock = newSvc(t, perms, nil)
	expectFinding(mock, ActionReclassify)
	_, err = s.Resolve(context.Background(), ResolveRequest{WorkspaceID: "ws", FindingID: "f1", Action: "apply"})
	assert.True(t, errors.As(err, &pe))

	// Sortir des contacts exige l'écriture des automations.
	s, mock = newSvc(t, readPerms(), nil)
	expectFinding(mock, ActionExitContacts)
	_, err = s.Resolve(context.Background(), ResolveRequest{WorkspaceID: "ws", FindingID: "f1", Action: "apply"})
	require.True(t, errors.As(err, &pe))
	assert.Contains(t, err.Error(), "automations")
}

func TestService_ResolveDryRunChangesNothing(t *testing.T) {
	s, mock := newSvc(t, readPerms(), nil)
	expectFinding(mock, ActionReclassify)
	res, err := s.Resolve(context.Background(), ResolveRequest{WorkspaceID: "ws", FindingID: "f1", Action: "dry_run"})
	require.NoError(t, err)
	assert.False(t, res.Applied)
	assert.Equal(t, ActionReclassify, res.Result["would_apply"])
	assert.Contains(t, res.Message, "rien n'a été modifié")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ResolveUnknownFinding(t *testing.T) {
	s, mock := newSvc(t, readPerms(), nil)
	mock.ExpectQuery("FROM veridian_reconcile_finding WHERE id").WithArgs("f1").WillReturnRows(sqlmock.NewRows(findingRowCols))
	_, err := s.Resolve(context.Background(), ResolveRequest{WorkspaceID: "ws", FindingID: "f1"})
	var bad *ErrBadRequest
	assert.True(t, errors.As(err, &bad))
	assert.Contains(t, err.Error(), "introuvable")
}

func TestService_OnePassAtATimePerWorkspace(t *testing.T) {
	s, _ := newSvc(t, readPerms(), nil)
	require.True(t, s.acquire("ws"))
	assert.False(t, s.acquire("ws"), "un second passage est refusé")
	assert.True(t, s.acquire("autre"), "un autre workspace n'est pas bloqué")
	_, err := s.Run(context.Background(), "ws", nil)
	assert.ErrorIs(t, err, ErrBusy)
	s.release("ws")
	assert.True(t, s.acquire("ws"))
}

func TestHandler_RoutesStatusCodesAndBodies(t *testing.T) {
	s, _ := newSvc(t, readPerms(), nil)
	passthrough := func(h http.Handler) http.Handler { return h }
	mux := http.NewServeMux()
	(&Handler{Svc: s, RequireAuth: passthrough, Log: logger.NewLoggerWithLevel("error")}).RegisterRoutes(mux)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	assert.Equal(t, 400, do("POST", "/api/veridian/reconcile.run", `{`).Code, "JSON invalide")
	assert.Equal(t, 400, do("POST", "/api/veridian/reconcile.run", `{}`).Code, "workspace_id manquant")
	assert.Equal(t, 400, do("GET", "/api/veridian/reconcile.report", ``).Code)
	assert.Equal(t, 400, do("POST", "/api/veridian/reconcile.resolve", `{"workspace_id":"ws"}`).Code, "finding_id manquant")
	assert.Equal(t, 405, do("GET", "/api/veridian/reconcile.run", ``).Code, "run est un POST")
	assert.Equal(t, 405, do("GET", "/api/veridian/reconcile.resolve", ``).Code, "resolve est un POST")

	require.True(t, s.acquire("ws"))
	rec := do("POST", "/api/veridian/reconcile.run", `{"workspace_id":"ws"}`)
	assert.Equal(t, 409, rec.Code, "passage déjà en cours")
	assert.Contains(t, rec.Body.String(), "déjà en cours")
}
