package iafils

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/http/middleware"
)

const (
	secretHub = "secret-de-test-des-fils"
	session1  = "33333333-3333-4333-8333-333333333333"
	autreUser = "44444444-4444-4444-8444-444444444444"
	autreOrg  = "55555555-5555-4555-8555-555555555555"
)

// memStore : la base en mémoire. Un seul espace, "ws-1", relié à hubTenant ; seuls les membres listés y entrent.
type memStore struct {
	mu      sync.Mutex
	membres map[string]bool // hub_user_id -> membre
	fils    map[string]struct{ ws, user, tenant, messages string }
}

func newMem() *memStore {
	return &memStore{membres: map[string]bool{hubUser: true}, fils: map[string]struct{ ws, user, tenant, messages string }{}}
}

func (m *memStore) Resolve(_ context.Context, tenant, user string) (string, error) {
	if tenant != hubTenant {
		return "", ErrUnlinked
	}
	if !m.membres[user] {
		return "", ErrNotMember
	}
	return "ws-1", nil
}

func (m *memStore) Owner(_ context.Context, s string) (string, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.fils[s]
	return f.user, f.tenant, ok, nil
}

func (m *memStore) Get(_ context.Context, s, user, tenant string) (json.RawMessage, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.fils[s]
	if !ok || f.user != user || f.tenant != tenant {
		return nil, false, nil
	}
	return json.RawMessage(f.messages), true, nil
}

func (m *memStore) Put(_ context.Context, s, ws, user, tenant string, msgs json.RawMessage, _ int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.fils[s]; ok && (f.user != user || f.tenant != tenant) {
		return false, nil
	}
	m.fils[s] = struct{ ws, user, tenant, messages string }{ws, user, tenant, string(msgs)}
	return true, nil
}

func serveur(t *testing.T) (*http.ServeMux, *memStore) {
	t.Helper()
	mux := http.NewServeMux()
	st := newMem()
	h := &Handler{Store: st}
	Mount(mux, h, &EtatHandler{Store: fauxEtatStore{}, Auth: fauxAuth{}, Client: &fauxClient{}}, secretHub, func(n http.Handler) http.Handler { return n })
	return mux, st
}

func msg(i int, texte string) map[string]any {
	return map[string]any{"id": "m" + string(rune('a'+i%26)), "role": "user", "parts": []any{map[string]any{"type": "text", "text": texte}}}
}

// signe : la requête d'un Hub honnête (v2 : méthode, chemin, query, empreinte du corps, usage unique).
func signe(method, url string, corps []byte) *http.Request {
	time.Sleep(2 * time.Millisecond) // le Hub signe à la milliseconde : deux appels identiques de la même ms seraient un rejeu
	r := httptest.NewRequest(method, url, bytes.NewReader(corps))
	r.Header.Set("Content-Type", "application/json")
	middleware.SignVeridianHubRequest(r, secretHub, corps)
	return r
}

func put(t *testing.T, mux http.Handler, session, user, tenant string, messages any) *httptest.ResponseRecorder {
	t.Helper()
	corps, err := json.Marshal(map[string]any{"hub_user_id": user, "tenant_id": tenant, "messages": messages})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, signe(http.MethodPut, "/api/hub/ia/fils/"+session, corps))
	return rec
}

func get(mux http.Handler, session, user, tenant string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, signe(http.MethodGet, "/api/hub/ia/fils/"+session+"?hub_user_id="+user+"&tenant_id="+tenant, nil))
	return rec
}

func code(rec *httptest.ResponseRecorder) string {
	var o map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &o)
	c, _ := o["error"].(string)
	return c
}

func TestFils_NonSigne_401_EtSansSecret_503(t *testing.T) {
	mux, st := serveur(t)
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(m, "/api/hub/ia/fils/"+session1+"?hub_user_id="+hubUser+"&tenant_id="+hubTenant, strings.NewReader(`{}`)))
		assert.Equal(t, 401, rec.Code, m)
	}
	// Signature d'un autre secret : refusée.
	corps := []byte(`{"hub_user_id":"` + hubUser + `","tenant_id":"` + hubTenant + `","messages":[]}`)
	r := httptest.NewRequest(http.MethodPut, "/api/hub/ia/fils/"+session1, bytes.NewReader(corps))
	middleware.SignVeridianHubRequest(r, "un-autre-secret", corps)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	assert.Equal(t, 401, rec.Code)
	assert.Empty(t, st.fils, "rien n'est écrit sans signature valide")

	sans := http.NewServeMux()
	Mount(sans, &Handler{Store: st}, &EtatHandler{}, "", func(n http.Handler) http.Handler { return n })
	rec = httptest.NewRecorder()
	sans.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hub/ia/fils/"+session1, nil))
	assert.Equal(t, 503, rec.Code)
}

func TestFils_RejeuV2_401(t *testing.T) {
	mux, _ := serveur(t)
	corps, _ := json.Marshal(map[string]any{"hub_user_id": hubUser, "tenant_id": hubTenant, "messages": []any{msg(1, "a")}})
	r := signe(http.MethodPut, "/api/hub/ia/fils/"+session1, corps)
	rejeu := httptest.NewRequest(http.MethodPut, "/api/hub/ia/fils/"+session1, bytes.NewReader(corps))
	rejeu.Header = r.Header.Clone()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	assert.Equal(t, 200, rec.Code)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, rejeu)
	assert.Equal(t, 401, rec.Code, "la même signature v2 ne sert qu'une fois")
}

func TestFils_OrigineNavigateur_Refusee_MemeSigne(t *testing.T) {
	mux, st := serveur(t)
	corps, _ := json.Marshal(map[string]any{"hub_user_id": hubUser, "tenant_id": hubTenant, "messages": []any{}})
	for en, v := range map[string]string{"Origin": "https://notifuse.app.veridian.site", "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Dest": "empty"} {
		r := signe(http.MethodPut, "/api/hub/ia/fils/"+session1, corps)
		r.Header.Set(en, v)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		assert.Equal(t, 403, rec.Code, en)
		assert.Equal(t, "browser_origin_refused", code(rec))
	}
	assert.Empty(t, st.fils)
	// Témoin : le même appel sans en-tête de navigateur passe.
	assert.Equal(t, 200, put(t, mux, session1, hubUser, hubTenant, []any{}).Code)
}

// Le Hub appelle avec le fetch de Node (undici), qui ajoute de lui-même Sec-Fetch-Mode: cors. Mesuré le 27/11 (cas 56 du Hub, 403 à
// 00:59:44Z) : un appel signé du Hub portant cet en-tête seul doit passer, sinon le fil n'est jamais rangé.
func TestFils_HubAvecSecFetchModeSeul_Passe(t *testing.T) {
	mux, st := serveur(t)
	corps, _ := json.Marshal(map[string]any{"hub_user_id": hubUser, "tenant_id": hubTenant, "messages": []any{map[string]any{"id": "m1", "role": "user", "parts": []any{}}}})
	r := signe(http.MethodPut, "/api/hub/ia/fils/"+session1, corps)
	r.Header.Set("Sec-Fetch-Mode", "cors")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	assert.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Len(t, st.fils, 1)
}

func TestFils_PutPuisGet_MemesMessages_TelsQuels(t *testing.T) {
	mux, _ := serveur(t)
	messages := []any{
		map[string]any{"id": "m1", "role": "user", "parts": []any{map[string]any{"type": "text", "text": "Bonjour, é à ü « »"}}, "metadata": map[string]any{"z": 1, "a": []any{true, nil}}},
		msg(2, "réponse"),
	}
	rec := put(t, mux, session1, hubUser, hubTenant, messages)
	require.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{}`, rec.Body.String())
	rec = get(mux, session1, hubUser, hubTenant)
	require.Equal(t, 200, rec.Code)
	var out struct {
		Messages []any `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	attendu, _ := json.Marshal(messages)
	rendu, _ := json.Marshal(out.Messages)
	assert.JSONEq(t, string(attendu), string(rendu))
	// PUT remplace le fil entier.
	require.Equal(t, 200, put(t, mux, session1, hubUser, hubTenant, []any{msg(3, "seul")}).Code)
	rec = get(mux, session1, hubUser, hubTenant)
	out.Messages = nil
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Len(t, out.Messages, 1)
}

func TestFils_AutreTriplet_404EnGet_409EnPut_RienEcrit(t *testing.T) {
	mux, st := serveur(t)
	st.membres[autreUser] = true
	require.Equal(t, 200, put(t, mux, session1, hubUser, hubTenant, []any{msg(1, "secret")}).Code)
	avant := st.fils[session1]
	for nom, tr := range map[string][2]string{"autre personne": {autreUser, hubTenant}, "autre organisation": {hubUser, autreOrg}} {
		rec := get(mux, session1, tr[0], tr[1])
		assert.Equal(t, 404, rec.Code, nom)
		assert.NotContains(t, rec.Body.String(), "secret")
		rec = put(t, mux, session1, tr[0], tr[1], []any{msg(9, "pirate")})
		assert.Equal(t, 409, rec.Code, nom)
		assert.Equal(t, "fil_autre_auteur", code(rec))
	}
	assert.Equal(t, avant, st.fils[session1], "le 409 n'écrit rien")
	// Une session qui n'existe pas : 404.
	assert.Equal(t, 404, get(mux, "66666666-6666-4666-8666-666666666666", hubUser, hubTenant).Code)
}

func TestFils_PersonneRetiree_404EnGet_403EnPut(t *testing.T) {
	mux, st := serveur(t)
	require.Equal(t, 200, put(t, mux, session1, hubUser, hubTenant, []any{msg(1, "a")}).Code)
	delete(st.membres, hubUser)
	assert.Equal(t, 404, get(mux, session1, hubUser, hubTenant).Code)
	rec := put(t, mux, session1, hubUser, hubTenant, []any{msg(1, "b")})
	assert.Equal(t, 403, rec.Code)
	assert.Equal(t, "member_removed", code(rec))
}

func TestFils_BornesDuContrat(t *testing.T) {
	mux, _ := serveur(t)
	const s = "77777777-7777-4777-8777-777777777777"
	enveloppe := len(`{"hub_user_id":"` + hubUser + `","tenant_id":"` + hubTenant + `","messages":[{"id":"m","role":"user","parts":[{"type":"text","text":""}]}]}`)
	corps := func(n int) []any {
		return []any{map[string]any{"id": "m", "role": "user", "parts": []any{map[string]any{"type": "text", "text": strings.Repeat("a", n)}}}}
	}
	assert.Equal(t, 200, put(t, mux, s, hubUser, hubTenant, corps(MaxBodyBytes-enveloppe)).Code, "juste sous 1 Mo")
	rec := put(t, mux, s, hubUser, hubTenant, corps(MaxBodyBytes-enveloppe+1))
	assert.Equal(t, 413, rec.Code, "au-delà de 1 Mo")
	assert.Equal(t, "fil_trop_grand", code(rec))
	// Les octets, pas les caractères : « é » pèse 2 octets.
	assert.Equal(t, 413, put(t, mux, s, hubUser, hubTenant, []any{map[string]any{"id": "m", "role": "user", "parts": []any{map[string]any{"text": strings.Repeat("é", 600_000)}}}}).Code)
	// Plus de 1 MiB : refusé par la porte de signature elle-même, même réponse.
	assert.Equal(t, 413, put(t, mux, s, hubUser, hubTenant, corps(2<<20)).Code)

	n := func(k int) []any {
		m := make([]any, k)
		for i := range m {
			m[i] = msg(i, "x")
		}
		return m
	}
	assert.Equal(t, 200, put(t, mux, s, hubUser, hubTenant, n(MaxMessages)).Code)
	rec = put(t, mux, s, hubUser, hubTenant, n(MaxMessages+1))
	assert.Equal(t, 413, rec.Code)
	assert.Equal(t, "fil_trop_long", code(rec))
}

func TestFils_CorpsInvalides_400(t *testing.T) {
	mux, st := serveur(t)
	for nom, c := range map[string]string{
		"pas du json":       `{`,
		"sans messages":     `{"hub_user_id":"` + hubUser + `","tenant_id":"` + hubTenant + `"}`,
		"user pas un uuid":  `{"hub_user_id":"moi","tenant_id":"` + hubTenant + `","messages":[]}`,
		"message non objet": `{"hub_user_id":"` + hubUser + `","tenant_id":"` + hubTenant + `","messages":["texte"]}`,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, signe(http.MethodPut, "/api/hub/ia/fils/"+session1, []byte(c)))
		assert.Equal(t, 400, rec.Code, nom)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, signe(http.MethodPut, "/api/hub/ia/fils/pas-un-uuid", []byte(`{}`)))
	assert.Equal(t, 400, rec.Code)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, signe(http.MethodGet, "/api/hub/ia/fils/"+session1+"?hub_user_id=moi&tenant_id="+hubTenant, nil))
	assert.Equal(t, 400, rec.Code)
	assert.Empty(t, st.fils)
}

func TestFils_OrganisationNonReliee_404EnPut(t *testing.T) {
	mux, _ := serveur(t)
	rec := put(t, mux, session1, hubUser, autreOrg, []any{})
	assert.Equal(t, 404, rec.Code)
	assert.Equal(t, "espace_non_relie", code(rec))
}
