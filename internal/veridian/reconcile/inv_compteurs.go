package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/lib/pq"
)

// Invariant 4 — Compteurs identiques partout : le tableau de bord, la fiche séquence,
// l'API et la base donnent les mêmes nombres. Une seule définition par grandeur
// (definitions.go). Quatre contrôles :
//
//	a. compteurs stockés d'une séquence (automations.stats) contre le décompte de ses inscriptions ;
//	b. « envoyés à l'étape » servi par le tableau de bord contre les mails réellement partis ;
//	c. sorties pour réponse contre réponses humaines enregistrées ; réponses par étape contre réponses de la séquence ;
//	d. raisons de sortie que le tableau de bord range à tort dans « autre ».
//
// Cas fondateurs du rapport UX du 27/10 : 16 réponses contre 17 pour la même séquence ;
// inscrits 1 108 contre 1 127 ; « J0 envoyés » 2 947 servi contre 2 891 en base.

const codeCompteurs = "compteurs"

type compteursInvariant struct{}

func init() { Register(compteursInvariant{}) }

func (compteursInvariant) Code() string  { return codeCompteurs }
func (compteursInvariant) Number() int   { return 4 }
func (compteursInvariant) Title() string { return "Les compteurs sont identiques partout" }

// ---------- a. compteurs stockés ----------

type counters struct{ Enrolled, Completed, Exited, Failed int }

type storedStatsRow struct {
	AutomationID string
	Name         string
	Stored       counters
	Real         counters
}

const storedStatsQuery = `
	SELECT a.id, a.name,
	       COALESCE((a.stats->>'enrolled')::int, 0), COALESCE((a.stats->>'completed')::int, 0),
	       COALESCE((a.stats->>'exited')::int, 0), COALESCE((a.stats->>'failed')::int, 0),
	       COUNT(ca.id),
	       COUNT(ca.id) FILTER (WHERE ca.status = 'completed'),
	       COUNT(ca.id) FILTER (WHERE ca.status = 'exited'),
	       COUNT(ca.id) FILTER (WHERE ca.status = 'failed')
	FROM automations a
	LEFT JOIN contact_automations ca ON ca.automation_id = a.id
	WHERE a.deleted_at IS NULL
	GROUP BY a.id, a.name`

func readStoredStats(ctx context.Context, env Env) ([]storedStatsRow, error) {
	rs, err := env.DB.QueryContext(ctx, storedStatsQuery)
	if err != nil {
		return nil, fmt.Errorf("compteurs stockés: %w", err)
	}
	defer rs.Close()
	var out []storedStatsRow
	for rs.Next() {
		var r storedStatsRow
		if err := rs.Scan(&r.AutomationID, &r.Name,
			&r.Stored.Enrolled, &r.Stored.Completed, &r.Stored.Exited, &r.Stored.Failed,
			&r.Real.Enrolled, &r.Real.Completed, &r.Real.Exited, &r.Real.Failed); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// confirmedStoredStats : les compteurs bougent pendant qu'on les lit. Un écart n'est
// retenu que s'il est identique à deux lectures espacées de env.Pause.
func confirmedStoredStats(ctx context.Context, env Env) ([]storedStatsRow, error) {
	first, err := readStoredStats(ctx, env)
	if err != nil {
		return nil, err
	}
	drift := func(r storedStatsRow) bool { return r.Stored != r.Real }
	any := false
	for _, r := range first {
		if drift(r) {
			any = true
		}
	}
	if !any {
		return first, nil
	}
	if env.Pause > 0 {
		select {
		case <-time.After(env.Pause):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	second, err := readStoredStats(ctx, env)
	if err != nil {
		return nil, err
	}
	prev := map[string]storedStatsRow{}
	for _, r := range first {
		prev[r.AutomationID] = r
	}
	var out []storedStatsRow
	for _, r := range second {
		if p, ok := prev[r.AutomationID]; ok && drift(r) && p.Stored == r.Stored && p.Real == r.Real {
			out = append(out, r)
		}
	}
	return out, nil
}

func judgeStoredStats(rows []storedStatsRow) []Finding {
	sort.Slice(rows, func(i, j int) bool { return rows[i].AutomationID < rows[j].AutomationID })
	var out []Finding
	for _, r := range rows {
		if r.Stored == r.Real {
			continue
		}
		var diffs []string
		add := func(label string, stored, real int) {
			if stored != real {
				diffs = append(diffs, fmt.Sprintf("%s : fiche %s, base %s", label, frInt(stored), frInt(real)))
			}
		}
		add("inscrits", r.Stored.Enrolled, r.Real.Enrolled)
		add("terminés", r.Stored.Completed, r.Real.Completed)
		add("sortis", r.Stored.Exited, r.Real.Exited)
		add("échoués", r.Stored.Failed, r.Real.Failed)
		out = append(out, Finding{
			SubjectRef: "stats_stockees:" + r.AutomationID,
			Severity:   SeverityWarning,
			Title:      fmt.Sprintf("Les compteurs de la fiche « %s » ne correspondent pas aux inscriptions", r.Name),
			Expected:   fmt.Sprintf("inscrits %s, terminés %s, sortis %s, échoués %s", frInt(r.Real.Enrolled), frInt(r.Real.Completed), frInt(r.Real.Exited), frInt(r.Real.Failed)),
			Observed:   fmt.Sprintf("inscrits %s, terminés %s, sortis %s, échoués %s", frInt(r.Stored.Enrolled), frInt(r.Stored.Completed), frInt(r.Stored.Exited), frInt(r.Stored.Failed)),
			Explanation: "La fiche séquence lit des compteurs que le moteur incrémente un par un à chaque événement (inscription, sortie, fin). " +
				"Un événement perdu (redémarrage pendant l'écriture, sortie faite à la main avant le 04/10) les décale durablement, alors que le tableau de bord recompte les inscriptions. Écarts : " + strings.Join(diffs, " ; ") + ".",
			Evidence: map[string]any{
				"automation_id": r.AutomationID, "stored": countersMap(r.Stored), "real": countersMap(r.Real),
				"definition": "compteurs_stockes", "control_query": "contact_automations groupées par statut",
			},
			ProposedAction: &Action{
				Kind: ActionRecountStats, Label: "Recalculer les compteurs de la fiche",
				Impact:     "Remplace automations.stats par le décompte réel des inscriptions. Aucun contact ni mail n'est touché.",
				Reversible: true, Params: map[string]any{"automation_id": r.AutomationID},
			},
		})
	}
	return out
}

func countersMap(c counters) map[string]any {
	return map[string]any{"enrolled": c.Enrolled, "completed": c.Completed, "exited": c.Exited, "failed": c.Failed}
}

// ---------- b. envoyés par étape ----------

type exitDiag struct {
	Status string
	Reason string
	Count  int
}

type stageRow struct {
	AutomationID string
	Name         string
	Stage        int
	Label        string
	Served       int // « envoyés » servi par le tableau de bord
	Base         int // contacts distincts à qui un mail de l'étape ou d'une suivante est réellement parti
	Diag         []exitDiag
}

const stageBaseQuery = `
	SELECT COUNT(DISTINCT contact_email) FROM message_history
	WHERE automation_id = $1 AND template_id = ANY($2) AND sent_at IS NOT NULL AND failed_at IS NULL
	  AND transactional_notification_id IS NULL AND veridian_message_type IS DISTINCT FROM 'transactional'`

// stageDiagQuery : contacts que le tableau de bord croit servis alors qu'aucun mail de
// l'étape n'est parti, par statut et raison de sortie.
const stageDiagQuery = `
	SELECT ca.status, COALESCE(ca.exit_reason, ''), COUNT(*)
	FROM contact_automations ca
	WHERE ca.automation_id = $1
	  AND EXISTS (SELECT 1 FROM automation_node_executions e WHERE e.contact_automation_id = ca.id
	              AND e.node_type = 'email' AND e.action = 'completed' AND e.node_id = ANY($2))
	  AND NOT EXISTS (SELECT 1 FROM message_history m WHERE m.automation_id = ca.automation_id AND m.contact_email = ca.contact_email
	              AND m.template_id = ANY($3) AND m.sent_at IS NOT NULL AND m.failed_at IS NULL)
	GROUP BY 1, 2`

// servedStats reconstruit le chiffre SERVI : même code que prospection.stats.
func servedStats(ctx context.Context, env Env) (*domain.VeridianProspectionStats, error) {
	if env.Stats == nil {
		return nil, &ErrNotApplicable{Reason: "source des compteurs servis absente"}
	}
	raw, err := env.Stats.GetProspectionRaw(ctx, env.WorkspaceID, time.Time{}, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("lecture des compteurs servis: %w", err)
	}
	return domain.VeridianBuildProspectionStats(raw, time.Time{}, time.Time{}, env.now()), nil
}

// queryInt lit un entier unique.
func queryInt(ctx context.Context, db Querier, q string, args ...any) (int, error) {
	rs, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rs.Close()
	n := 0
	if rs.Next() {
		if err := rs.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rs.Err()
}

type stageKey struct {
	automation string
	stage      int
}

type stageScope struct {
	row       stageRow
	templates []string // modèles de l'étape et des suivantes
	nodes     []string // nœuds de l'étape et des suivantes
}

func readStages(ctx context.Context, env Env, served *domain.VeridianProspectionStats) (map[stageKey]stageScope, error) {
	out := map[stageKey]stageScope{}
	for _, seq := range served.Sequences {
		if seq.Enrolled == 0 {
			continue
		}
		for i, st := range seq.Stages {
			var tpls, nodes []string
			for _, later := range seq.Stages[i:] {
				tpls = append(tpls, later.TemplateIDs...)
				nodes = append(nodes, later.NodeIDs...)
			}
			if len(tpls) == 0 {
				continue
			}
			row := stageRow{AutomationID: seq.AutomationID, Name: seq.Name, Stage: st.Stage, Label: st.Label, Served: st.Sent}
			base, err := queryInt(ctx, env.DB, stageBaseQuery, seq.AutomationID, pq.Array(tpls))
			if err != nil {
				return nil, fmt.Errorf("mails partis de l'étape %s de %s: %w", st.Label, seq.AutomationID, err)
			}
			row.Base = base
			out[stageKey{seq.AutomationID, st.Stage}] = stageScope{row: row, templates: tpls, nodes: nodes}
		}
	}
	return out, nil
}

// checkStages compare « envoyés à l'étape » servi et réel. Les envois continuent pendant
// la lecture : un écart n'est retenu que si la différence est la même à deux lectures.
func checkStages(ctx context.Context, env Env, served *domain.VeridianProspectionStats) ([]Finding, error) {
	first, err := readStages(ctx, env, served)
	if err != nil {
		return nil, err
	}
	mismatch := false
	for _, sc := range first {
		if sc.row.Served != sc.row.Base {
			mismatch = true
		}
	}
	if !mismatch {
		return nil, nil
	}
	if env.Pause > 0 {
		select {
		case <-time.After(env.Pause):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	served2, err := servedStats(ctx, env)
	if err != nil {
		return nil, err
	}
	second, err := readStages(ctx, env, served2)
	if err != nil {
		return nil, err
	}
	var rows []stageRow
	for k, sc := range second {
		p, ok := first[k]
		if !ok || sc.row.Served == sc.row.Base {
			continue
		}
		if p.row.Served-p.row.Base != sc.row.Served-sc.row.Base {
			continue
		}
		row := sc.row
		if row.Served > row.Base {
			diag, err := readStageDiag(ctx, env, row.AutomationID, sc.nodes, sc.templates)
			if err != nil {
				return nil, err
			}
			row.Diag = diag
		}
		rows = append(rows, row)
	}
	return judgeStages(rows), nil
}

func readStageDiag(ctx context.Context, env Env, automationID string, nodes, templates []string) ([]exitDiag, error) {
	rs, err := env.DB.QueryContext(ctx, stageDiagQuery, automationID, pq.Array(nodes), pq.Array(templates))
	if err != nil {
		return nil, fmt.Errorf("diagnostic de l'étape: %w", err)
	}
	defer rs.Close()
	var out []exitDiag
	for rs.Next() {
		var d exitDiag
		if err := rs.Scan(&d.Status, &d.Reason, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rs.Err()
}

// describeDiag explique en français pourquoi des contacts sont comptés « envoyés » sans mail parti.
func describeDiag(diag []exitDiag) string {
	buckets := map[string]int{}
	for _, d := range diag {
		label := "dans un autre état"
		switch {
		case d.Status == string(domain.ContactAutomationStatusExited):
			switch domain.VeridianClassifyExitReason(d.Reason) {
			case domain.VeridianExitClassUnsubscribed:
				label = "désinscrits entre la mise en file et l'envoi"
			case domain.VeridianExitClassReplied:
				label = "sortis pour réponse entre la mise en file et l'envoi"
			case domain.VeridianExitClassRejected:
				label = "sortis pour rebond entre la mise en file et l'envoi"
			case domain.VeridianExitClassExcluded:
				label = "exclus entre la mise en file et l'envoi"
			default:
				label = "sortis pour une autre raison entre la mise en file et l'envoi"
			}
		case d.Status == string(domain.ContactAutomationStatusSending):
			label = "en cours d'envoi"
		}
		buckets[label] += d.Count
	}
	labels := make([]string, 0, len(buckets))
	for l := range buckets {
		labels = append(labels, l)
	}
	sort.Slice(labels, func(i, j int) bool {
		if buckets[labels[i]] != buckets[labels[j]] {
			return buckets[labels[i]] > buckets[labels[j]]
		}
		return labels[i] < labels[j]
	})
	parts := make([]string, 0, len(labels))
	for _, l := range labels {
		parts = append(parts, fmt.Sprintf("%s %s", frInt(buckets[l]), l))
	}
	return strings.Join(parts, ", ")
}

func judgeStages(rows []stageRow) []Finding {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].AutomationID != rows[j].AutomationID {
			return rows[i].AutomationID < rows[j].AutomationID
		}
		return rows[i].Stage < rows[j].Stage
	})
	var out []Finding
	for _, r := range rows {
		if r.Served == r.Base {
			continue
		}
		gap := r.Served - r.Base
		var why string
		if gap > 0 {
			cause := describeDiag(r.Diag)
			if cause == "" {
				cause = "cause non identifiée par le diagnostic"
			}
			why = fmt.Sprintf("Le tableau de bord compte %s %s « envoyé%s à %s » alors qu'aucun mail de cette étape n'est parti pour eux : %s. Le moteur marque un mail « terminé » dès qu'il est mis en file ; si le contact sort avant l'envoi, le mail ne part jamais. La grandeur « envoyés à une étape » se définit sur le mail réellement parti (message_history).",
				frInt(gap), plural(gap, "contact", "contacts"), pluralS(gap), r.Label, cause)
		} else {
			why = fmt.Sprintf("Le tableau de bord ne compte pas %s %s à qui un mail de l'étape %s est pourtant parti : leur inscription ne porte pas la trace de l'étape (nœud d'envoi non marqué terminé).", frInt(-gap), plural(-gap, "contact", "contacts"), r.Label)
		}
		out = append(out, Finding{
			SubjectRef:  fmt.Sprintf("etape_envoyes:%s:%d", r.AutomationID, r.Stage),
			Severity:    SeverityWarning,
			Title:       fmt.Sprintf("« Envoyés à %s » de « %s » : %s servis, %s réellement partis", r.Label, r.Name, frInt(r.Served), frInt(r.Base)),
			Expected:    fmt.Sprintf("%s contacts à qui un mail de l'étape %s ou d'une suivante est parti", frInt(r.Base), r.Label),
			Observed:    fmt.Sprintf("%s servis par le tableau de bord", frInt(r.Served)),
			Explanation: why,
			Evidence: map[string]any{
				"automation_id": r.AutomationID, "stage": r.Stage, "label": r.Label, "served": r.Served, "base": r.Base, "gap": gap,
				"exits_without_mail": diagToAny(r.Diag), "definition": "envoyes_etape",
				"control_query": "contacts distincts de message_history (automation_id, modèles de l'étape et suivantes, sent_at renseigné, failed_at vide)",
			},
		})
	}
	return out
}

func diagToAny(d []exitDiag) []any {
	out := make([]any, 0, len(d))
	for _, x := range d {
		out = append(out, map[string]any{"status": x.Status, "exit_reason": x.Reason, "count": x.Count})
	}
	return out
}

// ---------- c. réponses ----------

// replyRow : par séquence, ce que disent les différentes surfaces des réponses.
type replyRow struct {
	AutomationID      string
	Name              string
	RepliesServed     int // réponses humaines de la séquence, servies
	FirstStageServed  int // réponses humaines à la première étape, servies
	NoMailRepliers    int // contacts avec réponse humaine et aucun mail de la séquence terminé
	ExitedReplied     int // sorties pour réponse (classe replied)
	ExitedNoHumanRow  int // dont sans aucune réponse humaine enregistrée
	ExitedNoHumanSamp []string
	ManualOnly        bool // les sorties sans réponse sont toutes manuelles (replied_manual_*)
}

const noMailRepliersQuery = `
	SELECT ca.automation_id, COUNT(DISTINCT r.contact_email)
	FROM contact_automations ca
	JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
	JOIN veridian_contact_reply r ON r.contact_email = lower(ca.contact_email) AND r.reply_type = 'human'
	WHERE NOT EXISTS (SELECT 1 FROM automation_node_executions e WHERE e.contact_automation_id = ca.id
	                  AND e.node_type = 'email' AND e.action = 'completed')
	GROUP BY 1`

// exitedNoReply : sorties pour réponse sans réponse humaine enregistrée (le tri par
// famille de raison se fait en Go avec le classeur du tableau de bord).
const exitedNoReplyQuery = `
	SELECT ca.automation_id, COALESCE(ca.exit_reason, ''), COUNT(*), (array_agg(lower(ca.contact_email) ORDER BY ca.contact_email))[1:3]
	FROM contact_automations ca
	JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
	WHERE ca.status = 'exited' AND NOT EXISTS (
	      SELECT 1 FROM veridian_contact_reply r WHERE r.contact_email = lower(ca.contact_email) AND r.reply_type = 'human')
	GROUP BY 1, 2`

func readReplyRows(ctx context.Context, env Env, served *domain.VeridianProspectionStats) ([]replyRow, error) {
	byID := map[string]*replyRow{}
	var order []string
	for _, seq := range served.Sequences {
		r := &replyRow{AutomationID: seq.AutomationID, Name: seq.Name, RepliesServed: seq.RepliesHuman, ExitedReplied: seq.Exits.Replied, ManualOnly: true}
		if len(seq.Stages) > 0 {
			r.FirstStageServed = seq.Stages[0].RepliesHuman
		}
		byID[seq.AutomationID] = r
		order = append(order, seq.AutomationID)
	}
	rs, err := env.DB.QueryContext(ctx, noMailRepliersQuery)
	if err != nil {
		return nil, fmt.Errorf("réponses sans mail: %w", err)
	}
	for rs.Next() {
		var id string
		var n int
		if err := rs.Scan(&id, &n); err != nil {
			_ = rs.Close()
			return nil, err
		}
		if r := byID[id]; r != nil {
			r.NoMailRepliers = n
		}
	}
	if err := rs.Close(); err != nil {
		return nil, err
	}
	rs2, err := env.DB.QueryContext(ctx, exitedNoReplyQuery)
	if err != nil {
		return nil, fmt.Errorf("sorties pour réponse sans réponse: %w", err)
	}
	defer rs2.Close()
	for rs2.Next() {
		var id, reason string
		var n int
		var smp pq.StringArray
		if err := rs2.Scan(&id, &reason, &n, &smp); err != nil {
			return nil, err
		}
		r := byID[id]
		if r == nil || domain.VeridianClassifyExitReason(reason) != domain.VeridianExitClassReplied {
			continue
		}
		r.ExitedNoHumanRow += n
		r.ExitedNoHumanSamp = append(r.ExitedNoHumanSamp, smp...)
		if !strings.HasPrefix(strings.ToLower(reason), "replied_manual") {
			r.ManualOnly = false
		}
	}
	if err := rs2.Err(); err != nil {
		return nil, err
	}
	out := make([]replyRow, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

func judgeReplies(rows []replyRow) []Finding {
	sort.Slice(rows, func(i, j int) bool { return rows[i].AutomationID < rows[j].AutomationID })
	var out []Finding
	for _, r := range rows {
		// (1) Réponses par étape + réponses sans mail = réponses de la séquence.
		if want := r.FirstStageServed + r.NoMailRepliers; want != r.RepliesServed {
			out = append(out, Finding{
				SubjectRef: "reponses_etapes:" + r.AutomationID,
				Severity:   SeverityWarning,
				Title:      fmt.Sprintf("« %s » : %s réponses humaines pour la séquence, %s à la première étape", r.Name, frInt(r.RepliesServed), frInt(r.FirstStageServed)),
				Expected:   fmt.Sprintf("%s réponses à la séquence = %s à la première étape + %s sans mail reçu", frInt(r.RepliesServed), frInt(r.FirstStageServed), frInt(r.NoMailRepliers)),
				Observed:   fmt.Sprintf("%s + %s = %s", frInt(r.FirstStageServed), frInt(r.NoMailRepliers), frInt(want)),
				Explanation: "Les réponses par étape ne comptent que les contacts à qui un mail de la séquence a été terminé ; les réponses de la séquence comptent tous ses contacts. " +
					"La différence doit être exactement le nombre de contacts qui ont répondu sans qu'aucun mail de la séquence soit terminé. Ici elle ne l'est pas : une des deux requêtes de comptage a dérivé.",
				Evidence: map[string]any{
					"automation_id": r.AutomationID, "replies_sequence": r.RepliesServed, "replies_first_stage": r.FirstStageServed,
					"repliers_without_mail": r.NoMailRepliers, "definition": "reponses_humaines",
				},
			})
		}
		// (2) Sorties pour réponse sans réponse humaine : le cas 16 contre 17.
		if r.ExitedNoHumanRow > 0 {
			sev := SeverityWarning
			why := fmt.Sprintf("%s %s sorti%s de « %s » pour réponse, mais aucune réponse humaine n'est enregistrée pour %s. Les « sorties pour réponse » (%s) dépassent donc les « réponses humaines » (%s) : c'est l'écart 16 contre 17 du rapport du 27/10. Soit la réponse était un message automatique ou un défi anti-spam, soit la ligne de réponse manque.",
				frInt(r.ExitedNoHumanRow), plural(r.ExitedNoHumanRow, "contact est", "contacts sont"), pluralS(r.ExitedNoHumanRow), r.Name,
				plural(r.ExitedNoHumanRow, "lui", "eux"), frInt(r.ExitedReplied), frInt(r.RepliesServed))
			if r.ManualOnly {
				sev = SeverityInfo
				why = fmt.Sprintf("%s %s sorti%s de « %s » à la main avec une raison « a répondu » (sortie manuelle), sans réponse enregistrée dans la boîte : la réponse est arrivée hors des boîtes relevées. Les « sorties pour réponse » dépassent donc les « réponses humaines ».",
					frInt(r.ExitedNoHumanRow), plural(r.ExitedNoHumanRow, "contact est", "contacts sont"), pluralS(r.ExitedNoHumanRow), r.Name)
			}
			out = append(out, Finding{
				SubjectRef:  "sorties_reponse:" + r.AutomationID,
				Severity:    sev,
				Title:       fmt.Sprintf("« %s » : %s %s pour réponse sans réponse humaine enregistrée", r.Name, frInt(r.ExitedNoHumanRow), plural(r.ExitedNoHumanRow, "sortie", "sorties")),
				Expected:    "chaque sortie pour réponse a une réponse humaine enregistrée",
				Observed:    fmt.Sprintf("%s sans réponse enregistrée", frInt(r.ExitedNoHumanRow)),
				Explanation: why,
				Evidence: map[string]any{
					"automation_id": r.AutomationID, "exited_replied_without_human_reply": r.ExitedNoHumanRow,
					"sample": stringsToAny(sample(r.ExitedNoHumanSamp, 5)), "definition": "sorties_reponse",
				},
			})
		}
	}
	return out
}

// ---------- d. raisons de sortie mal rangées ----------

type exitReasonRow struct {
	AutomationID string
	Reason       string
	Count        int
}

const exitReasonsQuery = `
	SELECT ca.automation_id, COALESCE(ca.exit_reason, ''), COUNT(*)
	FROM contact_automations ca
	JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
	WHERE ca.status = 'exited'
	GROUP BY 1, 2`

func readExitReasons(ctx context.Context, env Env) ([]exitReasonRow, error) {
	rs, err := env.DB.QueryContext(ctx, exitReasonsQuery)
	if err != nil {
		return nil, fmt.Errorf("raisons de sortie: %w", err)
	}
	defer rs.Close()
	var out []exitReasonRow
	for rs.Next() {
		var r exitReasonRow
		if err := rs.Scan(&r.AutomationID, &r.Reason, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// misfiledFamily : la famille que le texte d'une raison désigne sans ambiguïté, même
// si le classeur l'a rangée dans « autre ».
func misfiledFamily(reason string) string {
	r := strings.ToLower(reason)
	switch {
	case strings.Contains(r, "repl"):
		return domain.VeridianExitClassReplied
	case strings.Contains(r, "unsubscri"):
		return domain.VeridianExitClassUnsubscribed
	case strings.Contains(r, "bounce"):
		return domain.VeridianExitClassRejected
	}
	return ""
}

func judgeExitClasses(rows []exitReasonRow) []Finding {
	type agg struct {
		count  int
		seqs   map[string]bool
		family string
	}
	byReason := map[string]*agg{}
	for _, r := range rows {
		if domain.VeridianClassifyExitReason(r.Reason) != domain.VeridianExitClassOther {
			continue
		}
		fam := misfiledFamily(r.Reason)
		if fam == "" {
			continue
		}
		g := byReason[r.Reason]
		if g == nil {
			g = &agg{seqs: map[string]bool{}, family: fam}
			byReason[r.Reason] = g
		}
		g.count += r.Count
		g.seqs[r.AutomationID] = true
	}
	reasons := make([]string, 0, len(byReason))
	for k := range byReason {
		reasons = append(reasons, k)
	}
	sort.Strings(reasons)
	labels := map[string]string{
		domain.VeridianExitClassReplied: "a répondu", domain.VeridianExitClassUnsubscribed: "désinscrit", domain.VeridianExitClassRejected: "rebond",
	}
	var out []Finding
	for _, reason := range reasons {
		g := byReason[reason]
		out = append(out, Finding{
			SubjectRef: "sortie_mal_rangee:" + reason,
			Severity:   SeverityWarning,
			Title:      fmt.Sprintf("Raison de sortie « %s » rangée dans « autre » (%s %s)", reason, frInt(g.count), plural(g.count, "sortie", "sorties")),
			Expected:   fmt.Sprintf("rangée dans « %s »", labels[g.family]),
			Observed:   "rangée dans « autre »",
			Explanation: fmt.Sprintf("Le moteur pose la raison « %s » à la sortie d'un contact, mais le classeur du tableau de bord ne la reconnaît pas : ces %s sont absentes de « sorties : %s » et gonflent « autre ». C'est une cause directe de chiffres qui ne se recoupent pas. Correctif : ajouter la raison au classeur (VeridianClassifyExitReason).",
				reason, frInt(g.count), labels[g.family]),
			Evidence: map[string]any{"reason": reason, "count": g.count, "family": g.family, "definition": "sorties_reponse"},
		})
	}
	return out
}
func (compteursInvariant) Check(ctx context.Context, env Env) ([]Finding, error) {
	var out []Finding
	stored, err := confirmedStoredStats(ctx, env)
	if err != nil {
		return nil, err
	}
	out = append(out, judgeStoredStats(stored)...)

	served, err := servedStats(ctx, env)
	if err != nil {
		return nil, err
	}
	out = append(out, judgeServedPartition(served.Sequences)...)
	stageFindings, err := checkStages(ctx, env, served)
	if err != nil {
		return nil, err
	}
	out = append(out, stageFindings...)

	replyRows, err := readReplyRows(ctx, env, served)
	if err != nil {
		return nil, err
	}
	out = append(out, judgeReplies(replyRows)...)

	exitRows, err := readExitReasons(ctx, env)
	if err != nil {
		return nil, err
	}
	out = append(out, judgeExitClasses(exitRows)...)
	return out, nil
}

// ---------- e. partition de la première étape ----------

// judgeServedPartition : pour une séquence sans fin ni échec, chaque inscrit est dans
// exactement UN des quatre compartiments de la première étape : mail parti, mail en
// file, mail attendu, sorti avant tout mail. « Sortis après » est un SOUS-ENSEMBLE de
// « partis » : l'ajouter aux autres compte deux fois ces contacts (c'est la lecture qui a
// donné 1 127 pour 1 108 inscrits dans le rapport du 27/10).
func judgeServedPartition(seqs []domain.VeridianSequenceStats) []Finding {
	var out []Finding
	for _, s := range seqs {
		if len(s.Stages) == 0 || s.Completed != 0 || s.Failed != 0 || s.Enrolled == 0 {
			continue
		}
		st := s.Stages[0]
		sum := st.Sent + st.Queued + st.Waiting + s.Exits.BeforeFirstMail
		if sum == s.Enrolled {
			continue
		}
		out = append(out, Finding{
			SubjectRef:  "partition_etape1:" + s.AutomationID,
			Severity:    SeverityWarning,
			Title:       fmt.Sprintf("« %s » : %s inscrits, mais les compartiments de J0 en totalisent %s", s.Name, frInt(s.Enrolled), frInt(sum)),
			Expected:    fmt.Sprintf("partis + en file + attendus + sortis avant tout mail = %s inscrits", frInt(s.Enrolled)),
			Observed:    fmt.Sprintf("%s + %s + %s + %s = %s", frInt(st.Sent), frInt(st.Queued), frInt(st.Waiting), frInt(s.Exits.BeforeFirstMail), frInt(sum)),
			Explanation: "Chaque inscrit doit tomber dans un seul compartiment de la première étape. Quand la somme dépasse les inscrits, un contact est compté deux fois (typiquement un sorti après envoi ajouté en plus des partis, dont il fait déjà partie) ; quand elle est inférieure, un contact n'est dans aucun compartiment.",
			Evidence: map[string]any{
				"automation_id": s.AutomationID, "enrolled": s.Enrolled, "sent": st.Sent, "queued": st.Queued,
				"waiting": st.Waiting, "before_first_mail": s.Exits.BeforeFirstMail, "sum": sum, "definition": "inscrits",
			},
		})
	}
	return out
}
