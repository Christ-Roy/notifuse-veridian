package service

import (
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper : construit une ContactAutomationWithWorkspace minimale pour les tests de tri.
func mkCA(id string, scheduledAt *time.Time, enteredAt time.Time) *domain.ContactAutomationWithWorkspace {
	return &domain.ContactAutomationWithWorkspace{
		WorkspaceID: "ws1",
		ContactAutomation: domain.ContactAutomation{
			ID:           id,
			AutomationID: "auto1",
			ContactEmail: id + "@example.com",
			Status:       domain.ContactAutomationStatusActive,
			ScheduledAt:  scheduledAt,
			EnteredAt:    enteredAt,
		},
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// ids extrait l'ordre des IDs après tri (lisibilité des assertions).
func ids(items []*domain.ContactAutomationWithWorkspace) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestVeridianPrioritizeFollowups(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	enrolled := now.Add(-30 * 24 * time.Hour) // enrôlement "neutre" partagé

	t.Run("relance la plus en retard (J+7 dû depuis 2j) passe avant un J+0 dû à l'instant", func(t *testing.T) {
		// followup7 : échéance dépassée de 2 jours (fenêtre qui se ferme) → urgent.
		followup7 := mkCA("followup7", ptrTime(now.Add(-2*24*time.Hour)), enrolled)
		// fresh0 : échéance atteinte à l'instant (peut attendre demain) → moins urgent.
		fresh0 := mkCA("fresh0", ptrTime(now), enrolled)

		items := []*domain.ContactAutomationWithWorkspace{fresh0, followup7}
		veridianPrioritizeFollowups(items, now)

		assert.Equal(t, []string{"followup7", "fresh0"}, ids(items),
			"le plus en retard doit être servi en premier sous contrainte de capacité")
	})

	t.Run("en retard avant à l'heure (échéance future ou pile)", func(t *testing.T) {
		late := mkCA("late", ptrTime(now.Add(-1*time.Hour)), enrolled)    // 1h de retard
		ontime := mkCA("ontime", ptrTime(now), enrolled)                  // pile à l'heure
		early := mkCA("early", ptrTime(now.Add(10*time.Minute)), enrolled) // pas encore dû

		items := []*domain.ContactAutomationWithWorkspace{early, ontime, late}
		veridianPrioritizeFollowups(items, now)

		assert.Equal(t, []string{"late", "ontime", "early"}, ids(items))
	})

	t.Run("ordre strict par retard croissant de l'urgence", func(t *testing.T) {
		d1 := mkCA("d1", ptrTime(now.Add(-1*time.Hour)), enrolled)
		d2 := mkCA("d2", ptrTime(now.Add(-5*time.Hour)), enrolled)
		d3 := mkCA("d3", ptrTime(now.Add(-72*time.Hour)), enrolled)
		d4 := mkCA("d4", ptrTime(now.Add(-30*time.Minute)), enrolled)

		items := []*domain.ContactAutomationWithWorkspace{d1, d2, d3, d4}
		veridianPrioritizeFollowups(items, now)

		// d3 (72h) > d2 (5h) > d1 (1h) > d4 (30min)
		assert.Equal(t, []string{"d3", "d2", "d1", "d4"}, ids(items))
	})

	t.Run("départage à retard égal : ScheduledAt le plus ancien d'abord (FIFO)", func(t *testing.T) {
		// Même instant 'now', mais on force un retard IDENTIQUE via des échéances égales :
		// départage doit alors retomber sur EnteredAt. On teste ScheduledAt distinct ici.
		sameOverdueOlder := mkCA("older", ptrTime(now.Add(-3*time.Hour)), enrolled)
		sameOverdueNewer := mkCA("newer", ptrTime(now.Add(-2*time.Hour)), enrolled)

		items := []*domain.ContactAutomationWithWorkspace{sameOverdueNewer, sameOverdueOlder}
		veridianPrioritizeFollowups(items, now)

		// older a un retard plus grand (3h > 2h) → en premier.
		assert.Equal(t, []string{"older", "newer"}, ids(items))
	})

	t.Run("départage à retard ET échéance identiques : EnteredAt le plus ancien d'abord", func(t *testing.T) {
		sched := ptrTime(now.Add(-1 * time.Hour))
		earlier := mkCA("earlier", sched, now.Add(-40*24*time.Hour))
		later := mkCA("later", sched, now.Add(-10*24*time.Hour))

		items := []*domain.ContactAutomationWithWorkspace{later, earlier}
		veridianPrioritizeFollowups(items, now)

		assert.Equal(t, []string{"earlier", "later"}, ids(items),
			"à échéance égale, le contact enrôlé le plus tôt passe d'abord")
	})

	t.Run("départage total déterministe par ID quand tout est égal", func(t *testing.T) {
		sched := ptrTime(now.Add(-1 * time.Hour))
		b := mkCA("bbb", sched, enrolled)
		a := mkCA("aaa", sched, enrolled)
		c := mkCA("ccc", sched, enrolled)

		items := []*domain.ContactAutomationWithWorkspace{c, b, a}
		veridianPrioritizeFollowups(items, now)

		assert.Equal(t, []string{"aaa", "bbb", "ccc"}, ids(items),
			"ordre reproductible (anti map-order aléatoire)")
	})

	t.Run("ScheduledAt nil relégué en fin (best-effort, ne devrait pas arriver)", func(t *testing.T) {
		due := mkCA("due", ptrTime(now.Add(-1*time.Hour)), enrolled)
		noSched := mkCA("nosched", nil, enrolled)

		items := []*domain.ContactAutomationWithWorkspace{noSched, due}
		veridianPrioritizeFollowups(items, now)

		assert.Equal(t, []string{"due", "nosched"}, ids(items))
	})

	t.Run("sous contrainte de capacité : tronquer au limit garde les plus urgents", func(t *testing.T) {
		// Simule le cas réel : on collecte 5 dûs mais la capacité ne permet d'en servir que 2.
		urgent2 := mkCA("urgent_72h", ptrTime(now.Add(-72*time.Hour)), enrolled)
		urgent1 := mkCA("urgent_48h", ptrTime(now.Add(-48*time.Hour)), enrolled)
		mid := mkCA("mid_6h", ptrTime(now.Add(-6*time.Hour)), enrolled)
		fresh1 := mkCA("fresh_5m", ptrTime(now.Add(-5*time.Minute)), enrolled)
		fresh2 := mkCA("fresh_now", ptrTime(now), enrolled)

		items := []*domain.ContactAutomationWithWorkspace{fresh2, mid, urgent1, fresh1, urgent2}
		veridianPrioritizeFollowups(items, now)

		const capacity = 2
		served := items[:capacity]
		assert.Equal(t, []string{"urgent_72h", "urgent_48h"}, ids(served),
			"si on ne peut servir que 2, ce sont les 2 plus en retard")
	})

	t.Run("non-régression : sans contrainte, le SET traité est identique (seul l'ordre change)", func(t *testing.T) {
		a := mkCA("a", ptrTime(now.Add(-10*time.Minute)), enrolled)
		b := mkCA("b", ptrTime(now.Add(-1*time.Hour)), enrolled)
		c := mkCA("c", ptrTime(now), enrolled)

		items := []*domain.ContactAutomationWithWorkspace{a, b, c}
		veridianPrioritizeFollowups(items, now)

		// Le set complet est conservé (aucune perte), longueur inchangée.
		require.Len(t, items, 3)
		got := map[string]bool{}
		for _, it := range items {
			got[it.ID] = true
		}
		assert.True(t, got["a"] && got["b"] && got["c"], "aucune automation ne doit disparaître du batch")
		// L'ordre est le FIFO sain par retard décroissant : b (1h) > a (10m) > c (0).
		assert.Equal(t, []string{"b", "a", "c"}, ids(items))
	})

	t.Run("no-op sur slice vide ou singleton (pas de panic)", func(t *testing.T) {
		assert.NotPanics(t, func() {
			veridianPrioritizeFollowups(nil, now)
			veridianPrioritizeFollowups([]*domain.ContactAutomationWithWorkspace{}, now)
			single := []*domain.ContactAutomationWithWorkspace{mkCA("solo", ptrTime(now), enrolled)}
			veridianPrioritizeFollowups(single, now)
			assert.Equal(t, []string{"solo"}, ids(single))
		})
	})

	t.Run("stabilité : tri stable préserve l'ordre relatif des égaux non départagés par ID identiques impossibles", func(t *testing.T) {
		// IDs uniques en pratique ; ce test vérifie juste que deux passes donnent le même résultat.
		mk := func() []*domain.ContactAutomationWithWorkspace {
			return []*domain.ContactAutomationWithWorkspace{
				mkCA("x", ptrTime(now.Add(-2*time.Hour)), enrolled),
				mkCA("y", ptrTime(now.Add(-2*time.Hour)), enrolled),
				mkCA("z", ptrTime(now.Add(-3*time.Hour)), enrolled),
			}
		}
		first := mk()
		veridianPrioritizeFollowups(first, now)
		second := mk()
		veridianPrioritizeFollowups(second, now)
		assert.Equal(t, ids(first), ids(second), "tri déterministe et reproductible")
		assert.Equal(t, []string{"z", "x", "y"}, ids(first))
	})
}

func TestVeridianOverdue(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	enrolled := now.Add(-time.Hour)

	t.Run("retard positif quand échéance dépassée", func(t *testing.T) {
		ca := mkCA("a", ptrTime(now.Add(-90*time.Minute)), enrolled)
		assert.Equal(t, 90*time.Minute, veridianOverdue(ca, now))
	})

	t.Run("retard négatif quand échéance future", func(t *testing.T) {
		ca := mkCA("a", ptrTime(now.Add(30*time.Minute)), enrolled)
		assert.Equal(t, -30*time.Minute, veridianOverdue(ca, now))
	})

	t.Run("retard zéro quand pile à l'heure", func(t *testing.T) {
		ca := mkCA("a", ptrTime(now), enrolled)
		assert.Equal(t, time.Duration(0), veridianOverdue(ca, now))
	})

	t.Run("ScheduledAt nil → 0", func(t *testing.T) {
		ca := mkCA("a", nil, enrolled)
		assert.Equal(t, time.Duration(0), veridianOverdue(ca, now))
	})

	t.Run("item nil → 0 (best-effort, pas de panic)", func(t *testing.T) {
		assert.NotPanics(t, func() {
			assert.Equal(t, time.Duration(0), veridianOverdue(nil, now))
		})
	})
}
