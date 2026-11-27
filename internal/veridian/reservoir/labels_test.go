package reservoir

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

func str(v string) *domain.NullableString { return &domain.NullableString{String: v} }

func TestLabelsFor_SeulsLesChampsRemplis(t *testing.T) {
	contacts := []*domain.Contact{
		{Email: "a@x.test", CustomString4: str("La Masia"), CustomNumber1: &domain.NullableFloat64{Float64: 299}},
		{Email: "b@x.test", CustomString2: str("P3"), CustomString5: str("google"), CustomString3: &domain.NullableString{IsNull: true}},
	}
	assert.Equal(t, map[string]string{"custom_string_2": "Priorité", "custom_string_4": "Entreprise", "custom_string_5": "Messagerie", "custom_number_1": "Score"}, LabelsFor(contacts))
	assert.Empty(t, LabelsFor([]*domain.Contact{{Email: "c@x.test"}}), "un import qui n'écrit que l'adresse ne nomme rien")
}

// Cas mesurés en prod le 27/11 : ces espaces emploient les mêmes champs dans un autre sens, rien ne doit être nommé à leur place.
func TestLabelsFor_HorsExportDeProspectionRienNEstNomme(t *testing.T) {
	ville := []*domain.Contact{{Email: "a@x.test", CustomString1: str("I2MIND"), CustomString2: str("Paris"), CustomString5: str("google")}}
	assert.Empty(t, LabelsFor(ville), "custom_string_2 = ville : Paris commence par P mais n'est pas une priorité")
	classeInconnue := []*domain.Contact{{Email: "a@x.test", CustomString2: str("P1"), CustomString5: str("Site web")}}
	assert.Empty(t, LabelsFor(classeInconnue))
	sansClasse := []*domain.Contact{{Email: "a@x.test", CustomString1: str("RAL 7016"), CustomString2: str("P2")}}
	assert.Empty(t, LabelsFor(sansClasse), "une priorité seule ne suffit pas à reconnaître l'export")
	melange := []*domain.Contact{
		{Email: "a@x.test", CustomString2: str("P1"), CustomString5: str("google")},
		{Email: "b@x.test", CustomString2: str("Lyon"), CustomString5: str("google")},
	}
	assert.Empty(t, LabelsFor(melange), "une seule valeur hors format suffit à douter")
}

// La base système réduite à ce que touche le nommage : la table workspaces et ses réglages.
func labelsDB(t *testing.T, settings string) (SQLLabeler, func() map[string]string) {
	db := workspaceDB(t)
	_, err := db.Exec(`CREATE TABLE workspaces (id TEXT PRIMARY KEY, settings JSONB, updated_at TIMESTAMPTZ)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO workspaces (id, settings, updated_at) VALUES ('ws', $1::jsonb, '2026-01-01')`, settings)
	require.NoError(t, err)
	read := func() map[string]string {
		var raw []byte
		require.NoError(t, db.QueryRow(`SELECT COALESCE(settings->'custom_field_labels', '{}'::jsonb) FROM workspaces WHERE id = 'ws'`).Scan(&raw))
		out := map[string]string{}
		require.NoError(t, json.Unmarshal(raw, &out))
		return out
	}
	return SQLLabeler{DB: db}, read
}

func TestSQLLabeler_NommeSansJamaisRenommer(t *testing.T) {
	l, read := labelsDB(t, `{"timezone": "Europe/Paris", "custom_field_labels": {"custom_string_4": "Lieu"}}`)
	require.NoError(t, l.EnsureLabels(context.Background(), "ws", map[string]string{"custom_string_4": "Entreprise", "custom_number_1": "Score"}))
	assert.Equal(t, map[string]string{"custom_string_4": "Lieu", "custom_number_1": "Score"}, read(), "le libellé choisi par l'espace gagne")

	var tz string
	require.NoError(t, l.DB.QueryRow(`SELECT settings->>'timezone' FROM workspaces WHERE id = 'ws'`).Scan(&tz))
	assert.Equal(t, "Europe/Paris", tz, "les autres réglages ne bougent pas")
}

func TestSQLLabeler_RienAEcrireQuandToutEstNomme(t *testing.T) {
	l, _ := labelsDB(t, `{"custom_field_labels": {"custom_number_1": "Note"}}`)
	require.NoError(t, l.EnsureLabels(context.Background(), "ws", map[string]string{"custom_number_1": "Score"}))
	var touched bool
	require.NoError(t, l.DB.QueryRow(`SELECT updated_at > '2026-01-02' FROM workspaces WHERE id = 'ws'`).Scan(&touched))
	assert.False(t, touched, "un import de plus ne réécrit pas l'espace")
}

func TestSQLLabeler_EspaceSansReglages(t *testing.T) {
	l, read := labelsDB(t, `null`)
	require.NoError(t, l.EnsureLabels(context.Background(), "ws", map[string]string{"custom_string_2": "Priorité"}))
	assert.Equal(t, map[string]string{"custom_string_2": "Priorité"}, read())
}

// L'import réel nomme les champs qu'il remplit (Robert, 27/11 : fiches illisibles « Custom String 1 à 5 »).
func TestIntake_NommeLesChampsDeProspection(t *testing.T) {
	b := newBench(t)
	l, read := labelsDB(t, `{}`)
	b.svc.Labels = l
	var res IntakeResult
	code := b.call("POST", "/api/veridian/contacts.intake", map[string]any{"workspace_id": "ws", "contacts": []map[string]any{
		{"email": "info@lamasia.test", "custom_string_2": "P3", "custom_string_4": "La Masia", "custom_string_5": "google", "custom_number_1": 299},
	}}, &res)
	require.Equal(t, 200, code)
	require.Equal(t, 1, res.Created)
	assert.Equal(t, map[string]string{"custom_string_2": "Priorité", "custom_string_4": "Entreprise", "custom_string_5": "Messagerie", "custom_number_1": "Score"}, read())
}
