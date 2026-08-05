package queue

import (
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian — gate de PLAFOND JOURNALIER d'envoi cold outbound, jumeau du gate
// de throttle par minute (veridian_provider_throttle.go). Appelé par
// worker.go:processEntry AU MÊME endroit (avant MarkAsProcessing, après le
// throttle minute) avec le même contrat skip-and-reschedule : une entrée
// plafonnée est re-planifiée via SetNextRetry SANS incrémenter les attempts
// (pas de consommation de retry, pas de head-of-line blocking).
//
// Pourquoi un gate distinct du throttle minute : le throttle minute est un
// token-bucket EN MÉMOIRE (rate.Limiter) qui se réinitialise au redémarrage du
// worker — incapable de garantir un plafond JOURNALIER durable. Ce gate-ci lit
// la SOURCE DE VÉRITÉ persistée (message_history, peuplée à chaque envoi) et
// compte les envois depuis minuit : pas de table compteur parallèle, pas de
// cron de reset (le "jour" = sent_at >= date_trunc('day', now()) calculé à la
// lecture). Survit aux redémarrages par construction.
//
// Deux plafonds, le PLUS RESTRICTIF gagne (n'importe lequel déclenche le skip) :
//  1. cap par DESTINATAIRE : max N envois/jour vers une MÊME adresse (anti-
//     harcèlement). COUNT message_history WHERE contact_email=? AND sent_at>=minuit.
//  2. cap par CLASSE : max N envois/jour vers toute une classe de provider
//     (réputation). COUNT … WHERE classe(contact_email) AND sent_at>=minuit ;
//     la classe n'étant pas en DB, le repo filtre par la liste de domaines de
//     la classe (dérivée en Go).
//
// ⚠️ Le cap par CLASSE est keyé PAR INFRA ÉMETTRICE (warm-up multi-domaine,
// 2026-06-18) : la doctrine warm-up = « 1 infra (IP + domaine d'envoi) → 1 classe
// destinataire = N/jour ». Le compteur de classe est donc filtré par le DOMAINE de
// l'adresse FROM de l'entrée (veridian_sender_email, V53) → deux domaines d'envoi
// frappant la même classe ne se marchent plus dessus, chacun a son propre plafond
// vers cette classe. Si l'entrée n'a pas de FROM exploitable (legacy, pré-V53), on
// retombe sur le COUNT workspace-global (toutes infras confondues) = non-régression
// stricte. Le cap par DESTINATAIRE reste workspace-global (anti-harcèlement = ne
// JAMAIS sur-solliciter une personne, peu importe l'infra qui envoie).
//
// Résolution de la config (du plus spécifique au plus général), identique aux
// rates : payload (copié à l'enqueue depuis broadcast.metadata) → infra
// (EmailProvider, R2) → workspace settings (lus en live) → rien = no-op strict
// (non-régression upstream).
//
// V55 : une erreur DB bloque et replanifie l'entrée. Un plafond de réputation
// n'est pas une métrique best-effort : sans preuve de capacité disponible, le
// worker n'est pas autorisé à ouvrir SMTP.

// veridianDailyCapRecheckInterval borne le report d'une entrée plafonnée.
// Sémantiquement le compteur se réinitialise au prochain minuit, mais re-checker
// toutes les heures permet de prendre en compte un relèvement de cap en cours
// de journée sans laisser l'entrée endormie jusqu'au lendemain.
const veridianDailyCapRecheckInterval = time.Hour

// veridianStartOfDayUTC retourne minuit UTC du jour de `now`. Le compteur
// journalier raisonne en jour calendaire UTC (cohérent avec sent_at stocké en
// TIMESTAMPTZ et le now() serveur des conteneurs, en UTC).
func veridianStartOfDayUTC(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// veridianResolveDailyCaps fusionne la config des trois niveaux de la cascade
// (du plus spécifique au plus général) : payload broadcast → infra
// (EmailProvider, R2) → workspace. Le cap classe et le cap destinataire sont
// résolus INDÉPENDAMMENT (chacun prend son premier niveau non vide) : une infra
// peut poser son cap-classe et hériter le cap-destinataire du workspace.
// provider peut être nil (legacy / intégration sans config Veridian) → niveau
// sauté. Retourne le cap classe (map, peut être nil) et le cap destinataire
// (0 = illimité).
func veridianResolveDailyCaps(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) (classCaps map[string]int, perRecipientCap int) {
	switch {
	case len(entry.Payload.VeridianProviderClassDailyCap) > 0:
		classCaps = entry.Payload.VeridianProviderClassDailyCap
	case provider != nil && len(provider.VeridianProviderClassDailyCap) > 0:
		classCaps = provider.VeridianProviderClassDailyCap
	case workspace != nil:
		classCaps = workspace.Settings.VeridianProviderClassDailyCap
	}

	switch {
	case entry.Payload.VeridianPerRecipientDailyCap > 0:
		perRecipientCap = entry.Payload.VeridianPerRecipientDailyCap
	case provider != nil && provider.VeridianPerRecipientDailyCap > 0:
		perRecipientCap = provider.VeridianPerRecipientDailyCap
	case workspace != nil:
		perRecipientCap = workspace.Settings.VeridianPerRecipientDailyCap
	}
	return classCaps, perRecipientCap
}

// veridianWarmupCap retourne le cap journalier TOTAL imposé par la rampe de warmup
// de l'infra (si active), 0 sinon. C'est un plafond HOLISTIQUE de l'infra : « J1 = N
// mails max, TOUTES classes destinataires confondues » (standard Lemlist/Instantly).
// Il NE s'applique PAS par classe — il plafonne le VOLUME TOTAL émis par l'IP/domaine
// d'envoi (cf. veridianWarmupTotalCount qui compte le total par domaine émetteur, sans
// filtre classe). Le warmup se règle PAR INFRA uniquement (l'objet d'une rampe est une
// IP/un domaine d'envoi, pas un workspace) : pas de niveau broadcast/workspace ici.
// Cf. veridian_warmup.go.
func veridianWarmupCap(provider *domain.EmailProvider, now time.Time) int {
	if provider == nil {
		return 0
	}
	return domain.VeridianWarmupCapForDay(
		provider.VeridianWarmupStartedAt,
		provider.VeridianWarmupSchedule,
		provider.VeridianWarmupStepDays,
		now,
	)
}

// veridianDailyCapGate décide si l'entrée doit être reportée pour cause de
// plafond journalier (destinataire ou classe). Retourne (délai, true) si
// l'entrée doit être re-planifiée, (0, false) si elle peut partir (aucun cap
// atteint) ou si aucun cap ne s'applique. No-op strict sans configuration.
func (w *EmailQueueWorker) veridianDailyCapGate(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) (time.Duration, bool) {
	classCaps, perRecipientCap := veridianResolveDailyCaps(workspace, provider, entry)

	// Rampe de warmup : si l'infra est en warmup, son cap courant est un plafond
	// TOTAL de l'infra (toutes classes confondues) qui PRIME sur le cap-classe
	// statique. 0 = pas de warmup actif. Cf. veridianWarmupTotalCount plus bas.
	warmupCap := veridianWarmupCap(provider, time.Now())

	if len(classCaps) == 0 && perRecipientCap <= 0 && warmupCap <= 0 {
		return 0, false
	}

	workspaceID := ""
	if workspace != nil {
		workspaceID = workspace.ID
	}
	since := veridianStartOfDayUTC(time.Now())

	// 1. Cap par destinataire (le plus net, indexé). Le plus restrictif gagne :
	//    on le teste en premier car il borne le harcèlement d'un même contact
	//    indépendamment de la classe.
	if perRecipientCap > 0 {
		count, err := w.messageHistoryRepo.CountSentSinceForContact(w.ctx, workspaceID, entry.ContactEmail, since)
		if err != nil {
			w.logger.WithFields(map[string]interface{}{
				"entry_id":     entry.ID,
				"workspace_id": workspaceID,
				"error":        err.Error(),
			}).Error("Daily per-recipient cap count failed; SMTP blocked")
			return w.veridianRescheduleCapped(entry, "per_recipient_count_error", 0, perRecipientCap, "")
		} else if count >= perRecipientCap {
			return w.veridianRescheduleCapped(entry, "per_recipient", count, perRecipientCap, "")
		}
	}

	// 2a. Plafond WARMUP (si l'infra est en warmup) — TOTAL par infra, toutes classes.
	//     C'est un cap HOLISTIQUE : « J1 = N max, peu importe le destinataire » — il
	//     PRIME sur le cap-classe statique et le COURT-CIRCUITE (on n'évalue pas en
	//     plus le cap-classe ; le warmup est plus restrictif et déjà vérifié). On
	//     compte le TOTAL des envois du jour DEPUIS le domaine émetteur de l'infra
	//     (sans aucun filtre sur la classe destinataire) → robuste aux classes MX,
	//     que le COUNT-par-classe n'enforçait PAS (VeridianDomainsForClass = [] sur MX).
	//     Sans domaine émetteur exploitable (legacy / sender absent), aucune infra
	//     n'est attribuable → on ne peut pas enforcer le warmup (best-effort, pass).
	if warmupCap > 0 {
		senderDomain := veridianEmailDomain(entry.Payload.FromAddress)
		if senderDomain != "" {
			count, err := w.messageHistoryRepo.CountSentSinceForSenderDomain(w.ctx, workspaceID, senderDomain, since)
			if err != nil {
				w.logger.WithFields(map[string]interface{}{
					"entry_id":      entry.ID,
					"workspace_id":  workspaceID,
					"sender_domain": senderDomain,
					"error":         err.Error(),
				}).Error("Daily warmup cap count failed; SMTP blocked")
				return w.veridianRescheduleCapped(entry, "warmup_count_error", 0, warmupCap, "")
			} else if count >= warmupCap {
				return w.veridianRescheduleCapped(entry, "warmup", count, warmupCap, "")
			}
		}
		// Warmup actif → il GOUVERNE le plafond de l'infra. On ne retombe PAS sur le
		// cap-classe statique (le warmup est le plafond effectif pendant la rampe).
		return 0, false
	}

	// 2b. Cap par classe STATIQUE (réputation), uniquement HORS warmup. Dérivation de
	//     la classe : tag contact (option B) sinon classification par MX RÉEL (Lot 4 :
	//     suffixe connu sans lookup, inconnu via MX caché). ⚠️ Pour une classe MX
	//     (ovh/ionos/…), VeridianDomainsForClass renvoie une liste vide → le COUNT par
	//     domaine ne s'enforce pas (dégradation gracieuse documentée ; le throttle
	//     minute protège la réputation sur le hot path). Le cap-classe reste pleinement
	//     enforcé pour les classes adossées à un suffixe (google public, etc.).
	if len(classCaps) > 0 {
		class := w.veridianClassifyRecipient(entry)
		if classCap, ok := classCaps[class]; ok && classCap > 0 {
			domains, exclude := domain.VeridianDomainsForClass(class)
			count, err := w.veridianCountClassForInfra(workspaceID, domains, exclude, entry, since)
			if err != nil {
				w.logger.WithFields(map[string]interface{}{
					"entry_id":       entry.ID,
					"workspace_id":   workspaceID,
					"provider_class": class,
					"error":          err.Error(),
				}).Error("Daily provider-class cap count failed; SMTP blocked")
				return w.veridianRescheduleCapped(entry, "provider_class_count_error", 0, classCap, class)
			} else if count >= classCap {
				return w.veridianRescheduleCapped(entry, "provider_class", count, classCap, class)
			}
		}
	}

	return 0, false
}

// veridianCountClassForInfra compte les envois du jour vers une classe destinataire
// (domains + exclude) en les attribuant à l'INFRA ÉMETTRICE de l'entrée courante
// (warm-up multi-domaine, 2026-06-18). L'infra réputationnelle = le DOMAINE de
// l'adresse FROM (les N adresses d'un même domaine partagent l'IP/réputation, donc
// comptent ensemble) → on dérive senderDomain via veridianEmailDomain(FromAddress)
// (helper partagé avec le pré-filtre, normalisation identique : lowercase, trim,
// point FQDN retiré).
//
// Si l'entrée n'a pas de FROM exploitable (legacy / pré-V53 / sender inconnu), on
// retombe sur le COUNT workspace-global (CountSentSinceForDomains) = comportement
// strictement antérieur (non-régression). Toute la cascade de résolution du cap
// reste inchangée ; seule la GRANULARITÉ DU COMPTEUR change (par infra au lieu de
// par workspace) quand l'attribution est possible.
func (w *EmailQueueWorker) veridianCountClassForInfra(workspaceID string, domains []string, exclude bool, entry *domain.EmailQueueEntry, since time.Time) (int, error) {
	senderDomain := veridianEmailDomain(entry.Payload.FromAddress)
	if senderDomain == "" {
		// Pas d'attribution infra possible → compteur workspace-global (legacy).
		return w.messageHistoryRepo.CountSentSinceForDomains(w.ctx, workspaceID, domains, exclude, since)
	}
	return w.messageHistoryRepo.CountSentSinceForDomainsAndSenderDomain(w.ctx, workspaceID, domains, exclude, senderDomain, since)
}

// veridianRescheduleCapped construit le délai de report d'une entrée plafonnée
// et logge le motif. Le report est borné à veridianDailyCapRecheckInterval
// (re-check horaire) : suffisant pour absorber un changement de cap sans
// attendre le prochain minuit, négligeable en charge.
func (w *EmailQueueWorker) veridianRescheduleCapped(entry *domain.EmailQueueEntry, capKind string, count, cap int, class string) (time.Duration, bool) {
	w.logger.WithFields(map[string]interface{}{
		"entry_id":       entry.ID,
		"integration_id": entry.IntegrationID,
		"cap_kind":       capKind,
		"provider_class": class,
		"sent_today":     count,
		"daily_cap":      cap,
		"retry_in":       veridianDailyCapRecheckInterval.String(),
	}).Debug("Daily cap reached, rescheduling without attempt increment")

	return veridianDailyCapRecheckInterval, true
}
