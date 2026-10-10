package domain

import (
	"sort"
	"time"
)

// Veridian fork (10/10/2026) : DEPILAGE EQUITABLE de email_queue.
//
// Incident : la requete historique triait `priority, created_at` avec un LIMIT.
// Toutes les entrees ayant la meme priorite (5), les plus anciennes (les J0
// d'un gros segment) occupaient la tete a chaque tick, replanifiees en boucle
// par les plafonds, tandis que les relances J+4 et les segments plus recents
// (vetuste, croissance) n'etaient jamais lus : famine. Cf. docs/claude/58.
//
// Politique (fonction pure, testee sans base) :
//  1. la priorite explicite reste la premiere cle (inchangee) ;
//  2. a priorite egale, les RELANCES (le contact a deja recu un mail de cette
//     meme automation) passent avant les premiers contacts (J0) ;
//  3. dans chaque palier, TOURNIQUET entre les sources (automation ou
//     diffusion) : un tour = une entree par source, le point de depart tourne a
//     chaque appel ;
//  4. dans une source, la moins recemment examinee d'abord
//     (COALESCE(next_retry_at, created_at)) : une entree replanifiee par un gate
//     cede la place aux entrees jamais essayees.

// VeridianFairCandidate est une entree due, avec son palier.
type VeridianFairCandidate struct {
	Entry      *EmailQueueEntry
	IsFollowup bool
}

// VeridianExaminedAt est la date de derniere inspection de l'entree : sa
// replanification si un gate l'a repoussee, sinon sa creation.
func VeridianExaminedAt(e *EmailQueueEntry) time.Time {
	if e.NextRetryAt != nil {
		return *e.NextRetryAt
	}
	return e.CreatedAt
}

func veridianFairLess(a, b *EmailQueueEntry) bool {
	ea, eb := VeridianExaminedAt(a), VeridianExaminedAt(b)
	if !ea.Equal(eb) {
		return ea.Before(eb)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

// VeridianSelectFairBatch choisit au plus limit entrees parmi les candidates.
// rotation decale la source qui ouvre chaque tour (le depot l'incremente a
// chaque appel) ; deux appels successifs n'ouvrent donc pas par la meme source.
// Aucune entree n'est retournee deux fois.
func VeridianSelectFairBatch(cands []VeridianFairCandidate, limit int, rotation uint64) []*EmailQueueEntry {
	if limit <= 0 || len(cands) == 0 {
		return nil
	}

	type tier struct {
		priority int
		followup bool
	}
	tiers := map[tier]map[string][]*EmailQueueEntry{}
	seen := map[string]bool{}
	for _, c := range cands {
		if c.Entry == nil || seen[c.Entry.ID] {
			continue
		}
		seen[c.Entry.ID] = true
		k := tier{c.Entry.Priority, c.IsFollowup}
		if tiers[k] == nil {
			tiers[k] = map[string][]*EmailQueueEntry{}
		}
		tiers[k][c.Entry.SourceID] = append(tiers[k][c.Entry.SourceID], c.Entry)
	}

	order := make([]tier, 0, len(tiers))
	for k := range tiers {
		order = append(order, k)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].priority != order[j].priority {
			return order[i].priority < order[j].priority
		}
		return order[i].followup && !order[j].followup
	})

	out := make([]*EmailQueueEntry, 0, limit)
	for _, k := range order {
		bySource := tiers[k]
		sources := make([]string, 0, len(bySource))
		for s, list := range bySource {
			sort.Slice(list, func(i, j int) bool { return veridianFairLess(list[i], list[j]) })
			sources = append(sources, s)
		}
		sort.Strings(sources)
		start := int(rotation % uint64(len(sources)))
		for round := 0; len(out) < limit; round++ {
			progressed := false
			for i := 0; i < len(sources) && len(out) < limit; i++ {
				list := bySource[sources[(start+i)%len(sources)]]
				if round < len(list) {
					out = append(out, list[round])
					progressed = true
				}
			}
			if !progressed {
				break
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}
