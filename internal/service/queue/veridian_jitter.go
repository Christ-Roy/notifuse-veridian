package queue

import (
	"math/rand"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian — JITTER TEMPOREL du throttle par classe de provider destinataire
// (cold outbound). Le throttle minute (veridian_provider_throttle.go) est un
// token-bucket à burst 1 → quand une classe est saturée, le gate renvoie un
// délai de re-planification STRICTEMENT constant (60/rate secondes), produisant
// un rythme métronomique parfait. Cette régularité est un "tell de machine"
// documenté (GMass/Smartlead/Instantly randomisent tous l'espacement) : les
// filtres et les outils de warmup détectent la cadence parfaite.
//
// On disperse donc le DÉLAI de re-planification autour de sa valeur nominale
// (±jitter_pct), SANS toucher au rate.Limiter : le débit MOYEN reste piloté par
// le token-bucket (c'est son Allow() qui autorise l'envoi au tick suivant), le
// jitter ne fait que disperser les re-checks → il ne crée NI sur-débit NI
// sous-débit garanti. Choix volontaire : on jitte uniquement le throttle PAR
// CLASSE (l'étage qui gouverne la cadence cold réelle vers chaque provider),
// pas le Wait émetteur upstream (code chaud non-veridian, hors scope ce ticket).
//
// Cascade de config identique aux rates/caps (du plus spécifique au plus
// général) : payload broadcast → infra (EmailProvider) → workspace settings.
// Subtilité du ZÉRO : 0 est une valeur LÉGITIME (jitter explicitement désactivé)
// distincte de "non configuré" (→ défaut cold). D'où le pointeur *float64 : nil
// = non configuré, *0 = désactivé voulu. Voir veridianResolveJitterPct.

// veridianDefaultJitterPct est l'amplitude de jitter appliquée en contexte cold
// quand AUCUN niveau de la cascade ne définit de jitter explicite. ±30 % :
// disperse visiblement le rythme tout en gardant le débit moyen stable
// (conservateur et sûr, cf. recherche web ticket 2026-06-15).
const veridianDefaultJitterPct = 0.30

// veridianMaxJitterPct borne supérieure de l'amplitude. Au-delà, le délai
// pourrait approcher 0 (rafale) ou doubler (sous-débit inutile). Clampé.
const veridianMaxJitterPct = 0.90

// veridianApplyJitter disperse un délai de re-planification autour de sa valeur
// nominale pour casser le rythme métronomique du throttle (tell de machine
// cold). pct ∈ [0, 0.9] : fraction d'amplitude (±) ; pct<=0 → no-op strict
// (retourne delay inchangé, non-régression). rng est injecté pour le test (en
// prod rand.Float64, en test une fonction figée) et doit retourner une valeur
// dans [0,1). Formule : delay × (1 + (rng()*2 - 1)·pct). Le résultat n'est
// jamais négatif (pct clampé ≤ 0.9 ⇒ facteur ∈ [0.1, 1.9]). Les bornes 1s/5min
// restent appliquées par l'appelant (gate), inchangées. PUR.
func veridianApplyJitter(delay time.Duration, pct float64, rng func() float64) time.Duration {
	if pct <= 0 || rng == nil {
		return delay
	}
	if pct > veridianMaxJitterPct {
		pct = veridianMaxJitterPct
	}
	// rng() ∈ [0,1) → (rng()*2 - 1) ∈ [-1,1) → facteur ∈ [1-pct, 1+pct).
	factor := 1 + (rng()*2-1)*pct
	jittered := time.Duration(float64(delay) * factor)
	if jittered < 0 {
		// Défensif : ne peut pas arriver avec pct ≤ 0.9 et delay ≥ 0, mais on
		// ne renvoie jamais un délai négatif (qui ferait re-planifier dans le
		// passé).
		return 0
	}
	return jittered
}

// veridianResolveJitterPct résout l'amplitude de jitter via la cascade
// payload → infra → workspace (premier niveau DÉFINI gagne). Un niveau est
// "défini" quand son pointeur *float64 est non-nil (y compris *0 = jitter OFF
// voulu). Si AUCUN niveau ne définit de jitter, on applique le défaut cold
// (veridianDefaultJitterPct) : ce helper n'est appelé QUE depuis le gate
// throttle, qui ne s'exécute lui-même que si des rates par classe sont
// configurés → on est en contexte cold par construction (proxy le plus léger,
// cf. ticket). provider peut être nil (legacy) → niveau sauté. Résultat clampé
// à [0, 0.9].
func veridianResolveJitterPct(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) float64 {
	var pct float64
	switch {
	case entry != nil && entry.Payload.VeridianJitterPct != nil:
		pct = *entry.Payload.VeridianJitterPct
	case provider != nil && provider.VeridianJitterPct != nil:
		pct = *provider.VeridianJitterPct
	case workspace != nil && workspace.Settings.VeridianJitterPct != nil:
		pct = *workspace.Settings.VeridianJitterPct
	default:
		// Aucun niveau défini : défaut cold (le gate n'est atteint qu'en cold).
		pct = veridianDefaultJitterPct
	}
	if pct < 0 {
		return 0
	}
	if pct > veridianMaxJitterPct {
		return veridianMaxJitterPct
	}
	return pct
}

// veridianJitterDelay applique le jitter résolu sur un délai nominal de
// re-planification, depuis le gate throttle (qui a déjà workspace/provider/entry
// en main). Centralise la résolution + l'application pour que le call-site du
// gate reste minimal et que le worker n'ait RIEN à connaître du jitter (diff
// worker.go = 0). En prod le RNG est rand.Float64 ; le déterminisme est injecté
// dans les tests via veridianApplyJitter directement.
func veridianJitterDelay(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry, delay time.Duration) time.Duration {
	pct := veridianResolveJitterPct(workspace, provider, entry)
	return veridianApplyJitter(delay, pct, rand.Float64)
}
