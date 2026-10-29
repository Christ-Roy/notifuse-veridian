package policy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service/queue"
)

// --- Résolveur ---

func TestBuildSnapshot_SurchargesParDessusLeCatalogueSansLeModifier(t *testing.T) {
	presetCap := domain.VeridianPresetSnapshot().Caps["ovh"]
	s := BuildSnapshot([]Override{
		{Class: "ovh", DailyCap: 7, SlowdownThreshold: 0.05},
		{Class: "classe_orpheline", DailyCap: 1},
		{Class: "google"}, // ligne sans valeur : aucune surcharge
	})
	assert.Equal(t, 7, s.Caps["ovh"])
	assert.Equal(t, 7, s.Classes["ovh"].DailyCap)
	assert.Equal(t, 0.05, s.Classes["ovh"].SlowdownThreshold)
	assert.Equal(t, domain.VeridianPresetSnapshot().Rates["ovh"], s.Rates["ovh"], "débit non surchargé : preset")
	assert.True(t, s.Overridden["ovh"].DailyCap)
	assert.True(t, s.Overridden["ovh"].SlowdownThreshold)
	assert.False(t, s.Overridden["ovh"].RatePerMin)
	assert.NotContains(t, s.Caps, "classe_orpheline")
	assert.False(t, s.Overridden["google"].Any())
	assert.Equal(t, presetCap, domain.VeridianPresetSnapshot().Caps["ovh"], "le catalogue embarqué n'est jamais modifié")
}

func TestResolver_CacheTTLEtInvalidation(t *testing.T) {
	st := newFakeStore()
	st.overrides["ovh"] = Override{Class: "ovh", DailyCap: 7}
	r := NewResolver(st, noopLog{}, time.Minute)
	clock := time.Now()
	r.now = func() time.Time { return clock }

	assert.Equal(t, 7, r.PolicyFor("ws").Caps["ovh"])
	st.overrides["ovh"] = Override{Class: "ovh", DailyCap: 9}
	assert.Equal(t, 7, r.PolicyFor("ws").Caps["ovh"], "dans le TTL : la valeur en cache")
	assert.Equal(t, 1, st.reads)

	r.Invalidate("ws")
	assert.Equal(t, 9, r.PolicyFor("ws").Caps["ovh"], "après invalidation : relu tout de suite")

	st.overrides["ovh"] = Override{Class: "ovh", DailyCap: 11}
	clock = clock.Add(2 * time.Minute)
	assert.Equal(t, 11, r.PolicyFor("ws").Caps["ovh"], "TTL écoulé : relu")
}

func TestResolver_BaseIllisibleGardeLaDerniereValeurPuisLesPresets(t *testing.T) {
	st := newFakeStore()
	st.overrides["ovh"] = Override{Class: "ovh", DailyCap: 7}
	r := NewResolver(st, noopLog{}, time.Minute)
	clock := time.Now()
	r.now = func() time.Time { return clock }

	// Jamais lu et base en panne : presets, jamais d'erreur, jamais nil.
	st.failList = errBoom
	snap := r.PolicyFor("ws-a")
	require.NotNil(t, snap)
	assert.Equal(t, domain.VeridianPresetSnapshot().Caps["ovh"], snap.Caps["ovh"])

	// Une valeur connue puis une panne : la dernière valeur connue sert, sans relire à chaque appel.
	st.failList = nil
	assert.Equal(t, 7, r.PolicyFor("ws-b").Caps["ovh"])
	clock = clock.Add(2 * time.Minute)
	st.failList = errBoom
	readsBefore := st.reads
	assert.Equal(t, 7, r.PolicyFor("ws-b").Caps["ovh"], "panne : dernière valeur connue")
	assert.Equal(t, 7, r.PolicyFor("ws-b").Caps["ovh"])
	assert.Equal(t, readsBefore+1, st.reads, "pas de relecture à chaque appel pendant le délai de réessai")
}

func TestResolver_Priorites(t *testing.T) {
	st := newFakeStore()
	st.prios["broadcast:b1"] = PrioritySource{Priority: 2}
	r := NewResolver(st, noopLog{}, time.Minute)
	assert.Equal(t, 2, r.SendPriorityFor("ws", domain.EmailQueueSourceBroadcast, "b1"))
	assert.Equal(t, 0, r.SendPriorityFor("ws", domain.EmailQueueSourceBroadcast, "b2"), "non posée : 0 (le domaine met 5)")
	assert.Equal(t, 0, r.SendPriorityFor("ws", domain.EmailQueueSourceAutomation, "b1"), "même id, autre type : pas de mélange")
	st.failList = errBoom
	r.Invalidate("ws")
	assert.Equal(t, 0, r.SendPriorityFor("ws", domain.EmailQueueSourceBroadcast, "b1"), "base en panne sans cache : défaut")
}

// Un test de câblage : le texte des explications cite le même minimum d'envois que le fusible.
func TestMinSentForSlowdown_EgaleLeFusibleDuWorker(t *testing.T) {
	assert.Equal(t, queue.VeridianReputationMinSent, MinSentForSlowdown)
}

// --- Explications ---

func TestBuildView_ExplicationLisibleEtOrigineDeChaqueRegle(t *testing.T) {
	cat := domain.VeridianClassPresets()
	g, _ := domain.VeridianClassPresetFor("google")
	snap := BuildSnapshot([]Override{{Class: "google", DailyCap: 12}})
	sh := Shadows{DailyCap: map[string][]string{"google": {"profil « Communication ASD »"}}, Rate: map[string][]string{}}
	v := BuildView(g, cat, snap, sh, &domain.VeridianObservedEngagement{Sent: 200, Bounced: 1, RepliedHuman: 14})

	assert.Equal(t, 12, v.DailyCap.Value)
	assert.Equal(t, SourceWorkspaceOverride, v.DailyCap.Source)
	assert.Equal(t, g.DailyCap, v.DailyCap.Preset)
	assert.Equal(t, SourcePreset, v.RatePerMin.Source)
	assert.Len(t, v.Rules, 4)
	assert.Contains(t, v.Rules[0].Text, "12 mails par jour")
	assert.Contains(t, v.Rules[0].Text, "réglé pour ce workspace")
	assert.Contains(t, v.Rules[0].Text, "Masquée pour : profil « Communication ASD »")
	assert.Contains(t, v.Rules[1].Text, "1 mail toutes les 3,3 minutes")
	assert.Contains(t, v.Rules[1].Text, "règle par défaut")
	assert.Contains(t, v.Rules[2].Text, "50 %")
	assert.Contains(t, v.Rules[3].Text, "2 % des envois")
	assert.Contains(t, v.Rules[3].Text, "par 4 à partir de 4 %")
	assert.Contains(t, v.Rules[3].Text, "20 envois")
	assert.Equal(t, "workspace", v.Observed.Basis)
	assert.Contains(t, v.Explanation, "14 réponses humaines sur 200 envois (7 %), 1 rebond")
	assert.Contains(t, v.Explanation, "Google")
	assert.NotContains(t, v.Explanation, " — ", "pas de tiret cadratin entouré d'espaces")
}

func TestBuildObserved_MesureDuCatalogueSiLeWorkspaceNaPasEnvoye(t *testing.T) {
	cat := domain.VeridianClassPresets()
	g, _ := domain.VeridianClassPresetFor("google")
	o := BuildObserved(g, cat, nil)
	assert.Equal(t, "catalog", o.Basis)
	assert.Equal(t, g.Observed.Sent, o.Sent)
	o = BuildObserved(g, cat, &domain.VeridianObservedEngagement{})
	assert.Equal(t, "catalog", o.Basis, "workspace à zéro envoi : catalogue")
	o = BuildObserved(g, cat, &domain.VeridianObservedEngagement{Sent: 10, RepliedHuman: 5})
	assert.Equal(t, "workspace", o.Basis)
	require.NotNil(t, o.ReplyRate)
	assert.InDelta(t, 0.5, *o.ReplyRate, 1e-9)
	assert.Less(t, o.ReplyRateSmoothed, 0.5, "10 envois ne suffisent pas : le taux est ramené vers la moyenne")
}

func TestSmooth(t *testing.T) {
	prior := domain.VeridianObservedEngagement{Sent: 1000, RepliedHuman: 30}
	assert.InDelta(t, 0.03, Smooth(0, 0, prior, 30), 1e-9, "sans envoi : la moyenne")
	assert.InDelta(t, 0.03, Smooth(3, 100, prior, 30)-0.0, 0.0155, "100 envois : entre la mesure et la moyenne")
	big := Smooth(1000, 10000, prior, 30)
	assert.InDelta(t, 0.1, big, 0.002, "gros échantillon : la mesure")
	assert.Equal(t, 0.0, Smooth(0, 0, domain.VeridianObservedEngagement{}, 30), "pas d'a priori : 0, pas de division par zéro")
}

func TestFormatting(t *testing.T) {
	assert.Equal(t, "2 %", fmtPct(0.02))
	assert.Equal(t, "7,1 %", fmtPct(0.0707))
	assert.Equal(t, "1 mail toutes les 10 minutes", fmtRate(0.1))
	assert.Equal(t, "2,5 mails par minute", fmtRate(2.5))
	assert.Equal(t, "non bridée", fmtRate(0))
}

func TestComputeShadows(t *testing.T) {
	ws := &domain.Workspace{
		Settings: domain.WorkspaceSettings{VeridianProviderClassDailyCap: map[string]int{domain.ProviderClassFreemailFR: 5}},
		Integrations: []domain.Integration{
			{Name: "Communication ASD", Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{VeridianProviderClassDailyCap: map[string]int{"google": 10}}},
			{Name: "Brevo", Type: domain.IntegrationTypeIMAP},
		},
	}
	sh := ComputeShadows(ws)
	assert.Equal(t, []string{`profil « Communication ASD »`}, sh.DailyCap["google"])
	assert.Equal(t, []string{"le réglage du workspace"}, sh.DailyCap["free"], "héritage freemail_fr -> free")
	assert.NotContains(t, sh.DailyCap, "ovh")
	assert.Empty(t, ComputeShadows(nil).DailyCap)
}

// --- Service ---

type fakeWS struct{ ws *domain.Workspace }

func (f fakeWS) GetByID(context.Context, string) (*domain.Workspace, error) { return f.ws, nil }

func newTestService(t *testing.T, auth *fakeAuth) (*Service, *fakeStore, *Resolver) {
	t.Helper()
	st := newFakeStore()
	r := NewResolver(st, noopLog{}, time.Minute)
	return NewService(auth, fakeWS{ws: &domain.Workspace{ID: "ws"}}, st, nil, r, noopLog{}), st, r
}

func perms(read, write bool, resources ...domain.PermissionResource) domain.UserPermissions {
	p := domain.UserPermissions{}
	for _, r := range resources {
		p[r] = domain.ResourcePermissions{Read: read, Write: write}
	}
	return p
}

func TestService_GetLitLaPolitiqueExpliquee(t *testing.T) {
	svc, st, _ := newTestService(t, &fakeAuth{owner: true})
	st.overrides["ovh"] = Override{Class: "ovh", DailyCap: 7}
	v, err := svc.Get(context.Background(), "ws", "")
	require.NoError(t, err)
	assert.Len(t, v.Classes, len(domain.VeridianAllProviderClasses()))
	assert.Equal(t, 1, v.Overrides)
	one, err := svc.Get(context.Background(), "ws", "ovh")
	require.NoError(t, err)
	require.Len(t, one.Classes, 1)
	assert.Equal(t, 7, one.Classes[0].DailyCap.Value)
	assert.Equal(t, SourceWorkspaceOverride, one.Classes[0].DailyCap.Source)
	_, err = svc.Get(context.Background(), "ws", "nimporte")
	var ve *ValidationError
	assert.ErrorAs(t, err, &ve)
}

func TestService_SetEtResetEcriventLaSurchargeEtInvalidentLeCache(t *testing.T) {
	svc, st, r := newTestService(t, &fakeAuth{owner: true, email: "claude@example.test"})
	ctx := context.Background()

	assert.Equal(t, 40, r.PolicyFor("ws").Caps["ovh"], "preset avant")
	res, err := svc.Set(ctx, SetRequest{WorkspaceID: "ws", Class: "ovh", DailyCap: 7, Reason: "rebonds du 28/10"})
	require.NoError(t, err)
	assert.Equal(t, 7, res.Class.DailyCap.Value)
	assert.Equal(t, "claude@example.test", st.overrides["ovh"].UpdatedBy)
	assert.Equal(t, 7, r.PolicyFor("ws").Caps["ovh"], "le worker voit la valeur tout de suite (cache invalidé)")

	// Deuxième règle : la première est conservée.
	_, err = svc.Set(ctx, SetRequest{WorkspaceID: "ws", Class: "ovh", SlowdownThreshold: 0.05})
	require.NoError(t, err)
	assert.Equal(t, 7, st.overrides["ovh"].DailyCap)
	assert.Equal(t, 0.05, st.overrides["ovh"].SlowdownThreshold)

	// Retrait d'une règle puis de tout.
	res, err = svc.Reset(ctx, "ws", "ovh", []string{RuleDailyCap})
	require.NoError(t, err)
	assert.Equal(t, 40, res.Class.DailyCap.Value)
	assert.Equal(t, SourcePreset, res.Class.DailyCap.Source)
	assert.Equal(t, 0.05, res.Class.SlowdownThreshold.Value)
	_, err = svc.Reset(ctx, "ws", "ovh", nil)
	require.NoError(t, err)
	assert.Empty(t, st.overrides)
	assert.Equal(t, 40, r.PolicyFor("ws").Caps["ovh"])
}

func TestService_SetRefuseLesEntreesInvalides(t *testing.T) {
	svc, st, _ := newTestService(t, &fakeAuth{owner: true})
	ctx := context.Background()
	bad := []SetRequest{
		{WorkspaceID: "ws", Class: "nimporte", DailyCap: 5},
		{WorkspaceID: "ws", Class: "ovh"},
		{WorkspaceID: "ws", Class: "ovh", DailyCap: 5000},
		{WorkspaceID: "ws", Class: "ovh", DailyCap: -3},
		{WorkspaceID: "ws", Class: "ovh", RatePerMin: 99},
		{WorkspaceID: "ws", Class: "ovh", WarmupShare: 2},
		{WorkspaceID: "ws", Class: "ovh", SlowdownThreshold: 0.9},
		{WorkspaceID: "ws", Class: "ovh", DailyCap: 5, Reason: strings.Repeat("x", 301)},
		{Class: "ovh", DailyCap: 5},
	}
	for i, req := range bad {
		_, err := svc.Set(ctx, req)
		var ve *ValidationError
		assert.ErrorAs(t, err, &ve, "cas %d", i)
	}
	assert.Empty(t, st.overrides, "rien d'écrit sur une entrée refusée")
	_, err := svc.Reset(ctx, "ws", "ovh", []string{"inconnue"})
	var ve *ValidationError
	assert.ErrorAs(t, err, &ve)
}

func TestService_SetAvertitQuandUnProfilMasqueLaValeur(t *testing.T) {
	st := newFakeStore()
	r := NewResolver(st, noopLog{}, time.Minute)
	ws := &domain.Workspace{ID: "ws", Integrations: []domain.Integration{
		{Name: "Infomaniak", Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{VeridianProviderClassDailyCap: map[string]int{"google": 10}}}}}
	svc := NewService(&fakeAuth{owner: true}, fakeWS{ws: ws}, st, nil, r, noopLog{})
	res, err := svc.Set(context.Background(), SetRequest{WorkspaceID: "ws", Class: "google", DailyCap: 25})
	require.NoError(t, err)
	require.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "Infomaniak")
}

func TestService_PermissionsEtAuthentificationAvantToutAcces(t *testing.T) {
	ctx := context.Background()
	// Authentification refusée : rien ne se lit ni ne s'écrit.
	svc, st, _ := newTestService(t, &fakeAuth{err: errBoom})
	_, err := svc.Get(ctx, "ws", "")
	assert.Error(t, err)
	_, err = svc.Set(ctx, SetRequest{WorkspaceID: "ws", Class: "ovh", DailyCap: 5})
	assert.Error(t, err)
	assert.Zero(t, st.reads)
	assert.Empty(t, st.overrides)

	// Membre en lecture seule : peut lire la politique, pas la modifier.
	svc, st, _ = newTestService(t, &fakeAuth{perms: perms(true, false, domain.PermissionResourceWorkspace)})
	_, err = svc.Get(ctx, "ws", "")
	assert.NoError(t, err)
	_, err = svc.Set(ctx, SetRequest{WorkspaceID: "ws", Class: "ovh", DailyCap: 5})
	var perm *domain.PermissionError
	assert.ErrorAs(t, err, &perm)
	_, err = svc.Reset(ctx, "ws", "ovh", nil)
	assert.ErrorAs(t, err, &perm)
	assert.Empty(t, st.overrides)

	// Sans droit sur le workspace : refus même en lecture.
	svc, _, _ = newTestService(t, &fakeAuth{perms: perms(true, true, domain.PermissionResourceContacts)})
	_, err = svc.Get(ctx, "ws", "")
	assert.ErrorAs(t, err, &perm)
}

func TestService_Priorites(t *testing.T) {
	ctx := context.Background()
	svc, st, r := newTestService(t, &fakeAuth{owner: true})
	st.sources["broadcast:b1"] = "zz-sprint-camp"
	st.sources["automation:a1"] = "zz-sprint-seq"
	st.pending["broadcast:b1"] = 12

	list, err := svc.ListPriorities(ctx, "ws")
	require.NoError(t, err)
	assert.Equal(t, 5, list.Default)
	assert.Equal(t, 2, list.Min)
	assert.Equal(t, 9, list.Max)
	require.Len(t, list.Sources, 2)
	assert.Equal(t, 5, list.Sources[0].Priority)
	assert.False(t, list.Sources[0].Explicit)
	assert.Contains(t, list.Note, "engorgée")

	ch, err := svc.SetPriority(ctx, "ws", "broadcast", "b1", 2)
	require.NoError(t, err)
	assert.Equal(t, 2, ch.Priority)
	assert.EqualValues(t, 12, ch.RequeuedPending)
	assert.Equal(t, 2, r.SendPriorityFor("ws", domain.EmailQueueSourceBroadcast, "b1"), "visible tout de suite par les enqueues")

	ch, err = svc.ClearPriority(ctx, "ws", "broadcast", "b1")
	require.NoError(t, err)
	assert.Equal(t, 5, ch.Priority)
	assert.Equal(t, 0, r.SendPriorityFor("ws", domain.EmailQueueSourceBroadcast, "b1"))

	for _, p := range []int{0, 1, 10, -2} {
		_, err = svc.SetPriority(ctx, "ws", "broadcast", "b1", p)
		var ve *ValidationError
		assert.ErrorAs(t, err, &ve, "priorité %d", p)
	}
	_, err = svc.SetPriority(ctx, "ws", "broadcast", "inconnue", 3)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = svc.SetPriority(ctx, "ws", "segment", "b1", 3)
	var ve *ValidationError
	assert.ErrorAs(t, err, &ve)
	_, err = svc.SetPriority(ctx, "ws", "broadcast", "", 3)
	assert.ErrorAs(t, err, &ve)
}

func TestService_PrioritesPermissionsParType(t *testing.T) {
	ctx := context.Background()
	// Écriture sur les campagnes seulement : l'automation est refusée.
	svc, st, _ := newTestService(t, &fakeAuth{perms: perms(true, true, domain.PermissionResourceBroadcasts)})
	st.sources["broadcast:b1"] = "x"
	st.sources["automation:a1"] = "y"
	_, err := svc.SetPriority(ctx, "ws", "broadcast", "b1", 3)
	assert.NoError(t, err)
	var perm *domain.PermissionError
	_, err = svc.SetPriority(ctx, "ws", "automation", "a1", 3)
	assert.ErrorAs(t, err, &perm)
	_, err = svc.ListPriorities(ctx, "ws")
	assert.ErrorAs(t, err, &perm, "lister exige aussi la lecture des automations")
}

// --- Handler ---

func newTestHandler(t *testing.T, auth *fakeAuth) (*Handler, *fakeStore) {
	svc, st, _ := newTestService(t, auth)
	return NewHandler(svc, func() ([]byte, error) { return []byte("x"), nil }, noopLog{}), st
}

func call(h http.HandlerFunc, method, url, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestHandler_RoutesEtCodesHTTP(t *testing.T) {
	h, st := newTestHandler(t, &fakeAuth{owner: true})
	st.sources["broadcast:b1"] = "zz-sprint-camp"

	rec := call(h.get, "GET", "/api/veridian/policy.get?workspace_id=ws&class=google", "")
	assert.Equal(t, 200, rec.Code)
	var v PolicyView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	require.Len(t, v.Classes, 1)
	assert.Equal(t, "google", v.Classes[0].Class)
	assert.NotEmpty(t, v.Classes[0].Explanation)

	assert.Equal(t, 400, call(h.get, "GET", "/api/veridian/policy.get", "").Code, "workspace_id requis")
	assert.Equal(t, 400, call(h.get, "GET", "/api/veridian/policy.get?workspace_id=ws&class=zzz", "").Code)
	assert.Equal(t, 400, call(h.set, "POST", "/x", "{pas du json").Code)

	rec = call(h.set, "POST", "/x", `{"workspace_id":"ws","class":"ovh","daily_cap":7,"slowdown_threshold":0.05,"reason":"test"}`)
	assert.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Equal(t, 7, st.overrides["ovh"].DailyCap)
	assert.Equal(t, 400, call(h.set, "POST", "/x", `{"workspace_id":"ws","class":"ovh","daily_cap":5000}`).Code)
	assert.Equal(t, 400, call(h.set, "POST", "/x", `{"workspace_id":"ws","class":"ovh","daily_cap":"abc"}`).Code)
	assert.Equal(t, 400, call(h.set, "POST", "/x", `{"workspace_id":"ws","class":"ovh","daily_cap":2.5}`).Code, "entier exigé")

	assert.Equal(t, 200, call(h.reset, "POST", "/x", `{"workspace_id":"ws","class":"ovh","rules":["daily_cap"]}`).Code)
	assert.Equal(t, 0, st.overrides["ovh"].DailyCap)

	assert.Equal(t, 200, call(h.priorityList, "GET", "/x?workspace_id=ws", "").Code)
	assert.Equal(t, 200, call(h.prioritySet, "POST", "/x", `{"workspace_id":"ws","source_type":"broadcast","source_id":"b1","priority":3}`).Code)
	assert.Equal(t, 3, st.prios["broadcast:b1"].Priority)
	assert.Equal(t, 400, call(h.prioritySet, "POST", "/x", `{"workspace_id":"ws","source_type":"broadcast","source_id":"b1","priority":1}`).Code)
	assert.Equal(t, 400, call(h.prioritySet, "POST", "/x", `{"workspace_id":"ws","source_type":"broadcast","source_id":"b1"}`).Code, "priority requis")
	assert.Equal(t, 404, call(h.prioritySet, "POST", "/x", `{"workspace_id":"ws","source_type":"broadcast","source_id":"nope","priority":3}`).Code)
	assert.Equal(t, 200, call(h.priorityClear, "POST", "/x", `{"workspace_id":"ws","source_type":"broadcast","source_id":"b1"}`).Code)
	assert.NotContains(t, st.prios, "broadcast:b1")
}

func TestHandler_ErreursDAuthentificationEtDePermission(t *testing.T) {
	h, _ := newTestHandler(t, &fakeAuth{err: &domain.ErrAuthenticationFailed{Message: "clé révoquée"}})
	assert.Equal(t, 401, call(h.get, "GET", "/x?workspace_id=ws", "").Code)
	h, _ = newTestHandler(t, &fakeAuth{perms: perms(true, false, domain.PermissionResourceWorkspace)})
	assert.Equal(t, 403, call(h.set, "POST", "/x", `{"workspace_id":"ws","class":"ovh","daily_cap":5}`).Code)
	h, _ = newTestHandler(t, &fakeAuth{owner: true})
	h.svc.store = failingStore{newFakeStore()}
	assert.Equal(t, 500, call(h.get, "GET", "/x?workspace_id=ws", "").Code)
}

type failingStore struct{ *fakeStore }

func (failingStore) ListOverrides(context.Context, string) ([]Override, error) { return nil, errBoom }

func TestHandler_LesRoutesSontMontees(t *testing.T) {
	h, _ := newTestHandler(t, &fakeAuth{owner: true})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	for _, path := range []string{"policy.get", "policy.set", "policy.reset", "policy.priority.list", "policy.priority.set", "policy.priority.clear"} {
		req := httptest.NewRequest("POST", "/api/veridian/"+path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, 401, rec.Code, "%s existe et exige une authentification", path)
	}
}
