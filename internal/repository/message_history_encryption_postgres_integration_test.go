package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/golang/mock/gomock"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestMessageHistoryRepository_EncryptionPostgres prouve sur un vrai PostgreSQL
// (pas sqlmock) que message_data reste chiffré en base après Create puis Update
// (l'envoi réécrit la ligne), et que la lecture déchiffre sans erreur un mélange
// de lignes anciennes en clair et de lignes chiffrées.
//
// Exige VERIDIAN_TEST_POSTGRES_DSN pointant vers un PostgreSQL jetable : la base
// de test est créée puis supprimée. Sans DSN, le test est ignoré.
func TestMessageHistoryRepository_EncryptionPostgres(t *testing.T) {
	dsn := os.Getenv("VERIDIAN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set VERIDIAN_TEST_POSTGRES_DSN to a disposable PostgreSQL server")
	}
	ctx := context.Background()

	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	require.NoError(t, admin.Ping())

	dbName := fmt.Sprintf("lot0_enc_%d", time.Now().UnixNano())
	_, err = admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, dbName))
	require.NoError(t, err)
	defer func() {
		_, _ = admin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, dbName))
	}()

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + dbName
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, database.InitializeWorkspaceDatabase(db))

	ctrl := gomock.NewController(t)
	workspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	workspaceRepo.EXPECT().GetConnection(gomock.Any(), "ws").Return(db, nil).AnyTimes()
	repo := NewMessageHistoryRepository(workspaceRepo)

	const secret = "lot0-secret-key-for-postgres-integration-test"
	const magicCode = "738291"
	const legacyCode = "111222"

	rawData := func(id string) string {
		var raw string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT message_data::text FROM message_history WHERE id = $1`, id).Scan(&raw))
		return raw
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	msg := &domain.MessageHistory{
		ID:              "lot0-enc-1",
		ContactEmail:    "lot0@example.com",
		TemplateID:      "tpl-lot0",
		TemplateVersion: 1,
		Channel:         "email",
		MessageData:     domain.MessageData{Data: map[string]interface{}{"code": magicCode}},
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// 1. Create chiffre : la colonne brute ne contient pas le code.
	require.NoError(t, repo.Create(ctx, "ws", secret, msg))
	raw := rawData(msg.ID)
	require.NotContains(t, raw, magicCode, "Create doit chiffrer message_data")
	require.Contains(t, raw, "_encrypted")

	// 2. Envoi : email_service pose sent_at puis appelle Update avec le message en mémoire.
	sentAt := now.Add(time.Second)
	msg.SentAt = &sentAt
	msg.UpdatedAt = sentAt
	require.NoError(t, repo.Update(ctx, "ws", secret, msg))
	raw = rawData(msg.ID)
	require.NotContains(t, raw, magicCode, "Update doit chiffrer message_data (défaut lot 0)")
	require.Contains(t, raw, "_encrypted")

	// Le message en mémoire de l'appelant reste en clair.
	require.Equal(t, magicCode, msg.MessageData.Data["code"])

	// 3. Lecture : déchiffre la ligne chiffrée.
	got, err := repo.Get(ctx, "ws", secret, msg.ID)
	require.NoError(t, err)
	require.Equal(t, magicCode, got.MessageData.Data["code"])

	// 4. Lignes anciennes en clair (écrites avant le correctif) : la lecture
	// doit les rendre sans erreur, à côté des lignes chiffrées.
	legacy := `{"data":{"code":"` + legacyCode + `"},"metadata":null}`
	_, err = db.ExecContext(ctx, `
		INSERT INTO message_history (id, contact_email, template_id, template_version, channel, message_data, channel_options, created_at, updated_at)
		VALUES ('lot0-legacy-1', 'legacy@example.com', 'tpl-lot0', 1, 'email', $1::jsonb, '{}'::jsonb, NOW(), NOW())`, legacy)
	require.NoError(t, err)
	require.True(t, strings.Contains(rawData("lot0-legacy-1"), legacyCode), "fixture: ligne ancienne en clair")

	gotLegacy, err := repo.Get(ctx, "ws", secret, "lot0-legacy-1")
	require.NoError(t, err, "une ligne ancienne en clair doit se lire sans erreur")
	require.Equal(t, legacyCode, gotLegacy.MessageData.Data["code"])

	gotEnc, err := repo.Get(ctx, "ws", secret, msg.ID)
	require.NoError(t, err, "une ligne chiffrée doit se lire à côté des anciennes")
	require.Equal(t, magicCode, gotEnc.MessageData.Data["code"])
}
