package veridian

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// V65 rejouee sur une vraie base (VERIDIAN_TEST_POSTGRES_DSN) avec les segments reels d'animalsd :
// elle range sans modifier un nom, et le runner ne la joue qu'une fois.
func TestV65_ConvertitLesSegmentsReelsSansToucherAuxNoms(t *testing.T) {
	dsn := os.Getenv("VERIDIAN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("VERIDIAN_TEST_POSTGRES_DSN absent")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("v65_%d", os.Getpid())
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) }()
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE segments (id VARCHAR(32) PRIMARY KEY, name VARCHAR(255) NOT NULL, status VARCHAR(20) NOT NULL DEFAULT 'active')`,
		`CREATE TABLE lists (id VARCHAR(32) PRIMARY KEY, name VARCHAR(255) NOT NULL, deleted_at TIMESTAMPTZ)`,
		`CREATE TABLE templates (id VARCHAR(32) NOT NULL, name VARCHAR(255) NOT NULL, version INTEGER NOT NULL, deleted_at TIMESTAMPTZ, PRIMARY KEY (id, version))`,
	} {
		_, err := db.Exec(ddl)
		require.NoError(t, err)
	}
	raw, err := os.ReadFile("../../veridian/folders/testdata/animalsd_segments.json")
	require.NoError(t, err)
	var segs []struct{ ID, Name string }
	require.NoError(t, json.Unmarshal(raw, &segs))
	require.NotEmpty(t, segs)
	for _, s := range segs {
		_, err := db.Exec(`INSERT INTO segments (id, name) VALUES ($1, $2)`, s.ID, s.Name)
		require.NoError(t, err)
	}

	var spec Spec
	for _, s := range Default.List() {
		if s.Number == 65 {
			spec = s
		}
	}
	require.Equal(t, 65, spec.Number, "V65 est enregistree par init()")
	reg := NewRegistry()
	require.NoError(t, reg.Register(spec))

	played, err := reg.ApplyWorkspace(context.Background(), db, "ws")
	require.NoError(t, err)
	assert.Equal(t, []int{65}, played)
	played, err = reg.ApplyWorkspace(context.Background(), db, "ws")
	require.NoError(t, err)
	assert.Empty(t, played, "une migration deja jouee ne se rejoue pas")

	var changed, filed, folders int
	for _, s := range segs {
		var name string
		require.NoError(t, db.QueryRow(`SELECT name FROM segments WHERE id = $1`, s.ID).Scan(&name))
		if name != s.Name {
			changed++
		}
	}
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_folder_items WHERE kind = 'segments' AND folder_id IS NOT NULL`).Scan(&filed))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_folders WHERE kind = 'segments'`).Scan(&folders))
	assert.Equal(t, 0, changed, "aucun nom modifie")
	assert.Equal(t, len(segs), filed, "chaque segment est range")
	assert.Equal(t, 13, folders)
}
