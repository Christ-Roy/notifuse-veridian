package leader

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/veridian/testpg"
)

func base(t *testing.T) *sql.DB {
	return testpg.System(t, func(*sql.DB) error { return nil }) // ignoré sur un poste sans base, exigé en CI
}

// Deux exemplaires sur la même base : un seul mène ; quand il s'arrête, l'autre prend la main au tour suivant.
func TestElector_UnSeulMeneEtLAutreReprend(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	a, b := &Elector{DB: db}, &Elector{DB: db}
	a.Tick(ctx)
	b.Tick(ctx)
	assert.True(t, a.Leads())
	assert.False(t, b.Leads(), "deux leaders : l'envoi partirait deux fois plus vite")
	b.Tick(ctx)
	assert.False(t, b.Leads())

	a.release()
	assert.False(t, a.Leads())
	b.Tick(ctx)
	assert.True(t, b.Leads(), "le verrou rendu, l'autre exemplaire mène")
	a.Tick(ctx)
	assert.False(t, a.Leads())
}

// La connexion du leader meurt (instance tuée, réseau coupé) : il cesse de mener, et le verrou est libre pour l'autre.
func TestElector_ConnexionPerdueCesseDeMener(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	a, b := &Elector{DB: db}, &Elector{DB: db}
	a.Tick(ctx)
	require.True(t, a.Leads())
	var pid int
	require.NoError(t, a.conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	_, err := db.Exec(`SELECT pg_terminate_backend($1)`, pid)
	require.NoError(t, err)

	a.Tick(ctx)
	assert.False(t, a.Leads(), "verrou perdu avec la session : ne plus mener")
	b.Tick(ctx)
	assert.True(t, b.Leads())
}

type gate bool

func (g gate) Leads() bool { return bool(g) }

func TestAlways(t *testing.T) {
	assert.True(t, Always{}.Leads())
	t.Setenv("VERIDIAN_LEADER", "off")
	_, isAlways := New(&sql.DB{}, nil).(Always)
	assert.True(t, isAlways, "coupe-circuit VERIDIAN_LEADER=off")
	_, isAlways = New(nil, nil).(Always)
	assert.True(t, isAlways, "sans base : un seul exemplaire possible")
}

func TestNew_ElectionParDefaut(t *testing.T) {
	t.Setenv("VERIDIAN_LEADER", "")
	_, isElector := New(&sql.DB{}, nil).(*Elector)
	assert.True(t, isElector)
}
