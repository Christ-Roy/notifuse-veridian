// Package reconcile est le réconciliateur de Notifuse Veridian : il recoupe ce que le
// logiciel croit (compteurs, statuts de contacts, classes) avec ce qui s'est réellement
// passé (messages envoyés, réponses, boîtes), signale chaque écart avec sa cause en
// français et propose une réparation exécutable par API.
//
// Un invariant = un fichier (inv_*.go) : sa requête, son jugement pur (testé sans base),
// son explication en français, son action proposée. Les écarts sont persistés dans
// veridian_reconcile_finding (migration V64). Trois états, jamais deux : un contrôle est
// « ok », « ecarts » ou « non_abouti » (il n'a pas pu conclure : ce n'est pas un succès).
package reconcile

import (
	"time"
)

// Severity : gravité d'un écart.
type Severity string

const (
	// SeverityCritical : de l'argent ou de la réputation est en jeu maintenant (un mail
	// part vers quelqu'un qui ne doit plus en recevoir).
	SeverityCritical Severity = "critical"
	// SeverityWarning : un chiffre ou un statut est faux, rien ne part de travers.
	SeverityWarning Severity = "warning"
	// SeverityInfo : constat à connaître, sans conséquence immédiate.
	SeverityInfo Severity = "info"
)

// CheckStatus : issue d'UN contrôle lors d'un passage.
type CheckStatus string

const (
	StatusOK        CheckStatus = "ok"
	StatusFindings  CheckStatus = "ecarts"
	StatusIncomplet CheckStatus = "non_abouti"
)

// Action : réparation proposée. Elle s'exécute par l'API (`API` donne l'appel exact) ;
// le serveur ne se fie jamais à la liste vue à la détection : il relit les cibles au
// moment d'appliquer (voir actions.go).
type Action struct {
	Kind       string         `json:"kind"`
	Label      string         `json:"label"`  // texte du bouton, en français
	Impact     string         `json:"impact"` // ce que ça change, en français
	Reversible bool           `json:"reversible"`
	Params     map[string]any `json:"params"`
	API        *ActionAPI     `json:"api,omitempty"`
}

// ActionAPI : l'appel HTTP qui applique l'action.
type ActionAPI struct {
	Method string         `json:"method"`
	Path   string         `json:"path"`
	Body   map[string]any `json:"body"`
}

// Finding : un écart constaté. Expected et Observed sont des textes courts lisibles ;
// Evidence porte la preuve structurée (effectifs, échantillon, requêtes de contrôle).
type Finding struct {
	ID             string         `json:"id"`
	Check          string         `json:"check"`
	Severity       Severity       `json:"severity"`
	SubjectRef     string         `json:"subject_ref"`
	Title          string         `json:"title"`
	Expected       string         `json:"expected"`
	Observed       string         `json:"observed"`
	Explanation    string         `json:"explanation"`
	Evidence       map[string]any `json:"evidence"`
	ProposedAction *Action        `json:"proposed_action"`
	FirstSeenAt    time.Time      `json:"first_seen_at"`
	LastSeenAt     time.Time      `json:"last_seen_at"`
	ResolvedAt     *time.Time     `json:"resolved_at"`
	ResolvedBy     string         `json:"resolved_by"`
	Resolution     map[string]any `json:"resolution"`
}

// CheckReport : le résultat d'un contrôle pour un passage.
type CheckReport struct {
	Check      string      `json:"check"`
	Number     int         `json:"number"`
	Title      string      `json:"title"`
	Status     CheckStatus `json:"status"`
	Count      int         `json:"count"`
	Error      string      `json:"error,omitempty"` // raison, quand non abouti
	DurationMS int64       `json:"duration_ms"`
}

// Report : état des écarts ouverts après un passage (ou à la demande).
type Report struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Ran         bool          `json:"ran"` // vrai si un passage vient d'avoir lieu
	Checks      []CheckReport `json:"checks"`
	Findings    []Finding     `json:"findings"`
	Summary     Summary       `json:"summary"`
	// Definitions : la définition écrite de chaque grandeur comptée (une seule par grandeur).
	Definitions []Definition `json:"definitions,omitempty"`
}

// Summary : décompte des écarts ouverts.
type Summary struct {
	Open       int `json:"open"`
	Critical   int `json:"critical"`
	Warning    int `json:"warning"`
	Info       int `json:"info"`
	NotReached int `json:"not_reached"` // contrôles non aboutis dans ce passage
}

// SeverityRank ordonne les gravités (plus grand = plus grave).
func SeverityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}
