package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Invariant 3 — Statut cohérent : un contact qui a répondu ou rebondi dur n'est plus
// inscrit à une séquence active ; un désinscrit ne reçoit rien ; un contact exclu n'est
// jamais en file. La séquence garde ses propres règles (une séquence qui contient un
// nœud « branche réponse » traite la réponse elle-même : elle n'est pas jugée).
//
// Les statuts du moteur sont mis à jour par des événements asynchrones (relève IMAP,
// webhooks). Un délai de grâce évite de signaler l'écart d'un instant : on ne juge que
// ce qui dure depuis plus de StatusGrace.

const codeStatut = "statut_coherent"

// StatusGrace : ancienneté minimale d'un changement de statut pour être jugé.
const StatusGrace = 15 * time.Minute

// SentAfterLookback : fenêtre de recherche des envois partis après une réponse ou une désinscription.
const SentAfterLookback = 14 * 24 * time.Hour

// sentAfterMargin : un mail déjà dépilé quand la réponse est détectée peut partir quelques minutes après.
const sentAfterMargin = "10 minutes"

type statutInvariant struct{}

func init() { Register(statutInvariant{}) }

func (statutInvariant) Code() string { return codeStatut }
func (statutInvariant) Number() int  { return 3 }
func (statutInvariant) Title() string {
	return "Statut cohérent (réponse, rebond, désinscription, exclusion)"
}

// Causes. Chaque cause est une façon de violer l'invariant.
const (
	causeReplied      = "replied_enrolled"      // a répondu et reste inscrit
	causeTerminal     = "terminal_enrolled"     // rebond/plainte sur la liste et reste inscrit
	causeUnsub        = "unsubscribed_enrolled" // désinscrit de la liste et reste inscrit
	causeExcluded     = "excluded_enrolled"     // dans une liste d'exclusion et reste inscrit
	causeQueueSuppr   = "queue_suppressed"      // en file malgré un statut de liste non actif
	causeQueueReplied = "queue_replied"         // en file malgré une réponse
	causeQueueExcl    = "queue_excluded"        // en file malgré une liste d'exclusion
	causeSentReply    = "sent_after_reply"      // mail parti après la réponse
	causeSentUnsub    = "sent_after_unsub"      // mail parti après la désinscription
)

const noReplyBranchSQL = `NOT EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(a.nodes, '[]'::jsonb)) n WHERE n->>'type' = 'reply_branch')`

// statutCause : requête de contrôle d'une cause. `from` commence à FROM et finit avant
// GROUP BY ; elle expose les alias t (cible : ca ou e), a (séquence) et le champ `ts`.
// $1 = instant limite (maintenant moins la grâce) ; $2 = identifiant de séquence (ciblage).
type statutCause struct {
	code     string
	idExpr   string // identifiant de séquence
	emailExp string
	tsExpr   string // horodatage de l'événement fautif
	from     string
	// exitReason : raison de sortie posée par l'action « sortir de la séquence ».
	exitReason string
	enrolled   bool // la cible est une inscription (ca) ; sinon une entrée de file (e)
}

var statutCauses = []statutCause{
	{
		code: causeReplied, idExpr: "ca.automation_id", emailExp: "ca.contact_email", tsExpr: "r.replied_at", exitReason: "replied", enrolled: true,
		from: `FROM contact_automations ca
			JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL AND ` + noReplyBranchSQL + `
			JOIN veridian_contact_reply r ON r.contact_email = lower(ca.contact_email) AND r.reply_type = 'human'
			WHERE ca.status IN ('active', 'sending') AND r.replied_at < $1`,
	},
	{
		code: causeTerminal, idExpr: "ca.automation_id", emailExp: "ca.contact_email", tsExpr: "cl.updated_at", exitReason: "bounced", enrolled: true,
		from: `FROM contact_automations ca
			JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
			JOIN contact_lists cl ON cl.list_id = a.list_id AND cl.email = ca.contact_email AND cl.deleted_at IS NULL
			  AND cl.status IN ('bounced', 'complained') AND cl.updated_at < $1
			WHERE ca.status IN ('active', 'sending')`,
	},
	{
		code: causeUnsub, idExpr: "ca.automation_id", emailExp: "ca.contact_email", tsExpr: "cl.updated_at", exitReason: "unsubscribed", enrolled: true,
		from: `FROM contact_automations ca
			JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
			JOIN contact_lists cl ON cl.list_id = a.list_id AND cl.email = ca.contact_email AND cl.deleted_at IS NULL
			  AND cl.status = 'unsubscribed' AND cl.updated_at < $1
			WHERE ca.status IN ('active', 'sending')`,
	},
	{
		code: causeExcluded, idExpr: "ca.automation_id", emailExp: "ca.contact_email", tsExpr: "x.updated_at", exitReason: "excluded_list", enrolled: true,
		from: `FROM contact_automations ca
			JOIN automations a ON a.id = ca.automation_id AND a.deleted_at IS NULL
			JOIN contact_lists x ON x.email = ca.contact_email AND x.deleted_at IS NULL AND x.status = 'active' AND x.updated_at < $1
			JOIN lists xl ON xl.id = x.list_id AND xl.deleted_at IS NULL AND xl.name ILIKE '%exclu%' AND xl.id <> COALESCE(a.list_id, '')
			WHERE ca.status IN ('active', 'sending')`,
	},
	{
		code: causeQueueSuppr, idExpr: "e.source_id", emailExp: "e.contact_email", tsExpr: "cl.updated_at",
		from: `FROM email_queue e
			JOIN automations a ON a.id = e.source_id AND a.deleted_at IS NULL
			JOIN contact_lists cl ON cl.list_id = a.list_id AND cl.email = e.contact_email AND cl.deleted_at IS NULL
			  AND cl.status IN ('bounced', 'complained', 'unsubscribed') AND cl.updated_at < $1
			WHERE e.source_type = 'automation' AND e.status = 'pending'`,
	},
	{
		code: causeQueueReplied, idExpr: "e.source_id", emailExp: "e.contact_email", tsExpr: "r.replied_at",
		from: `FROM email_queue e
			JOIN automations a ON a.id = e.source_id AND a.deleted_at IS NULL AND ` + noReplyBranchSQL + `
			JOIN veridian_contact_reply r ON r.contact_email = lower(e.contact_email) AND r.reply_type = 'human' AND r.replied_at < $1
			WHERE e.source_type = 'automation' AND e.status = 'pending'`,
	},
	{
		code: causeQueueExcl, idExpr: "e.source_id", emailExp: "e.contact_email", tsExpr: "x.updated_at",
		from: `FROM email_queue e
			JOIN automations a ON a.id = e.source_id AND a.deleted_at IS NULL
			JOIN contact_lists x ON x.email = e.contact_email AND x.deleted_at IS NULL AND x.status = 'active' AND x.updated_at < $1
			JOIN lists xl ON xl.id = x.list_id AND xl.deleted_at IS NULL AND xl.name ILIKE '%exclu%' AND xl.id <> COALESCE(a.list_id, '')
			WHERE e.source_type = 'automation' AND e.status = 'pending'`,
	},
}

// Les envois déjà partis ne se « réparent » pas : constat seul. $1 = début de la fenêtre.
var statutSentCauses = []statutCause{
	{
		code: causeSentReply, idExpr: "mh.automation_id", emailExp: "mh.contact_email", tsExpr: "mh.sent_at",
		from: `FROM message_history mh
			JOIN automations a ON a.id = mh.automation_id AND a.deleted_at IS NULL AND ` + noReplyBranchSQL + `
			JOIN veridian_contact_reply r ON r.contact_email = lower(mh.contact_email) AND r.reply_type = 'human'
			WHERE mh.sent_at >= $1 AND mh.failed_at IS NULL AND mh.transactional_notification_id IS NULL
			  AND mh.veridian_message_type IS DISTINCT FROM 'transactional'
			  AND mh.sent_at > r.replied_at + interval '` + sentAfterMargin + `'`,
	},
	{
		code: causeSentUnsub, idExpr: "mh.automation_id", emailExp: "mh.contact_email", tsExpr: "mh.sent_at",
		from: `FROM message_history mh
			JOIN automations a ON a.id = mh.automation_id AND a.deleted_at IS NULL
			JOIN contact_lists cl ON cl.list_id = a.list_id AND cl.email = mh.contact_email AND cl.deleted_at IS NULL AND cl.status = 'unsubscribed'
			WHERE mh.sent_at >= $1 AND mh.failed_at IS NULL AND mh.transactional_notification_id IS NULL
			  AND mh.veridian_message_type IS DISTINCT FROM 'transactional'
			  AND mh.sent_at > cl.updated_at + interval '` + sentAfterMargin + `'`,
	},
}

func (c statutCause) groupQuery() string {
	return `SELECT ` + c.idExpr + `, a.name, COUNT(DISTINCT ` + c.emailExp + `),
		(array_agg(DISTINCT lower(` + c.emailExp + `)))[1:5], MIN(` + c.tsExpr + `) ` + c.from + ` GROUP BY 1, 2`
}

// statutRow : un groupe (cause, séquence) d'écarts.
type statutRow struct {
	Cause        string
	AutomationID string
	Automation   string
	Count        int
	Sample       []string
	Oldest       *time.Time
}

func (statutInvariant) Check(ctx context.Context, env Env) ([]Finding, error) {
	now := env.now()
	var rows []statutRow
	run := func(c statutCause, cutoff time.Time) error {
		rs, err := env.DB.QueryContext(ctx, c.groupQuery(), cutoff)
		if err != nil {
			return fmt.Errorf("cause %s: %w", c.code, err)
		}
		defer rs.Close()
		for rs.Next() {
			r := statutRow{Cause: c.code}
			var smp pq.StringArray
			var oldest *time.Time
			if err := rs.Scan(&r.AutomationID, &r.Automation, &r.Count, &smp, &oldest); err != nil {
				return err
			}
			r.Sample, r.Oldest = smp, oldest
			rows = append(rows, r)
		}
		return rs.Err()
	}
	for _, c := range statutCauses {
		if err := run(c, now.Add(-StatusGrace)); err != nil {
			return nil, err
		}
	}
	for _, c := range statutSentCauses {
		if err := run(c, now.Add(-SentAfterLookback)); err != nil {
			return nil, err
		}
	}
	return judgeStatut(rows), nil
}

type causeText struct {
	severity   Severity
	title      func(n int, seq string) string
	expected   string
	observed   func(n int) string
	why        string
	actionKind string // vide : constat seul
	label      func(n int) string
	impact     string
}

var statutTexts = map[string]causeText{
	causeReplied: {
		severity: SeverityWarning,
		title: func(n int, seq string) string {
			return fmt.Sprintf("%s %s répondu mais %s encore inscrit%s à « %s »", frInt(n), plural(n, "contact a", "contacts ont"), plural(n, "reste", "restent"), pluralS(n), seq)
		},
		expected:   "sortis de la séquence dès la réponse",
		observed:   func(n int) string { return fmt.Sprintf("%s encore inscrit%s", frInt(n), pluralS(n)) },
		why:        "La sortie sur réponse se fait à la détection du mail par la relève des boîtes, puis le moteur re-vérifie à chaque étape. Ces contacts ont une réponse humaine enregistrée depuis plus de 15 minutes et sont toujours actifs ou en cours d'envoi : la relance suivante est partie ou va partir tant que le moteur ne les rattrape pas.",
		actionKind: ActionExitContacts,
		label: func(n int) string {
			return fmt.Sprintf("Sortir %s %s de la séquence", frInt(n), plural(n, "contact", "contacts"))
		},
		impact: "Passe l'inscription en « sorti » (raison : a répondu), retire les envois en attente de ces contacts pour cette séquence. Aucun mail n'est envoyé.",
	},
	causeTerminal: {
		severity: SeverityWarning,
		title: func(n int, seq string) string {
			return fmt.Sprintf("%s %s rebondi ou plainte mais %s encore inscrit%s à « %s »", frInt(n), plural(n, "contact a", "contacts ont"), plural(n, "reste", "restent"), pluralS(n), seq)
		},
		expected:   "sortis de la séquence au rebond dur ou à la plainte",
		observed:   func(n int) string { return fmt.Sprintf("%s encore inscrit%s", frInt(n), pluralS(n)) },
		why:        "Leur statut sur la liste de la séquence est « rebondi » ou « plainte », ce qui doit les arrêter. Le contrôle de sortie se fait quand le contact atteint une étape : tant qu'il attend ou que son mail est en file, il reste compté comme inscrit (la porte d'envoi le refuse au dernier moment).",
		actionKind: ActionExitContacts,
		label: func(n int) string {
			return fmt.Sprintf("Sortir %s %s de la séquence", frInt(n), plural(n, "contact", "contacts"))
		},
		impact: "Passe l'inscription en « sorti » (raison : rebond) et retire les envois en attente. Aucun mail n'est envoyé.",
	},
	causeUnsub: {
		severity: SeverityWarning,
		title: func(n int, seq string) string {
			return fmt.Sprintf("%s %s désinscrit%s mais %s encore inscrit%s à « %s »", frInt(n), plural(n, "contact", "contacts"), pluralS(n), plural(n, "reste", "restent"), pluralS(n), seq)
		},
		expected:   "sortis de la séquence à la désinscription",
		observed:   func(n int) string { return fmt.Sprintf("%s encore inscrit%s", frInt(n), pluralS(n)) },
		why:        "Ils se sont désinscrits de la liste de la séquence. La porte d'envoi refuse leur mail au dernier moment, mais ils restent comptés dans les inscrits et dans la file.",
		actionKind: ActionExitContacts,
		label: func(n int) string {
			return fmt.Sprintf("Sortir %s %s de la séquence", frInt(n), plural(n, "contact", "contacts"))
		},
		impact: "Passe l'inscription en « sorti » (raison : désinscrit) et retire les envois en attente. Aucun mail n'est envoyé.",
	},
	causeExcluded: {
		severity: SeverityCritical,
		title: func(n int, seq string) string {
			return fmt.Sprintf("%s %s d'une liste d'exclusion %s inscrit%s à « %s »", frInt(n), plural(n, "contact", "contacts"), plural(n, "est", "sont"), pluralS(n), seq)
		},
		expected: "aucun contact exclu dans une séquence",
		observed: func(n int) string {
			return fmt.Sprintf("%s contact%s exclu%s inscrit%s", frInt(n), pluralS(n), pluralS(n), pluralS(n))
		},
		why:        "Ces contacts sont actifs dans une liste dont le nom contient « exclu » (par exemple les clients à ne pas prospecter) et sont pourtant dans une séquence de prospection : ils vont recevoir des mails de prospection.",
		actionKind: ActionExitContacts,
		label: func(n int) string {
			return fmt.Sprintf("Sortir %s %s exclu%s de la séquence", frInt(n), plural(n, "contact", "contacts"), pluralS(n))
		},
		impact: "Passe l'inscription en « sorti » (raison : liste d'exclusion) et retire les envois en attente. Aucun mail n'est envoyé.",
	},
	causeQueueSuppr: {
		severity: SeverityWarning,
		title: func(n int, seq string) string {
			return fmt.Sprintf("%s %s en file pour « %s » alors que le contact est désinscrit ou en rebond", frInt(n), plural(n, "envoi", "envois"), seq)
		},
		expected:   "aucun envoi en file pour un contact désinscrit, rebondi ou en plainte",
		observed:   func(n int) string { return fmt.Sprintf("%s envoi%s en file", frInt(n), pluralS(n)) },
		why:        "La file contient encore ces envois. La porte d'envoi les écartera au moment de les dépiler, mais ils gonflent la file et faussent le « reste à envoyer ».",
		actionKind: ActionCancelQueue,
		label: func(n int) string {
			return fmt.Sprintf("Retirer %s %s de la file", frInt(n), plural(n, "envoi", "envois"))
		},
		impact: "Supprime ces entrées de la file (journal des décisions : annulé) sans toucher à l'inscription.",
	},
	causeQueueReplied: {
		severity: SeverityWarning,
		title: func(n int, seq string) string {
			return fmt.Sprintf("%s %s en file pour « %s » alors que le contact a répondu", frInt(n), plural(n, "envoi", "envois"), seq)
		},
		expected:   "aucun envoi en file pour un contact qui a répondu",
		observed:   func(n int) string { return fmt.Sprintf("%s envoi%s en file", frInt(n), pluralS(n)) },
		why:        "Le contact a une réponse humaine enregistrée mais un mail de la séquence l'attend encore dans la file.",
		actionKind: ActionCancelQueue,
		label: func(n int) string {
			return fmt.Sprintf("Retirer %s %s de la file", frInt(n), plural(n, "envoi", "envois"))
		},
		impact: "Supprime ces entrées de la file (journal des décisions : annulé) sans toucher à l'inscription.",
	},
	causeQueueExcl: {
		severity: SeverityCritical,
		title: func(n int, seq string) string {
			return fmt.Sprintf("%s %s en file pour « %s » vers des contacts d'une liste d'exclusion", frInt(n), plural(n, "envoi", "envois"), seq)
		},
		expected:   "aucun contact exclu en file",
		observed:   func(n int) string { return fmt.Sprintf("%s envoi%s en file", frInt(n), pluralS(n)) },
		why:        "Ces contacts sont actifs dans une liste d'exclusion et un mail de prospection les attend dans la file.",
		actionKind: ActionCancelQueue,
		label: func(n int) string {
			return fmt.Sprintf("Retirer %s %s de la file", frInt(n), plural(n, "envoi", "envois"))
		},
		impact: "Supprime ces entrées de la file (journal des décisions : annulé) sans toucher à l'inscription.",
	},
	causeSentReply: {
		severity: SeverityCritical,
		title: func(n int, seq string) string {
			return fmt.Sprintf("%s %s un mail de « %s » après avoir répondu", frInt(n), plural(n, "contact a reçu", "contacts ont reçu"), seq)
		},
		expected: "aucun mail de séquence après une réponse",
		observed: func(n int) string {
			return fmt.Sprintf("%s contact%s relancé%s après réponse", frInt(n), pluralS(n), pluralS(n))
		},
		why: "Un mail de la séquence est parti plus de 10 minutes après la réponse humaine du contact. C'est un fait passé : il ne se répare pas, mais il faut comprendre pourquoi la sortie sur réponse n'a pas joué (voir les écarts « réponse et encore inscrit » de la même séquence).",
	},
	causeSentUnsub: {
		severity: SeverityCritical,
		title: func(n int, seq string) string {
			return fmt.Sprintf("%s %s un mail de « %s » après sa désinscription", frInt(n), plural(n, "contact a reçu", "contacts ont reçu"), seq)
		},
		expected: "aucun mail après une désinscription",
		observed: func(n int) string {
			return fmt.Sprintf("%s contact%s relancé%s après désinscription", frInt(n), pluralS(n), pluralS(n))
		},
		why: "Un mail de la séquence est parti plus de 10 minutes après la désinscription de la liste. C'est un fait passé, grave pour la réputation : il faut trouver quelle porte d'envoi l'a laissé passer.",
	},
}

func pluralS(n int) string {
	if n > 1 {
		return "s"
	}
	return ""
}

// judgeStatut : fonction pure. Un écart par (cause, séquence).
func judgeStatut(rows []statutRow) []Finding {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Cause != rows[j].Cause {
			return rows[i].Cause < rows[j].Cause
		}
		return rows[i].AutomationID < rows[j].AutomationID
	})
	var out []Finding
	for _, r := range rows {
		if r.Count <= 0 {
			continue
		}
		t, ok := statutTexts[r.Cause]
		if !ok {
			continue
		}
		name := r.Automation
		if name == "" {
			name = r.AutomationID
		}
		ev := map[string]any{
			"count": r.Count, "cause": r.Cause, "automation_id": r.AutomationID, "automation": r.Automation,
			"sample": stringsToAny(sample(r.Sample, 5)),
		}
		if r.Oldest != nil {
			ev["oldest_at"] = r.Oldest.UTC().Format(time.RFC3339)
		}
		f := Finding{
			SubjectRef:  r.Cause + ":" + r.AutomationID,
			Severity:    t.severity,
			Title:       t.title(r.Count, name),
			Expected:    t.expected,
			Observed:    t.observed(r.Count),
			Explanation: t.why,
			Evidence:    ev,
		}
		if t.actionKind != "" {
			f.ProposedAction = &Action{
				Kind: t.actionKind, Label: t.label(r.Count), Impact: t.impact, Reversible: false,
				Params: map[string]any{"automation_id": r.AutomationID, "cause": r.Cause},
			}
		}
		out = append(out, f)
	}
	return out
}

// statutCauseByCode retrouve une cause (pour l'action, qui rejoue la même requête).
func statutCauseByCode(code string) (statutCause, bool) {
	for _, c := range statutCauses {
		if c.code == code {
			return c, true
		}
	}
	return statutCause{}, false
}

var _ = strings.TrimSpace
