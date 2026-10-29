package policy

import "time"

// Sources d'une valeur de règle, de la plus spécifique à la plus générale.
const (
	SourceProfile           = "profile"            // posée sur un profil d'envoi (niveau historique)
	SourceWorkspaceSetting  = "workspace_setting"  // réglage workspace historique (veridian_provider_class_*)
	SourceWorkspaceOverride = "workspace_override" // surcharge de politique (policy.set)
	SourcePreset            = "preset"             // défaut du catalogue
)

// Value est une valeur de règle avec son origine et le défaut du catalogue.
type Value[T any] struct {
	Value  T      `json:"value"`
	Source string `json:"source"`
	Preset T      `json:"preset"`
	// ShadowedBy : profils ou réglage workspace qui posent EUX-MÊMES cette valeur et la
	// masquent pour leur périmètre. Vide = la valeur ci-dessus s'applique partout.
	ShadowedBy []string `json:"shadowed_by,omitempty"`
}

// Observed : ce qui a été mesuré pour la classe.
type Observed struct {
	Sent         int `json:"sent"`
	Bounced      int `json:"bounced"`
	RepliedHuman int `json:"replied_human"`
	// Basis : "workspace" (mesuré ici) ou "catalog" (mesure du catalogue, workspace sans envoi).
	Basis string `json:"basis"`
	// ReplyRate : réponses humaines / envois (nil sans envoi). ReplyRateSmoothed : même taux
	// ramené vers la moyenne globale quand l'échantillon est petit (jamais nil).
	ReplyRate         *float64 `json:"reply_rate"`
	ReplyRateSmoothed float64  `json:"reply_rate_smoothed"`
	BounceRate        *float64 `json:"bounce_rate"`
}

// Rule : une règle, sa valeur et son explication en français.
type Rule struct {
	Name   string `json:"name"` // daily_cap, rate_per_min, warmup_share, slowdown_threshold
	Value  any    `json:"value"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

// ClassView : la politique effective d'une classe, expliquée.
type ClassView struct {
	Class             string         `json:"class"`
	Label             string         `json:"label"`
	DailyCap          Value[int]     `json:"daily_cap"`
	RatePerMin        Value[float64] `json:"rate_per_min"`
	WarmupShare       Value[float64] `json:"warmup_share"`
	SlowdownThreshold Value[float64] `json:"slowdown_threshold"`
	Observed          Observed       `json:"observed"`
	Why               string         `json:"why"`
	Rules             []Rule         `json:"rules"`
	// Explanation : la politique de la classe en un paragraphe lisible.
	Explanation string `json:"explanation"`
}

// PolicyView : la réponse de policy.get.
type PolicyView struct {
	WorkspaceID string      `json:"workspace_id"`
	CatalogAsOf string      `json:"catalog_as_of"`
	Source      string      `json:"catalog_source"`
	Classes     []ClassView `json:"classes"`
	// Overrides : nombre de classes ayant au moins une surcharge de politique.
	Overrides int `json:"overrides"`
}

// Override : une surcharge de politique d'un workspace pour une classe. Zéro = non posé.
type Override struct {
	Class             string    `json:"class"`
	DailyCap          int       `json:"daily_cap,omitempty"`
	RatePerMin        float64   `json:"rate_per_min,omitempty"`
	WarmupShare       float64   `json:"warmup_share,omitempty"`
	SlowdownThreshold float64   `json:"slowdown_threshold,omitempty"`
	Reason            string    `json:"reason,omitempty"`
	UpdatedBy         string    `json:"updated_by,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Les noms de règle acceptés par policy.reset.
const (
	RuleDailyCap          = "daily_cap"
	RuleRatePerMin        = "rate_per_min"
	RuleWarmupShare       = "warmup_share"
	RuleSlowdownThreshold = "slowdown_threshold"
)

// AllRules : les quatre règles surchargeables.
var AllRules = []string{RuleDailyCap, RuleRatePerMin, RuleWarmupShare, RuleSlowdownThreshold}

// Sources de priorité.
const (
	SourceTypeBroadcast  = "broadcast"
	SourceTypeAutomation = "automation"
)

// PrioritySource : une campagne ou automation, sa priorité effective et son état.
type PrioritySource struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	// Priority : valeur effective (2 à 9). Explicit : posée par policy.priority.set, sinon défaut 5.
	Priority  int       `json:"priority"`
	Explicit  bool      `json:"explicit"`
	Pending   int       `json:"pending"` // mails de cette source en attente dans la file
	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// PriorityChange : le résultat de policy.priority.set / clear.
type PriorityChange struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	Priority   int    `json:"priority"`
	// RequeuedPending : mails déjà en file (statut pending) dont la priorité a été changée.
	// Les mails déjà partis ne bougent pas.
	RequeuedPending int64 `json:"requeued_pending"`
	// Note : rappel de la limite (la priorité ne change l'ordre que si la file est engorgée).
	Note string `json:"note"`
}
