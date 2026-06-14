package domain

import "strings"

// Veridian — résolution du custom tracking domain par INFRA d'envoi (Lot 5 cold).
//
// Problème : l'endpoint des liens de tracking (pixel d'ouverture /t/, redirect de
// clic /r/) est GLOBAL upstream (config.APIEndpoint), éventuellement surchargé au
// niveau WORKSPACE par CustomEndpointURL. Tous les liens pointent donc vers le même
// domaine Notifuse, quel que soit le domaine d'envoi réel. En cold, un lien de
// tracking sur un domaine DIFFÉRENT du From est un signal anti-spam : le filtre voit
// un lien vers un domaine tiers inconnu, ce qui casse la cohérence perçue avec
// l'alignement DKIM/DMARC du domaine d'envoi. La pratique standard (Lemlist /
// Instantly) est un custom tracking domain ALIGNÉ au domaine d'envoi : envoi depuis
// agences-veridian.fr → tracking sur track.agences-veridian.fr.
//
// Comme l'infra d'envoi EST le domaine d'envoi (l'intégration EmailProvider porte le
// host/IP/relai SMTP + les senders), le tracking domain se règle AU NIVEAU INFRA, en
// cohérence avec les rates/caps par infra (R2). La cascade complète, du plus
// spécifique au plus général :
//
//	1. infra  : EmailProvider.VeridianTrackingDomain      [NOUVEAU, ce fichier]
//	2. workspace : WorkspaceSettings.CustomEndpointURL     [upstream, résolu en amont]
//	3. global : config.APIEndpoint                          [upstream, résolu en amont]
//
// Le niveau 1 est résolu ICI ; les niveaux 2 et 3 sont DÉJÀ collapsés dans le
// paramètre `resolvedEndpoint` par les appelants (orchestrator / services broadcast
// posent `endpoint = CustomEndpointURL ?? apiEndpoint` avant d'appeler le sender).
// Ce helper applique donc uniquement l'override infra par-dessus l'endpoint déjà
// résolu — premier niveau non vide gagne, pas de merge.
//
// Best-effort, NON-RÉGRESSION STRICTE : provider nil OU VeridianTrackingDomain vide
// → on retourne resolvedEndpoint inchangé. Aucun comportement n'est modifié pour les
// intégrations sans config Veridian.

// VeridianResolveTrackingEndpoint retourne l'endpoint à utiliser pour générer les
// liens de tracking (/t/ pixel, /r/ redirect), en appliquant l'override custom
// tracking domain de l'infra d'envoi par-dessus l'endpoint déjà résolu au niveau
// workspace/global (resolvedEndpoint).
//
// Le tracking domain de l'infra peut être fourni soit comme domaine nu
// (track.agences-veridian.fr → normalisé en https://track.agences-veridian.fr),
// soit comme URL complète (https://track.agences-veridian.fr[/...]). Le slash final
// est retiré pour éviter un double slash dans les liens générés (l'appelant concatène
// "%s/t/%s").
func VeridianResolveTrackingEndpoint(provider *EmailProvider, resolvedEndpoint string) string {
	if provider == nil {
		return resolvedEndpoint
	}

	domainEndpoint := veridianNormalizeTrackingDomain(provider.VeridianTrackingDomain)
	if domainEndpoint == "" {
		// Pas de custom tracking domain sur l'infra → fallback strict sur
		// l'endpoint workspace/global déjà résolu en amont.
		return resolvedEndpoint
	}

	return domainEndpoint
}

// veridianNormalizeTrackingDomain transforme une valeur de tracking domain (domaine
// nu ou URL) en base d'URL exploitable pour la génération de liens. Retourne "" si
// l'entrée est vide (après trim), ce qui signale au caller de retomber sur le
// fallback. Préfixe https:// quand aucun schéma n'est présent et retire le slash
// final pour rester cohérent avec la concaténation "%s/t/%s" des générateurs de liens.
func veridianNormalizeTrackingDomain(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	// Normalise le schéma : un domaine nu (track.example.com) devient
	// https://track.example.com. http:// explicite est préservé (utile en E2E
	// staging derrière un proxy local).
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		trimmed = "https://" + trimmed
	}

	// Retire le(s) slash(es) final/finaux pour éviter "https://track.example.com//t/...".
	trimmed = strings.TrimRight(trimmed, "/")

	return trimmed
}
