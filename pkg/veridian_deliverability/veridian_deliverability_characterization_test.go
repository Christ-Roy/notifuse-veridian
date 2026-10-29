package veridian_deliverability

import (
	"strings"
	"testing"
)

// Tests de caractérisation du linter de délivrabilité (lot L4, 29/10/2026) : bornes exactes des
// règles, écrites depuis les mutants qui survivaient aux tests d'origine. Une borne décalée
// d'un cran change le verdict « à risque » affiché avant l'envoi d'une campagne.

// exact construit un texte brut de longueur n octets (hors espaces de bord), avec le préfixe donné.
func exact(prefix string, n int) string {
	if len(prefix) >= n {
		return prefix[:n]
	}
	return prefix + strings.Repeat("a", n-len(prefix))
}

func rules(r Result) []string {
	out := make([]string, 0, len(r.Rules))
	for _, x := range r.Rules {
		out = append(out, x.Name)
	}
	return out
}

func TestChar_ModeExplicitPrimeSurLaClasse(t *testing.T) {
	body := exact("http://a.example/1 ", 300)
	// Mode laxiste explicite + classe Google : le mode explicite doit gagner (pas de règle « gros provider »).
	r := Score(Input{Subject: "Question", Body: body, Mode: ModeLenient, ProviderClass: "google"})
	if hasRule(r, "LINK_IN_STRICT_MODE") {
		t.Fatalf("un mode explicite ne doit pas être remplacé par celui de la classe : %v", rules(r))
	}
	// Sans mode explicite, la classe Google impose le mode strict.
	r = Score(Input{Subject: "Question", Body: body, ProviderClass: "google"})
	if !hasRule(r, "LINK_IN_STRICT_MODE") {
		t.Fatalf("la classe google doit déduire le mode strict : %v", rules(r))
	}
}

func TestChar_BornesNombreDeLiens(t *testing.T) {
	long := func(links int) string {
		var sb strings.Builder
		for i := 0; i < links; i++ {
			sb.WriteString("http://a.example/" + string(rune('a'+i)) + " ")
		}
		return exact(sb.String(), 600)
	}
	cases := []struct {
		links int
		want  string // règle attendue ("" = aucune des deux)
	}{
		{1, ""}, {2, ""}, {3, ""}, {4, "LINKS_MANY"}, {5, "LINKS_MANY"},
	}
	for _, c := range cases {
		r := Score(Input{Subject: "Question", Body: long(c.links), Mode: ModeLenient})
		if c.want == "" && (hasRule(r, "LINKS_MANY") || hasRule(r, "LINKS_RATIO_HIGH")) {
			t.Errorf("%d liens dans un corps long : aucune règle de liens attendue, obtenu %v", c.links, rules(r))
		}
		if c.want != "" && !hasRule(r, c.want) {
			t.Errorf("%d liens : %s attendue, obtenu %v", c.links, c.want, rules(r))
		}
	}
}

func TestChar_BorneRatioLiensTexte(t *testing.T) {
	two := "http://a.example/1 http://a.example/2 "
	for _, c := range []struct {
		length int
		want   bool
	}{{399, true}, {400, false}, {401, false}} {
		r := Score(Input{Subject: "Question", Body: exact(two, c.length), Mode: ModeLenient})
		if got := hasRule(r, "LINKS_RATIO_HIGH"); got != c.want {
			t.Errorf("2 liens, corps de %d octets : LINKS_RATIO_HIGH=%v, attendu %v", c.length, got, c.want)
		}
	}
	// Un seul lien dans un corps court n'est jamais « ratio élevé ».
	r := Score(Input{Subject: "Question", Body: exact("http://a.example/1 ", 150), Mode: ModeLenient})
	if hasRule(r, "LINKS_RATIO_HIGH") {
		t.Errorf("un lien ne suffit pas : %v", rules(r))
	}
}

func TestChar_BornesLongueurDuCorps(t *testing.T) {
	for _, c := range []struct {
		length         int
		short, tooLong bool
	}{{119, true, false}, {120, false, false}, {3000, false, false}, {3001, false, true}} {
		r := Score(Input{Subject: "Question", Body: exact("Bonjour ", c.length), Mode: ModeLenient})
		if got := hasRule(r, "BODY_TOO_SHORT"); got != c.short {
			t.Errorf("corps de %d : BODY_TOO_SHORT=%v, attendu %v", c.length, got, c.short)
		}
		if got := hasRule(r, "BODY_TOO_LONG"); got != c.tooLong {
			t.Errorf("corps de %d : BODY_TOO_LONG=%v, attendu %v", c.length, got, c.tooLong)
		}
	}
}

func htmlWithImage(alt string, textLen int) string {
	return `<p>` + exact("Bonjour ", textLen) + `</p><img src="https://a.example/i.png"` + alt + `>`
}

func TestChar_HTMLImageSeule_BorneDe100Caracteres(t *testing.T) {
	r := Score(Input{Subject: "Question", Body: htmlWithImage(` alt="logo"`, 99), IsHTML: true, Mode: ModeLenient})
	if !hasRule(r, "HTML_IMAGE_ONLY") {
		t.Errorf("99 caractères de texte + image : HTML_IMAGE_ONLY attendue, obtenu %v", rules(r))
	}
	r = Score(Input{Subject: "Question", Body: htmlWithImage(` alt="logo"`, 100), IsHTML: true, Mode: ModeLenient})
	if hasRule(r, "HTML_IMAGE_ONLY") {
		t.Errorf("100 caractères : plus « image seule », obtenu %v", rules(r))
	}
	if !hasRule(r, "IMAGE_RATIO_HIGH") {
		t.Errorf("100 caractères pour une image (200 attendus) : IMAGE_RATIO_HIGH attendue, obtenu %v", rules(r))
	}
}

func TestChar_ImageSansAlt(t *testing.T) {
	avec := Score(Input{Subject: "Question", Body: htmlWithImage(` alt="logo"`, 600), IsHTML: true, Mode: ModeLenient})
	if hasRule(avec, "IMAGE_NO_ALT") {
		t.Errorf("image avec alt : pas de règle IMAGE_NO_ALT (même à poids nul), obtenu %v", rules(avec))
	}
	sans := Score(Input{Subject: "Question", Body: htmlWithImage(``, 600), IsHTML: true, Mode: ModeLenient})
	w, ok := ruleWeight(sans, "IMAGE_NO_ALT")
	if !ok || w != wImageNoAlt {
		t.Errorf("image sans alt : poids %v attendu pour une image, obtenu %v (%v)", wImageNoAlt, w, ok)
	}
	// Plafond à 4 images sans alt.
	six := `<p>` + exact("Bonjour ", 900) + `</p>` + strings.Repeat(`<img src="https://a.example/i.png">`, 6)
	w, _ = ruleWeight(Score(Input{Subject: "Question", Body: six, IsHTML: true, Mode: ModeLenient}), "IMAGE_NO_ALT")
	if w != wImageNoAlt*4 {
		t.Errorf("poids plafonné à 4 images : %v attendu, obtenu %v", wImageNoAlt*4, w)
	}
}

func TestChar_BorneSujetTropLong(t *testing.T) {
	// Les caractères accentués comptent pour un seul (runes, pas octets).
	s70 := strings.Repeat("é", 70)
	s71 := strings.Repeat("é", 71)
	if hasRule(Score(Input{Subject: s70, Body: exact("Bonjour ", 300), Mode: ModeLenient}), "SUBJECT_TOO_LONG") {
		t.Error("70 caractères : sujet accepté")
	}
	if !hasRule(Score(Input{Subject: s71, Body: exact("Bonjour ", 300), Mode: ModeLenient}), "SUBJECT_TOO_LONG") {
		t.Error("71 caractères : sujet trop long")
	}
}

func TestChar_MajusculesDuSujet_LaLettreAComptePourUneMinuscule(t *testing.T) {
	body := exact("Bonjour ", 300)
	// 8 majuscules + un « a » : 8/9 = 89 %, sous le seuil de 90 % : pas un sujet en majuscules.
	if hasRule(Score(Input{Subject: "AAAAAAAAa", Body: body, Mode: ModeLenient}), "ALL_CAPS_SUBJECT") {
		t.Error("« AAAAAAAAa » (89 % de majuscules) ne doit pas être signalé")
	}
	// 9 majuscules + un « a » : exactement 90 % : signalé.
	if !hasRule(Score(Input{Subject: "AAAAAAAAAa", Body: body, Mode: ModeLenient}), "ALL_CAPS_SUBJECT") {
		t.Error("« AAAAAAAAAa » (90 %) doit être signalé")
	}
	// Moins de 4 lettres : jamais signalé ; 4 lettres majuscules : signalé.
	if hasRule(Score(Input{Subject: "OK FR", Body: body, Mode: ModeLenient}), "ALL_CAPS_SUBJECT") == false {
		t.Error("« OK FR » compte 4 lettres, toutes en majuscules : signalé")
	}
	if hasRule(Score(Input{Subject: "OKF", Body: body, Mode: ModeLenient}), "ALL_CAPS_SUBJECT") {
		t.Error("3 lettres : un acronyme isolé n'est pas un sujet en majuscules")
	}
}

func TestChar_PixelEtDomaineDeTracking(t *testing.T) {
	// Pixel 1x1 reconnu comme tracking en mode strict.
	pixel := `<p>` + exact("Bonjour ", 300) + `</p><img src="https://t.example/p.gif" width="1" height="1">`
	r := Score(Input{Subject: "Question", Body: pixel, IsHTML: true, Mode: ModeStrict})
	if !hasRule(r, "TRACKING_IN_STRICT_MODE") {
		t.Errorf("pixel 1x1 : TRACKING_IN_STRICT_MODE attendue, obtenu %v", rules(r))
	}
	if hasRule(Score(Input{Subject: "Question", Body: exact("Bonjour ", 300), Mode: ModeStrict}), "TRACKING_IN_STRICT_MODE") {
		t.Error("sans tracking, pas de règle")
	}
	// Lien nu (sans href) vers un autre domaine que le domaine d'envoi.
	bare := exact("Voir http://autre-domaine.example/page ", 300)
	r = Score(Input{Subject: "Question", Body: bare, FromDomain: "agences-veridian.fr", Mode: ModeLenient})
	if !hasRule(r, "TRACKING_DOMAIN_MISMATCH") {
		t.Errorf("lien nu vers un autre domaine : TRACKING_DOMAIN_MISMATCH attendue, obtenu %v", rules(r))
	}
	// Lien nu vers un sous-domaine du domaine d'envoi : aligné.
	ok := exact("Voir http://t.agences-veridian.fr/page ", 300)
	if hasRule(Score(Input{Subject: "Question", Body: ok, FromDomain: "agences-veridian.fr", Mode: ModeLenient}), "TRACKING_DOMAIN_MISMATCH") {
		t.Error("un sous-domaine du domaine d'envoi est aligné")
	}
	// Quand des href existent, seuls eux comptent (un lien nu cité dans le texte ne déclenche rien).
	mixte := `<a href="https://t.agences-veridian.fr/x">voir</a> ` + exact("et http://autre-domaine.example ", 300)
	if hasRule(Score(Input{Subject: "Question", Body: mixte, IsHTML: true, FromDomain: "agences-veridian.fr", Mode: ModeLenient}), "TRACKING_DOMAIN_MISMATCH") {
		t.Error("les href priment sur les liens nus du texte")
	}
}
