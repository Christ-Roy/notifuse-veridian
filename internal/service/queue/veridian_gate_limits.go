package queue

import (
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork — lot 2 « vérité d'un profil » (08/10/2026).
//
// Ce fichier est LA résolution unique des plafonds que lisent les portes du
// worker (plafond journalier, réservation atomique du quota, débit par classe)
// ET que lit EffectivePlan (veridian_effective_plan.go, utilisé par l'API
// emailProfiles.overview). Avant ce lot, le calcul existait trois fois : dans
// chaque porte du worker, et en Python dans le CLI. Désormais une porte ne
// recalcule plus rien : elle appelle veridianResolveCapLimits, compare à ses
// compteurs, décide. L'écran ne peut donc plus diverger du worker.

// veridianCapLimits sont les plafonds statiques résolus pour (workspace,
// profil, entrée, classe). Zéro = pas de plafond de ce type.
type veridianCapLimits struct {
	PerRecipient int // plafond journalier par adresse destinataire
	Warmup       int // plafond TOTAL du domaine émetteur pendant la chauffe
	Class        string
	ClassBase    int // plafond de la classe tel que configuré
	ClassCap     int // plafond de la classe après ralentissement du fusible
	Profile      int // plafond journalier du profil (réservation atomique)
	PerSender    int // plafond journalier par adresse émettrice
	Factor       int // facteur de ralentissement du couple (1, 2 ou 4)
}

// veridianResolveCapLimits résout tous les plafonds journaliers. classOf n'est
// appelée que si une table de plafonds par classe existe (pas de classification
// MX inutile, comportement historique des portes) ; factorOf donne le facteur
// de ralentissement du couple (domaine émetteur, classe), 1 si inconnu.
// entry peut être vide (EffectivePlan : aucun override de payload).
func veridianResolveCapLimits(
	workspace *domain.Workspace,
	provider *domain.EmailProvider,
	entry *domain.EmailQueueEntry,
	classOf func() string,
	factorOf func(class string) int,
	now time.Time,
) veridianCapLimits {
	lim := veridianCapLimits{Factor: 1}
	classCaps, perRecipient := veridianResolveDailyCaps(workspace, provider, entry)
	lim.PerRecipient = perRecipient
	lim.Warmup = veridianWarmupCap(workspace, provider, now)
	lim.Profile = provider.VeridianEffectiveProfileDailyCap()
	lim.PerSender = veridianResolvePerSenderCap(workspace, provider, entry)
	if len(classCaps) > 0 && classOf != nil {
		lim.Class = classOf()
		if factorOf != nil {
			if f := factorOf(lim.Class); f > 1 {
				lim.Factor = f
			}
		}
		if configured, ok := domain.VeridianCapForClass(classCaps, lim.Class); ok && configured > 0 {
			lim.ClassBase = configured
			lim.ClassCap = veridianSlowCap(configured, lim.Factor)
		}
	}
	return lim
}

// veridianEffectiveClassRate donne le débit par minute d'une classe après le
// ralentissement du fusible. 0 = classe non bridée (aucun débit configuré).
func veridianEffectiveClassRate(rates map[string]float64, class string, factor int) float64 {
	base, ok := domain.VeridianRateForClass(rates, class)
	if !ok || base <= 0 {
		return 0
	}
	if factor < 1 {
		factor = 1
	}
	return base / float64(factor)
}

// veridianRateConfigured : débit brut configuré pour la classe (héritage des
// classes filles inclus), 0 si aucune entrée. Sert à l'affichage du plan.
func veridianRateConfigured(rates map[string]float64, class string) float64 {
	v, _ := domain.VeridianRateForClass(rates, class)
	return v
}
