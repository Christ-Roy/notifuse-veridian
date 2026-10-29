package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

func ip(i int) *int { return &i }

// Relevé réel du script stock-par-classe.py sur animalsd le 29/10/2026 (inscrits, réserve, plafond/jour, jours).
// C'est le témoin de la reproduction : mêmes entrées, mêmes jours.
var scriptReleve = []struct {
	class                  string
	enrolled, reserve, cap int
	jours                  *float64
}{
	{"corporate", 0, 0, 70, f(0.0)}, {"lws", 0, 2, 36, f(0.1)}, {"zoho", 3, 2, 36, f(0.1)}, {"bouygues", 12, 7, 46, f(0.4)},
	{"freemail_fr", 8, 16, 46, f(0.5)}, {"o2switch", 6, 13, 36, f(0.5)}, {"scaleway", 9, 5, 26, f(0.5)}, {"proton", 11, 13, 26, f(0.9)},
	{"apple_icloud", 16, 20, 36, f(1.0)}, {"free", 48, 65, 28, f(4.0)}, {"sfr_altice", 49, 67, 21, f(5.5)}, {"website_builder", 39, 60, 16, f(6.2)},
	{"laposte", 46, 51, 14, f(6.9)}, {"infomaniak", 65, 83, 21, f(7.0)}, {"yahoo_aol", 117, 135, 26, f(9.7)}, {"hostinger", 59, 86, 14, f(10.4)},
	{"gandi", 58, 91, 14, f(10.6)}, {"ionos", 174, 255, 36, f(11.9)}, {"other_hoster", 168, 342, 36, f(14.2)}, {"corporate_selfhost", 280, 540, 36, f(22.8)},
	{"security_gateway", 87, 134, 9, f(24.6)}, {"google", 1798, 1963, 56, f(67.2)}, {"microsoft", 481, 761, 14, f(88.7)}, {"ovh", 734, 898, 16, f(102.0)},
	{"orange", 723, 712, 14, f(102.5)},
}

func f(x float64) *float64 { return &x }

func TestBuildStock_ReproduitLeTableauDuScript(t *testing.T) {
	enrolled, reserve, capacity := map[string]ListCounts{}, map[string]ListCounts{}, map[string]Capacity{}
	for _, r := range scriptReleve {
		enrolled[r.class] = ListCounts{Total: r.enrolled}
		reserve[r.class] = ListCounts{Total: r.reserve}
		capacity[r.class] = Capacity{Today: r.cap, Nominal: r.cap}
	}
	rows := BuildStock(StockConfig{}, enrolled, reserve, capacity, nil)
	require.Len(t, rows, len(scriptReleve))
	for i, want := range scriptReleve {
		got := rows[i]
		assert.Equal(t, want.class, got.Class, "même ordre que le script (jours croissants) à la ligne %d", i)
		assert.Equal(t, want.enrolled, got.Enrolled, want.class)
		assert.Equal(t, want.reserve, got.Reserve, want.class)
		assert.Equal(t, want.enrolled+want.reserve, got.Prospectable, want.class)
		assert.Equal(t, want.cap, got.CapacityToday, want.class)
		require.NotNil(t, got.DaysOfCoverage, want.class)
		assert.InDelta(t, *want.jours, *got.DaysOfCoverage, 1e-9, "jours de couverture %s", want.class)
	}
	tot := Totals(rows)
	assert.Equal(t, 11312, tot.Prospectable, "total du script")
}

func TestBuildStock_ClasseSansCapaciteEnFinSansJours(t *testing.T) {
	rows := BuildStock(StockConfig{}, map[string]ListCounts{"ovh": {Total: 10, Unsent: 10}, "google": {Total: 5, Unsent: 5}}, nil,
		map[string]Capacity{"google": {Today: 10, Nominal: 10}}, nil)
	require.Len(t, rows, 2)
	assert.Equal(t, "google", rows[0].Class)
	assert.Equal(t, "ovh", rows[1].Class)
	assert.Nil(t, rows[1].DaysOfCoverage, "pas de capacité : pas de division par zéro")
	assert.Equal(t, StatusBlocked, rows[1].Status)
}

func TestBuildStock_CouvertureHonnetteBesoinEtStatuts(t *testing.T) {
	cfg := StockConfig{TargetDays: 3}
	enrolled := map[string]ListCounts{
		"google": {Total: 500, Unsent: 40}, // 40 jamais contactés pour 10/jour : 4 jours, ok
		"ovh":    {Total: 300, Unsent: 15}, // 15 pour 10/jour : 1,5 jour, bas
		"free":   {Total: 200, Unsent: 4},  // 4 pour 10/jour : critique
	}
	reserve := map[string]ListCounts{"ovh": {Total: 20, Unsent: 5}}
	capacity := map[string]Capacity{"google": {Today: 10, Nominal: 10}, "ovh": {Today: 0, Nominal: 10}, "free": {Today: 10, Nominal: 10}, "zoho": {}}
	rows := BuildStock(cfg, enrolled, reserve, capacity, func(string) float64 { return 0.04 })
	by := map[string]StockRow{}
	for _, r := range rows {
		by[r.Class] = r
	}
	assert.Equal(t, StatusOK, by["google"].Status)
	assert.Equal(t, 0, by["google"].RestockNeed)
	assert.Equal(t, 20, by["ovh"].Unsent)
	assert.Equal(t, StatusLow, by["ovh"].Status)
	assert.Equal(t, 10, by["ovh"].RestockNeed, "3 jours x 10 - 20 jamais contactés")
	assert.Equal(t, StatusCritical, by["free"].Status)
	assert.Equal(t, 26, by["free"].RestockNeed)
	assert.Equal(t, StatusIdle, by["zoho"].Status)
	assert.Equal(t, 0.04, by["free"].ReplyRateSmoothed)
	require.NotNil(t, by["ovh"].DaysUnsent)
	assert.InDelta(t, 2.0, *by["ovh"].DaysUnsent, 1e-9)
	assert.Nil(t, by["ovh"].DaysOfCoverage, "capacité du jour à 0 (fenêtre close) : le script donne None")
	tot := Totals(rows)
	assert.Equal(t, 36, tot.RestockNeed)
}

func profile(usage string, paused, rotation bool, full *int, classes ...domain.VeridianPlanClass) domain.VeridianEmailProfileOverview {
	return domain.VeridianEmailProfileOverview{Usage: usage, Paused: paused, InRotation: rotation,
		Plan: domain.VeridianEffectivePlan{Classes: classes, Capacity: domain.VeridianPlanCapacity{FullDayConfigured: full}}}
}

func pc(class string, cfgd, daily int, excluded bool) domain.VeridianPlanClass {
	return domain.VeridianPlanClass{Class: class, Excluded: excluded, CapacityConfigured: ip(cfgd), DailyCap: ip(daily)}
}

func TestCapacityByClass_MemeRegleQueProfilesGaps(t *testing.T) {
	ov := &domain.VeridianEmailProfilesOverview{Profiles: []domain.VeridianEmailProfileOverview{
		profile("commercial", false, true, nil, pc("google", 10, 10, false), pc("ovh", 6, 8, false), pc("microsoft", 5, 5, true)),
		profile("commercial", false, true, nil, pc("google", 4, 10, false)),
		profile("transactional", false, true, nil, pc("google", 99, 99, false)),
		profile("commercial", true, true, nil, pc("google", 77, 77, false)),   // en pause
		profile("commercial", false, false, nil, pc("google", 55, 55, false)), // hors rotation
	}}
	c := CapacityByClass(ov)
	assert.Equal(t, 14, c["google"].Today, "10 + 4 : profils commerciaux actifs seulement")
	assert.Equal(t, 20, c["google"].Nominal)
	assert.Equal(t, 6, c["ovh"].Today)
	assert.NotContains(t, c, "microsoft", "classe exclue")
	assert.Empty(t, CapacityByClass(nil))
}

func TestCapacityByClass_UneJourneeEntiereBorneLaSommeDesClasses(t *testing.T) {
	// Un profil en chauffe : 20 mails pour la journée entière, alors que ses classes en permettraient 40.
	ov := &domain.VeridianEmailProfilesOverview{Profiles: []domain.VeridianEmailProfileOverview{
		profile("commercial", false, true, ip(20), pc("google", 10, 30, false), pc("ovh", 10, 10, false)),
	}}
	c := CapacityByClass(ov)
	assert.Equal(t, 15, c["google"].Nominal, "30 x 20/40")
	assert.Equal(t, 5, c["ovh"].Nominal)
	assert.Equal(t, 10, c["google"].Today, "la capacité du jour reste celle du profil, comme le script")
}

func TestStockConfig_Validate(t *testing.T) {
	assert.NoError(t, (&StockConfig{}).Validate())
	assert.NoError(t, (&StockConfig{EnrolledListID: "prospectionasd", ClassField: "custom_string_5", RequireNonEmpty: []string{"custom_string_1"},
		ExcludePrefixes: map[string][]string{"custom_string_1": {"cart_"}}, ExcludeEmailRegex: `veridian\.site|zz-test`, TargetDays: 3}).Validate())
	bad := []StockConfig{
		{ClassField: "email; DROP TABLE contacts"},
		{ClassField: "custom_string_9"},
		{RequireNonEmpty: []string{"password"}},
		{ExcludePrefixes: map[string][]string{"email": {"x"}}},
		{ExcludePrefixes: map[string][]string{"custom_string_1": {""}}},
		{ExcludeEmailRegex: "(["},
		{ExcludeEmailRegex: strings.Repeat("a", 501)},
		{TargetDays: -1}, {TargetDays: 61},
		{EnrolledListID: strings.Repeat("l", 65)},
	}
	for i, c := range bad {
		c := c
		assert.Error(t, c.Validate(), "cas %d", i)
	}
}

func TestCountsSQL_ParametresEtListeBlanche(t *testing.T) {
	cfg := StockConfig{ClassField: "custom_string_5", ExcludeEmailRegex: `x\.fr`, RequireNonEmpty: []string{"custom_string_1"},
		ExcludePrefixes: map[string][]string{"custom_string_1": {"cart_", "50%_off"}}}
	q, args := countsSQL(cfg, []string{"l1", "l2"})
	assert.Contains(t, q, "cl.list_id IN ($1,$2)")
	assert.Contains(t, q, "c.email !~* $3")
	assert.Contains(t, q, "COALESCE(c.custom_string_1,'') <> ''")
	assert.Contains(t, q, `COALESCE(c.custom_string_1,'') NOT LIKE $4 ESCAPE '\'`)
	assert.Contains(t, q, "COALESCE(NULLIF(c.custom_string_5,''),'?')")
	assert.Contains(t, q, "cl.deleted_at IS NULL")
	assert.Contains(t, q, "m.sent_at IS NOT NULL")
	assert.Equal(t, []any{"l1", "l2", `x\.fr`, `cart\_%`, `50\%\_off%`}, args, "les préfixes sont échappés pour LIKE")
	assert.NotContains(t, q, "cart_", "aucune valeur n'est collée dans le SQL")
}

// --- Service ---

type fakeOverview struct {
	ov  *domain.VeridianEmailProfilesOverview
	err error
}

func (f fakeOverview) GetEmailProfilesOverview(context.Context, string) (*domain.VeridianEmailProfilesOverview, error) {
	return f.ov, f.err
}

func stockService(t *testing.T, auth *fakeAuth) (*Service, *fakeStore) {
	svc, st, _ := newTestService(t, auth)
	st.stock = map[string]map[string]ListCounts{
		"prospectionasd": {"google": {Total: 100, Unsent: 30}, "ovh": {Total: 10, Unsent: 10}},
		"stockodhmasse":  {"google": {Total: 200, Unsent: 200}},
	}
	svc.SetOverview(fakeOverview{ov: &domain.VeridianEmailProfilesOverview{Profiles: []domain.VeridianEmailProfileOverview{
		profile("commercial", false, true, nil, pc("google", 10, 10, false), pc("ovh", 5, 5, false))}}})
	return svc, st
}

func TestService_StockOverview_DefinitionEnregistreOuSurchargeeParLaRequete(t *testing.T) {
	svc, st := stockService(t, &fakeAuth{owner: true})
	ctx := context.Background()

	// Sans définition ni automation : aucune liste, avertissement, pas d'erreur.
	res, err := svc.StockOverview(ctx, StockRequest{WorkspaceID: "ws"})
	require.NoError(t, err)
	assert.Equal(t, "none", res.ConfigSource)
	assert.Contains(t, strings.Join(res.Notes, " "), "aucune liste d'inscrits")

	// Listes des automations actives par défaut.
	st.autoLists = []string{"prospectionasd"}
	res, err = svc.StockOverview(ctx, StockRequest{WorkspaceID: "ws"})
	require.NoError(t, err)
	assert.Equal(t, "automations", res.ConfigSource)
	assert.Equal(t, 3, res.TargetDays)

	// Définition enregistrée : listes, filtres, cible.
	_, err = svc.SetStockConfig(ctx, "ws", StockConfig{EnrolledListID: "prospectionasd", ReserveListID: "stockodhmasse",
		RequireNonEmpty: []string{"custom_string_1"}, TargetDays: 5})
	require.NoError(t, err)
	res, err = svc.StockOverview(ctx, StockRequest{WorkspaceID: "ws"})
	require.NoError(t, err)
	assert.Equal(t, "config", res.ConfigSource)
	assert.Equal(t, 5, res.TargetDays)
	by := map[string]StockRow{}
	for _, r := range res.Classes {
		by[r.Class] = r
	}
	g := by["google"]
	assert.Equal(t, 100, g.Enrolled)
	assert.Equal(t, 200, g.Reserve)
	assert.Equal(t, 300, g.Prospectable)
	assert.Equal(t, 230, g.Unsent)
	require.NotNil(t, g.DaysOfCoverage)
	assert.InDelta(t, 30.0, *g.DaysOfCoverage, 1e-9, "300 / 10")
	assert.Equal(t, []string{"custom_string_1"}, st.stockCalls[len(st.stockCalls)-1].RequireNonEmpty)

	// La requête prime, sans rien écrire (lecture seule).
	before := string(st.settings[StockKey])
	res, err = svc.StockOverview(ctx, StockRequest{WorkspaceID: "ws", Days: 2, Override: StockConfig{EnrolledListID: "stockodhmasse", ReserveListID: "prospectionasd"}})
	require.NoError(t, err)
	assert.Equal(t, "request", res.ConfigSource)
	assert.Equal(t, 2, res.TargetDays)
	assert.Equal(t, before, string(st.settings[StockKey]), "stock.overview n'écrit jamais")
}

func TestService_StockOverview_PermissionsEtBornes(t *testing.T) {
	ctx := context.Background()
	svc, _ := stockService(t, &fakeAuth{perms: perms(true, false, domain.PermissionResourceContacts)})
	_, err := svc.StockOverview(ctx, StockRequest{WorkspaceID: "ws"})
	var perm *domain.PermissionError
	assert.ErrorAs(t, err, &perm, "il faut aussi lire l'historique des messages")
	svc, _ = stockService(t, &fakeAuth{perms: perms(true, false, domain.PermissionResourceContacts, domain.PermissionResourceMessageHistory)})
	_, err = svc.StockOverview(ctx, StockRequest{WorkspaceID: "ws"})
	assert.NoError(t, err)
	_, err = svc.SetStockConfig(ctx, "ws", StockConfig{})
	assert.ErrorAs(t, err, &perm, "poser la définition exige workspace:write")

	svc, _ = stockService(t, &fakeAuth{owner: true})
	for _, d := range []int{-1, 61} {
		_, err = svc.StockOverview(ctx, StockRequest{WorkspaceID: "ws", Days: d})
		var ve *ValidationError
		assert.ErrorAs(t, err, &ve, d)
	}
	_, err = svc.StockOverview(ctx, StockRequest{WorkspaceID: "ws", Override: StockConfig{ClassField: "email"}})
	var ve *ValidationError
	assert.ErrorAs(t, err, &ve, "colonne hors liste blanche")

	svc, _ = stockService(t, &fakeAuth{owner: true})
	svc.SetOverview(fakeOverview{err: errBoom})
	_, err = svc.StockOverview(ctx, StockRequest{WorkspaceID: "ws"})
	assert.Error(t, err, "capacité illisible : erreur, jamais un tableau à capacité zéro")
}

func TestService_StockConfig_AllerRetour(t *testing.T) {
	svc, _ := stockService(t, &fakeAuth{owner: true})
	ctx := context.Background()
	v, err := svc.GetStockConfig(ctx, "ws")
	require.NoError(t, err)
	assert.Equal(t, "none", v.Source)
	assert.Equal(t, "custom_string_5", v.Config.ClassField, "défauts posés")
	assert.Equal(t, 3, v.Config.TargetDays)
	_, err = svc.SetStockConfig(ctx, "ws", StockConfig{EnrolledListID: " l1"})
	assert.Error(t, err)
	_, err = svc.SetStockConfig(ctx, "ws", StockConfig{EnrolledListID: "l1", ExcludePrefixes: map[string][]string{"custom_string_1": {"cart_"}}})
	require.NoError(t, err)
	v, err = svc.GetStockConfig(ctx, "ws")
	require.NoError(t, err)
	assert.Equal(t, "config", v.Source)
	assert.Equal(t, "l1", v.Config.EnrolledListID)
	assert.Equal(t, []string{"cart_"}, v.Config.ExcludePrefixes["custom_string_1"])
}

func TestHandler_Stock(t *testing.T) {
	h, st := newTestHandler(t, &fakeAuth{owner: true})
	h.svc.SetOverview(fakeOverview{ov: &domain.VeridianEmailProfilesOverview{Profiles: []domain.VeridianEmailProfileOverview{
		profile("commercial", false, true, nil, pc("google", 10, 10, false))}}})
	st.stock = map[string]map[string]ListCounts{"l1": {"google": {Total: 50, Unsent: 40}}}

	rec := call(h.stockOverview, "GET", "/x?workspace_id=ws&enrolled_list=l1&days=2&exclude_prefix=custom_string_1:cart_&require_non_empty=custom_string_1", "")
	assert.Equal(t, 200, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"days_of_coverage":5`)
	last := st.stockCalls[len(st.stockCalls)-1]
	assert.Equal(t, []string{"cart_"}, last.ExcludePrefixes["custom_string_1"])
	assert.Equal(t, []string{"custom_string_1"}, last.RequireNonEmpty)

	assert.Equal(t, 200, call(h.stockOverview, "POST", "/x", `{"workspace_id":"ws","enrolled_list":"l1","exclude_prefixes":{"custom_string_1":["cart_"]}}`).Code)
	assert.Equal(t, 400, call(h.stockOverview, "POST", "/x", `{"workspace_id":"ws","enroled_list":"l1"}`).Code, "faute de frappe : refusée")
	assert.Equal(t, 400, call(h.stockOverview, "GET", "/x?workspace_id=ws&days=abc", "").Code)
	assert.Equal(t, 400, call(h.stockOverview, "GET", "/x?workspace_id=ws&days=99", "").Code)
	assert.Equal(t, 400, call(h.stockOverview, "GET", "/x?workspace_id=ws&exclude_prefix=sans-deux-points", "").Code)
	assert.Equal(t, 400, call(h.stockOverview, "GET", "/x", "").Code)

	assert.Equal(t, 200, call(h.stockConfigSet, "POST", "/x", `{"workspace_id":"ws","enrolled_list_id":"l1","target_days":4}`).Code)
	rec = call(h.stockConfig, "GET", "/x?workspace_id=ws", "")
	assert.Equal(t, 200, rec.Code)
	assert.Contains(t, rec.Body.String(), `"target_days":4`)
	assert.Equal(t, 400, call(h.stockConfigSet, "POST", "/x", `{"workspace_id":"ws","class_field":"email"}`).Code)
}
