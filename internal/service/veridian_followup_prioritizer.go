package service

import (
	"sort"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian cold outreach — priorisation des follow-up sous contrainte de capacité.
//
// PROBLÈME (roadmap Robert 2026-06-15) : une cadence cold relance à J+3 / J+7
// (veridian_cold_sequence.go). Quand la capacité QUOTIDIENNE d'envoi est CONTRAINTE
// — caps par provider destinataire (veridian_daily_cap.go), sending windows, nombre
// de senders/IP limité en warm-up — le batch de scheduling (ProcessBatch, taille
// `batchSize`) ne peut PAS vider toutes les automations dûes à chaque tick. Il faut
// donc CHOISIR lesquelles passent en premier, sinon on sert dans un ordre arbitraire
// et on laisse des relances pourrir au-delà de leur fenêtre utile.
//
// CRITÈRE DE PRIORITÉ RETENU : le RETARD (overdue = now - ScheduledAt).
//
//   Plus une automation est en retard sur son échéance, plus sa fenêtre se ferme :
//   un prospect dont la relance J+7 est due depuis 2 jours refroidit (le sens même de
//   la relance — "je reviens vers vous" — se périme), alors qu'un J+0 fraîchement
//   enrôlé, dû depuis 1 minute, peut sans dommage attendre le prochain tick / demain.
//   On sert donc le PLUS EN RETARD d'abord (overdue décroissant).
//
//   Pourquoi le retard, et pas l'étape de séquence (J+7 > J+3 > J+0) en critère
//   premier ? Parce que l'étape est déjà CAPTURÉE par le retard dans le cas réel :
//   un J+7 et un J+0 enrôlés en même temps n'arrivent pas dûs au même instant ; et
//   à instant d'échéance égal, c'est la fenêtre qui se referme (le retard) qui tranche,
//   pas le numéro d'étape. Prioriser par retard évite aussi d'avoir à matérialiser le
//   numéro d'étape sur ContactAutomation (qu'on ne stocke pas) : `now - ScheduledAt`
//   est dérivable à la lecture, zéro nouvelle colonne, zéro migration.
//
//   Départage stable à retard égal : ScheduledAt le plus ancien d'abord (FIFO sur
//   l'échéance), puis EnteredAt le plus ancien (le contact enrôlé le plus tôt), puis
//   ID (déterminisme total, reproductible en test). Aucun hasard de map-order.
//
// NON-RÉGRESSION quand la capacité N'EST PAS contrainte : si le batch absorbe tout
// le dû (cas nominal, faible volume), TOUTES les automations collectées sont traitées
// de toute façon — le tri ne change pas le SET traité, seulement l'ordre, et un ordre
// "le plus en retard d'abord" reste un FIFO sain sur l'échéance (équivalent au
// `scheduled_at ASC` du repo). Le tri n'introduit donc aucune régression : il ne fait
// la différence QUE quand on doit couper, et alors il coupe les moins urgents.
//
// Cette fonction est PURE (pas d'I/O, pas d'état) : elle trie en place le slice fourni
// par le repo (round-robin par workspace, anti-starvation) AVANT que l'executor ne
// boucle dessus. Le round-robin a déjà garanti l'équité entre workspaces ; ce tri
// re-priorise la fenêtre collectée par urgence réelle, toutes workspaces confondues.

// veridianPrioritizeFollowups trie EN PLACE les automations dûes par urgence de
// follow-up (le plus en retard sur son échéance d'abord), pour que — quand la capacité
// d'envoi est contrainte et que tout le batch ne sera pas servi — ce soient les
// relances dont la fenêtre se ferme qui passent en premier.
//
// `now` est l'instant de référence (UTC) utilisé pour calculer le retard. Une
// ScheduledAt nil (ne devrait pas arriver pour une automation "active and due", mais
// best-effort) est traitée comme "pas de retard mesurable" → reléguée en fin.
func veridianPrioritizeFollowups(items []*domain.ContactAutomationWithWorkspace, now time.Time) {
	if len(items) < 2 {
		return
	}

	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]

		oa := veridianOverdue(a, now)
		ob := veridianOverdue(b, now)
		if oa != ob {
			// Plus le retard est grand, plus c'est urgent → en premier.
			return oa > ob
		}

		// Départage 1 : échéance la plus ancienne d'abord (FIFO sur ScheduledAt).
		// Aligné sur le tri repo `scheduled_at ASC` : non-régression d'ordre.
		switch {
		case a.ScheduledAt == nil && b.ScheduledAt != nil:
			return false // a sans échéance → après b
		case a.ScheduledAt != nil && b.ScheduledAt == nil:
			return true
		case a.ScheduledAt != nil && b.ScheduledAt != nil && !a.ScheduledAt.Equal(*b.ScheduledAt):
			return a.ScheduledAt.Before(*b.ScheduledAt)
		}

		// Départage 2 : contact enrôlé le plus tôt d'abord.
		if !a.EnteredAt.Equal(b.EnteredAt) {
			return a.EnteredAt.Before(b.EnteredAt)
		}

		// Départage 3 : ID, pour un ordre totalement déterministe (reproductible en test).
		return a.ID < b.ID
	})
}

// veridianOverdue retourne le retard d'une automation par rapport à `now` : positif si
// l'échéance est dépassée (en retard), négatif/zéro sinon. Une échéance nil = 0 (pas de
// retard mesurable, reléguée par le départage).
func veridianOverdue(item *domain.ContactAutomationWithWorkspace, now time.Time) time.Duration {
	if item == nil || item.ScheduledAt == nil {
		return 0
	}
	return now.Sub(*item.ScheduledAt)
}
