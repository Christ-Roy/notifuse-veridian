package domauth

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/database"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/veridian/testpg"
)

const secret = "secret-de-test-domauth-0123456789"

func pgStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db := testpg.System(t, func(db *sql.DB) error {
		if err := database.InitializeDatabase(db, ""); err != nil {
			return err
		}
		_, err := veridianmig.Default.ApplySystem(context.Background(), db)
		return err
	})
	for _, w := range []string{"wsa", "wsb"} {
		_, err := db.Exec(`INSERT INTO workspaces (id,name,created_at,updated_at) VALUES ($1,$1,NOW(),NOW())`, w)
		require.NoError(t, err)
	}
	return &Store{DB: db, Secret: secret}, db
}

func TestStore_CleChiffreeEtSignatureSeulementVerifie(t *testing.T) {
	s, db := pgStore(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "wsa", "Client.FR", "humain:u1")
	require.NoError(t, err)
	assert.Equal(t, "client.fr", d.Domain)
	assert.Equal(t, StatusPending, d.Status)

	var enc string
	require.NoError(t, db.QueryRow(`SELECT encrypted_private_key FROM veridian_sending_domain WHERE id=$1`, d.ID).Scan(&enc))
	assert.NotContains(t, enc, "PRIVATE KEY", "la clé privée n'est jamais en clair en base")

	// En attente : aucune clé pour signer.
	sel, k, err := s.SigningKey(ctx, "wsa", "client.fr")
	require.NoError(t, err)
	assert.Nil(t, k)
	assert.Empty(t, sel)

	_, err = s.SaveCheck(ctx, "wsa", d.ID, StatusVerified, []Check{{Kind: KindDKIM, OK: true, Verdict: "pass"}})
	require.NoError(t, err)
	sel, k, err = s.SigningKey(ctx, "wsa", "CLIENT.fr")
	require.NoError(t, err)
	require.NotNil(t, k)
	assert.Equal(t, d.Selector, sel)

	// Garde d'espace : le même domaine vérifié dans A ne signe rien pour B.
	_, k, err = s.SigningKey(ctx, "wsb", "client.fr")
	require.NoError(t, err)
	assert.Nil(t, k)

	// Mauvaise SECRET_KEY : erreur, jamais une clé fausse.
	_, _, err = (&Store{DB: db, Secret: "autre"}).SigningKey(ctx, "wsa", "client.fr")
	assert.Error(t, err)
}

func TestStore_GardeEspaceEtDoublons(t *testing.T) {
	s, db := pgStore(t)
	ctx := context.Background()
	d, err := s.Create(ctx, "wsa", "client.fr", "")
	require.NoError(t, err)
	_, err = s.Create(ctx, "wsa", "client.fr", "")
	assert.ErrorIs(t, err, ErrExists)
	// Un autre espace peut déclarer le même nom (sa propre clé) : seul celui qui publie sa clé sera vérifié.
	other, err := s.Create(ctx, "wsb", "client.fr", "")
	require.NoError(t, err)
	assert.NotEqual(t, d.Selector, other.Selector)

	_, err = s.Get(ctx, "wsb", d.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.SaveCheck(ctx, "wsb", d.ID, StatusVerified, nil)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.Delete(ctx, "wsb", d.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	l, err := s.List(ctx, "wsa")
	require.NoError(t, err)
	require.Len(t, l, 1)
	_, err = s.Delete(ctx, "wsa", d.ID)
	require.NoError(t, err)
	l, _ = s.List(ctx, "wsa")
	assert.Empty(t, l)

	_, err = s.Create(ctx, "wsa", "pas un domaine", "")
	assert.ErrorIs(t, err, ErrInvalidName)

	// L'espace supprimé emporte ses domaines et leurs clés.
	_, err = db.Exec(`DELETE FROM workspaces WHERE id='wsb'`)
	require.NoError(t, err)
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_sending_domain WHERE workspace_id='wsb'`).Scan(&n))
	assert.Zero(t, n)
}

func TestStore_Plafond(t *testing.T) {
	s, _ := pgStore(t)
	ctx := context.Background()
	for i := 0; i < MaxPerWorkspace; i++ {
		_, err := s.Create(ctx, "wsa", "d"+strings.Repeat("x", i)+".fr", "")
		require.NoError(t, err)
	}
	_, err := s.Create(ctx, "wsa", "encore.fr", "")
	assert.ErrorIs(t, err, ErrTooMany)
}
