package domain

import (
	"context"

	deliverability "github.com/Notifuse/notifuse/pkg/veridian_deliverability"
)

//go:generate mockgen -destination mocks/mock_veridian_deliverability_score_service.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianDeliverabilityScoreService

// Veridian — endpoint de score de délivrabilité (spam score) sur un template
// cold RENDU. Couche domain : DTO de requête/réponse + interface service.
//
// Le moteur de scoring est le package pur pkg/veridian_deliverability (zéro I/O,
// zéro dépendance). Cette couche ajoute UNIQUEMENT l'auth workspace + permission
// templates:read (le scoring ne touche aucune donnée du workspace, mais on garde
// le même gardien que le breakdown R1 pour ne pas exposer un endpoint console
// sans appartenance vérifiée). Cf. ticket
// todo/2026-06-15-linter-deliverabilite-spam-score-templates.md.

// VeridianDeliverabilityScoreRequest porte les paramètres du scoring. Subject +
// Body sont le RENDU FINAL (Liquid + spintax déjà résolus côté appelant ; le
// linter détecte ce qui n'a PAS été résolu). ProviderClass (optionnel) déduit le
// mode strict/lenient ; Mode (optionnel : "strict"|"lenient"|"default") le force.
type VeridianDeliverabilityScoreRequest struct {
	WorkspaceID   string `json:"workspace_id" valid:"required,alphanum,stringlength(1|20)"`
	Subject       string `json:"subject"`
	Body          string `json:"body"`
	IsHTML        bool   `json:"is_html"`
	FromDomain    string `json:"from_domain,omitempty"`
	ProviderClass string `json:"provider_class,omitempty"`
	Mode          string `json:"mode,omitempty"`
}

// VeridianParseMode mappe la string de mode publique vers le type du package.
// Vide / inconnue = ModeDefault (sera éventuellement raffiné par ProviderClass).
func VeridianParseMode(s string) deliverability.Mode {
	switch s {
	case "strict":
		return deliverability.ModeStrict
	case "lenient":
		return deliverability.ModeLenient
	default:
		return deliverability.ModeDefault
	}
}

// ToLinterInput convertit la requête en Input du package linter.
func (r *VeridianDeliverabilityScoreRequest) ToLinterInput() deliverability.Input {
	return deliverability.Input{
		Subject:       r.Subject,
		Body:          r.Body,
		IsHTML:        r.IsHTML,
		FromDomain:    r.FromDomain,
		ProviderClass: r.ProviderClass,
		Mode:          VeridianParseMode(r.Mode),
	}
}

// VeridianDeliverabilityScoreService expose le scoring à la couche HTTP, derrière
// l'auth workspace. Implémentation :
// internal/service/veridian_deliverability_score_service.go.
type VeridianDeliverabilityScoreService interface {
	Score(ctx context.Context, req *VeridianDeliverabilityScoreRequest) (*deliverability.Result, error)
}
