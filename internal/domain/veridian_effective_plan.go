package domain

import "time"

// Veridian fork — lot 2 « vérité d'un profil » (08/10/2026).
//
// Types de sortie de EffectivePlan (calcul dans internal/service/queue,
// veridian_effective_plan.go) : ce que le worker appliquera à ce profil
// MAINTENANT. Données pures, sérialisées telles quelles par l'API
// emailProfiles.overview.

// Noms des portes de plafond journalier, dans l'ordre de priorité à égalité.
const (
	VeridianPlanGateWarmup     = "warmup"
	VeridianPlanGateProfileCap = "profile_cap"
	VeridianPlanGatePerSender  = "per_sender"
	VeridianPlanGateClassCap   = "class_cap"
	VeridianPlanGateNone       = "none"
)

// Raisons de blocage d'un envoi MAINTENANT (profil entier ou une classe).
const (
	VeridianPlanBlockPaused            = "paused"
	VeridianPlanBlockNotInRotation     = "not_in_rotation"
	VeridianPlanBlockUnverified        = "unverified"
	VeridianPlanBlockWindowClosed      = "window_closed"
	VeridianPlanBlockExcludedClass     = "excluded_class"
	VeridianPlanBlockReputationStopped = "reputation_stopped"
	VeridianPlanBlockWarmup            = "warmup"
	VeridianPlanBlockProfileCap        = "profile_cap"
	VeridianPlanBlockPerSender         = "per_sender"
	VeridianPlanBlockClassCap          = "class_cap"
)

// VeridianPlanGate est un plafond journalier, son compteur et ce qu'il reste.
type VeridianPlanGate struct {
	Name      string `json:"name"`
	Cap       int    `json:"cap"`
	Used      int    `json:"used"`
	Remaining int    `json:"remaining"`
	// Detail : périmètre du compteur (domaine émetteur, adresses, classes).
	Detail string `json:"detail,omitempty"`
}

// VeridianPlanClass est l'état d'une classe de destinataire pour ce profil.
type VeridianPlanClass struct {
	Class    string `json:"class"`
	Excluded bool   `json:"excluded"`
	// RatePerMin : débit effectif (après ralentissement), 0 = non bridé.
	RatePerMin     float64 `json:"rate_per_min"`
	RateConfigured float64 `json:"rate_configured"`
	// DailyCap : plafond de la classe après ralentissement, nil = aucun.
	DailyCap           *int `json:"daily_cap"`
	DailyCapConfigured *int `json:"daily_cap_configured"`
	SentToday          int  `json:"sent_today"`
	// Remaining : ce qu'il reste à envoyer vers cette classe aujourd'hui, toutes
	// portes confondues (nil = aucune limite de volume).
	Remaining *int `json:"remaining"`
	// Factor / Reason : ralentissement du couple (domaine émetteur, classe).
	Factor  int    `json:"slowdown_factor"`
	Reason  string `json:"slowdown_reason,omitempty"`
	Stopped bool   `json:"stopped"`
	// SlowdownRate : taux (0 a 1) qui a declenche le ralentissement, selon la
	// raison : rejets durs ou refus de politique 5.7.x sur 7 jours. 0 pour une
	// plainte ou un refus en bloc. Sent7d : envois du couple sur 7 jours. Lot 3 :
	// l ecran affiche la raison en clair (ex. 9,9 % de rejets).
	SlowdownRate float64 `json:"slowdown_rate,omitempty"`
	Sent7d       int     `json:"sent_7d,omitempty"`
	// SendableNow : le worker enverrait un message vers cette classe maintenant.
	SendableNow bool   `json:"sendable_now"`
	BlockedBy   string `json:"blocked_by,omitempty"`
}

type VeridianPlanWarmup struct {
	Active   bool       `json:"active"`
	Day      int        `json:"day,omitempty"`
	Of       int        `json:"of,omitempty"`
	CapToday int        `json:"cap_today,omitempty"`
	Started  *time.Time `json:"started_at,omitempty"`
}

type VeridianPlanWindow struct {
	Configured bool       `json:"configured"`
	OpenNow    bool       `json:"open_now"`
	NextOpenAt *time.Time `json:"next_open_at,omitempty"`
	Days       []int      `json:"days,omitempty"`
	StartHour  int        `json:"start_hour,omitempty"`
	EndHour    int        `json:"end_hour,omitempty"`
	Timezone   string     `json:"timezone,omitempty"`
	Source     string     `json:"source"`
}

// VeridianEffectivePlan est la vérité d'un profil d'envoi à un instant donné.
type VeridianEffectivePlan struct {
	Date string `json:"date"`
	// DayTimezone, DayStart, DayEnd : le jour de compte de ce profil (lot 4). Le
	// jour suit le fuseau de la fenêtre d'envoi du profil, pas minuit UTC ; un jour de
	// changement d'heure dure 23 h ou 25 h.
	DayTimezone string    `json:"day_timezone,omitempty"`
	DayStart    time.Time `json:"day_start,omitempty"`
	DayEnd      time.Time `json:"day_end,omitempty"`
	// Applicable false : profil transactionnel (aucune porte commerciale ne
	// s'applique, le worker n'en passe aucune) ou profil non assigné.
	Applicable bool   `json:"applicable"`
	Mode       string `json:"mode"`
	Paused     bool   `json:"paused"`

	// DailyCapToday : plafond total du jour (le plus bas des portes) ; nil = aucun
	// plafond configuré. LimitingGate nomme la porte qui le fixe.
	DailyCapToday  *int   `json:"daily_cap_today"`
	LimitingGate   string `json:"limiting_gate"`
	LimitingDetail string `json:"limiting_detail,omitempty"`

	SentToday      int  `json:"sent_today"`
	ReservedToday  int  `json:"reserved_today"`
	RemainingToday *int `json:"remaining_today"`
	// RemainingGate : la porte qui se vide en premier (peut différer de
	// LimitingGate : la plus basse n'est pas toujours la plus consommée).
	RemainingGate string             `json:"remaining_gate"`
	Gates         []VeridianPlanGate `json:"gates"`

	PerRecipientDailyCap int `json:"per_recipient_daily_cap"`
	PerSenderDailyCap    int `json:"per_sender_daily_cap"`
	ProfileDailyCap      int `json:"profile_daily_cap"`
	// NativeRatePerMin : cadence technique du profil (rate_limit_per_minute x
	// nombre d'adresses), distincte de la capacité.
	NativeRatePerMin int `json:"native_rate_per_min"`

	// Origine de la table de plafonds / débits par classe : profile, workspace, none.
	ClassCapsSource  string `json:"class_caps_source"`
	ClassRatesSource string `json:"class_rates_source"`

	Warmup          VeridianPlanWarmup `json:"warmup"`
	Window          VeridianPlanWindow `json:"window"`
	ExcludedClasses []string           `json:"excluded_classes"`

	// Réputation du domaine émetteur : facteur commun (4 sur plainte), plaintes 7 j.
	SenderDomains        []string `json:"sender_domains"`
	DomainSlowdownFactor int      `json:"domain_slowdown_factor"`
	Complaints7d         int      `json:"complaints_7d"`
	ReputationAlert      bool     `json:"reputation_alert"`

	Classes []VeridianPlanClass `json:"classes"`

	// SendableNow : au moins une classe partirait maintenant. BlockedBy : raisons
	// valables pour le profil entier (pause, fenêtre, hors rotation...).
	SendableNow bool     `json:"sendable_now"`
	BlockedBy   []string `json:"blocked_by"`
}

// VeridianPlanObserved sont les compteurs du jour (UTC) dont EffectivePlan a
// besoin. Mêmes sources que les portes du worker : message_history (envois
// acceptés) et veridian_daily_quota_counters (réservations atomiques).
type VeridianPlanObserved struct {
	// ProfileReserved : compteur atomique du profil (kind profile).
	ProfileReserved int
	// ProfileAccepted : envois acceptés par ce profil (message_history).
	ProfileAccepted int
	// DomainSent : envois acceptés par domaine émetteur, toutes classes.
	DomainSent map[string]int
	// SenderSent : envois acceptés par adresse émettrice (minuscules).
	SenderSent map[string]int
	// DomainClassSent : envois acceptés par domaine émetteur puis classe.
	DomainClassSent map[string]map[string]int
	// Compteurs atomiques (réservations) : plus hauts que l'historique quand un
	// résultat SMTP est ambigu. La capacité réelle est le maximum des deux.
	DomainReserved      map[string]int
	DomainClassReserved map[string]map[string]int
}
