package queue

import (
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian — gate de throttle par classe de provider destinataire, appelé par
// worker.go:processEntry AVANT MarkAsProcessing (même position que le check
// circuit breaker, pour ne pas consommer d'attempt sur un simple skip).
//
// Design skip-and-reschedule (et non Wait bloquant) :
//   - une classe saturée (ex. gmail à 0.5/min) ne bloque pas le worker — les
//     entrées des autres classes du batch continuent de partir (pas de
//     head-of-line blocking sur un batch mixte) ;
//   - l'entrée skippée est re-planifiée via SetNextRetry SANS incrémenter les
//     attempts (pattern circuit breaker existant) ;
//   - le throttle émetteur existant (IntegrationRateLimiter, Wait bloquant)
//     reste appliqué par-dessus, inchangé — les deux étages composent.
//
// Résolution de la config (du plus spécifique au plus général) :
//  1. débits du broadcast (copiés dans le payload à l'enqueue depuis
//     broadcast.metadata["veridian_provider_class_rates"]) ;
//  2. débits de l'INFRA d'envoi (intégration EmailProvider, R2) — une IP en
//     warm-up porte ses propres débits, indépendants du workspace ;
//  3. défauts du workspace (settings, lus en live — pas de staleness) ;
//  4. rien → no-op strict, comportement upstream inchangé.
//
// Résolution de la classe : tag contact posé en amont (payload, option B du
// contrat provider_class), sinon classification locale par suffixe de domaine
// (option A, fonction pure, zéro I/O).

// veridianMaxProviderClassRetryDelay borne le report d'une entrée throttlée.
// Pour les cadences très lentes (warm-up 7/jour → un token toutes les ~3h25),
// re-checker périodiquement permet de prendre en compte un changement de
// config sans attendre le prochain token théorique.
const veridianMaxProviderClassRetryDelay = 5 * time.Minute

// veridianResolveProviderClassRates fusionne la config des trois niveaux de la
// cascade (du plus spécifique au plus général) : payload broadcast → infra
// (EmailProvider) → workspace. Le premier niveau non vide gagne (pas de merge
// par classe entre niveaux : un override infra REMPLACE le workspace, comme un
// override broadcast remplace l'infra). provider peut être nil (cas legacy /
// intégration sans config Veridian) → on saute simplement ce niveau.
func veridianResolveProviderClassRates(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) map[string]float64 {
	if rates := entry.Payload.VeridianProviderClassRates; len(rates) > 0 {
		return rates
	}
	if provider != nil && len(provider.VeridianProviderClassRates) > 0 {
		return provider.VeridianProviderClassRates
	}
	if workspace != nil && len(workspace.Settings.VeridianProviderClassRates) > 0 {
		return workspace.Settings.VeridianProviderClassRates
	}
	return nil
}

// veridianProviderClassGate décide si l'entrée doit être reportée pour cause
// de throttle par classe destinataire. Retourne (délai, true) si l'entrée doit
// être re-planifiée, (0, false) si elle peut partir maintenant (un token a
// alors été consommé) ou si aucun throttle classe ne s'applique. provider est
// l'infra d'envoi (intégration EmailProvider) déjà en main du worker au call-site.
func (w *EmailQueueWorker) veridianProviderClassGate(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) (time.Duration, bool) {
	rates := veridianResolveProviderClassRates(workspace, provider, entry)
	if len(rates) == 0 {
		return 0, false
	}

	// Classe du destinataire : tag amont (payload) sinon classification par MX
	// RÉEL (suffixe connu = sans lookup ; inconnu = MX caché best-effort).
	class := w.veridianClassifyRecipient(entry)

	ratePerMinute, ok := rates[class]
	if !ok || ratePerMinute <= 0 {
		// Classe sans débit configuré = non throttlée (seul l'étage émetteur
		// s'applique). Permet de ne contraindre que gmail/microsoft et de
		// laisser filer le corporate.
		return 0, false
	}

	if w.providerClassLimiter.Allow(entry.IntegrationID, class, ratePerMinute) {
		return 0, false
	}

	// Pas de token : estimer l'arrivée du prochain (60/rate secondes).
	delay := time.Duration(60.0 / ratePerMinute * float64(time.Second))

	// Veridian — JITTER TEMPOREL : disperser ce délai autour de sa valeur
	// nominale (±jitter_pct) AVANT le clamp, pour casser le rythme métronomique
	// (tell de machine cold). Le débit moyen reste piloté par le token-bucket
	// ci-dessus (Allow()), le jitter ne disperse que les re-checks. Le gate
	// applique LUI-MÊME le jitter (le worker n'en sait rien, diff worker.go = 0).
	// No-op strict si jitter résolu à 0. Cf. veridian_jitter.go.
	delay = veridianJitterDelay(workspace, provider, entry, delay)
	if delay > veridianMaxProviderClassRetryDelay {
		delay = veridianMaxProviderClassRetryDelay
	}
	if delay < time.Second {
		delay = time.Second
	}

	w.logger.WithFields(map[string]interface{}{
		"entry_id":       entry.ID,
		"integration_id": entry.IntegrationID,
		"provider_class": class,
		"rate_per_min":   ratePerMinute,
		"retry_in":       delay.String(),
	}).Debug("Provider class throttled (jittered), rescheduling without attempt increment")

	return delay, true
}

// GetProviderClassStats expose les stats des limiters par classe
// (clé "integrationID|classe") à côté de GetStats (limiters émetteurs).
func (w *EmailQueueWorker) GetProviderClassStats() map[string]RateLimiterStats {
	return w.providerClassLimiter.GetStats()
}

// SetVeridianMXClassifier remplace le classifier MX du worker (DI pour les
// tests : injection d'un faux resolver sans DNS réel). En prod, le constructeur
// installe le classifier réseau par défaut — ne PAS appeler ce setter.
func (w *EmailQueueWorker) SetVeridianMXClassifier(c *domain.VeridianMXClassifier) {
	if c != nil {
		w.providerMXClassifier = c
	}
}

// veridianClassifyRecipient résout la classe de provider destinataire pour une
// entrée de queue, dans l'ordre de précédence du contrat cold :
//  1. tag posé en amont sur le payload (option B, custom_string_5 → résolu à
//     l'enqueue) : prime, ZÉRO I/O ;
//  2. classification par MX RÉEL (Lot 4) : suffixe connu → classe directe sans
//     lookup ; suffixe inconnu → MX caché (best-effort, timeout court, fallback
//     corporate_selfhost). Si le classifier MX est absent (worker construit hors
//     constructeur normal), on retombe sur la classification pure par suffixe
//     (domain.ClassifyProviderClass) — comportement pré-Lot-4, jamais de panic.
//
// Utilisé par les DEUX gates (throttle minute + cap journalier) pour une
// classification unique et cohérente du destinataire.
func (w *EmailQueueWorker) veridianClassifyRecipient(entry *domain.EmailQueueEntry) string {
	if class := entry.Payload.VeridianProviderClass; domain.IsValidProviderClass(class) {
		return class
	}
	if w.providerMXClassifier != nil {
		return w.providerMXClassifier.ClassifyEmail(w.ctx, entry.ContactEmail)
	}
	return domain.ClassifyProviderClass(entry.ContactEmail)
}
