package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Invariant 1 — Envoyé = livré ou expliqué : chaque mail commercial sortant a, sous 48 h, un
// résultat : accepté par le relais (sent_at), refusé (failed_at), rebond ou refus de politique
// (bounced_at, bounce_type). Trois façons de violer :
//
//	a. « sans issue » : une ligne de l'historique de plus de 48 h sans date d'envoi, d'échec ni de rebond ;
//	b. rebond reçu mais non appliqué : un rapport de non-remise définitif (DSN) est arrivé dans la boîte
//	   du relais, relié à l'envoi, et l'historique ne porte aucun rebond (le contact reste joignable) ;
//	c. mail parti absent de la boîte unifiée : l'envoi n'a pas de copie dans veridian_mail_message. La copie
//	   capturée à l'envoi porte sent_ref ; celle de la reprise d'historique ne porte que link.sent_message_id
//	   (sent_ref NULL) : les deux comptent comme copie.
//
// Aucun journal de relais n'est lu : la preuve d'un rebond asynchrone est le DSN reçu, déjà ingéré par
// le lot L1. Un contrôle qui exigerait le journal Postfix le dira (non abouti), jamais « ok ».

const codeEnvoye = "envoye_livre"

// OutcomeDeadline : délai pour qu'un envoi ait un résultat.
const OutcomeDeadline = 48 * time.Hour

// MirrorGrace : délai pour que la copie d'un envoi apparaisse dans la boîte unifiée.
const MirrorGrace = time.Hour

type envoyeInvariant struct{}

func init() { Register(envoyeInvariant{}) }

func (envoyeInvariant) Code() string  { return codeEnvoye }
func (envoyeInvariant) Number() int   { return 1 }
func (envoyeInvariant) Title() string { return "Chaque mail envoyé est livré ou expliqué" }

const commercialMH = ` AND mh.transactional_notification_id IS NULL AND mh.veridian_message_type IS DISTINCT FROM 'transactional'`

const noOutcomeQuery = `
	SELECT COUNT(*), (array_agg(mh.id ORDER BY mh.created_at))[1:5], MIN(mh.created_at)
	FROM message_history mh
	WHERE mh.sent_at IS NULL AND mh.failed_at IS NULL AND mh.bounced_at IS NULL AND mh.created_at < $1` + commercialMH

const bounceNotAppliedQuery = `
	SELECT COUNT(DISTINCT mh.id), (array_agg(DISTINCT lower(mh.contact_email)))[1:5], MIN(m.occurred_at)
	FROM veridian_mail_message m
	JOIN message_history mh ON mh.id = m.link->>'sent_message_id'
	WHERE m.direction = 'in' AND m.kind = 'bounce_hard' AND mh.bounced_at IS NULL AND m.occurred_at < $1` + commercialMH

// bounceByCategoryQuery : les mêmes rebonds non appliqués, par catégorie du rapport (dsn.category,
// valeurs stables de mailbox.Cat*). Une boîte pleine et une adresse inexistante ne se traitent pas pareil.
const bounceByCategoryQuery = `
	SELECT COALESCE(NULLIF(m.dsn->>'category', ''), 'inconnu'), COUNT(DISTINCT mh.id)
	FROM veridian_mail_message m
	JOIN message_history mh ON mh.id = m.link->>'sent_message_id'
	WHERE m.direction = 'in' AND m.kind = 'bounce_hard' AND mh.bounced_at IS NULL AND m.occurred_at < $1` + commercialMH + `
	GROUP BY 1`

// bounceCategoryFR : libellé français des catégories de rebond (mailbox.Cat*).
var bounceCategoryFR = map[string]string{
	"adresse_inexistante": "adresse inexistante", "domaine_inexistant": "domaine inexistant",
	"boite_inactive": "boîte inactive", "boite_pleine": "boîte pleine", "message_trop_gros": "message trop gros",
	"refus_politique": "refus de politique", "reputation": "réputation", "authentification": "authentification",
	"greylisting": "mise en attente", "limite_debit": "limite de débit", "serveur_injoignable": "serveur injoignable",
	"relais_refuse": "relais refusé", "retard": "retard", "inconnu": "non classé",
}

const notMirroredQuery = `
	SELECT COUNT(*), (array_agg(mh.id ORDER BY mh.sent_at DESC))[1:5], MIN(mh.sent_at)
	FROM message_history mh
	WHERE mh.sent_at IS NOT NULL AND mh.failed_at IS NULL AND mh.sent_at < $1` + commercialMH + `
	  AND NOT EXISTS (SELECT 1 FROM veridian_mail_message m WHERE m.sent_ref = mh.id)
	  AND NOT EXISTS (SELECT 1 FROM veridian_mail_message m WHERE m.direction = 'out' AND m.link->>'sent_message_id' = mh.id)`

type envoyeRow struct {
	Cause  string
	Count  int
	Sample []string
	Oldest *time.Time
	// ByCategory : pour les rebonds non appliqués, nombre d'envois par catégorie du rapport.
	ByCategory map[string]int
}

const (
	causeNoOutcome   = "sans_issue"
	causeBounceLost  = "rebond_non_applique"
	causeNotMirrored = "absent_de_la_boite"
)

func (envoyeInvariant) Check(ctx context.Context, env Env) ([]Finding, error) {
	if err := requireTable(ctx, env, "veridian_mail_message"); err != nil {
		return nil, err
	}
	now := env.now()
	var rows []envoyeRow
	for _, q := range []struct {
		cause, sql string
		cutoff     time.Time
	}{
		{causeNoOutcome, noOutcomeQuery, now.Add(-OutcomeDeadline)},
		{causeBounceLost, bounceNotAppliedQuery, now.Add(-MirrorGrace)},
		{causeNotMirrored, notMirroredQuery, now.Add(-MirrorGrace)},
	} {
		rs, err := env.DB.QueryContext(ctx, q.sql, q.cutoff)
		if err != nil {
			return nil, fmt.Errorf("cause %s: %w", q.cause, err)
		}
		r := envoyeRow{Cause: q.cause}
		var smp pq.StringArray
		if rs.Next() {
			if err := rs.Scan(&r.Count, &smp, &r.Oldest); err != nil {
				_ = rs.Close()
				return nil, err
			}
		}
		r.Sample = smp
		if err := rs.Close(); err != nil {
			return nil, err
		}
		if q.cause == causeBounceLost && r.Count > 0 {
			if r.ByCategory, err = bounceCategories(ctx, env, q.cutoff); err != nil {
				return nil, err
			}
		}
		rows = append(rows, r)
	}
	return judgeEnvoye(rows), nil
}

func bounceCategories(ctx context.Context, env Env, cutoff time.Time) (map[string]int, error) {
	rs, err := env.DB.QueryContext(ctx, bounceByCategoryQuery, cutoff)
	if err != nil {
		return nil, fmt.Errorf("rebonds par catégorie: %w", err)
	}
	defer rs.Close()
	out := map[string]int{}
	for rs.Next() {
		var c string
		var n int
		if err := rs.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rs.Err()
}

// categoriesFR : « 60 boîte pleine, 40 adresse inexistante » (plus grand d'abord, puis par nom).
func categoriesFR(by map[string]int) string {
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if by[keys[i]] != by[keys[j]] {
			return by[keys[i]] > by[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		l := bounceCategoryFR[k]
		if l == "" {
			l = k
		}
		parts = append(parts, frInt(by[k])+" "+l)
	}
	return strings.Join(parts, ", ")
}

func judgeEnvoye(rows []envoyeRow) []Finding {
	var out []Finding
	for _, r := range rows {
		if r.Count <= 0 {
			continue
		}
		f := Finding{
			SubjectRef: "envoye:" + r.Cause,
			Evidence:   map[string]any{"count": r.Count, "sample": stringsToAny(sample(r.Sample, 5)), "definition": "envoyes"},
		}
		if r.Oldest != nil {
			f.Evidence["oldest_at"] = r.Oldest.UTC().Format(time.RFC3339)
		}
		n := r.Count
		switch r.Cause {
		case causeNoOutcome:
			f.Severity = SeverityCritical
			f.Title = fmt.Sprintf("%s %s sans issue depuis plus de 48 heures", frInt(n), plural(n, "mail", "mails"))
			f.Expected = "un résultat sous 48 h : accepté par le relais, refusé ou rebond"
			f.Observed = fmt.Sprintf("%s %s sans date d'envoi, d'échec ni de rebond", frInt(n), plural(n, "ligne", "lignes"))
			f.Explanation = "Ces lignes de l'historique ont été créées il y a plus de 48 heures et n'ont ni date d'envoi, ni échec, ni rebond : l'envoi est resté en suspens (arrêt du worker pendant l'envoi, ligne orpheline). Il faut retrouver ce qu'est devenu le mail avant de le considérer comme parti."
		case causeBounceLost:
			f.Severity = SeverityCritical
			f.Title = fmt.Sprintf("%s %s de non-remise définitif reçu%s sans effet sur l'historique", frInt(n), plural(n, "rapport", "rapports"), pluralS(n))
			f.Expected = "chaque rapport de non-remise définitif marque l'envoi comme rebondi et arrête le contact"
			f.Observed = fmt.Sprintf("%s rapport%s définitif%s reçu%s, historique sans rebond", frInt(n), pluralS(n), pluralS(n), pluralS(n))
			if len(r.ByCategory) > 0 {
				f.Observed += " (" + categoriesFR(r.ByCategory) + ")"
				byc := map[string]any{}
				for k, v := range r.ByCategory {
					byc[k] = v
				}
				f.Evidence["by_category"] = byc
			}
			f.Explanation = "Le relais a renvoyé un rapport de non-remise définitif (adresse inexistante ou refusée) dans sa boîte, relié à l'envoi, mais l'historique n'enregistre aucun rebond : le contact reste joignable et sera relancé. La chaîne « rapport de non-remise vers suppression du contact » n'a pas joué pour ces envois."
		case causeNotMirrored:
			f.Severity = SeverityWarning
			f.Title = fmt.Sprintf("%s %s parti%s sans copie dans la boîte unifiée", frInt(n), plural(n, "mail", "mails"), pluralS(n))
			f.Expected = "chaque envoi a sa copie dans la boîte unifiée"
			f.Observed = fmt.Sprintf("%s envoi%s sans copie", frInt(n), pluralS(n))
			f.Explanation = "Ces envois figurent dans l'historique mais pas dans la boîte unifiée : le fil du contact est incomplet (l'envoi d'origine manque à côté de la réponse). La capture à l'envoi ou la reprise de l'historique de la boîte unifiée n'est pas passée sur eux."
		}
		out = append(out, f)
	}
	return out
}
