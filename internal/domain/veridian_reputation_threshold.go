package domain

import (
	"fmt"
	"math"
)

// === Veridian patch — seuil du fusible de reputation PAR PROFIL (2026-10-07) ===
//
// Le fusible (internal/service/queue/veridian_reputation_gate.go) gele une infra
// quand son taux de bounce dur sur 7 jours atteint un seuil. Defaut 3%. Un profil
// peut porter son propre seuil (EmailProvider.VeridianHardBounceFreezeThreshold),
// borne pour qu'un reglage maladroit ne desactive jamais le fusible : 15% max.
// Le fusible plainte (une plainte = gel) n'est pas concerne.

const (
	// VeridianDefaultHardBounceFreezeThreshold est le seuil quand le profil n'en porte pas.
	VeridianDefaultHardBounceFreezeThreshold = 0.03
	// VeridianMinHardBounceFreezeThreshold / Max bornent le seuil configurable.
	VeridianMinHardBounceFreezeThreshold = 0.01
	VeridianMaxHardBounceFreezeThreshold = 0.15
)

// ValidateVeridianHardBounceFreezeThreshold : 0 (vide) accepte, sinon [0.01 ; 0.15].
func (e *EmailProvider) ValidateVeridianHardBounceFreezeThreshold() error {
	v := e.VeridianHardBounceFreezeThreshold
	if v == 0 {
		return nil
	}
	if math.IsNaN(v) || math.IsInf(v, 0) || v < VeridianMinHardBounceFreezeThreshold || v > VeridianMaxHardBounceFreezeThreshold {
		return fmt.Errorf("veridian_hard_bounce_freeze_threshold must be between %.2f and %.2f (got %v)",
			VeridianMinHardBounceFreezeThreshold, VeridianMaxHardBounceFreezeThreshold, v)
	}
	return nil
}

// VeridianEffectiveHardBounceFreezeThreshold retourne le seuil applique (nil-safe) :
// celui du profil s'il est valide et renseigne, sinon le defaut 0.03.
func (e *EmailProvider) VeridianEffectiveHardBounceFreezeThreshold() float64 {
	if e == nil || e.ValidateVeridianHardBounceFreezeThreshold() != nil || e.VeridianHardBounceFreezeThreshold == 0 {
		return VeridianDefaultHardBounceFreezeThreshold
	}
	return e.VeridianHardBounceFreezeThreshold
}

// VeridianHasCustomHardBounceFreezeThreshold indique si le seuil vient du profil.
func (e *EmailProvider) VeridianHasCustomHardBounceFreezeThreshold() bool {
	return e != nil && e.VeridianHardBounceFreezeThreshold != 0 && e.ValidateVeridianHardBounceFreezeThreshold() == nil
}
