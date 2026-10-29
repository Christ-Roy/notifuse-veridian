package main

import (
	"context"
	"crypto/md5"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/pkg/crypto"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Épreuve de bout en bout sur un PostgreSQL JETABLE (jamais la prod) :
//   - le format produit est relu par le VRAI lecteur du dépôt (MessageHistoryRepository.Get) ;
//   - dry-run n'écrit rien ; apply convertit ; une seconde passe ne trouve plus rien ;
//   - lignes déjà chiffrées, vides ou sans objet data : octet pour octet inchangées ;
//   - une ligne modifiée entre lecture et écriture n'est pas écrasée ;
//   - une ligne qui échoue l'aller-retour arrête tout (code 2) sans rien écrire sur elle.
//
// Exige VERIDIAN_TEST_POSTGRES_DSN (serveur jetable). Sans DSN, ignoré.

const (
	itWsKey  = "backfill-integration-workspace-key-0123456789"
	itSecret = "738291"
)

func itDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("VERIDIAN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set VERIDIAN_TEST_POSTGRES_DSN to a disposable PostgreSQL server")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { admin.Close() })
	require.NoError(t, admin.Ping())
	name := fmt.Sprintf("l4_backfill_%d", time.Now().UnixNano())
	_, err = admin.Exec(fmt.Sprintf(`CREATE DATABASE "%s"`, name))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, name)) })
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, database.InitializeWorkspaceDatabase(db))
	return db
}

func itInsert(t *testing.T, db *sql.DB, id, messageData string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO message_history (id, contact_email, template_id, template_version, channel, message_data, channel_options, created_at, updated_at)
		VALUES ($1, $2, 'tpl', 1, 'email', $3::jsonb, '{}'::jsonb, NOW(), NOW())`, id, id+"@example.com", messageData)
	require.NoError(t, err)
}

func itRaw(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var raw string
	require.NoError(t, db.QueryRow(`SELECT message_data::text FROM message_history WHERE id = $1`, id).Scan(&raw))
	return raw
}

func itDigest(t *testing.T, db *sql.DB, ids ...string) string {
	var sb strings.Builder
	for _, id := range ids {
		sb.WriteString(id + "=" + itRaw(t, db, id) + "|")
	}
	return fmt.Sprintf("%x", md5.Sum([]byte(sb.String())))
}

func itCfg(db *sql.DB, apply bool) (config, *[]string) {
	var logs []string
	return config{db: db, wsKey: itWsKey, batch: 2, apply: apply,
		logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) }}, &logs
}

func TestBackfillPostgres_BoutEnBout(t *testing.T) {
	db := itDB(t)
	ctx := context.Background()

	clear1 := `{"data":{"code":"` + itSecret + `","n":3,"nested":{"token":"abc"}},"metadata":{"subject":"Objet"}}`
	clear2 := `{"data":{"code":"555000"},"metadata":null}`
	clear3 := `{"data":{"liste":[1,2,"x"]},"metadata":{"k":"v"}}`
	empty := `{"data":{},"metadata":{"k":"v"}}`
	nodata := `{"metadata":{"k":"v"}}`
	itInsert(t, db, "m-clear-1", clear1)
	itInsert(t, db, "m-clear-2", clear2)
	itInsert(t, db, "m-clear-3", clear3)
	itInsert(t, db, "m-empty", empty)
	itInsert(t, db, "m-nodata", nodata)

	// Ligne déjà chiffrée par le vrai dépôt (Create).
	ctrl := gomock.NewController(t)
	wsRepo := mocks.NewMockWorkspaceRepository(ctrl)
	wsRepo.EXPECT().GetConnection(gomock.Any(), "ws").Return(db, nil).AnyTimes()
	repo := repository.NewMessageHistoryRepository(wsRepo)
	untouched := []string{"m-empty", "m-nodata"}
	{
		_, err := db.Exec(`INSERT INTO message_history (id, contact_email, template_id, template_version, channel, message_data, channel_options, created_at, updated_at)
			VALUES ('m-enc', 'enc@example.com', 'tpl', 1, 'email', $1::jsonb, '{}'::jsonb, NOW(), NOW())`,
			func() string {
				enc, err := crypto.EncryptString(`{"code":"already"}`, itWsKey)
				require.NoError(t, err)
				return `{"data":{"_encrypted":"` + enc + `"},"metadata":null}`
			}())
		require.NoError(t, err)
	}
	untouched = append(untouched, "m-enc")
	avant := itDigest(t, db, untouched...)
	tout := append([]string{"m-clear-1", "m-clear-2", "m-clear-3"}, untouched...)
	avantTout := itDigest(t, db, tout...)

	// 1. Dry-run : compte, n'écrit rien.
	cfg, _ := itCfg(db, false)
	b, err := backfill(cfg)
	require.NoError(t, err)
	assert.Equal(t, 3, b.total)
	assert.Equal(t, 3, b.converted)
	assert.Equal(t, avantTout, itDigest(t, db, tout...), "le dry-run ne doit rien écrire")
	assert.Contains(t, itRaw(t, db, "m-clear-1"), itSecret)

	// 2. Apply : plus aucun clair, et le VRAI lecteur retrouve les données d'origine.
	cfg, logs := itCfg(db, true)
	b, err = backfill(cfg)
	require.NoError(t, err)
	assert.Equal(t, 3, b.converted)
	assert.Equal(t, 0, b.failed)
	for _, id := range []string{"m-clear-1", "m-clear-2", "m-clear-3"} {
		raw := itRaw(t, db, id)
		assert.Contains(t, raw, "_encrypted", id)
		assert.NotContains(t, raw, itSecret, id)
		assert.NotContains(t, raw, "555000", id)
	}
	got, err := repo.Get(ctx, "ws", itWsKey, "m-clear-1")
	require.NoError(t, err)
	assert.Equal(t, itSecret, got.MessageData.Data["code"])
	assert.EqualValues(t, 3, got.MessageData.Data["n"])
	assert.Equal(t, map[string]interface{}{"token": "abc"}, got.MessageData.Data["nested"])
	assert.Equal(t, map[string]interface{}{"subject": "Objet"}, got.MessageData.Metadata, "metadata conservée en clair")
	got3, err := repo.Get(ctx, "ws", itWsKey, "m-clear-3")
	require.NoError(t, err)
	assert.Equal(t, []interface{}{float64(1), float64(2), "x"}, got3.MessageData.Data["liste"])
	assert.Equal(t, avant, itDigest(t, db, untouched...), "lignes chiffrées, vides ou sans data: inchangées")
	// Le journal ne contient jamais un contenu.
	for _, l := range *logs {
		assert.NotContains(t, l, itSecret)
		assert.NotContains(t, l, "555000")
	}

	// 3. Idempotence.
	cfg, _ = itCfg(db, true)
	b, err = backfill(cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, b.total, "une seconde passe ne doit plus rien trouver")
}

func TestBackfillPostgres_LigneModifieeEntreLectureEtEcritureNEstPasEcrasee(t *testing.T) {
	db := itDB(t)
	itInsert(t, db, "m-1", `{"data":{"code":"111"},"metadata":null}`)
	itInsert(t, db, "m-2", `{"data":{"code":"222"},"metadata":null}`)
	cfg, _ := itCfg(db, true)
	cfg.beforeWrite = func(id string) {
		if id == "m-1" { // l'application réécrit la ligne pendant le lot
			_, err := db.Exec(`UPDATE message_history SET message_data = '{"data":{"code":"NOUVEAU"},"metadata":null}'::jsonb WHERE id = 'm-1'`)
			require.NoError(t, err)
		}
	}
	b, err := backfill(cfg)
	require.NoError(t, err)
	assert.Equal(t, 1, b.skipped)
	assert.Contains(t, itRaw(t, db, "m-1"), "NOUVEAU", "la version écrite par l'application doit survivre")
	assert.Contains(t, itRaw(t, db, "m-2"), "_encrypted")
}

func TestBackfillPostgres_EchecAllerRetourArreteSansEcrireLaLigne(t *testing.T) {
	db := itDB(t)
	itInsert(t, db, "m-1", `{"data":{"code":"111"},"metadata":null}`)
	itInsert(t, db, "m-2", `{"data":{"code":"222"},"metadata":null}`)
	cfg, _ := itCfg(db, true)
	// Chiffreur défectueux : produit un résultat qui ne relit pas l'original.
	cfg.encrypt = func(text, key string) (string, error) {
		return encryptRow(`{"data":{"code":"AUTRE"},"metadata":null}`, key)
	}
	b, err := backfill(cfg)
	require.ErrorIs(t, err, errIncomplet)
	assert.Equal(t, 1, b.failed)
	assert.Contains(t, itRaw(t, db, "m-1"), "111", "la ligne en échec n'est pas écrite")
	assert.Contains(t, itRaw(t, db, "m-2"), "222", "l'arrêt est immédiat")
}

func TestLoadWorkspaceKeyPostgres(t *testing.T) {
	db := itDB(t)
	_, err := db.Exec(`CREATE TABLE workspaces (id text primary key, settings jsonb)`)
	require.NoError(t, err)
	enc, err := crypto.EncryptString(itWsKey, "global-secret")
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO workspaces VALUES ('ws', jsonb_build_object('encrypted_secret_key', $1::text))`, enc)
	require.NoError(t, err)

	k, err := loadWorkspaceKey(db, "ws", "global-secret")
	require.NoError(t, err)
	assert.Equal(t, itWsKey, k)
	_, err = loadWorkspaceKey(db, "ws", "mauvaise-cle")
	assert.Error(t, err, "une mauvaise SECRET_KEY doit être refusée")
	_, err = loadWorkspaceKey(db, "absent", "global-secret")
	assert.Error(t, err)
}
