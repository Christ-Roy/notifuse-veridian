package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/veridian/mcp"
)

const enrollBody = `{"sequence_id":"s1","contacts":[{"email":"A@x.test","first_name":"Ana"}]}`

func enrollScope(c *mcp.Claims) {
	c.Scope = "notifuse:sequences.inscrire"
	c.Act.Subject = "app:crm"
}

func motifs(out map[string]any) map[string]string {
	m := map[string]string{}
	for _, d := range out["resultat"].(map[string]any)["details"].([]any) {
		x := d.(map[string]any)
		v, _ := x["motif"].(string)
		m[x["email"].(string)] = v
	}
	return m
}

func TestSequencesInscrire_UnMotifParPersonne(t *testing.T) {
	e := newEnv(t)
	e.d.replies = []reply{
		{200, `{"received":9,"created":1,"details":[{"email":"des@x.test","reason":"desinscrit"},{"email":"faux@x.test","reason":"email_invalide"}]}`},
		{200, `{"emails":["neuf@x.test"],"states":{"neuf@x.test":"ok","rep@x.test":"replied","dans@x.test":"already","autre@x.test":"in_sequence","vu@x.test":"contacted","pl@x.test":"plainte","attente@x.test":"ok"}}`}, // perdu@x.test : sans état
	}
	b := `{"sequence_id":"s1","contacts":[{"email":" Neuf@X.test ","first_name":"Ana","phone":" 06 "},{"email":"neuf@x.test"},{"email":"des@x.test"},{"email":"faux@x.test"},{"email":"rep@x.test"},
		{"email":"dans@x.test"},{"email":"autre@x.test"},{"email":"vu@x.test"},{"email":"pl@x.test"},{"email":"attente@x.test"},{"email":"perdu@x.test"},{"email":"stop@x.test","ne_plus_contacter":true},{"email":"pas une adresse"}]}`
	st, out := e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000001", b)
	require.Equal(t, 200, st, out)
	assert.Equal(t, map[string]string{"neuf@x.test": "", "des@x.test": "desinscrit", "faux@x.test": "adresse_invalide", "rep@x.test": "a_deja_repondu", "dans@x.test": "deja_dans_la_sequence",
		"autre@x.test": "dans_une_autre_sequence", "vu@x.test": "deja_contacte", "pl@x.test": "plainte", "attente@x.test": "non_inscrit", "perdu@x.test": "non_inscrit", "stop@x.test": "ne_plus_contacter", "pas une adresse": "adresse_invalide"}, motifs(out))
	res := out["resultat"].(map[string]any)
	assert.EqualValues(t, 1, res["inscrits"])
	assert.EqualValues(t, 12, res["received"], "un doublon compte une fois")
	assert.EqualValues(t, 2, res["skipped"].(map[string]any)["adresse_invalide"])

	require.Equal(t, []string{"POST /api/veridian/contacts.intake", "POST /api/veridian/reservoir.activate"}, e.d.paths)
	var in, act map[string]any
	require.NoError(t, json.Unmarshal([]byte(e.d.bodies[0]), &in))
	require.NoError(t, json.Unmarshal([]byte(e.d.bodies[1]), &act))
	assert.Equal(t, "ws1", in["workspace_id"], "l'espace vient du jeton")
	assert.Equal(t, "attach", in["existing"], "une fiche existante n'est pas réécrite")
	assert.Equal(t, false, in["allow_triggers"])
	assert.Empty(t, in["list_ids"], "aucune liste avant le contrôle de l'activation")
	assert.NotContains(t, e.d.bodies[0], "stop@x.test", "« ne plus contacter » ne quitte pas la porte")
	assert.Equal(t, map[string]any{"email": "neuf@x.test", "first_name": "Ana", "phone": "06", "veridian_source": "crm"}, in["contacts"].([]any)[0])
	assert.NotContains(t, act["emails"], "des@x.test", "un refus de la fiche n'atteint pas l'activation")
	assert.Equal(t, false, act["include_contacted"])
	assert.Equal(t, false, act["dry_run"])
	assert.True(t, strings.HasPrefix(act["batch_id"].(string), "app-"))
	assert.Len(t, e.st.done, 1)
}

// Le lot de l'activation suit la clé : le même geste rejoué côté base n'inscrit pas deux fois, deux gestes n'ont pas le même lot.
func TestSequencesInscrire_LotSuitLaCle(t *testing.T) {
	lot := func(app, key string) string {
		e := newEnv(t)
		e.d.replies = []reply{{200, `{}`}, {200, `{"emails":["a@x.test"],"states":{"a@x.test":"ok"}}`}}
		st, out := e.post("sequences.inscrire", e.hub.token(t, func(c *mcp.Claims) { enrollScope(c); c.Act.Subject = app }), key, enrollBody)
		require.Equal(t, 200, st, out)
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(e.d.bodies[1]), &m))
		return m["batch_id"].(string)
	}
	a := lot("app:crm", "cle-000001")
	assert.Equal(t, a, lot("app:crm", "cle-000001"))
	assert.NotEqual(t, a, lot("app:crm", "cle-000002"))
	assert.NotEqual(t, a, lot("app:prospection", "cle-000001"))
}

func TestSequencesInscrire_Simulation(t *testing.T) {
	e := newEnv(t)
	e.st.enrolled = 10000
	e.d.replies = []reply{{200, `{"created":1,"dry_run":true}`}, {200, `{"dry_run":true,"emails":["b@x.test"],"states":{"b@x.test":"ok","c@x.test":"contacted"}}`}}
	st, out := e.post("sequences.inscrire", e.hub.token(t, enrollScope), "", `{"sequence_id":"s1","dry_run":true,"contacts":[{"email":"a@x.test"},{"email":"b@x.test"},{"email":"c@x.test"}]}`)
	require.Equal(t, 200, st, out)
	assert.Equal(t, true, out["simulation"])
	assert.Equal(t, map[string]string{"a@x.test": "", "b@x.test": "", "c@x.test": "deja_contacte"}, motifs(out), "une fiche qui serait créée serait inscrite")
	assert.Contains(t, e.d.bodies[0], `"dry_run":true`)
	assert.Contains(t, e.d.bodies[1], `"dry_run":true`)
	assert.Contains(t, e.d.bodies[1], `"batch_id":""`)
	assert.Empty(t, e.st.done)
	assert.Empty(t, e.st.counted, "une simulation ne compte pas")
}

func TestSequencesInscrire_DejaContacteSurChoixExplicite(t *testing.T) {
	e := newEnv(t)
	e.d.replies = []reply{{200, `{}`}, {200, `{"emails":["a@x.test"],"states":{"a@x.test":"contacted"}}`}}
	st, out := e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000001", `{"sequence_id":"s1","inclure_deja_contactes":true,"contacts":[{"email":"a@x.test"}]}`)
	require.Equal(t, 200, st)
	assert.Contains(t, e.d.bodies[1], `"include_contacted":true`)
	assert.Equal(t, map[string]string{"a@x.test": ""}, motifs(out))
}

func TestSequencesInscrire_RienAInscrire_AucuneRequete(t *testing.T) {
	e := newEnv(t)
	st, out := e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000001", `{"sequence_id":"s1","contacts":[{"email":"a@x.test","ne_plus_contacter":true}]}`)
	require.Equal(t, 200, st, out)
	assert.Empty(t, e.d.paths)
	assert.Empty(t, e.st.counted)

	// Tous écartés par la fiche : l'activation n'est pas appelée.
	e.d.replies = []reply{{200, `{"details":[{"email":"a@x.test","reason":"rebond"}]}`}}
	st, out = e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000002", enrollBody)
	require.Equal(t, 200, st)
	assert.Equal(t, map[string]string{"a@x.test": "rebond"}, motifs(out))
	assert.Equal(t, []string{"POST /api/veridian/contacts.intake"}, e.d.paths)
}

func TestSequencesInscrire_RefusDeLApp_CleRendue(t *testing.T) {
	for name, replies := range map[string][]reply{
		"la création démarrerait une autre séquence": {{409, `{"error":"démarrerait","code":"would_trigger"}`}},
		"séquence en pause":                          {{200, `{}`}, {400, `{"error":"la séquence n'est pas active"}`}},
		"droit manquant":                             {{200, `{}`}, {403, `{"error":"droit requis"}`}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.d.replies = replies
			st, out := e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000001", enrollBody)
			assert.Equal(t, replies[len(replies)-1].status, st)
			assert.Equal(t, "refus_de_l_app", out["error"])
			assert.Equal(t, 1, e.st.released)
			assert.Empty(t, e.st.done)
		})
	}
	// Réponse illisible : jamais « inscrit » sans preuve.
	e := newEnv(t)
	e.d.replies = []reply{{200, `{}`}, {200, `pas du json`}}
	st, _ := e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000001", enrollBody)
	assert.Equal(t, 503, st)
	assert.Equal(t, 1, e.st.released)
}

func TestSequencesInscrire_PlafondDuJour(t *testing.T) {
	e := newEnv(t)
	e.st.enrolled = maxEnrollPerDay
	st, out := e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000001", enrollBody)
	assert.Equal(t, 429, st)
	assert.Equal(t, "plafond_du_jour", out["error"])
	assert.Empty(t, e.d.paths, "rien n'est fait")
	assert.Equal(t, 1, e.st.released, "la clé est rendue")
	assert.Equal(t, []string{"ws1|app:crm|cle-000001|" + hubUser}, e.st.counted)

	e.st.enrolledErr = errors.New("base injoignable")
	e.st.enrolled = 0
	st, _ = e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000001", enrollBody)
	assert.Equal(t, 503, st, "sans compte, on n'inscrit pas")
	assert.Empty(t, e.d.paths)
	assert.Equal(t, 2, e.st.released)

	// La dernière place se prend ; un rejeu rend son résultat même plafond atteint ; un import n'est pas compté.
	e.st.enrolledErr, e.st.enrolled = nil, maxEnrollPerDay-1
	e.d.replies = []reply{{200, `{}`}, {200, `{"emails":["a@x.test"],"states":{"a@x.test":"ok"}}`}}
	st, _ = e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000001", enrollBody)
	assert.Equal(t, 200, st)
	e.st.enrolled = maxEnrollPerDay
	st, out = e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000001", enrollBody)
	assert.Equal(t, 200, st)
	assert.Equal(t, true, out["rejoue"])
	n := len(e.st.counted)
	st, _ = e.post(imp, e.hub.token(t, nil), "cle-import1", body)
	assert.Equal(t, 200, st)
	assert.Len(t, e.st.counted, n)
}

func TestSequencesInscrire_DemandeInvalide(t *testing.T) {
	many := []string{}
	for i := 0; i < 51; i++ {
		many = append(many, fmt.Sprintf(`{"email":"c%d@x.test"}`, i))
	}
	for name, b := range map[string]string{
		"sans séquence":       `{"contacts":[{"email":"a@x.test"}]}`,
		"sans personne":       `{"sequence_id":"s1","contacts":[]}`,
		"51 personnes":        `{"sequence_id":"s1","contacts":[` + strings.Join(many, ",") + `]}`,
		"champ inconnu":       `{"sequence_id":"s1","contacts":[{"email":"a@x.test"}],"workspace_id":"autre"}`,
		"champ inconnu fiche": `{"sequence_id":"s1","contacts":[{"email":"a@x.test","custom_number_1":9}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			st, _ := e.post("sequences.inscrire", e.hub.token(t, enrollScope), "cle-000001", b)
			assert.Equal(t, 400, st)
			assert.Empty(t, e.d.paths)
		})
	}
}

// Verser un contact ne donne pas le droit d'inscrire dans une séquence, et inversement.
func TestSequencesInscrire_PorteeSeparee(t *testing.T) {
	e := newEnv(t)
	st, out := e.post("sequences.inscrire", e.hub.token(t, func(c *mcp.Claims) { c.Scope = "notifuse:contacts.ecrire" }), "cle-000001", enrollBody)
	assert.Equal(t, 403, st)
	assert.Equal(t, "portee_insuffisante", out["error"])
	st, _ = e.post("sequences.lister", e.hub.token(t, enrollScope), "", "")
	assert.Equal(t, 403, st)
	st, out = e.post("sequences.inscrire", e.hub.token(t, enrollScope), "", enrollBody)
	assert.Equal(t, 400, st)
	assert.Equal(t, "cle_requise", out["error"])
	assert.Empty(t, e.d.paths)
}

func TestSequencesLister(t *testing.T) {
	e := newEnv(t)
	e.d.out = `{"automations":[{"id":"s1","name":"Relance","status":"live","list_id":"l1","nodes":[{"secret":"x"}]},{"id":"s2","name":"B","status":"draft"},{"id":"s3","name":"P","status":"paused"},{"id":"s4","name":"Z","status":"archived"}],"total":4}`
	st, out := e.post("sequences.lister", e.hub.token(t, nil), "", "")
	require.Equal(t, 200, st, out)
	got, _ := json.Marshal(out["resultat"])
	assert.JSONEq(t, `{"sequences":[{"id":"s1","name":"Relance","status":"live","list_id":"l1","inscriptible":true},{"id":"s2","name":"B","status":"draft","inscriptible":false,"raison":"brouillon"},
		{"id":"s3","name":"P","status":"paused","inscriptible":false,"raison":"en_pause"},{"id":"s4","name":"Z","status":"archived","inscriptible":false,"raison":"inactive"}]}`, string(got))
	assert.Equal(t, []string{"ws1"}, e.d.queries[0]["workspace_id"])
	assert.Empty(t, e.st.done)

	st, _ = e.post("sequences.lister", e.hub.token(t, nil), "", `{"workspace_id":"autre"}`)
	assert.Equal(t, 400, st)
	e.d.out = `[`
	st, _ = e.post("sequences.lister", e.hub.token(t, nil), "", "{}")
	assert.Equal(t, 503, st)
}
