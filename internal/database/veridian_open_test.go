package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenPostgres_RendUnPoolEtGardeLesAutresPilotes(t *testing.T) {
	db, err := OpenPostgres("postgres", "postgres://u:p@127.0.0.1:1/x?sslmode=disable")
	require.NoError(t, err)
	defer db.Close()
	assert.Error(t, db.Ping(), "aucun serveur : la connexion echoue, l'ouverture non")

	_, err = OpenPostgres("pilote-inconnu", "x")
	assert.Error(t, err)
}
