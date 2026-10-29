package policy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

func byClass(rows []SuggestRow) map[string]SuggestRow {
	m := map[string]SuggestRow{}
	for _, r := range rows {
		m[r.Class] = r
	}
	return m
}

func TestBuildSuggestion_TientLHorizonSansDepasserLesPlafonds(t *testing.T) {
	capacity := map[string]Capacity{
		"google": {Nominal: 10}, // horizon 3 j = 30
		"ovh":    {Nominal: 8},  // 24
		"free":   {Nominal: 5},  // 15
		"orange": {Nominal: 0},  // aucune capacité : on n'inscrit pas
		"zoho":   {Nominal: 4},  // 12
	}
	enrolled := map[string]ListCounts{"google": {Unsent: 12}, "ovh": {Unsent: 0}, "free": {Unsent: 40}, "zoho": {Unsent: 2}}
	reserve := map[string]ListCounts{"google": {Unsent: 500}, "ovh": {Unsent: 10}, "free": {Unsent: 100}, "orange": {Unsent: 300}, "zoho": {Unsent: 0}}
	followups := map[string]int{"google": 4, "zoho": 20}
	rows, tot, restock := BuildSuggestion(3, enrolled, reserve, capacity, followups, func(c string) float64 {
		return map[string]float64{"ovh": 0.06, "zoho": 0.02}[c]
	})
	by := byClass(rows)
	assert.NotContains(t, by, "orange", "capacité nulle : jamais de suggestion")

	g := by["google"]
	assert.Equal(t, 30, g.HorizonCapacity)
	assert.Equal(t, 26, g.RoomForNew, "30 - 4 relances")
	assert.Equal(t, 14, g.Gap, "26 - 12 déjà inscrits non envoyés")
	assert.Equal(t, 14, g.SuggestEnroll)
	assert.Equal(t, 0, g.Shortfall)

	o := by["ovh"]
	assert.Equal(t, 24, o.Gap)
	assert.Equal(t, 10, o.SuggestEnroll, "limité par la réserve")
	assert.Equal(t, 14, o.Shortfall, "le reste est à chasser")

	assert.Equal(t, 0, by["free"].Gap, "déjà couvert (40 pour 15)")
	assert.Equal(t, 0, by["free"].SuggestEnroll)

	z := by["zoho"]
	assert.Equal(t, 0, z.RoomForNew, "les relances remplissent l'horizon : jamais négatif")
	assert.Equal(t, 0, z.SuggestEnroll)

	// Jamais plus que la place : inscrits non envoyés + suggestion <= place pour de nouveaux contacts.
	for _, r := range rows {
		assert.LessOrEqual(t, r.SuggestEnroll, r.ReserveUnsent, r.Class)
		if r.SuggestEnroll > 0 {
			assert.LessOrEqual(t, r.EnrolledUnsent+r.SuggestEnroll, r.RoomForNew, r.Class)
		}
	}
	assert.Equal(t, 14+10, tot.SuggestEnroll)
	assert.Equal(t, 14, tot.Shortfall)
	require.Len(t, restock, 1)
	assert.Equal(t, "ovh", restock[0].Class)
	assert.Equal(t, 0.06, restock[0].ReplyRateSmoothed)
}

func TestBuildSuggestion_OrdreDeChasseSelonLaValeurPuisLeManque(t *testing.T) {
	capacity := map[string]Capacity{"a": {Nominal: 10}, "b": {Nominal: 10}, "c": {Nominal: 10}}
	_, _, restock := BuildSuggestion(2, nil, nil, capacity, nil, func(c string) float64 {
		return map[string]float64{"a": 0.01, "b": 0.07, "c": 0.07}[c]
	})
	require.Len(t, restock, 3)
	assert.Equal(t, []string{"b", "c", "a"}, []string{restock[0].Class, restock[1].Class, restock[2].Class}, "les plus rentables d'abord, ex æquo par nom")
}

func TestHorizon_JoursDEnvoiEnJoursCalendaires(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	assert.Equal(t, now.Add(2*24*time.Hour), horizon(now, 1), "1 jour d'envoi = 2 jours calendaires (1,4 arrondi au-dessus)")
	assert.Equal(t, now.Add(5*24*time.Hour), horizon(now, 3), "3 jours d'envoi = 5 jours calendaires (week-end compris)")
	assert.Equal(t, now.Add(7*24*time.Hour), horizon(now, 5))
	assert.Equal(t, now.Add(14*24*time.Hour), horizon(now, 10))
}

func TestCandidatesSQL_ExclutLesNonEligiblesEtOrdonneParPriorite(t *testing.T) {
	cfg := StockConfig{ReserveListID: "res", EnrollAutomationID: "auto1", RequireNonEmpty: []string{"custom_string_1"}}
	q, args := candidatesSQL(cfg, "res", []string{"insc"}, 50)
	assert.Contains(t, q, "cl.status NOT IN ('unsubscribed','bounced','complained')")
	assert.Contains(t, q, "NOT EXISTS (SELECT 1 FROM message_history")
	assert.Contains(t, q, "e.list_id IN ($2)")
	assert.Contains(t, q, "x.automation_id = $3")
	assert.Contains(t, q, "ORDER BY c.custom_string_2 ASC NULLS LAST, c.custom_number_1 DESC NULLS LAST, c.email")
	assert.Equal(t, []any{"res", "insc", "auto1", 50}, args)
	assert.NotContains(t, q, "auto1")

	q2, args2 := followupsSQL(StockConfig{}, nil, time.Now())
	assert.Empty(t, q2, "sans automation ni liste : rien à compter")
	assert.Nil(t, args2)
	q3, args3 := followupsSQL(StockConfig{EnrollAutomationID: "auto1"}, []string{"insc"}, time.Now())
	assert.Contains(t, q3, "ca.status = 'active'")
	assert.Contains(t, q3, "a.id = $2 OR a.list_id IN ($3)")
	assert.Len(t, args3, 3)
}

func suggestService(t *testing.T, auth *fakeAuth) (*Service, *fakeStore) {
	svc, st := stockService(t, auth)
	st.settings[StockKey] = []byte(`{"enrolled_list_id":"prospectionasd","reserve_list_id":"stockodhmasse","enroll_automation_id":"seq","target_days":3}`)
	svc.now = func() time.Time { return time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC) }
	// Capacité : google 10/jour, ovh 5/jour. Inscrits non envoyés : google 30, ovh 10.
	st.followups = map[string]int{"ovh": 3}
	st.candidates = map[string]Candidates{
		"google": {Total: 500, Emails: []string{"a@x.fr", "b@x.fr", "c@x.fr"}},
		"ovh":    {Total: 4, Emails: []string{"o1@x.fr", "o2@x.fr", "o3@x.fr", "o4@x.fr"}},
	}
	return svc, st
}

func TestService_SuggestEnrollment_NInscritPersonneEtDonneLesAdressesSurDemande(t *testing.T) {
	svc, st := suggestService(t, &fakeAuth{owner: true})
	ctx := context.Background()
	settingsBefore := string(st.settings[StockKey])

	res, err := svc.SuggestEnrollment(ctx, SuggestRequest{WorkspaceID: "ws"})
	require.NoError(t, err)
	assert.False(t, res.Applied, "ce service n'inscrit jamais")
	assert.Equal(t, 3, res.Days)
	assert.Equal(t, "seq", res.EnrollAutomationID)
	by := byClass(res.Classes)
	g := by["google"]
	assert.Equal(t, 30, g.HorizonCapacity)
	assert.Equal(t, 30, g.EnrolledUnsent)
	assert.Equal(t, 0, g.Gap, "30 inscrits non envoyés couvrent déjà 3 jours à 10/jour")
	o := by["ovh"]
	assert.Equal(t, 15, o.HorizonCapacity)
	assert.Equal(t, 3, o.FollowupsDue)
	assert.Equal(t, 12, o.RoomForNew)
	assert.Equal(t, 10, o.EnrolledUnsent)
	assert.Equal(t, 2, o.Gap)
	assert.Equal(t, 2, o.SuggestEnroll)
	assert.Empty(t, o.Emails, "adresses non demandées")
	assert.Equal(t, []int{0}, st.candidateCalls, "sans include_emails la requête ne ramène aucune adresse")

	res, err = svc.SuggestEnrollment(ctx, SuggestRequest{WorkspaceID: "ws", Days: 5, IncludeEmails: true, Limit: 3})
	require.NoError(t, err)
	by = byClass(res.Classes)
	assert.Equal(t, 5, res.Days)
	g = by["google"]
	assert.Equal(t, 20, g.Gap, "50 de capacité - 30 inscrits")
	assert.Equal(t, 20, g.SuggestEnroll)
	assert.Equal(t, 0, g.Shortfall)
	assert.Equal(t, []string{"a@x.fr", "b@x.fr", "c@x.fr"}, g.Emails, "limitées par limit, dans l'ordre")
	assert.Equal(t, 3, st.candidateCalls[len(st.candidateCalls)-1])
	o = by["ovh"]
	assert.Equal(t, 4, o.SuggestEnroll, "limité par la réserve éligible")
	assert.Equal(t, []string{"o1@x.fr", "o2@x.fr", "o3@x.fr"}, o.Emails, "limit=3 borne la liste d'adresses, pas le calcul")
	assert.Equal(t, 4, o.SuggestEnroll)
	assert.Greater(t, o.Shortfall, 0)
	require.NotEmpty(t, res.RestockPriority)

	assert.Equal(t, settingsBefore, string(st.settings[StockKey]), "aucune écriture")
	assert.Empty(t, st.overrides)
	assert.Empty(t, st.prios)
}

func TestService_SuggestEnrollment_BornesPermissionsEtDefauts(t *testing.T) {
	ctx := context.Background()
	svc, st := suggestService(t, &fakeAuth{owner: true})
	for _, req := range []SuggestRequest{{WorkspaceID: "ws", Days: 61}, {WorkspaceID: "ws", Days: -1}, {WorkspaceID: "ws", Limit: 2001}, {WorkspaceID: "ws", Limit: -1}} {
		_, err := svc.SuggestEnrollment(ctx, req)
		var ve *ValidationError
		assert.ErrorAs(t, err, &ve, "%+v", req)
	}
	_, err := svc.SuggestEnrollment(ctx, SuggestRequest{WorkspaceID: "ws", IncludeEmails: true})
	require.NoError(t, err)
	assert.Equal(t, 200, st.candidateCalls[len(st.candidateCalls)-1], "défaut : 200 adresses par classe")

	svc, _ = suggestService(t, &fakeAuth{perms: perms(true, true, domain.PermissionResourceContacts)})
	_, err = svc.SuggestEnrollment(ctx, SuggestRequest{WorkspaceID: "ws"})
	var perm *domain.PermissionError
	assert.ErrorAs(t, err, &perm, "les adresses sont des données de contact : lecture des contacts ET de l'historique")

	svc, _ = suggestService(t, &fakeAuth{owner: true})
	svc.SetOverview(fakeOverview{err: errBoom})
	_, err = svc.SuggestEnrollment(ctx, SuggestRequest{WorkspaceID: "ws"})
	assert.Error(t, err, "capacité illisible : pas de suggestion à capacité zéro")
}

func TestService_SuggestEnrollment_SansReserveToutLeManqueEstAChasser(t *testing.T) {
	svc, st := suggestService(t, &fakeAuth{owner: true})
	st.settings[StockKey] = []byte(`{"enrolled_list_id":"prospectionasd"}`)
	st.candidates = nil
	res, err := svc.SuggestEnrollment(context.Background(), SuggestRequest{WorkspaceID: "ws", Days: 10})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Totals.SuggestEnroll)
	assert.Greater(t, res.Totals.Shortfall, 0)
	assert.Contains(t, strings.Join(res.Notes, " "), "aucune liste de réserve")
}

func TestHandler_SuggestEnrollment(t *testing.T) {
	svc, _ := suggestService(t, &fakeAuth{owner: true})
	h := NewHandler(svc, func() ([]byte, error) { return []byte("x"), nil }, noopLog{})
	rec := call(h.suggestEnrollment, "GET", "/x?workspace_id=ws&days=10&include_emails=true&limit=2", "")
	assert.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"applied":false`)
	assert.Contains(t, rec.Body.String(), `"a@x.fr"`)
	assert.NotContains(t, rec.Body.String(), `"c@x.fr"`, "limit=2")
	rec = call(h.suggestEnrollment, "POST", "/x", `{"workspace_id":"ws","include_emails":true,"limit":1}`)
	assert.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Equal(t, 400, call(h.suggestEnrollment, "GET", "/x?workspace_id=ws&include_emails=peut-etre", "").Code)
	assert.Equal(t, 400, call(h.suggestEnrollment, "GET", "/x?workspace_id=ws&limit=abc", "").Code)
	assert.Equal(t, 400, call(h.suggestEnrollment, "GET", "/x?workspace_id=ws&limit=9999", "").Code)
	assert.Equal(t, 400, call(h.suggestEnrollment, "POST", "/x", `{"workspace_id":"ws","emails":true}`).Code, "champ inconnu refusé")
}
