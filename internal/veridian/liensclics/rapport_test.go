package liensclics

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type faussAuth struct {
	droits domain.UserPermissions
}

func (f faussAuth) AuthenticateUserForWorkspace(ctx context.Context, ws string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
	return ctx, &domain.User{}, &domain.UserWorkspace{WorkspaceID: ws, Role: "member", Permissions: f.droits}, nil
}

type faussesBases struct{ db *sql.DB }

func (f faussesBases) GetConnection(ctx context.Context, ws string) (*sql.DB, error) {
	return f.db, nil
}

func baseAvecEnvoi(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", testpg.DSN(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`DROP TABLE IF EXISTS message_history`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE message_history (id TEXT PRIMARY KEY, broadcast_id TEXT, clicked_links JSONB)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO message_history (id, broadcast_id, clicked_links) VALUES
		('m1', 'envoi-A', '{"https://a.test/x": {"count": 3, "first_at": "2026-11-27T10:00:00Z", "last_at": "2026-11-27T11:00:00Z"}}'),
		('m2', 'envoi-A', '{"https://a.test/x": {"count": 1, "first_at": "2026-11-27T10:00:00Z", "last_at": "2026-11-27T10:00:00Z"}, "https://a.test/y": {"count": 2, "first_at": "2026-11-27T10:00:00Z", "last_at": "2026-11-27T10:00:00Z"}}'),
		('m3', 'envoi-B', '{"https://b.test/z": {"count": 9, "first_at": "2026-11-27T10:00:00Z", "last_at": "2026-11-27T10:00:00Z"}}'),
		('m4', 'envoi-A', NULL)`)
	require.NoError(t, err)
	return db
}

var droitsLecture = domain.UserPermissions{domain.PermissionResourceMessageHistory: {Read: true}}

func TestRapport_Lignes_AdditionneLesClicsDeLEnvoi(t *testing.T) {
	db := baseAvecEnvoi(t)
	svc := &Rapport{Auth: faussAuth{droits: droitsLecture}, Bases: faussesBases{db: db}}
	lignes, err := svc.Lignes(context.Background(), "ws1", "envoi-A")
	require.NoError(t, err)
	// x : 3 + 1 clics, deux messages ; y : 2 clics, un message. Le message de l'envoi B et le NULL sont exclus.
	assert.Equal(t, []Ligne{
		{URL: "https://a.test/x", Clics: 4, Cliqueurs: 2},
		{URL: "https://a.test/y", Clics: 2, Cliqueurs: 1},
	}, lignes)
}

func TestRapport_Lignes_RefuseSansDroitDeLecture(t *testing.T) {
	db := baseAvecEnvoi(t)
	svc := &Rapport{Auth: faussAuth{droits: domain.UserPermissions{}}, Bases: faussesBases{db: db}}
	_, err := svc.Lignes(context.Background(), "ws1", "envoi-A")
	require.Error(t, err)
}

func TestRapport_Lignes_ExigeLesDeuxIdentifiants(t *testing.T) {
	svc := &Rapport{Auth: faussAuth{droits: droitsLecture}}
	_, err := svc.Lignes(context.Background(), "", "envoi-A")
	require.Error(t, err)
	_, err = svc.Lignes(context.Background(), "ws1", " ")
	require.Error(t, err)
}
