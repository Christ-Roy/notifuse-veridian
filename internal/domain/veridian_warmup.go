package domain

import "time"

// Veridian fork — WARMUP PROGRESSIF (rampe automatique du cap journalier, cold
// outbound, 2026-06-17). Itération du preset warmup statique (V1) : au lieu d'un
// cap fixe 1/jour/classe, on monte par paliers (1→2→5→10→25→50…/jour/classe) au
// fil des jours écoulés depuis le DÉBUT du warmup de l'infra. C'est ce que font
// Lemlist/Instantly : démarrer une IP/domaine frais sans la griller, sans réglage
// manuel quotidien.
//
// Principe (cohérent règle d'or « pas de cron bricolé ») : AUCUNE table de
// progression, AUCUN cron d'incrément, AUCUNE migration. Le palier se DÉRIVE à la
// lecture de `now - startedAt` à chaque tick worker — exactement comme le daily-cap
// dérive « le jour » de now() plutôt qu'un compteur. Survit aux redémarrages par
// construction (la date fait tout le travail).
//
// Stockage : 3 champs omitempty sur EmailProvider (JSON blob `integrations`, pas de
// migration ni d'allowlist — pattern R2/jitter/tracking). La rampe est PAR INFRA
// (granularité recommandée par le ticket : une IP/domaine se warm individuellement).
//
// Le cap warmup, quand il est actif sur une infra, est un plafond HOLISTIQUE : il
// plafonne le VOLUME TOTAL émis par l'IP/domaine d'envoi sur la journée, TOUTES
// classes de provider destinataire confondues (« J1 = N mails max, point », standard
// Lemlist/Instantly), PAS un cap par classe. Il PRIME sur (et court-circuite) le
// cap-classe statique de cette infra pendant la rampe. L'enforcement compte le total
// par DOMAINE émetteur (veridian_sender_email V53), SANS dérivation de classe
// destinataire — ce qui le rend robuste aux classes MX (ovh/ionos/corporate_selfhost)
// que le COUNT-par-classe (VeridianDomainsForClass) n'enforce PAS. Cf.
// veridian_daily_cap.go (gate veridianDailyCapGate, branche warmup).

// VeridianWarmupActive indique si une infra a une rampe de warmup configurée et
// exploitable : une date de début ET une courbe non vide. Sans les deux, pas de
// warmup (no-op strict, non-régression).
func VeridianWarmupActive(startedAt *time.Time, schedule []int) bool {
	return startedAt != nil && !startedAt.IsZero() && len(schedule) > 0
}

// VeridianWarmupCapForDay calcule le cap journalier effectif d'une infra en warmup,
// en fonction du nombre de jours écoulés depuis le début du warmup. Fonction PURE
// (zéro I/O, déterministe, testable).
//
//   - startedAt : date de début du warmup de l'infra (nil/zéro → pas de warmup).
//   - schedule  : courbe de paliers (cap/jour au palier N), ex. [1,2,5,10,25,50,100].
//     Vide → pas de warmup.
//   - stepDays  : durée d'un palier en jours (<=0 → défaut 1 = un palier par jour ;
//     2 = on reste 2 jours à chaque valeur de la courbe).
//   - now       : instant de référence (le worker passe time.Now()).
//
// Retourne 0 si le warmup n'est pas applicable (pas de date / courbe vide) →
// l'appelant DOIT alors retomber sur le cap statique (0 = « pas de cap warmup »,
// PAS « cap = 0 »). Sinon retourne schedule[ min(elapsedDays/stepDays, len-1) ] :
// le palier courant, clampé au dernier palier (plein régime) une fois la courbe
// épuisée. Le tout-premier jour (elapsed négatif si startedAt dans le futur, ou
// elapsed 0) → palier 0 (premier cap, le plus conservateur).
func VeridianWarmupCapForDay(startedAt *time.Time, schedule []int, stepDays int, now time.Time) int {
	if !VeridianWarmupActive(startedAt, schedule) {
		return 0
	}
	if stepDays <= 0 {
		stepDays = 1
	}

	// Jours calendaires écoulés depuis le début du warmup, en UTC (cohérent avec le
	// raisonnement « jour » du daily-cap, qui compte sent_at >= minuit UTC).
	start := startedAt.UTC()
	elapsedDays := int(now.UTC().Sub(start).Hours() / 24)
	if elapsedDays < 0 {
		// Warmup programmé dans le futur : on reste au palier le plus conservateur.
		elapsedDays = 0
	}

	idx := elapsedDays / stepDays
	if idx >= len(schedule) {
		idx = len(schedule) - 1
	}

	cap := schedule[idx]
	if cap < 0 {
		// Une valeur de courbe négative n'a pas de sens (0 voudrait dire « bloqué »,
		// négatif est une erreur de saisie) : on la traite comme « pas de cap warmup »
		// pour ne jamais bloquer un envoi sur une config malformée (best-effort).
		return 0
	}
	return cap
}

// VeridianWarmupStep retourne le palier courant (1-indexé pour l'affichage) et le
// nombre total de paliers de la courbe, pour l'UI (« Warmup jour N/total »). Pur.
// Retourne (0, 0) si le warmup n'est pas actif.
func VeridianWarmupStep(startedAt *time.Time, schedule []int, stepDays int, now time.Time) (current, total int) {
	if !VeridianWarmupActive(startedAt, schedule) {
		return 0, 0
	}
	if stepDays <= 0 {
		stepDays = 1
	}
	start := startedAt.UTC()
	elapsedDays := int(now.UTC().Sub(start).Hours() / 24)
	if elapsedDays < 0 {
		elapsedDays = 0
	}
	idx := elapsedDays / stepDays
	if idx >= len(schedule) {
		idx = len(schedule) - 1
	}
	return idx + 1, len(schedule)
}
