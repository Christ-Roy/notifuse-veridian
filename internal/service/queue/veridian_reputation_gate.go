package queue

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork — fusible de réputation PROPORTIONNÉ (refonte du 07/10/2026).
//
// Historique : le fusible du 29/09 gelait TOUT un domaine émetteur dès que son
// taux de rejets durs sur 7 jours atteignait le seuil, ou sur une plainte. Une
// rafale de rejets chez UN fournisseur (IONOS refuse l'IP en 5.7.1 alors qu'OVH
// et Microsoft acceptent) bloquait tous les autres, et relai-agences-n2 est resté
// gelé à 16 %. Décision de Robert (07/10) : « je préfère être blacklisté plutôt
// que de ne pas envoyer du tout, il faut que ce soit proportionné ». Désormais :
//
//   - AUCUN gel total d'un domaine émetteur, ni sur un taux de rejets, ni sur une
//     plainte isolée. Une plainte (FBL) ralentit tout le domaine (÷4 pendant 7
//     jours, fenêtre glissante) et lève une alerte (log WARN + état exposé par
//     reputation-status), sans arrêter.
//   - COUPLE (domaine émetteur, classe du destinataire) : RALENTISSEMENT
//     progressif selon max(taux de rejets durs, taux de refus de politique 5.7.x)
//     sur 7 jours glissants, à partir de veridianReputationMinSent envois :
//     sous le seuil du profil = débit normal ; de 1 à 2 x le seuil = débit de la
//     classe ÷2 ; au-delà de 2 x = ÷4. Le ralentissement divise le débit par
//     minute ET le plafond journalier de la classe (gates existants, cf.
//     veridianSlowdownFactor), il ne les contourne jamais.
//   - ARRÊT d'un couple uniquement si le fournisseur REFUSE EN BLOC : plus de 50 %
//     de 5.7.x sur les 20 derniers envois du couple (24 h). Envoyer y serait
//     inutile ; le gate rend alors "gelé" pour ce candidat et le failover du pool
//     (veridian_pool_failover.go) essaie un autre profil dont le couple n'est pas
//     arrêté. Si tous le sont, l'entrée attend (SMTP jamais ouvert). Le refus en
//     bloc ne se lève pas tout seul faute d'envois : la fenêtre de 24 h vide les
//     20 derniers, puis le couple repart ralenti (÷4 tant que le taux 7 j le
//     justifie) et se réarrête aussitôt si le fournisseur refuse toujours.
//
// Le taux se recalcule à chaque gate-check depuis message_history (aucun flag
// persistant, aucune migration). Le facteur calculé est gardé quelques minutes en
// mémoire (veridianReputationFactors) pour que les gates de débit et de plafond
// le lisent sans nouvelle requête. Exposé par GET
// /api/veridian/admin/reputation-status, qui rejoue EXACTEMENT les mêmes règles
// (veridianEvaluateCouple) pour ne jamais diverger du gate.

// veridianHardBounceRateFreezeThreshold est le seuil (proportion, pas %) par
// défaut. 0.03 = 3%. Surchargeable par profil
// (EmailProvider.VeridianHardBounceFreezeThreshold, 2026-10-07).
const veridianHardBounceRateFreezeThreshold = domain.VeridianDefaultHardBounceFreezeThreshold

// veridianReputationWindow est la fenêtre glissante des taux et des plaintes.
const veridianReputationWindow = 7 * 24 * time.Hour

// veridianReputationMinSent : nombre minimal d'envois dans un couple sur 7 jours
// avant toute réaction à un taux. 20 : au seuil de 8 %, 2 rejets sur 20 (10 %)
// ralentissent ÷2, 1 sur 20 (5 %) non ; en dessous, un taux est du bruit (1 rejet
// sur 3 = 33 %).
const veridianReputationMinSent = 20

// Facteurs de ralentissement (le débit de la classe est DIVISÉ par ce facteur).
const (
	veridianSlowdownMild     = 2 // de 1 à 2 x le seuil
	veridianSlowdownStrong   = 4 // au-delà de 2 x le seuil
	veridianComplaintSlowdwn = 4 // plainte dans la fenêtre : tout le domaine ÷4
)

// Refus en bloc : plus de 50 % de refus 5.7.x sur les 20 derniers envois du
// couple, vus sur les dernières 24 h.
const (
	veridianBlockLastN    = 20
	veridianBlockShare    = 0.5
	veridianBlockLookback = 24 * time.Hour
)

// veridianReputationFactorTTL : âge maximal d'un facteur lu par les gates de débit.
const veridianReputationFactorTTL = 5 * time.Minute

// veridianReputationVerdict est la décision pour UN couple (domaine, classe).
type veridianReputationVerdict struct {
	Factor  int    // 1 = débit normal, 2 ou 4 = débit divisé
	Reason  string // "", "hard_bounce_rate", "policy_refusal_rate", "complaint"
	Stopped bool   // refus en bloc : le couple n'envoie plus
}

// veridianClassSlowdown calcule le facteur d'un couple depuis ses comptes 7 jours.
func veridianClassSlowdown(c domain.VeridianReputationCounts, threshold float64) (int, string) {
	if c.Sent < veridianReputationMinSent || threshold <= 0 {
		return 1, ""
	}
	hard := float64(c.HardBounces) / float64(c.Sent)
	policy := float64(c.PolicyRefusals) / float64(c.Sent)
	rate, reason := hard, "hard_bounce_rate"
	if policy > hard {
		rate, reason = policy, "policy_refusal_rate"
	}
	switch {
	case rate >= 2*threshold:
		return veridianSlowdownStrong, reason
	case rate >= threshold:
		return veridianSlowdownMild, reason
	}
	return 1, ""
}

// veridianBulkRefusal : le fournisseur refuse en bloc si, sur au moins
// veridianBlockLastN envois récents, plus de 50 % sont des refus 5.7.x.
func veridianBulkRefusal(recentSent, recentPolicy int) bool {
	return recentSent >= veridianBlockLastN && float64(recentPolicy)/float64(recentSent) > veridianBlockShare
}

// veridianEvaluateCouple combine plainte du domaine, taux du couple et refus en
// bloc. Partagé par le gate et l'API de statut. recentSent/recentPolicy ne sont
// utiles que si counts.PolicyRefusals peut atteindre le seuil de blocage.
func veridianEvaluateCouple(complaints int, counts domain.VeridianReputationCounts, recentSent, recentPolicy int, threshold float64) veridianReputationVerdict {
	v := veridianReputationVerdict{Factor: 1}
	if factor, reason := veridianClassSlowdown(counts, threshold); factor > v.Factor {
		v.Factor, v.Reason = factor, reason
	}
	if complaints > 0 && veridianComplaintSlowdwn > v.Factor {
		v.Factor, v.Reason = veridianComplaintSlowdwn, "complaint"
	}
	if veridianBulkRefusal(recentSent, recentPolicy) {
		v.Stopped = true
		v.Reason = "bulk_policy_refusal"
	}
	return v
}

// veridianNeedsBulkCheck : inutile d'interroger les 20 derniers envois tant que
// le couple n'a pas assez de refus 5.7.x sur 7 jours pour dépasser 50 % de 20.
func veridianNeedsBulkCheck(c domain.VeridianReputationCounts) bool {
	return c.Sent >= veridianBlockLastN && float64(c.PolicyRefusals) > veridianBlockShare*float64(veridianBlockLastN)
}

// --- cache des facteurs (lecture par les gates de débit et de plafond) ---

type veridianReputationFactorEntry struct {
	factor int
	at     time.Time
}

type veridianReputationFactors struct{ m sync.Map }

func veridianReputationFactorKey(workspaceID, senderDomain, class string) string {
	return workspaceID + "|" + senderDomain + "|" + class
}

func (f *veridianReputationFactors) set(workspaceID, senderDomain, class string, factor int) {
	f.m.Store(veridianReputationFactorKey(workspaceID, senderDomain, class), veridianReputationFactorEntry{factor: factor, at: time.Now()})
}

func (f *veridianReputationFactors) get(workspaceID, senderDomain, class string) int {
	if v, ok := f.m.Load(veridianReputationFactorKey(workspaceID, senderDomain, class)); ok {
		e := v.(veridianReputationFactorEntry)
		if e.factor > 1 && time.Since(e.at) <= veridianReputationFactorTTL {
			return e.factor
		}
	}
	return 1
}

// veridianSlowdownFactor est le facteur de ralentissement que le gate de
// réputation vient de calculer pour le couple (domaine de l'adresse FROM de
// l'entrée, classe du destinataire). 1 si inconnu ou périmé : les gates de débit
// ne font AUCUNE requête de réputation, ils lisent ce que le gate a posé juste
// avant dans la boucle de sélection des candidats.
func (w *EmailQueueWorker) veridianSlowdownFactor(workspace *domain.Workspace, entry *domain.EmailQueueEntry, class string) int {
	senderDomain := veridianEmailDomain(entry.Payload.FromAddress)
	if senderDomain == "" {
		return 1
	}
	workspaceID := ""
	if workspace != nil {
		workspaceID = workspace.ID
	}
	return w.reputationFactors.get(workspaceID, senderDomain, class)
}

// veridianSlowCap divise un plafond journalier par le facteur (plancher à 1).
func veridianSlowCap(cap, factor int) int {
	if factor <= 1 || cap <= 0 {
		return cap
	}
	c := (cap + factor - 1) / factor
	if c < 1 {
		c = 1
	}
	return c
}

// veridianReputationGate : retourne (délai, true) UNIQUEMENT si le couple est à
// l'arrêt (le fournisseur refuse en bloc) ; l'entrée est alors reportée sans
// ouvrir SMTP, ou bascule sur un autre profil du pool. Dans tous les autres cas
// (sain, ralenti, plainte) retourne (0, false) après avoir posé le facteur de
// ralentissement lu par les gates de débit. Une erreur DB n'arrête rien (ce
// fusible ne stoppe plus que sur preuve) : elle est loguée en ERROR.
func (w *EmailQueueWorker) veridianReputationGate(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) (time.Duration, bool) {
	senderDomain := veridianEmailDomain(entry.Payload.FromAddress)
	if senderDomain == "" {
		// Pas d'attribution infra possible : best-effort, on ne ralentit pas.
		return 0, false
	}

	workspaceID := ""
	if workspace != nil {
		workspaceID = workspace.ID
	}
	class := w.veridianClassifyRecipient(entry)
	threshold := provider.VeridianEffectiveHardBounceFreezeThreshold()
	now := time.Now().UTC()
	since := now.Add(-veridianReputationWindow)

	logErr := func(step string, err error) {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":      entry.ID,
			"workspace_id":  workspaceID,
			"sender_domain": senderDomain,
			"error":         err.Error(),
		}).Error("Reputation fuse: " + step + " failed; no slowdown applied")
	}

	complaints, err := w.messageHistoryRepo.CountComplainedSinceForSenderDomain(w.ctx, workspaceID, senderDomain, since)
	if err != nil {
		logErr("complaint count", err)
		complaints = 0
	}
	if complaints > 0 {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":       entry.ID,
			"integration_id": entry.IntegrationID,
			"sender_domain":  senderDomain,
			"complaints_7d":  complaints,
			"fuse":           "complaint",
			"slowdown":       veridianComplaintSlowdwn,
		}).Warn("Reputation ALERT: complaint in the last 7 days, sender domain slowed (not stopped)")
	}

	var counts domain.VeridianReputationCounts
	sentCount, err := w.messageHistoryRepo.CountSentSinceForSenderDomain(w.ctx, workspaceID, senderDomain, since)
	if err != nil {
		logErr("sent count", err)
	} else if sentCount > 0 {
		byClass, err := w.messageHistoryRepo.ReputationCountsByClassSinceForSenderDomain(w.ctx, workspaceID, senderDomain, since)
		if err != nil {
			logErr("per-class counts", err)
		} else {
			counts = byClass[class]
		}
	}

	recentSent, recentPolicy := 0, 0
	if veridianNeedsBulkCheck(counts) {
		recentSent, recentPolicy, err = w.messageHistoryRepo.RecentClassOutcomesForSenderDomain(w.ctx, workspaceID, senderDomain, class, veridianBlockLastN, now.Add(-veridianBlockLookback))
		if err != nil {
			logErr("recent class outcomes", err)
			recentSent, recentPolicy = 0, 0
		}
	}

	verdict := veridianEvaluateCouple(complaints, counts, recentSent, recentPolicy, threshold)
	w.reputationFactors.set(workspaceID, senderDomain, class, verdict.Factor)

	if verdict.Stopped {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":           entry.ID,
			"integration_id":     entry.IntegrationID,
			"sender_domain":      senderDomain,
			"provider_class":     class,
			"recent_sent":        recentSent,
			"recent_policy":      recentPolicy,
			"policy_refusals_7d": counts.PolicyRefusals,
			"fuse":               verdict.Reason,
		}).Warn("Reputation fuse: provider refuses in bulk (>50% 5.7.x on last 20 sends), this couple is stopped; failover to another profile applies")
		return veridianDailyCapRecheckInterval, true
	}
	if verdict.Factor > 1 {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":           entry.ID,
			"integration_id":     entry.IntegrationID,
			"sender_domain":      senderDomain,
			"provider_class":     class,
			"sent_7d":            counts.Sent,
			"hard_bounces_7d":    counts.HardBounces,
			"policy_refusals_7d": counts.PolicyRefusals,
			"threshold":          threshold,
			"slowdown":           verdict.Factor,
			"fuse":               verdict.Reason,
		}).Debug("Reputation fuse: couple slowed")
	}
	return 0, false
}

// VeridianReputationStatus is the read model shared by the gate above and the
// admin API (veridian_reputation_handler.go): same repository methods, same
// window, same rules (veridianEvaluateCouple).
type VeridianReputationStatus struct {
	SenderDomain    string  `json:"sender_domain"`
	WindowDays      int     `json:"window_days"`
	Sent7d          int     `json:"sent_7d"`
	HardBounces7d   int     `json:"hard_bounces_7d"`
	HardBounceRate  float64 `json:"hard_bounce_rate"`
	Threshold       float64 `json:"hard_bounce_rate_threshold"`
	ThresholdCustom bool    `json:"hard_bounce_rate_threshold_custom"`
	// MinSent : envois minimum dans un couple avant toute réaction à un taux.
	MinSent      int `json:"min_sent_for_reaction"`
	Complaints7d int `json:"complaints_7d"`
	// Alert : plainte dans la fenêtre (tout le domaine est ralenti ÷4, jamais arrêté).
	Alert bool `json:"alert"`
	// DomainFactor : facteur appliqué à TOUTES les classes du domaine (4 si plainte, sinon 1).
	DomainFactor   int                                    `json:"domain_slowdown_factor"`
	Classes        []domain.VeridianReputationClassStatus `json:"classes"`
	SlowedClasses  []string                               `json:"slowed_classes"`
	StoppedClasses []string                               `json:"stopped_classes"`
}

// VeridianComputeReputationStatus computes the live reputation status for one
// sender domain — exported so the HTTP admin handler can read it without
// duplicating the gate's thresholds or queries.
func VeridianComputeReputationStatus(
	ctx context.Context,
	repo domain.MessageHistoryRepository,
	workspaceID, senderDomain string,
	provider *domain.EmailProvider,
	now time.Time,
) (VeridianReputationStatus, error) {
	status := VeridianReputationStatus{
		SenderDomain:    senderDomain,
		WindowDays:      int(veridianReputationWindow / (24 * time.Hour)),
		Threshold:       provider.VeridianEffectiveHardBounceFreezeThreshold(),
		ThresholdCustom: provider.VeridianHasCustomHardBounceFreezeThreshold(),
		MinSent:         veridianReputationMinSent,
		DomainFactor:    1,
		Classes:         []domain.VeridianReputationClassStatus{},
		SlowedClasses:   []string{},
		StoppedClasses:  []string{},
	}
	since := now.UTC().Add(-veridianReputationWindow)

	complaints, err := repo.CountComplainedSinceForSenderDomain(ctx, workspaceID, senderDomain, since)
	if err != nil {
		return status, err
	}
	status.Complaints7d = complaints
	status.Alert = complaints > 0
	if complaints > 0 {
		status.DomainFactor = veridianComplaintSlowdwn
	}

	sentCount, err := repo.CountSentSinceForSenderDomain(ctx, workspaceID, senderDomain, since)
	if err != nil {
		return status, err
	}
	status.Sent7d = sentCount

	hardBounces, err := repo.CountHardBouncedSinceForSenderDomain(ctx, workspaceID, senderDomain, since)
	if err != nil {
		return status, err
	}
	status.HardBounces7d = hardBounces
	if sentCount > 0 {
		status.HardBounceRate = float64(hardBounces) / float64(sentCount)
	}

	byClass, err := repo.ReputationCountsByClassSinceForSenderDomain(ctx, workspaceID, senderDomain, since)
	if err != nil {
		return status, err
	}
	for class, c := range byClass {
		cs := domain.VeridianReputationClassStatus{
			Class: class, Sent7d: c.Sent, HardBounces7d: c.HardBounces, PolicyRefusals7d: c.PolicyRefusals, Factor: 1,
		}
		if c.Sent > 0 {
			cs.HardBounceRate = float64(c.HardBounces) / float64(c.Sent)
			cs.PolicyRefusalRate = float64(c.PolicyRefusals) / float64(c.Sent)
		}
		if class == "" {
			// Lignes sans classe persistée : comptées dans le domaine, jamais un
			// couple (aucun destinataire futur n'a cette classe).
			cs.Class = "unclassified"
			status.Classes = append(status.Classes, cs)
			continue
		}
		recentSent, recentPolicy := 0, 0
		if veridianNeedsBulkCheck(c) {
			recentSent, recentPolicy, err = repo.RecentClassOutcomesForSenderDomain(ctx, workspaceID, senderDomain, class, veridianBlockLastN, now.UTC().Add(-veridianBlockLookback))
			if err != nil {
				return status, err
			}
			cs.RecentSent, cs.RecentPolicyRefusals = recentSent, recentPolicy
		}
		v := veridianEvaluateCouple(complaints, c, recentSent, recentPolicy, status.Threshold)
		cs.Factor, cs.Reason, cs.Stopped = v.Factor, v.Reason, v.Stopped
		status.Classes = append(status.Classes, cs)
		switch {
		case cs.Stopped:
			status.StoppedClasses = append(status.StoppedClasses, class)
		case cs.Factor > 1:
			status.SlowedClasses = append(status.SlowedClasses, class)
		}
	}
	sort.Slice(status.Classes, func(i, j int) bool { return status.Classes[i].Class < status.Classes[j].Class })
	sort.Strings(status.SlowedClasses)
	sort.Strings(status.StoppedClasses)

	return status, nil
}

// VeridianEmailDomain exports veridianEmailDomain for callers outside this
// package (the reputation status service) that need the exact same domain
// extraction as the gate, so "which infra does this integration belong to"
// never diverges between the enforcement path and the read-only status API.
func VeridianEmailDomain(email string) string {
	return veridianEmailDomain(email)
}
