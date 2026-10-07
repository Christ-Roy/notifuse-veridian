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
	// ThresholdCustom : true si le seuil vient du profil (veridian_hard_bounce_freeze_threshold), false = defaut 0.03.
	ThresholdCustom bool `json:"hard_bounce_rate_threshold_custom"`
	// MinSent : envois minimum dans un couple avant toute reaction a un taux.
	MinSent      int `json:"min_sent_for_reaction"`
	Complaints7d int `json:"complaints_7d"`
	// Alert : plainte dans la fenetre. Le domaine est ralenti (DomainFactor = 4), jamais arrete.
	Alert        bool `json:"alert"`
	DomainFactor int  `json:"domain_slowdown_factor"`
	// Classes : etat de chaque couple (domaine emetteur, classe destinataire) avec
	// taux, facteur de ralentissement applique (1, 2 ou 4) et raison.
	// SlowedClasses : classes a debit divise ; StoppedClasses : fournisseurs qui
	// refusent en bloc (>50 % de 5.7.x sur les 20 derniers envois) : seuls arretes.
	Classes        []VeridianReputationClassStatus `json:"classes"`
	SlowedClasses  []string                        `json:"slowed_classes"`
	StoppedClasses []string                        `json:"stopped_classes"`
}

// VeridianReputationClassStatus est l'etat d'un couple (domaine emetteur, classe
// du fournisseur destinataire) sur 7 jours glissants.
type VeridianReputationClassStatus struct {
	Class             string  `json:"class"`
	Sent7d            int     `json:"sent_7d"`
	HardBounces7d     int     `json:"hard_bounces_7d"`
	PolicyRefusals7d  int     `json:"policy_refusals_7d"`
	HardBounceRate    float64 `json:"hard_bounce_rate"`
	PolicyRefusalRate float64 `json:"policy_refusal_rate"`
	// Factor : debit de la classe divise par ce facteur (1 = normal, 2, 4).
	Factor int `json:"slowdown_factor"`
	// Reason : hard_bounce_rate | policy_refusal_rate | complaint | bulk_policy_refusal.
	Reason  string `json:"reason,omitempty"`
	Stopped bool   `json:"stopped"`
	// RecentSent / RecentPolicyRefusals : 20 derniers envois (24 h), renseignes
	// seulement quand le controle de refus en bloc a ete evalue.
	RecentSent           int `json:"recent_sent,omitempty"`
	RecentPolicyRefusals int `json:"recent_policy_refusals,omitempty"`
}

// VeridianReputationStatusRequest est la requête du endpoint.
type VeridianReputationStatusRequest struct {
	WorkspaceID string
}

// VeridianReputationStatusResponse liste l'état de toutes les intégrations SMTP
// du workspace ayant un sender exploitable.
type VeridianReputationStatusResponse struct {
	Integrations []VeridianReputationIntegrationStatus `json:"integrations"`
	AnyStopped   bool                                  `json:"any_stopped"`
	AnySlowed    bool                                  `json:"any_slowed"`
}

// VeridianReputationStatusService est le contrat consommé par le handler HTTP.
type VeridianReputationStatusService interface {
	GetReputationStatus(ctx context.Context, req *VeridianReputationStatusRequest) (*VeridianReputationStatusResponse, error)
}
