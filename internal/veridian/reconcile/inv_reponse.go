package reconcile

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Invariant 2 — Réponse rattachée : chaque réponse humaine reçue dans une boîte est reliée à
// un envoi (références du mail, sinon même contact et même objet). Une réponse « orpheline »
// ne remonte dans aucun fil : l'agent ou l'humain ne la voit pas à côté du mail d'origine et
// le contact n'est pas rattaché à sa séquence. Source : veridian_mail_message (lot L1).

const codeReponse = "reponse_rattachee"

// ReplyLinkGrace : une réponse vient d'arriver, le rattachement est en cours : on ne la juge pas.
const ReplyLinkGrace = time.Hour

type reponseInvariant struct{}

func init() { Register(reponseInvariant{}) }

func (reponseInvariant) Code() string  { return codeReponse }
func (reponseInvariant) Number() int   { return 2 }
func (reponseInvariant) Title() string { return "Chaque réponse humaine est rattachée à un envoi" }

// Normalisation de l'objet : sans « Re: », « RE : », « TR: », « Fwd: », « Rép : » en tête.
const subjectNorm = `lower(btrim(regexp_replace(%s, '^((re|tr|fw|fwd|rép|rep)[[:space:]]*:[[:space:]]*)+', '', 'i')))`

func subjectExpr(col string) string { return fmt.Sprintf(subjectNorm, col) }

// sentRefExpr : l'id message_history d'une copie d'envoi. La copie capturée à l'envoi porte sent_ref ;
// celle de la reprise d'historique ne porte que link.sent_message_id.
func sentRefExpr(alias string) string {
	return fmt.Sprintf(`NULLIF(COALESCE(%[1]s.sent_ref, %[1]s.link->>'sent_message_id', ''), '')`, alias)
}

// orphanSelect : réponses humaines sans envoi relié, avec l'envoi probable (même contact, même objet).
// Un envoi probable n'est retenu que s'il nomme son message_history : sinon la réparation ne
// saurait pas quoi poser, et la réponse n'est pas « rattachable ».
// Une copie dédoublonnée (duplicate_of, V68) n'est jamais jugée : seul l'original est visible.
var orphanSelect = `
	SELECT m.id, m.contact_email, m.subject, m.occurred_at,
	       (SELECT s.id FROM veridian_mail_message s
	        WHERE s.direction = 'out' AND s.contact_email = m.contact_email AND s.occurred_at <= m.occurred_at
	          AND ` + sentRefExpr("s") + ` IS NOT NULL
	          AND ` + subjectExpr("s.subject") + ` = ` + subjectExpr("m.subject") + `
	        ORDER BY s.occurred_at DESC LIMIT 1) AS candidate
	FROM veridian_mail_message m
	WHERE m.direction = 'in' AND m.kind = 'reply_human'
	  AND m.duplicate_of IS NULL
	  AND COALESCE(m.link->>'sent_message_id', '') = '' AND m.occurred_at < $1`

type orphanRow struct {
	ID        string
	Contact   string
	Subject   string
	At        time.Time
	Candidate string
}

func (reponseInvariant) Check(ctx context.Context, env Env) ([]Finding, error) {
	if err := requireTable(ctx, env, "veridian_mail_message"); err != nil {
		return nil, err
	}
	rs, err := env.DB.QueryContext(ctx, orphanSelect+` ORDER BY m.occurred_at DESC LIMIT 2000`, env.now().Add(-ReplyLinkGrace))
	if err != nil {
		return nil, fmt.Errorf("réponses orphelines: %w", err)
	}
	defer rs.Close()
	var rows []orphanRow
	for rs.Next() {
		var r orphanRow
		var cand *string
		if err := rs.Scan(&r.ID, &r.Contact, &r.Subject, &r.At, &cand); err != nil {
			return nil, err
		}
		if cand != nil {
			r.Candidate = *cand
		}
		rows = append(rows, r)
	}
	return judgeOrphans(rows), rs.Err()
}

// requireTable : un contrôle qui lit une table d'un autre lot rend « non abouti » (jamais « ok ») si elle manque.
func requireTable(ctx context.Context, env Env, table string) error {
	n, err := queryInt(ctx, env.DB, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1`, table)
	if err != nil {
		return fmt.Errorf("vérification de la table %s: %w", table, err)
	}
	if n == 0 {
		return &ErrNotApplicable{Reason: "table " + table + " absente : la boîte unifiée (lot L1) n'est pas migrée sur ce workspace"}
	}
	return nil
}

func judgeOrphans(rows []orphanRow) []Finding {
	if len(rows) == 0 {
		return nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].At.After(rows[j].At) })
	fixable := 0
	var sampleOut, fixableOut []any
	for _, r := range rows {
		if r.Candidate != "" {
			fixable++
			// fixable_sample : ce que la réparation rattacherait, pour en juger avant d'appliquer.
			if len(fixableOut) < 20 {
				fixableOut = append(fixableOut, map[string]any{"message": r.ID, "contact": r.Contact, "subject": r.Subject,
					"received_at": r.At.UTC().Format(time.RFC3339), "probable_sent": r.Candidate})
			}
		}
		if len(sampleOut) < 8 {
			e := map[string]any{"message": r.ID, "contact": r.Contact, "subject": r.Subject, "received_at": r.At.UTC().Format(time.RFC3339)}
			if r.Candidate != "" {
				e["probable_sent"] = r.Candidate
			}
			sampleOut = append(sampleOut, e)
		}
	}
	n := len(rows)
	why := fmt.Sprintf("%s %s humaine%s %s reliée%s à aucun envoi : le mail ne cite pas notre message (pas de « In-Reply-To » ni de références utilisables) et aucun envoi du même contact n'a le même objet. %s dans aucun fil.",
		frInt(n), plural(n, "réponse", "réponses"), pluralS(n), plural(n, "n'est", "ne sont"), pluralS(n), plural(n, "Elle n'apparaît", "Elles n'apparaissent"))
	if fixable > 0 {
		why = fmt.Sprintf("%s %s humaine%s %s reliée%s à aucun envoi par les références du mail, mais %s %s un envoi du même contact avec le même objet (envoi probable) : le rattachement peut se faire maintenant.",
			frInt(n), plural(n, "réponse", "réponses"), pluralS(n), plural(n, "n'est", "ne sont"), pluralS(n), frInt(fixable), plural(fixable, "a", "ont"))
	}
	f := Finding{
		SubjectRef:  "orphelines:reply_human",
		Severity:    SeverityWarning,
		Title:       fmt.Sprintf("%s %s humaine%s sans envoi rattaché", frInt(n), plural(n, "réponse", "réponses"), pluralS(n)),
		Expected:    "chaque réponse humaine reliée à un envoi",
		Observed:    fmt.Sprintf("%s orpheline%s dont %s rattachable%s", frInt(n), pluralS(n), frInt(fixable), pluralS(fixable)),
		Explanation: why,
		Evidence:    map[string]any{"count": n, "fixable": fixable, "sample": sampleOut, "definition": "reponses_orphelines"},
	}
	if len(fixableOut) > 0 {
		f.Evidence["fixable_sample"] = fixableOut
	}
	if fixable > 0 {
		f.ProposedAction = &Action{
			Kind: ActionLinkReplies, Label: fmt.Sprintf("Rattacher %s %s à l'envoi probable", frInt(fixable), plural(fixable, "réponse", "réponses")),
			Impact:     "Pose le lien vers l'envoi (même contact, même objet) et place la réponse dans le fil de cet envoi. Aucun mail n'est envoyé ni modifié.",
			Reversible: true, Params: map[string]any{},
		}
	}
	return []Finding{f}
}
