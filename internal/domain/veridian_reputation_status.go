package domain

import "context"

// === Veridian patch — fusible de réputation (correctif 2026-09-29) ===
//
// Lecture, PAR INTÉGRATION SMTP, de l'état LIVE du fusible de réputation qui
// gèle automatiquement les envois d'une infra (cf. internal/service/queue/
// veridian_reputation_gate.go) : taux de bounce dur sur 7 jours glissants et
// présence d'une plainte. C'est le signal "visible dans l'interface ou l'API"
// exigé par la mission — un opérateur peut interroger cet endpoint pour savoir
// SANS ambiguïté si une infra est gelée, pourquoi, et avec quels chiffres,
// sans avoir à lire les logs du worker.

// VeridianReputationIntegrationStatus est l'état de réputation d'UNE
// intégration SMTP (une infra = un domaine d'envoi).
type VeridianReputationIntegrationStatus struct {
	IntegrationID   string  `json:"integration_id"`
	IntegrationName string  `json:"integration_name"`
	SenderDomain    string  `json:"sender_domain"`
	WindowDays      int     `json:"window_days"`
	Sent7d          int     `json:"sent_7d"`
	HardBounces7d   int     `json:"hard_bounces_7d"`
	HardBounceRate  float64 `json:"hard_bounce_rate"`
	Threshold       float64 `json:"hard_bounce_rate_threshold"`
	Complaints7d    int     `json:"complaints_7d"`
	Frozen          bool    `json:"frozen"`
	FrozenReason    string  `json:"frozen_reason,omitempty"`
}

// VeridianReputationStatusRequest est la requête du endpoint.
type VeridianReputationStatusRequest struct {
	WorkspaceID string
}

// VeridianReputationStatusResponse liste l'état de toutes les intégrations SMTP
// du workspace ayant un sender exploitable.
type VeridianReputationStatusResponse struct {
	Integrations []VeridianReputationIntegrationStatus `json:"integrations"`
	AnyFrozen    bool                                  `json:"any_frozen"`
}

// VeridianReputationStatusService est le contrat consommé par le handler HTTP.
type VeridianReputationStatusService interface {
	GetReputationStatus(ctx context.Context, req *VeridianReputationStatusRequest) (*VeridianReputationStatusResponse, error)
}
