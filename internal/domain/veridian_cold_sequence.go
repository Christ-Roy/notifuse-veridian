package domain

import (
	"fmt"

	"github.com/google/uuid"
)

// Veridian cold outreach — séquences de relance multi-step.
//
// Ce fichier package une vraie cadence cold (J+0 → relance 1 → relance 2) au-dessus
// de l'automation engine upstream (NodeTypeEmail/Delay déjà existants). Il n'invente
// AUCUN nouveau type de node : il assemble les nodes upstream et s'appuie sur le gate
// d'exit global (veridian_cold_exit.go) pour stopper la cadence dès qu'un prospect
// répond ou bounce. La logique d'exit vit dans l'executor, PAS dans des nodes spéciaux,
// pour qu'elle s'applique pendant TOUS les delays (un prospect qui répond pendant le
// J+3 d'attente sort au prochain tick, sans attendre le prochain email node).

// Raisons d'exit Veridian cold, en complément des raisons upstream
// (completed, filter_rejected, automation_node_deleted, manual, unsubscribed...).
const (
	// ExitReasonReplied : le prospect a répondu → on arrête de le relancer.
	// Posé par le gate d'exit quand le ColdReplyChecker (branché par le Lot 3
	// stop-on-reply) confirme une réponse. Une cadence cold pro ne relance JAMAIS
	// un prospect qui a déjà répondu.
	ExitReasonReplied = "replied"

	// ExitReasonBounced : l'adresse du prospect a bounced → on arrête de la marteler
	// (réputation d'envoi). Dérivé du statut contact_lists existant (bounced/complained),
	// qui est déjà posé par le pipeline de bounce upstream + le Lot 2 bounce-loop.
	ExitReasonBounced = "bounced"
)

// IsColdExitReason indique si la raison d'exit fournie est une raison de sortie cold
// "définitive" (le prospect ne doit plus jamais être relancé dans cette cadence).
func IsColdExitReason(reason string) bool {
	switch reason {
	case ExitReasonReplied, ExitReasonBounced:
		return true
	default:
		return false
	}
}

// ColdSequenceStep décrit une étape email de la cadence : un template à envoyer puis,
// optionnellement, un délai avant l'étape suivante. La dernière étape n'a pas de délai
// (DelayDays == 0) — c'est la fin de la cadence.
type ColdSequenceStep struct {
	// TemplateID du mail à envoyer à cette étape (requis).
	TemplateID string
	// DelayDays : nombre de jours d'attente APRÈS cet email avant l'étape suivante.
	// 0 (ou dernière étape) = pas de délai, la cadence se termine sur cet email.
	DelayDays int
	// IntegrationID optionnel : override de l'infra d'envoi pour cette étape
	// (warm-up multi-IP). nil = provider par défaut du workspace.
	IntegrationID *string
	// SubjectOverride optionnel : sujet custom pour cette relance (sinon sujet du template).
	SubjectOverride *string
}

// ColdSequenceOptions configure la construction d'une cadence cold.
type ColdSequenceOptions struct {
	// AutomationID auquel rattacher les nodes générés (requis).
	AutomationID string
	// Steps : les étapes email de la cadence, dans l'ordre d'envoi (au moins 1).
	// Cadence cold pro typique : 3 étapes (initial + 2 relances).
	Steps []ColdSequenceStep
}

// Délais par défaut d'une cadence cold standard J+0 → J+3 → J+7 (en jours).
const (
	DefaultColdRelance1DelayDays = 3 // attente entre l'email initial et la relance 1
	DefaultColdRelance2DelayDays = 4 // attente entre la relance 1 et la relance 2 (J+3 + J+4 = J+7)
)

// DefaultColdSequenceSteps construit les étapes d'une cadence cold standard 3-mails
// J+0 → J+3 → J+7 à partir des trois template IDs fournis. Les relances héritent du
// provider par défaut du workspace. C'est le raccourci "prêt à l'emploi" pour un tunnel
// cold ; pour une cadence sur-mesure (délais/infra custom), passer ColdSequenceStep à la main.
func DefaultColdSequenceSteps(initialTemplateID, relance1TemplateID, relance2TemplateID string) []ColdSequenceStep {
	return []ColdSequenceStep{
		{TemplateID: initialTemplateID, DelayDays: DefaultColdRelance1DelayDays},
		{TemplateID: relance1TemplateID, DelayDays: DefaultColdRelance2DelayDays},
		{TemplateID: relance2TemplateID, DelayDays: 0}, // dernière relance, fin de cadence
	}
}

// Validate vérifie qu'une cadence est constructible.
func (o ColdSequenceOptions) Validate() error {
	if o.AutomationID == "" {
		return fmt.Errorf("automation_id is required")
	}
	if len(o.Steps) == 0 {
		return fmt.Errorf("at least one step is required")
	}
	for i, step := range o.Steps {
		if step.TemplateID == "" {
			return fmt.Errorf("step %d: template_id is required", i)
		}
		if step.DelayDays < 0 {
			return fmt.Errorf("step %d: delay_days cannot be negative", i)
		}
		// Le délai n'a de sens qu'entre deux étapes : un délai sur la dernière étape
		// laisserait le contact pendre indéfiniment sans email derrière.
		if i == len(o.Steps)-1 && step.DelayDays > 0 {
			return fmt.Errorf("step %d (last): delay_days must be 0 on the last step", i)
		}
	}
	return nil
}

// BuildColdSequence assemble une cadence cold en nodes d'automation upstream.
//
// Elle produit une chaîne linéaire : email_1 → delay_1 → email_2 → delay_2 → email_3,
// où chaque email est un NodeTypeEmail upstream et chaque delay un NodeTypeDelay (en jours).
// Le node racine (root) est le premier email. Le dernier email a NextNodeID == nil
// (terminal = automation completed côté executor).
//
// L'exit anticipé sur réponse/bounce n'est PAS encodé dans les nodes : il est garanti
// par le gate veridianColdExitGate appliqué à CHAQUE tick par l'executor (voir
// veridian_cold_exit.go), donc il intercepte le contact même au milieu d'un delay.
// C'est volontaire : pas de node "check reply" à recâbler entre chaque étape, un seul
// point d'arrêt souverain qui couvre toute la durée de la cadence.
//
// Retourne les nodes et l'ID du node racine. Les IDs sont des UUID neufs.
func BuildColdSequence(opts ColdSequenceOptions) (nodes []*AutomationNode, rootNodeID string, err error) {
	if err := opts.Validate(); err != nil {
		return nil, "", err
	}

	// Pré-génère un ID par email pour pouvoir chaîner les delays vers l'email suivant.
	emailIDs := make([]string, len(opts.Steps))
	for i := range opts.Steps {
		emailIDs[i] = uuid.NewString()
	}

	built := make([]*AutomationNode, 0, len(opts.Steps)*2)

	for i, step := range opts.Steps {
		emailID := emailIDs[i]
		isLast := i == len(opts.Steps)-1

		// Config du node email upstream.
		emailConfig := map[string]interface{}{
			"template_id": step.TemplateID,
		}
		if step.IntegrationID != nil && *step.IntegrationID != "" {
			emailConfig["integration_id"] = *step.IntegrationID
		}
		if step.SubjectOverride != nil && *step.SubjectOverride != "" {
			emailConfig["subject_override"] = *step.SubjectOverride
		}

		emailNode := &AutomationNode{
			ID:           emailID,
			AutomationID: opts.AutomationID,
			Type:         NodeTypeEmail,
			Config:       emailConfig,
			Position:     NodePosition{X: float64(i) * 240, Y: 0},
		}

		if isLast {
			// Dernier email : pas de suite → l'automation se termine (completed).
			emailNode.NextNodeID = nil
			built = append(built, emailNode)
			continue
		}

		// Email intermédiaire : il pointe vers son delay, qui pointe vers l'email suivant.
		delayID := uuid.NewString()
		nextEmailID := emailIDs[i+1]

		emailNode.NextNodeID = &delayID

		delayNode := &AutomationNode{
			ID:           delayID,
			AutomationID: opts.AutomationID,
			Type:         NodeTypeDelay,
			Config: map[string]interface{}{
				"duration": step.DelayDays,
				"unit":     "days",
			},
			NextNodeID: &nextEmailID,
			Position:   NodePosition{X: float64(i)*240 + 120, Y: 0},
		}

		built = append(built, emailNode, delayNode)
	}

	return built, emailIDs[0], nil
}
