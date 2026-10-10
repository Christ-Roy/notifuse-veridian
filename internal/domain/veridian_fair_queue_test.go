package domain

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fairT0 = time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)

func fairEntry(id, source string, created time.Time, retry *time.Time) *EmailQueueEntry {
	return &EmailQueueEntry{ID: id, Priority: 5, SourceID: source, CreatedAt: created, NextRetryAt: retry}
}

func fairIsDue(e *EmailQueueEntry, now time.Time) bool {
	return e.NextRetryAt == nil || !e.NextRetryAt.After(now)
}

// legacyFetch reproduit a l'identique l'ancienne requete : ORDER BY priority,
// created_at LIMIT n, sur les entrees dues. Sert de temoin de la famine.
func legacyFetch(queue []*EmailQueueEntry, now time.Time, limit int) []*EmailQueueEntry {
	var due []*EmailQueueEntry
	for _, e := range queue {
		if fairIsDue(e, now) {
			due = append(due, e)
		}
	}
	sort.SliceStable(due, func(i, j int) bool {
		if due[i].Priority != due[j].Priority {
			return due[i].Priority < due[j].Priority
		}
		return due[i].CreatedAt.Before(due[j].CreatedAt)
	})
	if len(due) > limit {
		due = due[:limit]
	}
	return due
}

// fairFetch applique la politique equitable sur les memes entrees dues.
func fairFetch(queue []*EmailQueueEntry, followup map[string]bool, now time.Time, limit int, rotation uint64) []*EmailQueueEntry {
	var cands []VeridianFairCandidate
	for _, e := range queue {
		if fairIsDue(e, now) {
			cands = append(cands, VeridianFairCandidate{Entry: e, IsFollowup: followup[e.ID]})
		}
	}
	return VeridianSelectFairBatch(cands, limit, rotation)
}

// famineWorld reproduit la file de production du 10/10 : 1200 J0 "devenir"
// anciens que le gate de debit replanifie, 375 J0 devenir jamais essayes,
// 200 vetuste, 200 croissance et 190 relances, tous en priorite 5.
func famineWorld() (queue []*EmailQueueEntry, followup map[string]bool) {
	followup = map[string]bool{}
	add := func(n int, prefix, source string, created time.Time, fol bool) {
		for i := 0; i < n; i++ {
			e := fairEntry(fmt.Sprintf("%s-%d", prefix, i), source, created.Add(time.Duration(i)*time.Second), nil)
			queue = append(queue, e)
			if fol {
				followup[e.ID] = true
			}
		}
	}
	add(1200, "dev-old", "devenir", fairT0, false)
	add(375, "dev-new", "devenir", fairT0.Add(11*time.Hour), false)
	add(200, "vet", "vetuste", fairT0.Add(12*time.Hour), false)
	add(200, "sca", "scale", fairT0.Add(11*time.Hour+30*time.Minute), false)
	add(190, "rel", "devenir", fairT0.Add(4*24*time.Hour), true)
	return queue, followup
}

type simResult struct {
	sent      map[string]int // prefixe -> envois
	firstSent map[string]int // prefixe -> tick du premier envoi
	perID     map[string]int
}

func fairPrefix(id string) string {
	for _, p := range []string{"dev-old", "dev-new", "vet", "sca", "rel"} {
		if strings.HasPrefix(id, p+"-") {
			return p
		}
	}
	return "?"
}

// simulate rejoue 30 minutes (1 tick = 1 s) : un lot de 5 par tick ; le gate
// de debit replanifie de 1 minute tout J0 "devenir" sauf un tick sur trente ;
// les autres entrees passent le gate.
func simulate(fetch func(q []*EmailQueueEntry, fol map[string]bool, now time.Time, limit int, rot uint64) []*EmailQueueEntry) simResult {
	queue, fol := famineWorld()
	res := simResult{sent: map[string]int{}, firstSent: map[string]int{}, perID: map[string]int{}}
	for tick := 0; tick < 1800; tick++ {
		now := fairT0.Add(30*24*time.Hour + time.Duration(tick)*time.Second)
		batch := fetch(queue, fol, now, 5, uint64(tick))
		gone := map[string]bool{}
		for _, e := range batch {
			isDevenirJ0 := e.SourceID == "devenir" && !fol[e.ID]
			if isDevenirJ0 && tick%30 != 0 {
				next := now.Add(time.Minute)
				e.NextRetryAt = &next
				continue
			}
			p := fairPrefix(e.ID)
			res.sent[p]++
			res.perID[e.ID]++
			if _, ok := res.firstSent[p]; !ok {
				res.firstSent[p] = tick
			}
			gone[e.ID] = true
		}
		kept := make([]*EmailQueueEntry, 0, len(queue))
		for _, e := range queue {
			if !gone[e.ID] {
				kept = append(kept, e)
			}
		}
		queue = kept
	}
	return res
}

func TestFairQueue_LegacyOrderStarvesFollowupsAndOtherSegments(t *testing.T) {
	res := simulate(func(q []*EmailQueueEntry, _ map[string]bool, now time.Time, l int, _ uint64) []*EmailQueueEntry {
		return legacyFetch(q, now, l)
	})
	// Temoin de l'incident : l'ancien ordre n'envoie que du J0 "devenir".
	assert.Greater(t, res.sent["dev-old"]+res.sent["dev-new"], 0)
	assert.Zero(t, res.sent["rel"], "relances affamees par l'ancien ordre")
	assert.Zero(t, res.sent["vet"], "vetuste affame par l'ancien ordre")
	assert.Zero(t, res.sent["sca"], "croissance affame par l'ancien ordre")
}

func TestFairQueue_FairOrderServesFollowupsAndEverySegment(t *testing.T) {
	res := simulate(func(q []*EmailQueueEntry, f map[string]bool, now time.Time, l int, rot uint64) []*EmailQueueEntry {
		return fairFetch(q, f, now, l, rot)
	})
	assert.Equal(t, 190, res.sent["rel"], "toutes les relances dues partent")
	assert.Equal(t, 200, res.sent["vet"], "tout le segment vetuste part")
	assert.Equal(t, 200, res.sent["sca"], "tout le segment croissance part")
	assert.Greater(t, res.sent["dev-old"]+res.sent["dev-new"], 0, "le gros segment continue d'avancer, a son debit")
	// Les relances passent avant tout J0 : premier envoi au premier tick.
	assert.Equal(t, 0, res.firstSent["rel"])
	assert.LessOrEqual(t, res.firstSent["vet"], 60)
	assert.LessOrEqual(t, res.firstSent["sca"], 60)
	// Pas de double envoi.
	for id, n := range res.perID {
		assert.Equal(t, 1, n, "double envoi de %s", id)
	}
}

func TestFairQueue_FollowupsBeforeFirstContacts(t *testing.T) {
	q := []*EmailQueueEntry{
		fairEntry("j0-1", "a", fairT0, nil),
		fairEntry("j0-2", "a", fairT0.Add(time.Second), nil),
		fairEntry("rel-1", "a", fairT0.Add(time.Hour), nil),
		fairEntry("rel-2", "b", fairT0.Add(2*time.Hour), nil),
	}
	fol := map[string]bool{"rel-1": true, "rel-2": true}
	got := fairFetch(q, fol, fairT0.Add(24*time.Hour), 3, 0)
	require.Len(t, got, 3)
	assert.ElementsMatch(t, []string{"rel-1", "rel-2"}, []string{got[0].ID, got[1].ID})
	assert.Equal(t, "j0-1", got[2].ID)
}

func TestFairQueue_ExplicitPriorityStaysFirst(t *testing.T) {
	urgent := fairEntry("urgent", "a", fairT0.Add(time.Hour), nil)
	urgent.Priority = 1
	q := []*EmailQueueEntry{fairEntry("rel", "a", fairT0, nil), urgent}
	got := fairFetch(q, map[string]bool{"rel": true}, fairT0.Add(time.Hour), 1, 0)
	require.Len(t, got, 1)
	assert.Equal(t, "urgent", got[0].ID)
}

func TestFairQueue_RoundRobinBetweenThreeAutomations(t *testing.T) {
	var q []*EmailQueueEntry
	for _, s := range []string{"devenir", "scale", "vetuste"} {
		for i := 0; i < 10; i++ {
			// devenir est le plus ancien : l'ancien ordre ne lirait que lui.
			created := fairT0.Add(time.Duration(i) * time.Second)
			if s != "devenir" {
				created = created.Add(10 * time.Hour)
			}
			q = append(q, fairEntry(fmt.Sprintf("%s-%d", s, i), s, created, nil))
		}
	}
	now := fairT0.Add(48 * time.Hour)
	count := func(b []*EmailQueueEntry) map[string]int {
		m := map[string]int{}
		for _, e := range b {
			m[e.SourceID]++
		}
		return m
	}
	assert.Equal(t, map[string]int{"devenir": 9}, count(legacyFetch(q, now, 9)))
	assert.Equal(t, map[string]int{"devenir": 3, "scale": 3, "vetuste": 3}, count(fairFetch(q, nil, now, 9, 0)))
	// Lot de taille 1 : la source qui ouvre tourne d'un appel a l'autre.
	seen := map[string]bool{}
	for rot := uint64(0); rot < 3; rot++ {
		one := fairFetch(q, nil, now, 1, rot)
		require.Len(t, one, 1)
		seen[one[0].SourceID] = true
	}
	assert.Len(t, seen, 3, "un lot de 1 doit servir les 3 automations en 3 appels")
}

func TestFairQueue_ReplannedEntryYieldsToUntriedOnes(t *testing.T) {
	retry := fairT0.Add(40 * 24 * time.Hour)
	q := []*EmailQueueEntry{
		fairEntry("old-replanned", "a", fairT0, &retry),
		fairEntry("new-untried", "a", fairT0.Add(time.Hour), nil),
	}
	got := fairFetch(q, nil, retry.Add(time.Second), 1, 0)
	require.Len(t, got, 1)
	assert.Equal(t, "new-untried", got[0].ID, "une entree deja repoussee par un gate ne bloque pas la tete")
}

func TestFairQueue_NullNextRetryIsEligibleAndNoDuplicates(t *testing.T) {
	e := fairEntry("n", "a", fairT0, nil)
	got := VeridianSelectFairBatch([]VeridianFairCandidate{{Entry: e}, {Entry: e}}, 10, 0)
	assert.Len(t, got, 1)
	assert.Nil(t, VeridianSelectFairBatch(nil, 5, 0))
	assert.Nil(t, VeridianSelectFairBatch([]VeridianFairCandidate{{Entry: e}}, 0, 0))
}
