package domain

import "strings"

// Veridian — EXCLUSION de classes de provider destinataire (cold outbound,
// ticket todo/2026-06-16-config-exclusion-provider-cold.md).
//
// PROBLÈME résolu : les trois leviers cold existants (rate/min, cap/jour, pixel)
// sont tous OPT-IN avec la sémantique « 0 = pleine vitesse / illimité ». Aucun ne
// peut exprimer « ne PAS envoyer à cette classe » :
//   - rate microsoft = 0  → veridian_provider_throttle.go:75 traite ça comme
//     « classe SANS débit configuré = NON throttlée » → microsoft part à PLEINE
//     VITESSE (exactement l'inverse d'une exclusion). Piège dangereux.
//   - cap/jour microsoft = 0 → idem, illimité.
// Il faut donc un levier DÉDIÉ : une LISTE de classes explicitement exclues de
// l'envoi. Distinct des rates/caps (0 ≠ exclu), opt-in strict (liste vide = rien
// n'est exclu = non-régression upstream).
//
// CAS D'USAGE #1 (Robert, audit cold 2026-06-16) : une IP/un domaine fraîchement
// monté ne doit PAS taper Microsoft/Outlook (réputation la plus dure à warmer,
// SNDS/SmartScreen impitoyables). On exclut `microsoft` de l'infra le temps du
// warm-up, puis on l'ouvre quand l'IP est mature. Les contacts microsoft sont
// SKIPPÉS proprement par le gate worker (échec PERMANENT par envoi, pas de SMTP
// ouvert, pas de bounce) ; le reste du broadcast part normalement.
//
// CASCADE (du plus spécifique au plus général), identique aux rates/caps :
//   1. broadcast (payload, copié à l'enqueue depuis
//      broadcast.metadata["veridian_excluded_provider_classes"]) ;
//   2. infra (EmailProvider, JSON blob — pas de migration, comme les champs R2) ;
//   3. workspace settings (lus en live par le worker) ;
//   4. rien = no-op strict.
// Le premier niveau NON VIDE gagne (pas de merge inter-niveaux : une exclusion
// infra REMPLACE l'exclusion workspace, comme un override broadcast remplace
// l'infra). Cohérent avec veridianResolveProviderClassRates /
// veridianResolveDailyCaps.

// VeridianExcludedProviderClassesMetadataKey est la clé de broadcast.Metadata
// portant la liste des classes destinataires à NE PAS contacter pour ce
// broadcast.
const VeridianExcludedProviderClassesMetadataKey = "veridian_excluded_provider_classes"

// VeridianExcludedProviderClassesFromMetadata extrait la liste des classes
// exclues d'un broadcast.Metadata. Seules les classes canoniques sont retenues ;
// tout le reste est ignoré silencieusement (une config malformée ne doit jamais
// bloquer un envoi — elle dégrade vers « aucune exclusion », comme les autres
// extracteurs cold). Retourne nil si aucune classe valide n'est trouvée.
func VeridianExcludedProviderClassesFromMetadata(metadata MapOfAny) []string {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata[VeridianExcludedProviderClassesMetadataKey]
	if !ok {
		return nil
	}

	var classes []string
	switch v := raw.(type) {
	case []string:
		classes = v
	case []any:
		classes = make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				classes = append(classes, s)
			}
		}
	}

	return veridianNormalizeExcludedClasses(classes)
}

// veridianNormalizeExcludedClasses filtre une liste brute de classes : lowercase
// + trim, ne garde que les classes canoniques (IsValidProviderClass), déduplique.
// Retourne nil si rien de valide (pour préserver la sémantique « nil = pas
// d'exclusion » via omitempty).
func veridianNormalizeExcludedClasses(classes []string) []string {
	if len(classes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(classes))
	out := make([]string, 0, len(classes))
	for _, c := range classes {
		class := strings.ToLower(strings.TrimSpace(c))
		if !IsValidProviderClass(class) {
			continue
		}
		if _, dup := seen[class]; dup {
			continue
		}
		seen[class] = struct{}{}
		out = append(out, class)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// VeridianResolveExcludedClasses fusionne la config d'exclusion des trois niveaux
// de la cascade (du plus spécifique au plus général) : payload broadcast → infra
// (EmailProvider) → workspace. Le premier niveau NON VIDE gagne (pas de merge
// inter-niveaux). provider peut être nil (cas legacy / intégration sans config
// Veridian) → niveau sauté. Retourne un set {classe: true} pour un test O(1) par
// le gate, ou nil si aucune exclusion n'est configurée (no-op strict).
func VeridianResolveExcludedClasses(workspace *Workspace, provider *EmailProvider, entry *EmailQueueEntry) map[string]bool {
	var excluded []string
	switch {
	case entry != nil && len(entry.Payload.VeridianExcludedProviderClasses) > 0:
		excluded = entry.Payload.VeridianExcludedProviderClasses
	case provider != nil && len(provider.VeridianExcludedProviderClasses) > 0:
		excluded = provider.VeridianExcludedProviderClasses
	case workspace != nil && len(workspace.Settings.VeridianExcludedProviderClasses) > 0:
		excluded = workspace.Settings.VeridianExcludedProviderClasses
	}

	if len(excluded) == 0 {
		return nil
	}

	set := make(map[string]bool, len(excluded))
	for _, c := range excluded {
		// Les valeurs sont déjà normalisées à l'écriture (extracteur metadata +
		// UI), mais on re-normalise par défense en profondeur : un EmailProvider
		// JSON blob ou un settings persisté hors UI pourrait contenir une casse
		// inattendue. IsValidProviderClass filtre tout reste invalide.
		class := strings.ToLower(strings.TrimSpace(c))
		if IsValidProviderClass(class) {
			set[class] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}
