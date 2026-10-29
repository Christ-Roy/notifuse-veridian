package policy

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// StockKey : clé du réglage du stock dans veridian_policy_settings.
const StockKey = "stock"

// DefaultTargetDays : couverture visée par défaut (la pratique de equilibrer_classes.py : 3 jours).
const DefaultTargetDays = 3

// Colonnes de contact utilisables pour la classe et les filtres (liste blanche : jamais un nom venu de l'extérieur).
var contactColumns = map[string]bool{"custom_string_1": true, "custom_string_2": true, "custom_string_3": true, "custom_string_4": true, "custom_string_5": true}

// StockConfig définit ce qu'est le stock prospectable d'un workspace. Les valeurs vides prennent le défaut.
type StockConfig struct {
	// EnrolledListID : liste des contacts inscrits à la séquence (colonne « inscrits » du script).
	// Vide : les listes des automations actives.
	EnrolledListID string `json:"enrolled_list_id,omitempty"`
	// ReserveListID : liste des contacts en réserve, pas encore inscrits (colonne « réserve »). Vide : aucune réserve.
	ReserveListID string `json:"reserve_list_id,omitempty"`
	// EnrollAutomationID : automation dans laquelle on inscrit (exclut les déjà inscrits des suggestions).
	EnrollAutomationID string `json:"enroll_automation_id,omitempty"`
	// ClassField : colonne qui porte la classe du fournisseur (défaut custom_string_5).
	ClassField string `json:"class_field,omitempty"`
	// ExcludeEmailRegex : adresses écartées du stock (comptes internes, tests).
	ExcludeEmailRegex string `json:"exclude_email_regex,omitempty"`
	// RequireNonEmpty : colonnes qui doivent être renseignées pour qu'un contact soit prospectable.
	RequireNonEmpty []string `json:"require_non_empty,omitempty"`
	// ExcludePrefixes : par colonne, préfixes de valeur qui écartent le contact (ex. custom_string_1 : cart_).
	ExcludePrefixes map[string][]string `json:"exclude_prefixes,omitempty"`
	// TargetDays : jours de couverture visés. 0 : DefaultTargetDays.
	TargetDays int `json:"target_days,omitempty"`
}

// Validate refuse une définition dangereuse ou absurde.
func (c *StockConfig) Validate() error {
	for _, id := range []string{c.EnrolledListID, c.ReserveListID, c.EnrollAutomationID} {
		if len(id) > 64 {
			return invalid("identifiant trop long (64 caractères max)")
		}
	}
	if c.ClassField != "" && !contactColumns[c.ClassField] {
		return invalid("class_field doit être l'une de custom_string_1 à custom_string_5")
	}
	for _, col := range c.RequireNonEmpty {
		if !contactColumns[col] {
			return invalid("require_non_empty: colonne inconnue %q (custom_string_1 à custom_string_5)", col)
		}
	}
	for col, prefixes := range c.ExcludePrefixes {
		if !contactColumns[col] {
			return invalid("exclude_prefixes: colonne inconnue %q (custom_string_1 à custom_string_5)", col)
		}
		if len(prefixes) > 20 {
			return invalid("exclude_prefixes: 20 préfixes max par colonne")
		}
		for _, p := range prefixes {
			if p == "" || len(p) > 64 {
				return invalid("exclude_prefixes: préfixe vide ou trop long")
			}
		}
	}
	if len(c.ExcludeEmailRegex) > 500 {
		return invalid("exclude_email_regex: 500 caractères max")
	}
	if c.ExcludeEmailRegex != "" {
		if _, err := regexp.Compile(c.ExcludeEmailRegex); err != nil {
			return invalid("exclude_email_regex illisible: %v", err)
		}
	}
	if c.TargetDays < 0 || c.TargetDays > 60 {
		return invalid("target_days doit être entre 1 et 60")
	}
	return nil
}

// WithDefaults rend la définition avec ses valeurs par défaut posées.
func (c StockConfig) WithDefaults() StockConfig {
	if c.ClassField == "" {
		c.ClassField = "custom_string_5"
	}
	if c.TargetDays == 0 {
		c.TargetDays = DefaultTargetDays
	}
	return c
}

// ListCounts : contacts d'une liste rangés par classe, avec la part jamais contactée.
type ListCounts struct {
	Total  int `json:"total"`
	Unsent int `json:"unsent"`
}

// Capacity : ce qu'une classe peut envoyer par jour, tous profils commerciaux actifs confondus.
type Capacity struct {
	// Today : somme des capacités configurées du jour (envoyés + reste) : la définition du script
	// stock-par-classe.py, qui varie avec l'heure et la fenêtre d'envoi.
	Today int
	// Nominal : ce que la classe absorberait sur une journée d'ouverture entière (plafonds de classe après
	// ralentissement, bornés par la capacité d'une journée entière de chaque profil). Stable.
	Nominal int
}

// Statuts d'une classe.
const (
	StatusOK       = "ok"           // couverture visée tenue
	StatusLow      = "bas"          // sous la couverture visée
	StatusCritical = "critique"     // moins d'un jour
	StatusBlocked  = "sans_plafond" // du stock mais aucune capacité : classe exclue, profils en pause ou fenêtre close
	StatusIdle     = "inactive"     // ni stock ni capacité
)

// StockRow : une classe du tableau de stock.
type StockRow struct {
	Class string `json:"class"`
	Label string `json:"label"`
	// Colonnes du script stock-par-classe.py : inscrits, réserve, total, plafond/jour, jours.
	Enrolled      int `json:"enrolled"`
	Reserve       int `json:"reserve"`
	Prospectable  int `json:"prospectable"`
	CapacityToday int `json:"capacity_today"`
	// DaysOfCoverage : prospectables / capacité du jour, arrondi au dixième, comme le script (nil sans capacité).
	DaysOfCoverage *float64 `json:"days_of_coverage"`

	// Au-delà du script : la part jamais contactée, la capacité stable, la couverture honnête, le besoin.
	EnrolledUnsent  int      `json:"enrolled_unsent"`
	ReserveUnsent   int      `json:"reserve_unsent"`
	Unsent          int      `json:"unsent"`
	CapacityNominal int      `json:"capacity_nominal"`
	DaysUnsent      *float64 `json:"days_unsent"`
	Status          string   `json:"status"`
	// RestockNeed : contacts à aller chercher pour tenir TargetDays jours à la capacité nominale, une fois
	// épuisé tout ce qui n'a jamais été contacté (inscrits et réserve).
	RestockNeed int `json:"restock_need"`
	// ReplyRateSmoothed : taux de réponse observé (lissé), pour pondérer où chercher en premier.
	ReplyRateSmoothed float64 `json:"reply_rate_smoothed"`
}

// StockTotals : somme des lignes.
type StockTotals struct {
	Enrolled, Reserve, Prospectable, EnrolledUnsent, ReserveUnsent, Unsent int
	CapacityToday, CapacityNominal, RestockNeed                            int
	DaysOfCoverage, DaysUnsent                                             *float64
}

// StockOverview : la réponse de stock.overview.
type StockOverview struct {
	WorkspaceID string      `json:"workspace_id"`
	GeneratedAt string      `json:"generated_at"`
	TargetDays  int         `json:"target_days"`
	Config      StockConfig `json:"config"`
	// ConfigSource : d'où viennent les listes (request, config, automations, none).
	ConfigSource string         `json:"config_source"`
	Classes      []StockRow     `json:"classes"`
	Totals       StockTotalsOut `json:"totals"`
	Notes        []string       `json:"notes"`
}

// StockTotalsOut : totaux sérialisés.
type StockTotalsOut struct {
	Enrolled        int      `json:"enrolled"`
	Reserve         int      `json:"reserve"`
	Prospectable    int      `json:"prospectable"`
	EnrolledUnsent  int      `json:"enrolled_unsent"`
	ReserveUnsent   int      `json:"reserve_unsent"`
	Unsent          int      `json:"unsent"`
	CapacityToday   int      `json:"capacity_today"`
	CapacityNominal int      `json:"capacity_nominal"`
	DaysOfCoverage  *float64 `json:"days_of_coverage"`
	DaysUnsent      *float64 `json:"days_unsent"`
	RestockNeed     int      `json:"restock_need"`
}

// UnclassifiedKey : classe des contacts sans étiquette (comme le script).
const UnclassifiedKey = "?"

// round1 arrondit au dixième comme le round(x, 1) de Python que le script stock-par-classe.py utilise : sur la valeur exacte du
// flottant, les égalités exactes (36,25) vers le pair (36,2). math.Round les arrondirait vers le haut (36,3).
func round1(x float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 1, 64), 64)
	return v
}

func ratio(n, d int) *float64 {
	if d <= 0 {
		return nil
	}
	v := round1(float64(n) / float64(d))
	return &v
}

// CapacityByClass additionne, pour chaque classe, la capacité des profils COMMERCIAUX actifs (ni en pause ni hors
// rotation), sans les classes exclues : exactement la règle de `notifuse profiles:gaps` que lit le script.
func CapacityByClass(ov *domain.VeridianEmailProfilesOverview) map[string]Capacity {
	out := map[string]Capacity{}
	if ov == nil {
		return out
	}
	deref := func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	}
	for _, p := range ov.Profiles {
		if p.Usage != domain.VeridianProfileUsageCommercial || p.Paused || !p.InRotation {
			continue
		}
		nominal := map[string]int{}
		sum := 0
		for _, c := range p.Plan.Classes {
			if c.Excluded {
				continue
			}
			n := deref(c.DailyCap)
			nominal[c.Class] = n
			sum += n
		}
		// Une journée entière du profil borne la somme de ses classes (chauffe, plafond par expéditeur).
		scale := 1.0
		if full := p.Plan.Capacity.FullDayConfigured; full != nil && sum > 0 && *full < sum {
			scale = float64(*full) / float64(sum)
		}
		for _, c := range p.Plan.Classes {
			if c.Excluded {
				continue
			}
			cur := out[c.Class]
			cur.Today += deref(c.CapacityConfigured)
			cur.Nominal += int(math.Floor(float64(nominal[c.Class])*scale + 0.5))
			out[c.Class] = cur
		}
	}
	return out
}

// BuildStock assemble le tableau. enrolled et reserve : comptes par classe de chaque liste.
func BuildStock(cfg StockConfig, enrolled, reserve map[string]ListCounts, capacity map[string]Capacity, replyRate func(class string) float64) []StockRow {
	cfg = cfg.WithDefaults()
	classes := map[string]bool{}
	for k := range enrolled {
		classes[k] = true
	}
	for k := range reserve {
		classes[k] = true
	}
	for k := range capacity {
		classes[k] = true
	}
	var rows []StockRow
	for class := range classes {
		e, r, c := enrolled[class], reserve[class], capacity[class]
		row := StockRow{
			Class: class, Label: classLabel(class),
			Enrolled: e.Total, Reserve: r.Total, Prospectable: e.Total + r.Total,
			CapacityToday:  c.Today,
			EnrolledUnsent: e.Unsent, ReserveUnsent: r.Unsent, Unsent: e.Unsent + r.Unsent,
			CapacityNominal: c.Nominal,
		}
		row.DaysOfCoverage = ratio(row.Prospectable, c.Today)
		row.DaysUnsent = ratio(row.Unsent, c.Nominal)
		if replyRate != nil {
			row.ReplyRateSmoothed = replyRate(class)
		}
		if need := c.Nominal*cfg.TargetDays - row.Unsent; need > 0 {
			row.RestockNeed = need
		}
		row.Status = stockStatus(row, cfg.TargetDays)
		rows = append(rows, row)
	}
	// Comme le script : les classes les plus courtes en premier, celles sans capacité à la fin, puis par nom.
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].DaysOfCoverage, rows[j].DaysOfCoverage
		if (a == nil) != (b == nil) {
			return b == nil
		}
		if a != nil && *a != *b {
			return *a < *b
		}
		return rows[i].Class < rows[j].Class
	})
	return rows
}

func stockStatus(r StockRow, target int) string {
	switch {
	case r.CapacityNominal == 0 && r.CapacityToday == 0:
		if r.Unsent > 0 {
			return StatusBlocked
		}
		return StatusIdle
	case r.CapacityNominal == 0:
		return StatusBlocked
	case r.Unsent < r.CapacityNominal:
		return StatusCritical
	case r.Unsent < r.CapacityNominal*target:
		return StatusLow
	}
	return StatusOK
}

func classLabel(class string) string {
	if class == UnclassifiedKey {
		return "Non classé"
	}
	if p, ok := domain.VeridianClassPresetFor(class); ok {
		return p.Label
	}
	return class
}

// Totals additionne les lignes. Les jours des totaux suivent la même règle que les lignes.
func Totals(rows []StockRow) StockTotalsOut {
	var t StockTotalsOut
	for _, r := range rows {
		t.Enrolled += r.Enrolled
		t.Reserve += r.Reserve
		t.Prospectable += r.Prospectable
		t.EnrolledUnsent += r.EnrolledUnsent
		t.ReserveUnsent += r.ReserveUnsent
		t.Unsent += r.Unsent
		t.CapacityToday += r.CapacityToday
		t.CapacityNominal += r.CapacityNominal
		t.RestockNeed += r.RestockNeed
	}
	t.DaysOfCoverage = ratio(t.Prospectable, t.CapacityToday)
	t.DaysUnsent = ratio(t.Unsent, t.CapacityNominal)
	return t
}

// likePrefix échappe un préfixe pour un LIKE ... ESCAPE '\'.
func likePrefix(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(p) + "%"
}

// filterClauses rend les conditions « contact prospectable » de la définition (adresses écartées, colonnes obligatoires,
// préfixes interdits) et leurs paramètres, numérotés à la suite de args. Les noms de colonne viennent de la liste blanche,
// les valeurs sont des paramètres.
func filterClauses(cfg StockConfig, args []any) ([]string, []any) {
	var where []string
	if cfg.ExcludeEmailRegex != "" {
		args = append(args, cfg.ExcludeEmailRegex)
		where = append(where, fmt.Sprintf("c.email !~* $%d", len(args)))
	}
	for _, col := range cfg.RequireNonEmpty {
		where = append(where, fmt.Sprintf("COALESCE(c.%s,'') <> ''", col))
	}
	cols := make([]string, 0, len(cfg.ExcludePrefixes))
	for col := range cfg.ExcludePrefixes {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	for _, col := range cols {
		for _, p := range cfg.ExcludePrefixes[col] {
			args = append(args, likePrefix(p))
			where = append(where, fmt.Sprintf(`COALESCE(c.%s,'') NOT LIKE $%d ESCAPE '\'`, col, len(args)))
		}
	}
	return where, args
}

// placeholders ajoute des valeurs aux paramètres et rend la liste « $n,$m ».
func placeholders(args []any, vals []string) (string, []any) {
	ph := make([]string, len(vals))
	for i, v := range vals {
		args = append(args, v)
		ph[i] = fmt.Sprintf("$%d", len(args))
	}
	return strings.Join(ph, ","), args
}

// sentSQL : « ce contact a déjà reçu un mail commercial ». Le transactionnel ne compte pas comme un contact commercial.
const sentSQL = `EXISTS (SELECT 1 FROM message_history m WHERE m.contact_email = c.email AND m.sent_at IS NOT NULL AND COALESCE(m.veridian_message_type,'') <> 'transactional')`

// countsSQL construit la requête de comptage d'une ou deux listes par classe.
func countsSQL(cfg StockConfig, listIDs []string) (string, []any) {
	cfg = cfg.WithDefaults()
	ph, args := placeholders(make([]any, 0, len(listIDs)+8), listIDs)
	where := []string{"cl.deleted_at IS NULL", "cl.list_id IN (" + ph + ")"}
	extra, args := filterClauses(cfg, args)
	where = append(where, extra...)
	q := fmt.Sprintf(`SELECT cl.list_id, COALESCE(NULLIF(c.%[1]s,''),'%[2]s') AS class, COUNT(*) AS total,
		COUNT(*) FILTER (WHERE NOT %[4]s) AS unsent
		FROM contact_lists cl JOIN contacts c ON c.email = cl.email
		WHERE %[3]s GROUP BY 1, 2`, cfg.ClassField, UnclassifiedKey, strings.Join(where, " AND "), sentSQL)
	return q, args
}

// followupsSQL compte, par classe, les contacts déjà contactés dont la prochaine étape d'une séquence active échoit avant until.
func followupsSQL(cfg StockConfig, enrolledListIDs []string, until time.Time) (string, []any) {
	cfg = cfg.WithDefaults()
	args := []any{until.UTC()}
	where := []string{"ca.status = 'active'", "a.status = 'active'", "a.deleted_at IS NULL", "ca.scheduled_at IS NOT NULL", "ca.scheduled_at <= $1", sentSQL}
	var scope []string
	if cfg.EnrollAutomationID != "" {
		args = append(args, cfg.EnrollAutomationID)
		scope = append(scope, fmt.Sprintf("a.id = $%d", len(args)))
	}
	if len(enrolledListIDs) > 0 {
		var ph string
		ph, args = placeholders(args, enrolledListIDs)
		scope = append(scope, "a.list_id IN ("+ph+")")
	}
	if len(scope) == 0 {
		return "", nil
	}
	where = append(where, "("+strings.Join(scope, " OR ")+")")
	extra, args := filterClauses(cfg, args)
	where = append(where, extra...)
	q := fmt.Sprintf(`SELECT COALESCE(NULLIF(c.%s,''),'%s') AS class, COUNT(DISTINCT c.email)
		FROM contact_automations ca JOIN automations a ON a.id = ca.automation_id JOIN contacts c ON c.email = ca.contact_email
		WHERE %s GROUP BY 1`, cfg.ClassField, UnclassifiedKey, strings.Join(where, " AND "))
	return q, args
}

// Candidat : classes de contacts de la réserve que l'on peut inscrire.
// candidatesSQL liste les contacts de la réserve éligibles à l'inscription : jamais contactés, ni désabonnés ni rejetés, pas
// déjà inscrits (liste d'inscrits ou automation d'inscription). Rang par classe : priorité (custom_string_2, P1 d'abord),
// puis score (custom_number_1, le plus haut d'abord), puis adresse. Rend (classe, adresse, rang, total de la classe).
func candidatesSQL(cfg StockConfig, reserveListID string, enrolledListIDs []string, perClass int) (string, []any) {
	cfg = cfg.WithDefaults()
	args := []any{reserveListID}
	where := []string{"cl.deleted_at IS NULL", "cl.list_id = $1", "cl.status NOT IN ('unsubscribed','bounced','complained')", "NOT " + sentSQL}
	if len(enrolledListIDs) > 0 {
		var ph string
		ph, args = placeholders(args, enrolledListIDs)
		where = append(where, "NOT EXISTS (SELECT 1 FROM contact_lists e WHERE e.email = c.email AND e.deleted_at IS NULL AND e.list_id IN ("+ph+"))")
	}
	if cfg.EnrollAutomationID != "" {
		args = append(args, cfg.EnrollAutomationID)
		where = append(where, fmt.Sprintf("NOT EXISTS (SELECT 1 FROM contact_automations x WHERE x.automation_id = $%d AND x.contact_email = c.email)", len(args)))
	}
	extra, args := filterClauses(cfg, args)
	where = append(where, extra...)
	args = append(args, perClass)
	cls := fmt.Sprintf("COALESCE(NULLIF(c.%s,''),'%s')", cfg.ClassField, UnclassifiedKey)
	q := fmt.Sprintf(`SELECT class, email, rn, total FROM (
		SELECT %[1]s AS class, c.email AS email,
			ROW_NUMBER() OVER (PARTITION BY %[1]s ORDER BY c.custom_string_2 ASC NULLS LAST, c.custom_number_1 DESC NULLS LAST, c.email) AS rn,
			COUNT(*) OVER (PARTITION BY %[1]s) AS total
		FROM contact_lists cl JOIN contacts c ON c.email = cl.email
		WHERE %[2]s) t WHERE rn <= $%[3]d ORDER BY class, rn`, cls, strings.Join(where, " AND "), len(args))
	return q, args
}
