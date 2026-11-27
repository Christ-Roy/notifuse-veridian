package liensclics

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/testpg"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sur une vraie base PostgreSQL (testpg) : la colonne clicked_links, l'écriture atomique et les compteurs.
func ouvrirBaseDeTest(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", testpg.DSN(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`DROP TABLE IF EXISTS message_history`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE message_history (id TEXT PRIMARY KEY, clicked_links JSONB)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO message_history (id) VALUES ('m1')`)
	require.NoError(t, err)
	return db
}

func TestStore_Lier_CompteEtBornes(t *testing.T) {
	db := ouvrirBaseDeTest(t)
	ctx := context.Background()
	var s Store
	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

	ok, err := s.Lier(ctx, db, "m1", "https://a.test/offre", at)
	require.NoError(t, err)
	assert.True(t, ok)
	_, err = s.Lier(ctx, db, "m1", "https://a.test/offre", at.Add(time.Hour))
	require.NoError(t, err)

	var brut []byte
	require.NoError(t, db.QueryRow(`SELECT clicked_links FROM message_history WHERE id='m1'`).Scan(&brut))
	carte, err := Decoder(brut)
	require.NoError(t, err)
	got := carte["https://a.test/offre"]
	assert.Equal(t, 2, got.Count)
	assert.True(t, got.FirstAt.Equal(at), "premier clic = %v", got.FirstAt)
	assert.True(t, got.LastAt.Equal(at.Add(time.Hour)), "dernier clic = %v", got.LastAt)
}

func TestStore_Lier_MessageInconnuNeCompteRien(t *testing.T) {
	db := ouvrirBaseDeTest(t)
	ok, err := Store{}.Lier(context.Background(), db, "absent", "https://a.test/", time.Now())
	require.NoError(t, err)
	assert.False(t, ok)
}

// Deux exemplaires de Mail peuvent cliquer en même temps : aucun clic ne doit se perdre.
func TestStore_Lier_ClicsSimultanesNePerdentRien(t *testing.T) {
	db := ouvrirBaseDeTest(t)
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Store{}.Lier(context.Background(), db, "m1", "https://a.test/simultane", time.Now())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var brut []byte
	require.NoError(t, db.QueryRow(`SELECT clicked_links FROM message_history WHERE id='m1'`).Scan(&brut))
	carte, err := Decoder(brut)
	require.NoError(t, err)
	assert.Equal(t, n, carte["https://a.test/simultane"].Count)
}
