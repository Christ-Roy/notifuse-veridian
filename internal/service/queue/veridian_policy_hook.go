package queue

import (
	"math"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork, lot L3 « politique d'envoi et stock » (29/10/2026).
//
// Crochets entre le worker et la politique de classe en données (domain.
// VeridianPolicyFor : surcharge du workspace, sinon preset du catalogue). Ce fichier est
// la SEULE jonction : les portes et EffectivePlan appellent ces quatre fonctions, aucune
// ne relit un preset ni une surcharge ailleurs. Ordre de priorité inchangé pour tout
// ce qui existait : payload du broadcast > profil > réglage workspace historique >
// (nouveau) surcharge de politique du workspace > (nouveau) preset du catalogue.
// Une valeur posée à la main sur un niveau plus spécifique n'est jamais écrasée.
//
// Comme les autres défauts de classe, ces crochets se coupent avec
// veridianClassDefaultsEnabled (TestMain historique uniquement).

// VeridianReputationMinSent exporte le minimum d'envois du fusible : le texte des explications
// du module internal/veridian/policy le cite, un test de ce module vérifie l'égalité.
const VeridianReputationMinSent = veridianReputationMinSent

// veridianPolicyMergeCaps complète les plafonds de classe résolus avec la politique du
// workspace : toute classe sans valeur propre (ni héritée de sa classe parente) reçoit
// la surcharge du workspace, sinon son preset. Ne modifie jamais la table reçue.
func veridianPolicyMergeCaps(ws *domain.Workspace, caps map[string]int) map[string]int {
	if !veridianClassDefaultsEnabled || ws == nil {
		return caps
	}
	snap := domain.VeridianPolicyFor(ws.ID)
	if len(caps) == 0 {
		return snap.Caps
	}
	out := make(map[string]int, len(snap.Caps)+len(caps))
	for k, v := range caps {
		out[k] = v
	}
	for class, v := range snap.Caps {
		if _, set := domain.VeridianConfiguredCapForClass(caps, class); !set {
			out[class] = v
		}
	}
	return out
}

// veridianPolicyMergeRates : même chose pour les débits par minute.
func veridianPolicyMergeRates(ws *domain.Workspace, rates map[string]float64) map[string]float64 {
	if !veridianClassDefaultsEnabled || ws == nil {
		return rates
	}
	snap := domain.VeridianPolicyFor(ws.ID)
	if len(rates) == 0 {
		return snap.Rates
	}
	out := make(map[string]float64, len(snap.Rates)+len(rates))
	for k, v := range rates {
		out[k] = v
	}
	for class, v := range snap.Rates {
		if _, set := domain.VeridianConfiguredRateForClass(rates, class); !set {
			out[class] = v
		}
	}
	return out
}

// veridianPolicyWarmupClamp borne le plafond d'une classe pendant la chauffe du domaine
// d'envoi : au plus warmup_share du plafond de chauffe du jour (au moins 1). Ne s'applique
// pas quand le plafond de la classe est posé à la main (profil, workspace, surcharge de
// politique) : l'opérateur a décidé. warmup = plafond de chauffe du jour, 0 = pas de
// chauffe. rawCaps = la table AVANT complétion par la politique.
func veridianPolicyWarmupClamp(ws *domain.Workspace, rawCaps map[string]int, class string, warmup, classCap int) int {
	if !veridianClassDefaultsEnabled || ws == nil || warmup <= 0 || classCap <= 0 || class == "" {
		return classCap
	}
	if _, explicit := domain.VeridianConfiguredCapForClass(rawCaps, class); explicit {
		return classCap
	}
	snap := domain.VeridianPolicyFor(ws.ID)
	if snap.Overridden[class].DailyCap {
		return classCap
	}
	share := snap.Classes[class].WarmupShare
	if share <= 0 {
		return classCap
	}
	limit := int(math.Ceil(share * float64(warmup))) // > 0 : share et warmup sont positifs
	if limit < classCap {
		return limit
	}
	return classCap
}

// veridianPolicyThreshold donne le seuil de ralentissement du fusible pour une classe :
// celui que le PROFIL a posé s'il en a un, sinon la politique de la classe (surcharge
// du workspace ou preset), sinon le défaut du fusible. workspaceID peut être vide.
func veridianPolicyThreshold(workspaceID string, provider *domain.EmailProvider, class string) float64 {
	base := provider.VeridianEffectiveHardBounceFreezeThreshold()
	if !veridianClassDefaultsEnabled || workspaceID == "" || class == "" || provider.VeridianHasCustomHardBounceFreezeThreshold() {
		return base
	}
	if p, ok := domain.VeridianPolicyFor(workspaceID).Classes[class]; ok && p.SlowdownThreshold > 0 {
		return p.SlowdownThreshold
	}
	return base
}

// veridianPolicyTableSource dit d'où vient la table de plafonds ou de débits quand ni le
// profil ni le réglage workspace historique n'en pose : "policy" si le workspace a surchargé
// au moins une classe, "default" sinon (valeur historique, les écrans la connaissent).
func veridianPolicyTableSource(ws *domain.Workspace, rates bool) string {
	if !veridianClassDefaultsEnabled || ws == nil {
		return "default"
	}
	for _, o := range domain.VeridianPolicyFor(ws.ID).Overridden {
		if (rates && o.RatePerMin) || (!rates && o.DailyCap) {
			return "policy"
		}
	}
	return "default"
}
