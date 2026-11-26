package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/veridian/testpg"
)

// Une erreur enveloppée comme le font dépôt et service (fmt.Errorf %w, deux couches).
func wrappedUnique(table string) error {
	return fmt.Errorf("failed to create list: %w", fmt.Errorf("failed to create list: %w", &pq.Error{Code: "23505", Table: table}))
}

func TestVeridianUniqueViolation(t *testing.T) {
	table, ok := veridianUniqueViolation(wrappedUnique("lists"))
	assert.True(t, ok)
	assert.Equal(t, "lists", table)

	// Témoins négatifs : une autre erreur PostgreSQL (clé étrangère), une erreur quelconque, nil.
	_, ok = veridianUniqueViolation(fmt.Errorf("x: %w", &pq.Error{Code: "23503", Table: "lists"}))
	assert.False(t, ok)
	_, ok = veridianUniqueViolation(errors.New("connection refused"))
	assert.False(t, ok)
	_, ok = veridianUniqueViolation(nil)
	assert.False(t, ok)
}

func TestVeridianWriteConflict(t *testing.T) {
	rec := httptest.NewRecorder()
	assert.True(t, veridianWriteConflict(rec, wrappedUnique("segments")))
	assert.Equal(t, http.StatusConflict, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeAlreadyExists, body["code"])
	assert.Contains(t, body["error"], "already exists")
	assert.Equal(t, map[string]interface{}{"resource": "segments"}, body["details"])

	// Rien d'écrit pour une erreur qui n'est pas un doublon : le handler garde son propre statut.
	rec = httptest.NewRecorder()
	assert.False(t, veridianWriteConflict(rec, errors.New("boom")))
	assert.Equal(t, 0, rec.Body.Len())
}

// Contrôle sur le vrai pilote : un INSERT en double rend bien un *pq.Error 23505 avec sa table, et la chaîne
// enveloppée comme dans le dépôt des listes sort en 409 par WriteAuthAwareError. Sans cette épreuve, le test
// ci-dessus ne prouverait que notre propre fabrication d'erreur.
func TestVeridianConflict_VraiPostgres(t *testing.T) {
	dsn := testpg.DSN(t)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Conn(ctx) // une seule connexion : la table temporaire y vit
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `CREATE TEMP TABLE lists (id varchar(32) PRIMARY KEY, name text)`)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `INSERT INTO lists (id, name) VALUES ('reserve', 'a')`)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `INSERT INTO lists (id, name) VALUES ('reserve', 'b')`)
	require.Error(t, err)
	err = fmt.Errorf("failed to create list: %w", fmt.Errorf("failed to create list: %w", err))

	rec := httptest.NewRecorder()
	WriteAuthAwareError(rec, err, "Failed to create list", http.StatusInternalServerError)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `"code":"already_exists"`)
	assert.Contains(t, rec.Body.String(), `"resource":"lists"`)
}
