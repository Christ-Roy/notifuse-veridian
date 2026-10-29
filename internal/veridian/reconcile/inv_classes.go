package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/lib/pq"
)

// Invariant 6 — Classes : la classe de messagerie d'un contact (custom_string_5) doit
// correspondre à son domaine quand le domaine est connu sans ambiguïté (Gmail, Outlook,
// Orange, Free...). Cas fondateur du 29/10 : 1 218 contacts classés « freemail_fr » dont
// le domaine est orange, laposte, free, sfr ou bouygues. La classe pilote le plafond
// journalier, le débit et le fusible du couple (domaine émetteur x classe) : une classe
// fausse envoie dans la mauvaise file de réputation.
//
// Seuls les domaines de la table de suffixes du moteur comptent. Un domaine propre à une
// entreprise se classe par son MX (le worker le résout), pas par son suffixe : ce
// contrôle ne s'en mêle pas. Les résidus de la classe historique freemail_fr (gmx.fr,
// dartybox.com) n'ont pas de classe fine : ils ne sont pas jugés non plus.

const codeClasses = "classes"

type classesInvariant struct{}

func init() { Register(classesInvariant{}) }

func (classesInvariant) Code() string  { return codeClasses }
func (classesInvariant) Number() int   { return 6 }
func (classesInvariant) Title() string { return "La classe de messagerie correspond au domaine" }

// classRow : contacts d'un domaine connu portant une même étiquette de classe.
type classRow struct {
	Domain string
	Tag    string // minuscules, sans espaces ; vide = non classé
	Count  int
	Sample []string
}

// classesQuery regroupe par domaine et étiquette : une ligne par couple, pas par contact.
const classesQuery = `
	SELECT lower(split_part(c.email, '@', 2)) AS d,
	       lower(btrim(COALESCE(c.custom_string_5, ''))) AS t,
	       COUNT(*),
	       (array_agg(lower(c.email) ORDER BY c.email))[1:5]
	FROM contacts c
	WHERE lower(split_part(c.email, '@', 2)) = ANY($1)
	GROUP BY 1, 2`

// classKnownDomains : tous les domaines dont la classe est certaine (hors résidu freemail_fr).
func classKnownDomains() []string {
	var out []string
	for _, class := range domain.VeridianAllProviderClasses() {
		if class == domain.ProviderClassCorporate || class == domain.ProviderClassFreemailFR {
			continue
		}
		ds, _ := domain.VeridianDomainsForClass(class)
		out = append(out, ds...)
	}
	sort.Strings(out)
	return out
}

func (classesInvariant) Check(ctx context.Context, env Env) ([]Finding, error) {
	rows, err := env.DB.QueryContext(ctx, classesQuery, pq.Array(classKnownDomains()))
	if err != nil {
		return nil, fmt.Errorf("lecture des classes: %w", err)
	}
	defer rows.Close()
	var data []classRow
	for rows.Next() {
		var r classRow
		var sample pq.StringArray
		if err := rows.Scan(&r.Domain, &r.Tag, &r.Count, &sample); err != nil {
			return nil, err
		}
		r.Sample = sample
		data = append(data, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return judgeClasses(data), nil
}

// expectedClassFor : la classe certaine d'un domaine, "" si le domaine n'en a pas.
func expectedClassFor(d string) string {
	c := domain.ClassifyProviderClass("x@" + d)
	if c == domain.ProviderClassCorporate || c == domain.ProviderClassFreemailFR {
		return ""
	}
	return c
}

// judgeClasses : fonction pure. Un écart par couple (étiquette observée, classe attendue).
func judgeClasses(rows []classRow) []Finding {
	type key struct{ from, to string }
	type agg struct {
		count   int
		domains map[string]int
		sample  []string
	}
	groups := map[key]*agg{}
	for _, r := range rows {
		want := expectedClassFor(r.Domain)
		if want == "" || r.Tag == want {
			continue
		}
		k := key{r.Tag, want}
		g := groups[k]
		if g == nil {
			g = &agg{domains: map[string]int{}}
			groups[k] = g
		}
		g.count += r.Count
		g.domains[r.Domain] += r.Count
		g.sample = append(g.sample, r.Sample...)
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if groups[keys[i]].count != groups[keys[j]].count {
			return groups[keys[i]].count > groups[keys[j]].count
		}
		return keys[i].from+">"+keys[i].to < keys[j].from+">"+keys[j].to
	})
	out := make([]Finding, 0, len(keys))
	for _, k := range keys {
		g := groups[k]
		out = append(out, classFinding(k.from, k.to, g.count, g.domains, g.sample))
	}
	return out
}

func classLabel(tag string) string {
	if tag == "" {
		return "aucune classe"
	}
	return "« " + tag + " »"
}

func classFinding(from, to string, count int, domains map[string]int, smp []string) Finding {
	doms := make([]string, 0, len(domains))
	for d := range domains {
		doms = append(doms, d)
	}
	sort.Slice(doms, func(i, j int) bool {
		if domains[doms[i]] != domains[doms[j]] {
			return domains[doms[i]] > domains[doms[j]]
		}
		return doms[i] < doms[j]
	})
	doms = sample(doms, 8)
	sort.Strings(smp)
	smp = sample(smp, 5)

	sev := SeverityWarning
	why := ""
	switch {
	case from == "":
		sev = SeverityInfo
		why = "Ces contacts n'ont pas été classés à l'import. Le worker retrouve la classe par le domaine au moment d'envoyer, mais le stock par classe ne les voit pas."
	case from == domain.ProviderClassFreemailFR:
		why = "« freemail_fr » est l'ancienne classe qui regroupait tous les fournisseurs d'accès français. Elle a été scindée le 22/10 : chaque fournisseur (orange, sfr_altice, free, laposte, bouygues) a maintenant son plafond, son débit et son fusible. Tant que ces contacts gardent l'ancienne étiquette, le stock par classe est faux."
	default:
		why = "L'étiquette vient d'un import ou d'une résolution de MX qui ne correspond pas au domaine de l'adresse. Le plafond et le fusible appliqués ne sont pas ceux du vrai fournisseur."
	}
	return Finding{
		SubjectRef: fmt.Sprintf("classe:%s->%s", orEmpty(from, "vide"), to),
		Severity:   sev,
		Title:      fmt.Sprintf("%s %s classés %s au lieu de « %s »", frInt(count), plural(count, "contact", "contacts"), classLabel(from), to),
		Expected:   fmt.Sprintf("classe « %s » (d'après le domaine : %s)", to, strings.Join(sample(doms, 3), ", ")),
		Observed:   classLabel(from),
		Explanation: fmt.Sprintf("%s %s une adresse sur %s (classe certaine : « %s »), mais %s étiquetés %s. %s",
			frInt(count), plural(count, "contact a", "contacts ont"), strings.Join(sample(doms, 4), ", "), to,
			plural(count, "il est", "ils sont"), classLabel(from), why),
		Evidence: map[string]any{
			"count":         count,
			"from":          from,
			"to":            to,
			"domains":       doms,
			"sample":        smp,
			"definition":    "classe attendue = table de suffixes du moteur (domain.ClassifyProviderClass)",
			"control_query": "contacts groupés par domaine connu et custom_string_5",
		},
		ProposedAction: &Action{
			Kind:       ActionReclassify,
			Label:      fmt.Sprintf("Reclasser %s %s en « %s »", frInt(count), plural(count, "contact", "contacts"), to),
			Impact:     fmt.Sprintf("Met custom_string_5 à « %s » sur les contacts de ces domaines qui portent %s. Aucun mail n'est envoyé ni retiré de la file.", to, classLabel(from)),
			Reversible: true,
			Params:     map[string]any{"from": from, "to": to},
		},
	}
}

func orEmpty(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}
