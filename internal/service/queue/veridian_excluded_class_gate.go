package queue

import (
	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian — gate d'EXCLUSION de classe de provider destinataire (cold outbound,
// ticket todo/2026-06-16-config-exclusion-provider-cold.md).
//
// BUT : permettre d'EXCLURE une ou plusieurs classes de provider destinataire de
// l'envoi cold (cas concret : « ne PAS envoyer à microsoft/outlook » sur une
// IP/un domaine fraîchement monté, car Microsoft exige le warm-up le plus dur).
// Les contacts de la classe exclue sont SKIPPÉS proprement ; le reste du
// broadcast part normalement.
//
// POURQUOI un gate DÉDIÉ (et pas rate=0 / cap=0) : les leviers throttle/cap sont
// OPT-IN avec la sémantique « 0 = pleine vitesse / illimité ». Mettre
// `rate microsoft = 0` signifie « envoie microsoft SANS throttle »
// (veridian_provider_throttle.go:75), exactement l'INVERSE d'une exclusion.
// L'exclusion exige donc un levier distinct (cf. veridian_excluded_classes.go).
//
// CONTRAT DE SKIP : identique au pré-filtre Lot 7 (veridian_prefilter.go), PAS au
// throttle/cap qui reschedulent. Une classe exclue ne « redevient » pas
// contactable au prochain tick — l'entrée part en ÉCHEC PERMANENT par envoi via
// handleError(ClassifiedError{Type:recipient, Retryable:false}) : trace
// message_history avec FailedAt (raison « excluded_provider_class:<classe> »),
// Delete de l'entrée queue, AUCUN SMTP ouvert, circuit breaker NON déclenché
// (c'est une décision de politique, pas une erreur provider).
//
// POSITION dans processEntry : AVANT le throttle minute (inutile de réserver un
// token rate.Limiter pour une classe qu'on va skipper), après le circuit breaker.
//
// NON-RÉGRESSION : liste d'exclusion vide / nil à tous les niveaux de la cascade
// = no-op strict (le gate retourne ("", false) immédiatement, comportement
// upstream inchangé). La résolution de la classe réutilise le classifier MX déjà
// en main du worker (w.veridianClassifyRecipient) — aucune nouvelle DI, aucune
// duplication de classification.

// veridianExcludedClassGate décide si le destinataire de l'entrée appartient à
// une classe explicitement exclue de l'envoi. Retourne (classe, true) si l'entrée
// doit être SKIPPÉE en échec permanent (jamais re-tentée), ("", false) si elle
// peut continuer (aucune exclusion configurée, ou la classe du destinataire n'est
// pas exclue).
//
// provider est l'infra d'envoi (intégration EmailProvider) déjà en main du worker
// au call-site (peut être nil en legacy → niveau infra de la cascade sauté).
func (w *EmailQueueWorker) veridianExcludedClassGate(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) (string, bool) {
	excluded := domain.VeridianResolveExcludedClasses(workspace, provider, entry)
	if len(excluded) == 0 {
		// No-op strict : aucune exclusion configurée (cascade vide). On NE classe
		// PAS le destinataire (pas de lookup MX inutile) — comportement upstream.
		return "", false
	}

	// Classe du destinataire : tag amont (payload) sinon classification par MX
	// RÉEL (suffixe connu = sans lookup ; inconnu = MX caché best-effort). Même
	// résolution que les gates throttle/cap → cohérence totale (une classe exclue
	// au throttle l'est ici aussi).
	class := w.veridianClassifyRecipient(entry)
	if excluded[class] {
		return class, true
	}
	return "", false
}
