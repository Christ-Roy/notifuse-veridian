package policy

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// MinSentForSlowdown : envois minimum sur 7 jours dans un couple (domaine d'envoi x classe)
// avant que le fusible réagisse à un taux. Doit égaler queue.VeridianReputationMinSent
// (un test de câblage le vérifie).
const MinSentForSlowdown = 20

// Shadows : pour chaque classe, qui pose SA PROPRE valeur et masque donc la politique.
type Shadows struct {
	DailyCap map[string][]string
	Rate     map[string][]string
}

// ComputeShadows relève, dans les profils d'envoi et le réglage workspace historiques, les
// classes dont plafond ou débit sont posés à la main (la politique ne s'y applique pas).
func ComputeShadows(ws *domain.Workspace) Shadows {
	sh := Shadows{DailyCap: map[string][]string{}, Rate: map[string][]string{}}
	if ws == nil {
		return sh
	}
	addCaps := func(name string, m map[string]int) {
		if len(m) == 0 {
			return
		}
		for _, c := range domain.VeridianAllProviderClasses() {
			if _, ok := domain.VeridianConfiguredCapForClass(m, c); ok {
				sh.DailyCap[c] = append(sh.DailyCap[c], name)
			}
		}
	}
	addRates := func(name string, m map[string]float64) {
		if len(m) == 0 {
			return
		}
		for _, c := range domain.VeridianAllProviderClasses() {
			if _, ok := domain.VeridianConfiguredRateForClass(m, c); ok {
				sh.Rate[c] = append(sh.Rate[c], name)
			}
		}
	}
	for _, in := range ws.Integrations {
		if in.Type != domain.IntegrationTypeEmail {
			continue
		}
		name := "profil « " + in.Name + " »"
		addCaps(name, in.EmailProvider.VeridianProviderClassDailyCap)
		addRates(name, in.EmailProvider.VeridianProviderClassRates)
	}
	addCaps("le réglage du workspace", ws.Settings.VeridianProviderClassDailyCap)
	addRates("le réglage du workspace", ws.Settings.VeridianProviderClassRates)
	return sh
}

// Smooth ramène un taux de réponse vers la moyenne globale quand l'échantillon est petit :
// (réponses + poids x moyenne) / (envois + poids). Avec 0 envoi on retrouve la moyenne.
func Smooth(replied, sent int, prior domain.VeridianObservedEngagement, weight int) float64 {
	pr := 0.0
	if prior.Sent > 0 {
		pr = float64(prior.RepliedHuman) / float64(prior.Sent)
	}
	return (float64(replied) + float64(weight)*pr) / (float64(sent) + float64(weight))
}

// BuildObserved assemble ce qui est mesuré pour une classe : le workspace s'il a envoyé vers
// cette classe, sinon la mesure du catalogue.
func BuildObserved(p domain.VeridianClassPreset, cat *domain.VeridianClassPresetCatalog, ws *domain.VeridianObservedEngagement) Observed {
	src, basis := p.Observed, "catalog"
	if ws != nil && ws.Sent > 0 {
		src, basis = *ws, "workspace"
	}
	o := Observed{Sent: src.Sent, Bounced: src.Bounced, RepliedHuman: src.RepliedHuman, Basis: basis,
		ReplyRateSmoothed: Smooth(src.RepliedHuman, src.Sent, cat.ReplyPrior, cat.ReplySmoothingWeight)}
	if src.Sent > 0 {
		rr := float64(src.RepliedHuman) / float64(src.Sent)
		br := float64(src.Bounced) / float64(src.Sent)
		o.ReplyRate, o.BounceRate = &rr, &br
	}
	return o
}

// BuildView assemble la vue expliquée d'une classe.
func BuildView(p domain.VeridianClassPreset, cat *domain.VeridianClassPresetCatalog, snap *domain.VeridianPolicySnapshot, sh Shadows, wsObs *domain.VeridianObservedEngagement) ClassView {
	eff := snap.Classes[p.Class]
	ov := snap.Overridden[p.Class]
	src := func(over bool) string {
		if over {
			return SourceWorkspaceOverride
		}
		return SourcePreset
	}
	v := ClassView{
		Class: p.Class, Label: p.Label, Why: p.Why,
		DailyCap:          Value[int]{Value: eff.DailyCap, Source: src(ov.DailyCap), Preset: p.DailyCap, ShadowedBy: sh.DailyCap[p.Class]},
		RatePerMin:        Value[float64]{Value: eff.RatePerMin, Source: src(ov.RatePerMin), Preset: p.RatePerMin, ShadowedBy: sh.Rate[p.Class]},
		WarmupShare:       Value[float64]{Value: eff.WarmupShare, Source: src(ov.WarmupShare), Preset: p.WarmupShare},
		SlowdownThreshold: Value[float64]{Value: eff.SlowdownThreshold, Source: src(ov.SlowdownThreshold), Preset: p.SlowdownThreshold},
		Observed:          BuildObserved(p, cat, wsObs),
	}
	v.Rules = []Rule{
		{Name: RuleDailyCap, Value: v.DailyCap.Value, Source: v.DailyCap.Source, Text: capText(p.Label, v.DailyCap)},
		{Name: RuleRatePerMin, Value: v.RatePerMin.Value, Source: v.RatePerMin.Source, Text: rateText(v.RatePerMin)},
		{Name: RuleWarmupShare, Value: v.WarmupShare.Value, Source: v.WarmupShare.Source, Text: warmupText(v.WarmupShare)},
		{Name: RuleSlowdownThreshold, Value: v.SlowdownThreshold.Value, Source: v.SlowdownThreshold.Source, Text: thresholdText(v.SlowdownThreshold)},
	}
	parts := make([]string, 0, 6)
	for _, r := range v.Rules {
		parts = append(parts, r.Text)
	}
	parts = append(parts, observedText(v.Observed, cat.AsOf), p.Why)
	v.Explanation = p.Label + " : " + strings.Join(parts, " ")
	return v
}

func sourceSuffix(s string) string {
	if s == SourceWorkspaceOverride {
		return " (réglé pour ce workspace)"
	}
	return " (règle par défaut)"
}

func shadowText(by []string) string {
	if len(by) == 0 {
		return ""
	}
	sort.Strings(by)
	return " Masquée pour : " + strings.Join(by, ", ") + " (leur propre valeur s'applique)."
}

func capText(label string, v Value[int]) string {
	return fmt.Sprintf("Au plus %d mails par jour et par domaine d'envoi vers cette classe%s.", v.Value, sourceSuffix(v.Source)) + shadowText(v.ShadowedBy)
}

func rateText(v Value[float64]) string {
	return "Cadence : " + fmtRate(v.Value) + sourceSuffix(v.Source) + "." + shadowText(v.ShadowedBy)
}

func warmupText(v Value[float64]) string {
	return fmt.Sprintf("Tant qu'un domaine d'envoi chauffe, cette classe reçoit au plus %s du plafond de chauffe du jour%s ; cette part ne s'applique pas si le plafond de la classe est posé à la main.",
		fmtPct(v.Value), sourceSuffix(v.Source))
}

func thresholdText(v Value[float64]) string {
	return fmt.Sprintf("Le débit est divisé par 2 quand %s des envois des 7 derniers jours rebondissent ou sont refusés pour politique, par 4 à partir de %s ; il faut au moins %d envois sur 7 jours pour réagir%s.",
		fmtPct(v.Value), fmtPct(2*v.Value), MinSentForSlowdown, sourceSuffix(v.Source))
}

func observedText(o Observed, asOf string) string {
	if o.Sent == 0 {
		return "Aucun envoi mesuré vers cette classe."
	}
	where := "Mesuré sur ce workspace"
	if o.Basis == "catalog" {
		where = "Mesure du catalogue au " + asOf + " (workspace animalsd)"
	}
	return fmt.Sprintf("%s : %s sur %d envois (%s), %s.", where, plural(o.RepliedHuman, "réponse humaine", "réponses humaines"), o.Sent,
		fmtPct(float64(o.RepliedHuman)/float64(o.Sent)), plural(o.Bounced, "rebond", "rebonds"))
}

func plural(n int, one, many string) string {
	if n > 1 {
		return fmt.Sprintf("%d %s", n, many)
	}
	return fmt.Sprintf("%d %s", n, one)
}

// fmtNum : décimale à la française, sans zéros inutiles.
func fmtNum(f float64, decimals int) string {
	s := strconv.FormatFloat(math.Round(f*math.Pow10(decimals))/math.Pow10(decimals), 'f', decimals, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return strings.Replace(s, ".", ",", 1)
}

func fmtPct(f float64) string { return fmtNum(f*100, 1) + " %" }

func fmtRate(perMin float64) string {
	if perMin >= 1 {
		return fmtNum(perMin, 1) + " mails par minute"
	}
	if perMin <= 0 {
		return "non bridée"
	}
	return "1 mail toutes les " + fmtNum(1/perMin, 1) + " minutes"
}
