package reconcile

import (
	"context"
	"fmt"
	"sort"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/lib/pq"
)

// Invariant 7 — Stock : la file ne contient pas de contacts déjà contactés, et le stock
// annoncé égale le stock réel.
//
//	a. un envoi en file pour un mail (même modèle, même séquence ou campagne) déjà parti
//	   au même contact : doublon, sauf remise à zéro voulue du contact ;
//	b. « stock restant » servi par le tableau de bord contre un recompte indépendant,
//	   avec la définition écrite (definitions.go) : personnes actives jamais contactées,
//	   hors listes d'exclusion (nom contenant « exclu ») et hors personnes qui sont dans
//	   l'une d'elles. Cas fondateur N1 du rapport UX : 5 371 annoncés, dont 771 clients
//	   exclus, pour 4 600 prospectables.
//
// Le stock par classe annoncé (routes de stock du lot L3) se recoupera ici avec la même
// requête groupée par classe quand il existera.

const codeStock = "stock"

type stockInvariant struct{}

func init() { Register(stockInvariant{}) }

func (stockInvariant) Code() string  { return codeStock }
func (stockInvariant) Number() int   { return 7 }
func (stockInvariant) Title() string { return "La file et le stock disent vrai" }

// ---------- a. doublons en file ----------

type queueDupRow struct {
	SourceType string
	SourceID   string
	Name       string
	Count      int
	Sample     []string
}

const queueDupFrom = `
	FROM email_queue e
	LEFT JOIN automations a ON e.source_type = 'automation' AND a.id = e.source_id
	LEFT JOIN broadcasts b ON e.source_type = 'broadcast' AND b.id = e.source_id
	WHERE e.status = 'pending' AND e.source_type IN ('automation', 'broadcast')
	  AND COALESCE(e.payload->>'veridian_transactional', '') <> 'true'
	  AND EXISTS (
	      SELECT 1 FROM message_history m
	      WHERE m.contact_email = e.contact_email AND m.template_id = e.template_id
	        AND (m.automation_id = e.source_id OR m.broadcast_id = e.source_id)
	        AND m.sent_at IS NOT NULL AND m.failed_at IS NULL
	        AND m.transactional_notification_id IS NULL AND m.veridian_message_type IS DISTINCT FROM 'transactional')`

const queueDupQuery = `
	SELECT e.source_type, e.source_id, COALESCE(a.name, b.name, e.source_id), COUNT(*),
	       (array_agg(DISTINCT lower(e.contact_email)))[1:5]` + queueDupFrom + `
	GROUP BY 1, 2, 3`

func judgeQueueDup(rows []queueDupRow) []Finding {
	sort.Slice(rows, func(i, j int) bool { return rows[i].SourceID < rows[j].SourceID })
	var out []Finding
	for _, r := range rows {
		if r.Count <= 0 {
			continue
		}
		what := "la séquence"
		if r.SourceType == "broadcast" {
			what = "la campagne"
		}
		out = append(out, Finding{
			SubjectRef:  "file_deja_envoye:" + r.SourceID,
			Severity:    SeverityCritical,
			Title:       fmt.Sprintf("%s %s en file pour « %s » %s déjà reçu ce mail", frInt(r.Count), plural(r.Count, "envoi", "envois"), r.Name, plural(r.Count, "alors que le contact l'a", "alors que les contacts l'ont")),
			Expected:    "aucun envoi en file vers un contact qui a déjà reçu ce mail",
			Observed:    fmt.Sprintf("%s %s en double dans la file", frInt(r.Count), plural(r.Count, "envoi", "envois")),
			Explanation: fmt.Sprintf("Pour %s « %s », la file contient des envois dont le contact a déjà reçu exactement le même mail (même modèle). Ils partiraient en double, ce qui abîme la réputation d'envoi. Cause habituelle : un contact remis à zéro ou réinscrit après un premier passage. Si la remise à zéro est voulue, écartez cet écart.", what, r.Name),
			Evidence: map[string]any{
				"source_type": r.SourceType, "source_id": r.SourceID, "count": r.Count, "sample": stringsToAny(sample(r.Sample, 5)),
				"control_query": "email_queue en attente rapprochée de message_history (contact, modèle, séquence)",
			},
			ProposedAction: &Action{
				Kind: ActionCancelQueue, Label: fmt.Sprintf("Retirer %s %s de la file", frInt(r.Count), plural(r.Count, "envoi", "envois")),
				Impact:     "Supprime ces entrées de la file (journal des décisions : annulé). Les contacts restent inscrits et passeront à l'étape suivante.",
				Reversible: false, Params: map[string]any{"automation_id": r.SourceID, "source_type": r.SourceType, "cause": causeQueueDup},
			},
		})
	}
	return out
}

const causeQueueDup = "queue_already_sent"

// ---------- b. stock annoncé et stock réel ----------

type stockRow struct {
	Served int // stock restant servi (totals.stock_remaining)
	Base   int // recompte indépendant, définition écrite
	// ExcludedInServed : personnes du stock servi qui sont dans une liste d'exclusion.
	ExcludedInServed int
	ByClass          map[string]int
}

// stockBaseQuery : formulation volontairement différente de celle du tableau de bord
// (anti-jointure au lieu de NOT EXISTS) pour que le recoupement soit une vraie preuve.
const stockBaseQuery = `
	WITH sent AS (
	    SELECT DISTINCT contact_email FROM message_history
	    WHERE sent_at IS NOT NULL AND transactional_notification_id IS NULL AND veridian_message_type IS DISTINCT FROM 'transactional'),
	excl AS (
	    SELECT DISTINCT cl.email FROM contact_lists cl JOIN lists l ON l.id = cl.list_id AND l.deleted_at IS NULL AND l.name ILIKE '%exclu%'
	    WHERE cl.deleted_at IS NULL AND cl.status = 'active'),
	pool AS (
	    SELECT DISTINCT cl.email FROM contact_lists cl JOIN lists l ON l.id = cl.list_id AND l.deleted_at IS NULL
	    WHERE cl.deleted_at IS NULL AND cl.status = 'active')
	SELECT COUNT(*) FILTER (WHERE excl.email IS NULL), COUNT(*) FILTER (WHERE excl.email IS NOT NULL)
	FROM pool
	LEFT JOIN sent ON sent.contact_email = pool.email
	LEFT JOIN excl ON excl.email = pool.email
	WHERE sent.contact_email IS NULL`

func (stockInvariant) Check(ctx context.Context, env Env) ([]Finding, error) {
	var out []Finding
	rs, err := env.DB.QueryContext(ctx, queueDupQuery)
	if err != nil {
		return nil, fmt.Errorf("doublons en file: %w", err)
	}
	var dups []queueDupRow
	for rs.Next() {
		var r queueDupRow
		var smp pq.StringArray
		if err := rs.Scan(&r.SourceType, &r.SourceID, &r.Name, &r.Count, &smp); err != nil {
			_ = rs.Close()
			return nil, err
		}
		r.Sample = smp
		dups = append(dups, r)
	}
	if err := rs.Close(); err != nil {
		return nil, err
	}
	out = append(out, judgeQueueDup(dups)...)

	served, err := servedStats(ctx, env)
	if err != nil {
		return nil, err
	}
	var row stockRow
	row.Served = served.Totals.StockRemaining
	rs2, err := env.DB.QueryContext(ctx, stockBaseQuery)
	if err != nil {
		return nil, fmt.Errorf("recompte du stock: %w", err)
	}
	defer rs2.Close()
	var inExcl int
	if rs2.Next() {
		if err := rs2.Scan(&row.Base, &inExcl); err != nil {
			return nil, err
		}
	}
	if err := rs2.Err(); err != nil {
		return nil, err
	}
	row.ExcludedInServed = inExcl
	out = append(out, judgeStock(row)...)
	return out, nil
}

func judgeStock(r stockRow) []Finding {
	if r.Served == r.Base {
		return nil
	}
	gap := r.Served - r.Base
	var why string
	if gap > 0 {
		why = fmt.Sprintf("Le tableau de bord annonce %s personnes à contacter, le recompte indépendant en trouve %s. ", frInt(r.Served), frInt(r.Base))
		explained := r.ExcludedInServed
		if explained > gap {
			explained = gap
		}
		if explained > 0 {
			why += fmt.Sprintf("%s %s de la différence %s des personnes d'une liste d'exclusion (clients à ne pas prospecter) que le tableau de bord compte à tort comme du stock. ",
				frInt(explained), plural(explained, "personne", "personnes"), plural(explained, "est", "sont"))
		}
		if rest := gap - explained; rest > 0 {
			why += fmt.Sprintf("%s %s de la différence ne s'expliquent pas par les listes d'exclusion. ", frInt(rest), plural(rest, "personne", "personnes"))
		}
		why += "Le stock restant se définit comme les personnes actives jamais contactées, hors listes d'exclusion."
	} else {
		why = fmt.Sprintf("Le tableau de bord annonce %s personnes à contacter, le recompte indépendant en trouve %s de plus. Une liste ou un statut est lu différemment par les deux requêtes.", frInt(r.Served), frInt(-gap))
	}
	return []Finding{{
		SubjectRef:  "stock:total",
		Severity:    SeverityWarning,
		Title:       fmt.Sprintf("Stock restant : %s annoncés, %s réels", frInt(r.Served), frInt(r.Base)),
		Expected:    fmt.Sprintf("%s personnes prospectables", frInt(r.Base)),
		Observed:    fmt.Sprintf("%s annoncées", frInt(r.Served)),
		Explanation: why,
		Evidence: map[string]any{
			"served": r.Served, "base": r.Base, "gap": gap, "excluded_in_stock": r.ExcludedInServed, "definition": "stock_restant",
			"control_query": "personnes actives (listes non supprimées) sans mail commercial parti, hors listes dont le nom contient « exclu »",
		},
	}}
}

var _ = domain.ProviderClassGoogle
