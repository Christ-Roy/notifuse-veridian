package policy

import (
	"sort"
	"time"
)

// Suggestion d'inscription (policy.suggest-enrollment). RIEN n'est jamais inscrit par ce code : il calcule, par classe de
// fournisseur, combien de contacts de la réserve inscrire pour tenir N jours de couverture sans dépasser les plafonds, et
// combien il faudra aller chercher faute de réserve. L'inscription reste un geste de l'humain (ou de l'agent qui le décide).

// SuggestRow : le calcul d'une classe.
type SuggestRow struct {
	Class string `json:"class"`
	Label string `json:"label"`
	// CapacityPerDay : ce que la classe envoie par jour d'ouverture (plafonds de classe après ralentissement, bornés par la
	// capacité d'une journée entière des profils).
	CapacityPerDay int `json:"capacity_per_day"`
	// HorizonCapacity : CapacityPerDay x jours visés.
	HorizonCapacity int `json:"horizon_capacity"`
	// FollowupsDue : relances de contacts déjà en séquence qui échoient avant la fin de l'horizon. Elles prennent la capacité
	// avant les nouveaux contacts.
	FollowupsDue int `json:"followups_due"`
	// RoomForNew : place pour de premiers contacts sur l'horizon = HorizonCapacity - FollowupsDue (jamais négatif).
	RoomForNew int `json:"room_for_new"`
	// EnrolledUnsent : déjà inscrits, jamais contactés : ils partiront sans rien faire.
	EnrolledUnsent int `json:"enrolled_unsent"`
	// Gap : RoomForNew - EnrolledUnsent (jamais négatif) : ce qui manque pour remplir l'horizon.
	Gap int `json:"gap"`
	// ReserveUnsent : contacts de la réserve, jamais contactés, pas encore inscrits.
	ReserveUnsent int `json:"reserve_unsent"`
	// SuggestEnroll : à inscrire depuis la réserve = min(Gap, ReserveUnsent). Jamais plus que la place restante.
	SuggestEnroll int `json:"suggest_enroll"`
	// Shortfall : ce que la réserve ne couvre pas = Gap - SuggestEnroll : à aller chercher (chasse).
	Shortfall int `json:"shortfall"`
	// ReplyRateSmoothed : taux de réponse observé (lissé) : où chercher en premier.
	ReplyRateSmoothed float64 `json:"reply_rate_smoothed"`
	// Emails : les contacts à inscrire, dans l'ordre (priorité puis score), seulement si demandé.
	Emails []string `json:"emails,omitempty"`
}

// RestockItem : une classe à approvisionner, dans l'ordre où chercher.
type RestockItem struct {
	Class             string  `json:"class"`
	Label             string  `json:"label"`
	Shortfall         int     `json:"shortfall"`
	ReplyRateSmoothed float64 `json:"reply_rate_smoothed"`
}

// SuggestTotals : sommes.
type SuggestTotals struct {
	CapacityPerDay int `json:"capacity_per_day"`
	FollowupsDue   int `json:"followups_due"`
	SuggestEnroll  int `json:"suggest_enroll"`
	Shortfall      int `json:"shortfall"`
}

// Suggestion : la réponse de policy.suggest-enrollment.
type Suggestion struct {
	WorkspaceID string `json:"workspace_id"`
	GeneratedAt string `json:"generated_at"`
	Days        int    `json:"days"`
	// EnrollAutomationID : l'automation où inscrire (définition du stock).
	EnrollAutomationID string        `json:"enroll_automation_id,omitempty"`
	Classes            []SuggestRow  `json:"classes"`
	Totals             SuggestTotals `json:"totals"`
	// RestockPriority : classes à approvisionner, les plus rentables d'abord (taux de réponse lissé), puis le manque.
	RestockPriority []RestockItem `json:"restock_priority"`
	// Applied : toujours faux. Ce service n'inscrit personne.
	Applied bool     `json:"applied"`
	Notes   []string `json:"notes"`
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// BuildSuggestion calcule la suggestion à partir du stock, de la capacité et des relances à venir. Pure.
func BuildSuggestion(days int, enrolled, reserve map[string]ListCounts, capacity map[string]Capacity, followups map[string]int, replyRate func(string) float64) ([]SuggestRow, SuggestTotals, []RestockItem) {
	classes := map[string]bool{}
	for k := range capacity {
		classes[k] = true
	}
	var rows []SuggestRow
	for class := range classes {
		c := capacity[class]
		if c.Nominal <= 0 {
			continue // une classe sans capacité n'absorbe rien : inscrire serait stocker des contacts qui ne partiront pas
		}
		r := SuggestRow{Class: class, Label: classLabel(class), CapacityPerDay: c.Nominal, HorizonCapacity: c.Nominal * days,
			FollowupsDue: followups[class], EnrolledUnsent: enrolled[class].Unsent, ReserveUnsent: reserve[class].Unsent}
		r.RoomForNew = maxInt(0, r.HorizonCapacity-r.FollowupsDue)
		r.Gap = maxInt(0, r.RoomForNew-r.EnrolledUnsent)
		r.SuggestEnroll = minInt(r.Gap, r.ReserveUnsent)
		r.Shortfall = r.Gap - r.SuggestEnroll
		if replyRate != nil {
			r.ReplyRateSmoothed = replyRate(class)
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Shortfall+rows[i].SuggestEnroll != rows[j].Shortfall+rows[j].SuggestEnroll {
			return rows[i].Shortfall+rows[i].SuggestEnroll > rows[j].Shortfall+rows[j].SuggestEnroll
		}
		return rows[i].Class < rows[j].Class
	})
	var t SuggestTotals
	var restock []RestockItem
	for _, r := range rows {
		t.CapacityPerDay += r.CapacityPerDay
		t.FollowupsDue += r.FollowupsDue
		t.SuggestEnroll += r.SuggestEnroll
		t.Shortfall += r.Shortfall
		if r.Shortfall > 0 {
			restock = append(restock, RestockItem{Class: r.Class, Label: r.Label, Shortfall: r.Shortfall, ReplyRateSmoothed: r.ReplyRateSmoothed})
		}
	}
	sort.Slice(restock, func(i, j int) bool {
		if restock[i].ReplyRateSmoothed != restock[j].ReplyRateSmoothed {
			return restock[i].ReplyRateSmoothed > restock[j].ReplyRateSmoothed
		}
		if restock[i].Shortfall != restock[j].Shortfall {
			return restock[i].Shortfall > restock[j].Shortfall
		}
		return restock[i].Class < restock[j].Class
	})
	return rows, t, restock
}

// horizon : fin de l'horizon des relances. N jours de couverture sont N jours d'envoi (lundi au vendredi) : on les convertit
// en jours calendaires (x 7/5, arrondi au-dessus), de sorte qu'un week-end ne soit pas compté comme jour d'envoi.
func horizon(now time.Time, days int) time.Time {
	cal := (days*7 + 4) / 5
	return now.UTC().Add(time.Duration(cal) * 24 * time.Hour)
}
