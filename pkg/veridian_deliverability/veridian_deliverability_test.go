package veridian_deliverability

import (
	"strings"
	"testing"
)

// hasRule retourne true si une règle de ce nom est dans le résultat.
func hasRule(r Result, name string) bool {
	for _, rule := range r.Rules {
		if rule.Name == name {
			return true
		}
	}
	return false
}

func ruleWeight(r Result, name string) (float64, bool) {
	for _, rule := range r.Rules {
		if rule.Name == name {
			return rule.Weight, true
		}
	}
	return 0, false
}

// cleanColdBody : un cold plain text propre, personnalisé, sans lien.
const cleanColdBody = `Bonjour Marie,

Je suis tombé sur Cabinet Durand en cherchant des cabinets d'architecture
spécialisés dans la rénovation à Lyon. Votre approche du réemploi de matériaux
m'a vraiment parlé.

Je travaille avec des structures comme la vôtre pour automatiser la prospection
sans dénaturer la relation client. Auriez-vous 15 minutes la semaine prochaine
pour en discuter ?

Bien à vous,
Robert`

func TestScore_CleanColdPlainText_LowScore(t *testing.T) {
	res := Score(Input{
		Subject: "Question rapide sur Cabinet Durand",
		Body:    cleanColdBody,
		IsHTML:  false,
	})
	if res.Score > 2.0 {
		t.Fatalf("clean cold should score low, got %v with rules %+v", res.Score, res.Rules)
	}
	if res.IsRisky {
		t.Fatalf("clean cold should not be risky, got score %v", res.Score)
	}
}

func TestScore_BoundedZeroToTen(t *testing.T) {
	// Email cumulant un max de signaux : doit rester borné à 10.
	nasty := strings.Repeat("ACT NOW!!! FREE $$$ 100% GUARANTEED CLICK HERE ", 30)
	res := Score(Input{
		Subject: "RE: 🎉🎉🎉 FREE MONEY ACT NOW!!! $$$ WINNER",
		Body:    "<html><img src=x><img src=y> " + nasty + " {{ first_name }} {opt1|opt2} http://evil.tld http://x.tld http://y.tld http://z.tld</html>",
		IsHTML:  true,
		Mode:    ModeStrict,
	})
	if res.Score < 0 || res.Score > 10 {
		t.Fatalf("score must be bounded 0-10, got %v", res.Score)
	}
	if !res.IsRisky {
		t.Fatalf("nasty email must be risky, got %v", res.Score)
	}
}

func TestScore_RulesTable(t *testing.T) {
	tests := []struct {
		name      string
		in        Input
		wantRule  string
		wantNoRul string // règle qui ne doit PAS être présente (optionnel)
	}{
		{
			name:     "all caps subject",
			in:       Input{Subject: "URGENT OFFRE SPECIALE POUR VOUS", Body: cleanColdBody},
			wantRule: "ALL_CAPS_SUBJECT",
		},
		{
			name:     "empty subject",
			in:       Input{Subject: "", Body: cleanColdBody},
			wantRule: "SUBJECT_EMPTY",
		},
		{
			name:     "fake Re: on first contact",
			in:       Input{Subject: "Re: notre conversation", Body: cleanColdBody},
			wantRule: "FAKE_RE_SUBJECT",
		},
		{
			name:     "subject too long",
			in:       Input{Subject: strings.Repeat("mot ", 25), Body: cleanColdBody},
			wantRule: "SUBJECT_TOO_LONG",
		},
		{
			name:     "emoji burst in subject",
			in:       Input{Subject: "Offre 🎉🎁🚀 pour vous", Body: cleanColdBody},
			wantRule: "SUBJECT_EMOJI_BURST",
		},
		{
			name:     "excessive punctuation",
			in:       Input{Subject: "Bonjour", Body: "Une offre incroyable !!! Vous y croyez ???"},
			wantRule: "EXCESSIVE_PUNCTUATION",
		},
		{
			name:     "liquid unresolved leaking",
			in:       Input{Subject: "Bonjour {{ first_name }}", Body: "Salut {{ first_name }}, voici mon offre détaillée et personnalisée."},
			wantRule: "LIQUID_UNRESOLVED",
		},
		{
			name:     "liquid block tag unresolved",
			in:       Input{Subject: "Bonjour", Body: "Salut, {% if vip %}offre VIP{% endif %} voici un message un peu plus long pour passer la longueur min."},
			wantRule: "LIQUID_UNRESOLVED",
		},
		{
			name:     "spintax unresolved leaking",
			in:       Input{Subject: "Bonjour", Body: "Salut {Marie|Pierre}, voici mon message suffisamment long pour ne pas trigger body too short."},
			wantRule: "SPINTAX_UNRESOLVED",
		},
		{
			name:     "body too short",
			in:       Input{Subject: "Hello", Body: "Check this out"},
			wantRule: "BODY_TOO_SHORT",
		},
		{
			name:     "spammy click here",
			in:       Input{Subject: "Bonjour", Body: "Pour profiter de l'offre, click here maintenant et voyez le résultat par vous-même rapidement."},
			wantRule: "SPAMMY_WORD_CLICK_HERE",
		},
		{
			name:     "spammy 100% guaranteed",
			in:       Input{Subject: "Bonjour", Body: "Notre solution est 100% guaranteed et vous fera gagner du temps dans votre quotidien professionnel."},
			wantRule: "SPAMMY_WORD_100_PERCENT",
		},
		{
			name:     "spammy dollars",
			in:       Input{Subject: "Bonjour", Body: "Gagnez $$$ avec notre méthode éprouvée et reconnue par des milliers d'utilisateurs satisfaits aujourd'hui."},
			wantRule: "SPAMMY_WORD_DOLLARS",
		},
		{
			name:     "body caps run",
			in:       Input{Subject: "Bonjour", Body: "Notre offre est VRAIMENT EXCEPTIONNELLE POUR VOUS aujourd'hui, profitez-en vite avant la fin."},
			wantRule: "BODY_CAPS_RUN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := Score(tc.in)
			if tc.wantRule != "" && !hasRule(res, tc.wantRule) {
				t.Errorf("expected rule %q, got rules: %+v", tc.wantRule, ruleNames(res))
			}
			if tc.wantNoRul != "" && hasRule(res, tc.wantNoRul) {
				t.Errorf("did not expect rule %q, got rules: %+v", tc.wantNoRul, ruleNames(res))
			}
		})
	}
}

func ruleNames(r Result) []string {
	out := make([]string, 0, len(r.Rules))
	for _, rule := range r.Rules {
		out = append(out, rule.Name)
	}
	return out
}

func TestScore_HTMLImageOnly(t *testing.T) {
	res := Score(Input{
		Subject: "Découvrez notre offre",
		Body:    `<html><body><img src="https://cdn.x/promo.jpg"></body></html>`,
		IsHTML:  true,
	})
	if !hasRule(res, "HTML_IMAGE_ONLY") {
		t.Fatalf("expected HTML_IMAGE_ONLY, got %+v", ruleNames(res))
	}
	if !hasRule(res, "MIME_HTML_ONLY_RISK") {
		t.Fatalf("expected MIME_HTML_ONLY_RISK on HTML body, got %+v", ruleNames(res))
	}
}

func TestScore_ImageNoAlt(t *testing.T) {
	body := `<html><body><p>Bonjour Marie, voici un aperçu de notre travail récent sur des projets similaires au vôtre, avec quelques visuels.</p>` +
		`<img src="a.jpg"><img src="b.jpg" alt="logo Veridian"><img src="c.jpg"></body></html>`
	res := Score(Input{Subject: "Notre travail", Body: body, IsHTML: true})
	w, ok := ruleWeight(res, "IMAGE_NO_ALT")
	if !ok {
		t.Fatalf("expected IMAGE_NO_ALT, got %+v", ruleNames(res))
	}
	// 2 images sans alt → poids = wImageNoAlt * 2.
	if w != wImageNoAlt*2 {
		t.Errorf("expected weight %v for 2 images without alt, got %v", wImageNoAlt*2, w)
	}
}

func TestScore_PlainTextRulesSkippedOnHTMLAbsence(t *testing.T) {
	// En plain text pur, les règles HTML ne doivent jamais se déclencher.
	res := Score(Input{Subject: "Bonjour Marie", Body: cleanColdBody, IsHTML: false})
	for _, name := range []string{"HTML_IMAGE_ONLY", "MIME_HTML_ONLY_RISK", "IMAGE_NO_ALT", "IMAGE_RATIO_HIGH", "HTML_IN_STRICT_MODE"} {
		if hasRule(res, name) {
			t.Errorf("HTML rule %q must not fire on plain text", name)
		}
	}
}

func TestScore_ModeStrictPenalizesLinksAndHTML(t *testing.T) {
	body := `<html><body><p>Bonjour Marie, voici notre proposition détaillée et adaptée à votre cabinet d'architecture lyonnais.</p>` +
		`<a href="https://agences-veridian.fr/demo">Voir la démo</a></body></html>`

	strict := Score(Input{Subject: "Proposition", Body: body, IsHTML: true, Mode: ModeStrict})
	lenient := Score(Input{Subject: "Proposition", Body: body, IsHTML: true, Mode: ModeLenient})

	if !hasRule(strict, "LINK_IN_STRICT_MODE") {
		t.Errorf("strict mode must penalize links, got %+v", ruleNames(strict))
	}
	if !hasRule(strict, "HTML_IN_STRICT_MODE") {
		t.Errorf("strict mode must penalize HTML, got %+v", ruleNames(strict))
	}
	if hasRule(lenient, "LINK_IN_STRICT_MODE") {
		t.Errorf("lenient mode must NOT use the strict link rule")
	}
	if strict.Score <= lenient.Score {
		t.Errorf("strict score (%v) should exceed lenient score (%v) for the same HTML+link email", strict.Score, lenient.Score)
	}
}

func TestScore_ModeStrictPlainTextBonus(t *testing.T) {
	res := Score(Input{Subject: "Question rapide", Body: cleanColdBody, IsHTML: false, Mode: ModeStrict})
	w, ok := ruleWeight(res, "PLAIN_TEXT_BONUS")
	if !ok {
		t.Fatalf("expected PLAIN_TEXT_BONUS in strict mode plain text, got %+v", ruleNames(res))
	}
	if w >= 0 {
		t.Errorf("plain text bonus must be negative, got %v", w)
	}
}

func TestScore_ModeFromProviderClass(t *testing.T) {
	cases := []struct {
		class string
		want  Mode
	}{
		{"google", ModeStrict},
		{"microsoft", ModeStrict},
		{"apple_icloud", ModeStrict},
		{"freemail_fr", ModeLenient},
		{"corporate", ModeLenient},
		{"ovh", ModeLenient},
		{"ionos", ModeLenient},
		{"yahoo_aol", ModeLenient},
		{"security_gateway", ModeLenient},
		{"other_hoster", ModeLenient},
		{"corporate_selfhost", ModeLenient},
		{"", ModeDefault},
		{"unknown_bogus", ModeDefault},
		{"  GOOGLE  ", ModeStrict}, // trim + lower
	}
	for _, c := range cases {
		if got := VeridianModeForClass(c.class); got != c.want {
			t.Errorf("VeridianModeForClass(%q) = %v, want %v", c.class, got, c.want)
		}
	}
}

func TestScore_ProviderClassDrivesMode(t *testing.T) {
	body := `<html><body><p>Bonjour Marie, proposition adaptée à votre cabinet lyonnais avec une vraie valeur ajoutée concrète.</p><a href="https://agences-veridian.fr/x">démo</a></body></html>`
	// Pas de Mode forcé : doit se déduire de ProviderClass.
	res := Score(Input{Subject: "Proposition", Body: body, IsHTML: true, ProviderClass: "google"})
	if res.Mode != "strict" {
		t.Fatalf("provider class google should yield strict mode, got %q", res.Mode)
	}
	if !hasRule(res, "LINK_IN_STRICT_MODE") {
		t.Errorf("expected strict-mode link rule when ProviderClass=google")
	}
}

func TestScore_TrackingDomainMismatch(t *testing.T) {
	body := `<p>Bonjour Marie, voici une proposition concrète et personnalisée pour votre activité, avec un lien de suivi.</p>` +
		`<a href="https://tracking.tiers-louche.com/r/abc">détails</a>`
	res := Score(Input{
		Subject:    "Proposition",
		Body:       body,
		IsHTML:     true,
		FromDomain: "agences-veridian.fr",
		Mode:       ModeLenient,
	})
	if !hasRule(res, "TRACKING_DOMAIN_MISMATCH") {
		t.Fatalf("expected TRACKING_DOMAIN_MISMATCH, got %+v", ruleNames(res))
	}

	// Aligné : track.agences-veridian.fr est un sous-domaine du From → pas de mismatch.
	aligned := `<p>Bonjour Marie, voici une proposition concrète et personnalisée pour votre activité, avec un lien de suivi.</p>` +
		`<a href="https://track.agences-veridian.fr/r/abc">détails</a>`
	res2 := Score(Input{Subject: "Proposition", Body: aligned, IsHTML: true, FromDomain: "agences-veridian.fr", Mode: ModeLenient})
	if hasRule(res2, "TRACKING_DOMAIN_MISMATCH") {
		t.Errorf("aligned tracking subdomain must NOT trigger mismatch, got %+v", ruleNames(res2))
	}
}

func TestScore_TrackingInStrictMode(t *testing.T) {
	body := `<p>Bonjour Marie, proposition adaptée à votre cabinet avec un suivi d'ouverture pour mesurer l'intérêt réel.</p>` +
		`<img src="https://agences-veridian.fr/t/pixel.png" width="1" height="1">`
	res := Score(Input{Subject: "Proposition", Body: body, IsHTML: true, Mode: ModeStrict})
	if !hasRule(res, "TRACKING_IN_STRICT_MODE") {
		t.Fatalf("expected TRACKING_IN_STRICT_MODE, got %+v", ruleNames(res))
	}
}

func TestScore_ManyLinks(t *testing.T) {
	body := "Bonjour, voici plein de ressources utiles pour vous aujourd'hui sur notre site et nos partenaires de confiance : " +
		"http://a.com http://b.com http://c.com http://d.com http://e.com"
	res := Score(Input{Subject: "Ressources", Body: body, IsHTML: false, Mode: ModeLenient})
	if !hasRule(res, "LINKS_MANY") {
		t.Fatalf("expected LINKS_MANY for 5 links, got %+v", ruleNames(res))
	}
}

func TestScore_LinksRatioHighShortBody(t *testing.T) {
	body := "Voici 2 liens utiles : http://a.com et http://b.com — à bientôt." // court + 2 liens
	res := Score(Input{Subject: "Liens", Body: body, IsHTML: false, Mode: ModeLenient})
	if !hasRule(res, "LINKS_RATIO_HIGH") {
		t.Fatalf("expected LINKS_RATIO_HIGH, got %+v", ruleNames(res))
	}
}

func TestScore_BodyTooLong(t *testing.T) {
	body := strings.Repeat("Bonjour Marie, voici un paragraphe substantiel et personnalisé. ", 80)
	res := Score(Input{Subject: "Long message", Body: body, IsHTML: false})
	if !hasRule(res, "BODY_TOO_LONG") {
		t.Fatalf("expected BODY_TOO_LONG, got score %v rules %+v", res.Score, ruleNames(res))
	}
}

func TestScore_NoPersonalizationGeneric(t *testing.T) {
	// Corps court, générique, sans chiffre ni nom propre interne.
	body := "bonjour, je vous propose une solution pour votre entreprise. au plaisir d'echanger avec vous bientot."
	res := Score(Input{Subject: "bonjour", Body: body, IsHTML: false})
	if !hasRule(res, "NO_PERSONALIZATION") {
		t.Fatalf("expected NO_PERSONALIZATION on generic body, got %+v", ruleNames(res))
	}

	// Le clean cold (avec "Cabinet Durand", "Marie", "Lyon", "15 minutes") ne doit PAS être flaggé générique.
	clean := Score(Input{Subject: "Question", Body: cleanColdBody, IsHTML: false})
	if hasRule(clean, "NO_PERSONALIZATION") {
		t.Errorf("personalized clean cold must NOT be flagged generic, got %+v", ruleNames(clean))
	}
}

func TestScore_RulesSortedByWeightDesc(t *testing.T) {
	res := Score(Input{
		Subject: "RE: GAGNEZ $$$ MAINTENANT",
		Body:    "<html><img src=x> CLICK HERE !!! {{ first_name }} {a|b}</html>",
		IsHTML:  true,
		Mode:    ModeStrict,
	})
	for i := 1; i < len(res.Rules); i++ {
		if res.Rules[i-1].Weight < res.Rules[i].Weight {
			t.Fatalf("rules must be sorted by weight desc: %v before %v", res.Rules[i-1], res.Rules[i])
		}
	}
}

func TestScore_RenderedNotRaw(t *testing.T) {
	// Le linter doit flagger ce qui FUIT non résolu (le rendu final), pas un
	// template "censé" être résolu. Ici on simule un rendu où Liquid a fuité.
	leaked := Score(Input{Subject: "Bonjour {{ company }}", Body: "Salut {{ first_name }}, " + cleanColdBody, IsHTML: false})
	if !hasRule(leaked, "LIQUID_UNRESOLVED") {
		t.Fatalf("leaked liquid must be detected")
	}
	// Un rendu où tout est résolu (pas d'accolades) → pas de règle perso.
	resolved := Score(Input{Subject: "Bonjour Cabinet Durand", Body: cleanColdBody, IsHTML: false})
	if hasRule(resolved, "LIQUID_UNRESOLVED") || hasRule(resolved, "SPINTAX_UNRESOLVED") {
		t.Fatalf("fully resolved render must not flag liquid/spintax")
	}
}

func TestScore_SummaryReflectsScore(t *testing.T) {
	low := Score(Input{Subject: "Question rapide", Body: cleanColdBody, IsHTML: false})
	if !strings.Contains(strings.ToLower(low.Summary), "bon profil") {
		t.Errorf("low score summary should be positive, got %q", low.Summary)
	}
	high := Score(Input{
		Subject: "RE: FREE $$$ ACT NOW!!!",
		Body:    "<img src=x> CLICK HERE {{ x }} {a|b} http://a http://b http://c http://d",
		IsHTML:  true,
		Mode:    ModeStrict,
	})
	if !high.IsRisky || !strings.Contains(strings.ToUpper(high.Summary), "RISQUE") {
		t.Errorf("high score summary should warn, got %q (score %v)", high.Summary, high.Score)
	}
}

func TestScore_IsAllCapsHelper(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"URGENT OFFRE", true},
		{"Bonjour Marie", false},
		{"OK", false},     // <4 lettres
		{"FR", false},     // acronyme court
		{"HELLO", true},
		{"Hello WORLD", false}, // mixte
	}
	for _, c := range cases {
		if got := isAllCaps(c.in); got != c.want {
			t.Errorf("isAllCaps(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestScore_StripHTMLRemovesScriptStyle(t *testing.T) {
	html := `<style>.x{color:red}</style><script>alert(1)</script><p>Bonjour Marie</p>`
	out := stripHTML(html)
	if strings.Contains(out, "alert") || strings.Contains(out, "color:red") {
		t.Errorf("script/style content must be stripped, got %q", out)
	}
	if !strings.Contains(out, "Bonjour Marie") {
		t.Errorf("visible text must remain, got %q", out)
	}
}

func TestScore_URLHostHelper(t *testing.T) {
	cases := map[string]string{
		"https://track.agences-veridian.fr/r/abc?x=1": "track.agences-veridian.fr",
		"http://example.com:8080/path":                "example.com",
		"https://Foo.Bar.COM/":                        "foo.bar.com",
	}
	for in, want := range cases {
		if got := urlHost(in); got != want {
			t.Errorf("urlHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScore_DeterministicAndPure(t *testing.T) {
	in := Input{Subject: "RE: FREE $$$", Body: "<img src=x> CLICK HERE {{ y }} {a|b}", IsHTML: true, Mode: ModeStrict}
	a := Score(in)
	b := Score(in)
	if a.Score != b.Score || len(a.Rules) != len(b.Rules) {
		t.Fatalf("Score must be deterministic: %v vs %v", a, b)
	}
}

func TestScore_FloatStrFormatting(t *testing.T) {
	cases := map[float64]string{
		5.0: "5",
		5.5: "5.5",
		0.0: "0",
		2.3: "2.3",
		10:  "10",
	}
	for in, want := range cases {
		if got := floatStr(in); got != want {
			t.Errorf("floatStr(%v) = %q, want %q", in, got, want)
		}
	}
}
