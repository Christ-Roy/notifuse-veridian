package domain

import (
	"context"
	"strings"
	"time"
)

// Veridian fork (fiche 62, lot 1 « pourquoi ça n'envoie pas », 10/10/2026).
//
// Vocabulaire PARTAGÉ par le worker, l'API, le CLI et la console : codes de raison,
// issues, niveaux du journal, trace d'une décision (gate par gate avec valeur, limite
// et verdict) et contrat de l'explorateur de file. Aucun I/O ici.

// Codes de raison d'attente (stables : une valeur ne change jamais de sens).
const (
	VeridianReasonNotExamined      = "not_examined"       // jamais lue par le worker
	VeridianReasonWindowClosed     = "window_closed"      // fenêtre d'envoi fermée
	VeridianReasonCapacity         = "capacity"           // plafond du jour (détail = porte)
	VeridianReasonClassRate        = "class_rate"         // débit du fournisseur destinataire
	VeridianReasonReputationStop   = "reputation_stopped" // fusible de réputation : couple arrêté
	VeridianReasonExcludedClass    = "excluded_class"     // classe exclue sur tous les candidats
	VeridianReasonProfilePaused    = "profile_paused"     // tous les candidats sont en pause
	VeridianReasonNoProfile        = "no_profile_in_pool" // aucun candidat joignable
	VeridianReasonCircuitOpen      = "circuit_open"       // transport en cooldown
	VeridianReasonAnchorWait       = "anchor_wait"        // la relance attend son expéditeur d'origine
	VeridianReasonQuotaDenied      = "quota_denied"       // réservation atomique refusée
	VeridianReasonRenderFailed     = "render_failed"      // rendu au dépilage en échec transitoire
	VeridianReasonGuardRetry       = "guard_retry"        // garde finale d'automation en échec transitoire
	VeridianReasonAutomationPaused = "automation_paused"  // automation en pause
	VeridianReasonSendError        = "send_error"         // échec d'envoi, nouvelle tentative planifiée
	VeridianReasonDeferredLegacy   = "deferred_legacy"    // report ancien, raison non enregistrée
	VeridianReasonInFlight         = "in_flight"          // en cours de traitement
	VeridianReasonOrphanParked     = "orphan_parked"      // contact parqué en envoi sans entrée en file
)

// VeridianQueueReasons liste les codes d'attente (ordre d'affichage).
var VeridianQueueReasons = []string{
	VeridianReasonNotExamined, VeridianReasonWindowClosed, VeridianReasonCapacity,
	VeridianReasonClassRate, VeridianReasonReputationStop, VeridianReasonExcludedClass,
	VeridianReasonProfilePaused, VeridianReasonNoProfile, VeridianReasonCircuitOpen,
	VeridianReasonAnchorWait, VeridianReasonQuotaDenied, VeridianReasonRenderFailed,
	VeridianReasonGuardRetry, VeridianReasonAutomationPaused, VeridianReasonSendError,
	VeridianReasonDeferredLegacy, VeridianReasonInFlight,
}

// Issues d'une décision.
const (
	VeridianOutcomeSent       = "sent"
	VeridianOutcomeDeferred   = "deferred"
	VeridianOutcomeFailed     = "failed"
	VeridianOutcomeDiscarded  = "discarded"
	VeridianOutcomeExited     = "exited"
	VeridianOutcomeRecomputed = "recomputed"
)

// VeridianValidOutcome indique si l'issue est connue.
func VeridianValidOutcome(o string) bool {
	switch o {
	case VeridianOutcomeSent, VeridianOutcomeDeferred, VeridianOutcomeFailed,
		VeridianOutcomeDiscarded, VeridianOutcomeExited, VeridianOutcomeRecomputed:
		return true
	}
	return false
}

// Niveaux du journal des décisions (réglage de workspace).
const (
	VeridianDecisionLogOff         = "off"         // compteurs seuls, aucune ligne
	VeridianDecisionLogTransitions = "transitions" // défaut
	VeridianDecisionLogAll         = "all"         // chaque examen (borné par la rétention)
)

// VeridianNormalizeDecisionLogLevel rend un niveau connu ; tout le reste retombe sur
// le défaut « transitions ».
func VeridianNormalizeDecisionLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case VeridianDecisionLogOff:
		return VeridianDecisionLogOff
	case VeridianDecisionLogAll:
		return VeridianDecisionLogAll
	}
	return VeridianDecisionLogTransitions
}

// Portes évaluées pour une décision (ordre canonique de la cascade).
const (
	VeridianGateExcluded   = "excluded"
	VeridianGateReputation = "reputation"
	VeridianGateClassRate  = "class_rate"
	VeridianGateDailyCap   = "daily_cap"
	VeridianGateSenderCap  = "sender_cap"
	VeridianGateWindow     = "window"
)

// Verdicts d'une porte.
const (
	VeridianVerdictPass    = "pass"
	VeridianVerdictBlock   = "block"
	VeridianVerdictSlowed  = "slowed" // passe, mais le fusible a ralenti le couple
	VeridianVerdictSkipped = "skipped"
)

// VeridianGateRecord est ce qu'une porte a RÉELLEMENT utilisé : valeur observée,
// limite appliquée, verdict.
type VeridianGateRecord struct {
	Gate    string      `json:"gate"`
	Verdict string      `json:"verdict"`
	Value   interface{} `json:"value,omitempty"`
	Limit   interface{} `json:"limit,omitempty"`
	Name    string      `json:"name,omitempty"`
	DelayS  int         `json:"delay_s,omitempty"`
	Detail  string      `json:"detail,omitempty"`
}

// VeridianCandidateTrace est l'examen d'un profil candidat.
type VeridianCandidateTrace struct {
	Profile     string               `json:"profile"`
	ProfileName string               `json:"profile_name,omitempty"`
	From        string               `json:"from,omitempty"`
	Outcome     string               `json:"outcome"` // selected|blocked|excluded|paused|circuit_open|skipped
	Gates       []VeridianGateRecord `json:"gates"`
}

// VeridianTraceAnchor est l'expéditeur d'origine d'une séquence (continuité 48 h).
type VeridianTraceAnchor struct {
	Profile   string `json:"profile"`
	Available bool   `json:"available"`
}

// VeridianTraceDecision est la conclusion d'une décision.
type VeridianTraceDecision struct {
	Outcome string     `json:"outcome"`
	Reason  string     `json:"reason,omitempty"`
	Detail  string     `json:"detail,omitempty"`
	Until   *time.Time `json:"until,omitempty"`
	DelayS  int        `json:"delay_s,omitempty"`
}

// VeridianSendTrace est la trace complète d'une décision (colonne jsonb).
type VeridianSendTrace struct {
	Class      string                   `json:"class,omitempty"`
	Level      string                   `json:"level"` // full | reduced
	Anchor     *VeridianTraceAnchor     `json:"anchor,omitempty"`
	Candidates []VeridianCandidateTrace `json:"candidates"`
	Decision   VeridianTraceDecision    `json:"decision"`
}

// VeridianSendDecision est une ligne du journal des décisions d'envoi.
type VeridianSendDecision struct {
	ID           string             `json:"id"`
	At           time.Time          `json:"at"`
	EntryID      string             `json:"entry_id"`
	MessageID    string             `json:"message_id,omitempty"`
	ContactEmail string             `json:"contact_email"`
	AutomationID string             `json:"automation_id,omitempty"`
	NodeID       string             `json:"node_id,omitempty"`
	Outcome      string             `json:"outcome"`
	Reason       string             `json:"reason,omitempty"`
	Detail       string             `json:"detail,omitempty"`
	Until        *time.Time         `json:"until,omitempty"`
	ProfileID    string             `json:"profile_id,omitempty"`
	ProfileName  string             `json:"profile_name,omitempty"`
	Sampled      bool               `json:"sampled"`
	Trace        *VeridianSendTrace `json:"trace,omitempty"`
}

// VeridianSendDecisionFilter filtre la lecture du journal.
type VeridianSendDecisionFilter struct {
	AutomationID string
	NodeID       string
	Email        string
	EntryID      string
	Reason       string
	Outcome      string
	Since        *time.Time
	Limit        int
	Cursor       string
	WithTrace    bool
}

// VeridianSendDecisionRepository persiste le journal des décisions (table
// veridian_send_decisions de la base du workspace).
type VeridianSendDecisionRepository interface {
	// Insert écrit une ligne (best-effort côté appelant) et applique la rétention
	// opportuniste (aucune tâche planifiée).
	Insert(ctx context.Context, workspaceID string, d *VeridianSendDecision) error
	// List rend les décisions les plus récentes d'abord ; next_cursor vide = fin.
	List(ctx context.Context, workspaceID string, f VeridianSendDecisionFilter) ([]*VeridianSendDecision, string, error)
}

// --- Explorateur de file (GET /api/veridian/queue.explain) ---

// VeridianQueueGroupBy : dimensions de regroupement acceptées.
var VeridianQueueGroupBy = []string{"automation", "node", "reason", "profile", "class"}

// VeridianQueueExplainFilter filtre et regroupe l'explorateur.
type VeridianQueueExplainFilter struct {
	GroupBy      []string
	AutomationID string
	NodeID       string
	Reason       string
	ProfileID    string
	Class        string
	Status       string
	EntryID      string
}

// VeridianQueueGroup est un groupe d'entrées qui attendent pour la même raison.
type VeridianQueueGroup struct {
	AutomationID    string     `json:"automation_id"`
	AutomationName  string     `json:"automation_name"`
	NodeID          string     `json:"node_id"`
	Reason          string     `json:"reason"`
	ReasonDetail    string     `json:"reason_detail"`
	ProfileID       string     `json:"profile_id"`
	ProfileName     string     `json:"profile_name"`
	Class           string     `json:"class"`
	Count           int64      `json:"count"`
	NeverExamined   int64      `json:"never_examined"`
	OldestCreatedAt *time.Time `json:"oldest_created_at"`
	NextAttemptMin  *time.Time `json:"next_attempt_min"`
	NextAttemptMax  *time.Time `json:"next_attempt_max"`
	SampleEntryIDs  []string   `json:"sample_entry_ids"`
}

// VeridianQueueOrphanNode : contacts parqués en envoi sans entrée en file, par nœud.
type VeridianQueueOrphanNode struct {
	AutomationID   string `json:"automation_id"`
	AutomationName string `json:"automation_name"`
	NodeID         string `json:"node_id"`
	Count          int64  `json:"count"`
}

// VeridianQueueOrphans agrège les orphelins.
type VeridianQueueOrphans struct {
	Count  int64                     `json:"count"`
	ByNode []VeridianQueueOrphanNode `json:"by_node"`
}

// VeridianQueueEntryDetail est le détail d'une entrée de file.
type VeridianQueueEntryDetail struct {
	ID              string                `json:"id"`
	Status          string                `json:"status"`
	AutomationID    string                `json:"automation_id"`
	AutomationName  string                `json:"automation_name"`
	NodeID          string                `json:"node_id"`
	ContactEmail    string                `json:"contact_email"`
	IntegrationID   string                `json:"integration_id"`
	ProfileName     string                `json:"profile_name"`
	Class           string                `json:"class"`
	CreatedAt       time.Time             `json:"created_at"`
	Attempts        int                   `json:"attempts"`
	MaxAttempts     int                   `json:"max_attempts"`
	NextRetryAt     *time.Time            `json:"next_retry_at"`
	Reason          string                `json:"reason"`
	ReasonDetail    string                `json:"reason_detail"`
	DeferredAt      *time.Time            `json:"deferred_at"`
	DeferUntil      *time.Time            `json:"defer_until"`
	DeferCount      int                   `json:"defer_count"`
	FirstExaminedAt *time.Time            `json:"first_examined_at"`
	LastExaminedAt  *time.Time            `json:"last_examined_at"`
	LastError       string                `json:"last_error,omitempty"`
	LastDecision    *VeridianSendDecision `json:"last_decision"`
}

// VeridianQueueExplain est la réponse de queue.explain.
type VeridianQueueExplain struct {
	WorkspaceID string                    `json:"workspace_id"`
	GeneratedAt time.Time                 `json:"generated_at"`
	Total       int64                     `json:"total"`
	Groups      []VeridianQueueGroup      `json:"groups"`
	Orphans     VeridianQueueOrphans      `json:"orphans"`
	Entry       *VeridianQueueEntryDetail `json:"entry"`
}

// VeridianQueueRecomputeRequest : remise à zéro de la prochaine tentative d'un
// ensemble borné d'entrées. Jamais « tout le workspace ».
type VeridianQueueRecomputeRequest struct {
	WorkspaceID  string   `json:"workspace_id"`
	AutomationID string   `json:"automation_id,omitempty"`
	NodeID       string   `json:"node_id,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	ProfileID    string   `json:"profile_id,omitempty"`
	EntryIDs     []string `json:"entry_ids,omitempty"`
	Limit        int      `json:"limit"`
}

// VeridianQueueRecomputeMaxLimit borne une action de recalcul.
const VeridianQueueRecomputeMaxLimit = 5000

// Validate vérifie le garde-fou « au moins un filtre, borné ».
func (r *VeridianQueueRecomputeRequest) Validate() error {
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return ErrVeridianQueueRecompute("workspace_id is required")
	}
	if strings.TrimSpace(r.AutomationID) == "" && len(r.EntryIDs) == 0 {
		return ErrVeridianQueueRecompute("automation_id or entry_ids is required (never the whole workspace)")
	}
	if r.Limit < 1 || r.Limit > VeridianQueueRecomputeMaxLimit {
		return ErrVeridianQueueRecompute("limit must be between 1 and 5000")
	}
	return nil
}

// ErrVeridianQueueRecompute est une erreur de validation (400).
type ErrVeridianQueueRecompute string

func (e ErrVeridianQueueRecompute) Error() string { return string(e) }

// VeridianQueueExplainRepository lit la file et rend ses raisons d'attente.
type VeridianQueueExplainRepository interface {
	Explain(ctx context.Context, workspaceID string, f VeridianQueueExplainFilter) (*VeridianQueueExplain, error)
	EntryDetail(ctx context.Context, workspaceID, entryID string) (*VeridianQueueEntryDetail, error)
	// Recompute remet next_retry_at à NULL et efface la raison des entrées
	// pending/failed du filtre ; rend les identifiants touchés (borné par Limit).
	Recompute(ctx context.Context, workspaceID string, req VeridianQueueRecomputeRequest) ([]string, error)
}

// VeridianQueueExplainService : auth + permissions + résolution des noms.
type VeridianQueueExplainService interface {
	Explain(ctx context.Context, workspaceID string, f VeridianQueueExplainFilter) (*VeridianQueueExplain, error)
	Decisions(ctx context.Context, workspaceID string, f VeridianSendDecisionFilter) ([]*VeridianSendDecision, string, string, error)
	Recompute(ctx context.Context, req VeridianQueueRecomputeRequest) (int, error)
}
