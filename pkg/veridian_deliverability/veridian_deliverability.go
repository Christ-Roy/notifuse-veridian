// Package veridian_deliverability est un linter de délivrabilité (spam score)
// Go NATIF, in-process, zéro dépendance externe. Il score un email cold RENDU
// (après résolution Liquid + spintax) sur une échelle 0-10 façon SpamAssassin
// (>5 = risque) et liste les règles déclenchées avec leur poids.
//
// Décision d'archi (Robert 2026-06-15, ticket
// todo/2026-06-15-linter-deliverabilite-spam-score-templates.md) :
//   - PAS de rspamd démon, PAS de SpamAssassin/spamc, PAS de Python embarqué.
//   - Un package Go pur qui réimplémente les RÈGLES qui comptent pour le cold,
//     calquées sur les règles SpamAssassin publiques. Instantané, zéro infra.
//
// Le linter analyse TOUJOURS le RENDU FINAL (Liquid + spintax résolus), jamais
// le template brut : c'est ce que verra le destinataire, et les règles
// "personnalisation" détectent justement les variables qui FUITENT non résolues.
//
// Les poids v1 sont des estimations dérivées de SpamAssassin public + pratique
// cold (Lemlist/Instantly). Ils se calibrent ensuite sur de vrais envois et un
// rspamd local jetable servant d'oracle (cf. ticket §RAFFINEMENT). Garder les
// poids ICI (constantes nommées) pour qu'une calibration soit un diff lisible.
package veridian_deliverability

import (
	"regexp"
	"strings"
)

// Mode pondère/active les règles selon l'exigence du provider destinataire.
//
// En cold, les gros providers (Google/Microsoft) sont DRACONIENS : ils
// détestent les liens et le tracking au 1er contact, et préfèrent le plain
// text pur. Les petits providers (FAI FR, corporate, OVH, …) tolèrent un HTML
// léger, des liens et du tracking. Le mode se déduit de la classe de provider
// destinataire (VeridianModeForClass) ou est forcé en preview.
type Mode int

const (
	// ModeDefault : profil neutre, équilibré. Utilisé quand la classe
	// destinataire est inconnue/non spécifiée.
	ModeDefault Mode = iota
	// ModeStrict : gros providers (Google/Microsoft). Liens, tracking et HTML
	// fortement pénalisés ; plain text récompensé.
	ModeStrict
	// ModeLenient : petits providers (freemail_fr, corporate, OVH, …). Liens,
	// tracking et HTML léger tolérés.
	ModeLenient
)

func (m Mode) String() string {
	switch m {
	case ModeStrict:
		return "strict"
	case ModeLenient:
		return "lenient"
	default:
		return "default"
	}
}

// VeridianModeForClass mappe une classe de provider destinataire (cf.
// internal/domain/veridian_provider_class.go) au mode de linter approprié.
// Google/Microsoft = strict ; toutes les autres classes connues = lenient ;
// classe vide/inconnue = default. Garde le linter découplé du package domain
// (zéro import) : l'appelant passe la string de classe canonique.
func VeridianModeForClass(class string) Mode {
	switch strings.ToLower(strings.TrimSpace(class)) {
	case "google", "microsoft", "apple_icloud":
		// Apple iCloud est aussi très strict en réputation → profil strict.
		return ModeStrict
	case "yahoo_aol", "freemail_fr", "corporate", "ovh", "ionos",
		"security_gateway", "other_hoster", "corporate_selfhost":
		return ModeLenient
	default:
		return ModeDefault
	}
}

// Input décrit l'email RENDU à analyser. Subject et Body sont le rendu final
// (Liquid + spintax déjà résolus côté appelant — le linter détecte ce qui n'a
// PAS été résolu). IsHTML distingue un corps HTML d'un plain text pur (en plain
// text, les règles HTML_IMAGE_ONLY / images-sans-alt / ratio-image sautent).
// FromDomain (optionnel) sert à détecter un domaine de tracking ≠ domaine
// d'envoi (signal anti-spam). ProviderClass (optionnel) déduit le mode si Mode
// n'est pas forcé.
type Input struct {
	Subject       string
	Body          string
	IsHTML        bool
	FromDomain    string // domaine du From, ex. "agences-veridian.fr" (optionnel)
	ProviderClass string // classe destinataire canonique (optionnel)
	Mode          Mode   // override explicite ; sinon déduit de ProviderClass
}

// Rule est une règle déclenchée : un nom stable (façon SA, ex.
// "ALL_CAPS_SUBJECT"), son poids appliqué (positif = pénalité, négatif =
// bonus) et un message lisible expliquant QUOI corriger.
type Rule struct {
	Name    string  `json:"name"`
	Weight  float64 `json:"weight"`
	Message string  `json:"message"`
}

// Result est le verdict du linter : score borné 0-10, dépassement de seuil,
// mode effectif appliqué, et la liste ordonnée (poids décroissant) des règles
// déclenchées.
type Result struct {
	Score   float64 `json:"score"`           // 0-10, borné
	IsRisky bool    `json:"is_risky"`        // true si Score > RiskThreshold
	Mode    string  `json:"mode"`            // "default" | "strict" | "lenient"
	Rules   []Rule  `json:"rules"`           // règles déclenchées, poids décroissant
	Summary string  `json:"summary"`         // verdict humain en une phrase
}

// RiskThreshold : au-dessus, l'email est considéré à risque (façon SA, le seuil
// de classification spam par défaut est ~5).
const RiskThreshold = 5.0

// Poids des règles (constantes nommées pour calibration lisible). Positifs =
// pénalité, négatifs = bonus. Calqués sur SpamAssassin public + pratique cold.
const (
	wLinksRatioHigh      = 1.5 // beaucoup de liens / peu de texte
	wLinkStrict          = 1.2 // chaque lien en mode strict (Google/MS) — par lien au-delà de 0
	wTrackingDomainMismatch = 1.0 // lien de tracking sur domaine ≠ From
	wBodyTooShort        = 1.5 // corps quasi vide ("check this out")
	wBodyTooLong         = 1.0 // corps interminable
	wImageOnly           = 3.0 // HTML quasi sans texte (HTML_IMAGE_ONLY)
	wHTMLNoText          = 1.5 // HTML-only, pas de version texte fournie (MIME_HTML_ONLY)
	wImageRatioHigh      = 1.5 // trop d'images par rapport au texte
	wImageNoAlt          = 0.5 // par image sans alt (plafonné)
	wHTMLInStrict        = 1.5 // HTML là où le plain text est attendu (gros providers)
	wPlainTextBonus      = -0.5 // bonus plain text en mode strict
	wSpammyWordBase      = 0.0  // base ; chaque mot porte son propre poids (table)
	wAllCapsSubject      = 2.0 // sujet tout en MAJUSCULES
	wAllCapsBodyChunk    = 1.0 // longue séquence MAJUSCULES dans le corps
	wExcessivePunct      = 0.8 // !!!, ???, $$$ répétés
	wSubjectTooLong      = 0.5 // sujet > 70 caractères
	wSubjectEmpty        = 1.0 // sujet vide
	wFakeRe              = 1.5 // faux "Re:" / "Fwd:" sur un 1er contact
	wEmojiBurst          = 0.8 // rafale d'emojis dans le sujet
	wLiquidUnresolved    = 3.0 // {{ var }} qui fuit dans le rendu (bug amateur)
	wSpintaxUnresolved   = 2.5 // {A|B} qui fuit dans le rendu
	wNoPersonalization   = 0.5 // aucune trace de personnalisation (générique/bulk)
	wTrackingStrict      = 1.0 // présence de tracking en mode strict
)

// veridianSpammyWords : mots/phrases déclencheurs pondérés. Les poids sont
// volontairement modestes (un seul mot ne doit pas faire basculer) mais
// s'additionnent. Recherche insensible à la casse, sur frontière de mot quand
// pertinent. Liste v1 calquée sur les déclencheurs SA/cold les plus connus.
var veridianSpammyWords = []struct {
	pattern *regexp.Regexp
	name    string
	weight  float64
}{
	{regexp.MustCompile(`(?i)\b100\s*%\s*(free|guaranteed|satisfied)\b`), "WORD_100_PERCENT", 1.2},
	{regexp.MustCompile(`(?i)\bclick here\b`), "WORD_CLICK_HERE", 1.0},
	{regexp.MustCompile(`(?i)\bact now\b`), "WORD_ACT_NOW", 1.0},
	{regexp.MustCompile(`(?i)\bbuy now\b`), "WORD_BUY_NOW", 0.8},
	{regexp.MustCompile(`(?i)\border now\b`), "WORD_ORDER_NOW", 0.8},
	{regexp.MustCompile(`(?i)\blimited time\b`), "WORD_LIMITED_TIME", 0.7},
	{regexp.MustCompile(`(?i)\bguarantee(d)?\b`), "WORD_GUARANTEE", 0.6},
	{regexp.MustCompile(`(?i)\brisk[\s-]?free\b`), "WORD_RISK_FREE", 0.8},
	{regexp.MustCompile(`(?i)\bcash\b`), "WORD_CASH", 0.5},
	{regexp.MustCompile(`(?i)\bwinner\b`), "WORD_WINNER", 0.8},
	{regexp.MustCompile(`(?i)\bcongratulations\b`), "WORD_CONGRATS", 0.6},
	{regexp.MustCompile(`(?i)\bviagra\b`), "WORD_PHARMA", 2.0},
	{regexp.MustCompile(`(?i)\bcheap\b`), "WORD_CHEAP", 0.5},
	{regexp.MustCompile(`(?i)\bdiscount\b`), "WORD_DISCOUNT", 0.4},
	{regexp.MustCompile(`(?i)\bspecial promotion\b`), "WORD_SPECIAL_PROMO", 0.6},
	{regexp.MustCompile(`(?i)\bno obligation\b`), "WORD_NO_OBLIGATION", 0.6},
	{regexp.MustCompile(`(?i)\bunsubscribe\b`), "WORD_UNSUBSCRIBE", 0.3}, // cold = pas d'unsub façon Lemlist
	{regexp.MustCompile(`(?i)\bfree\b`), "WORD_FREE", 0.5},
	{regexp.MustCompile(`(?i)\$\$\$+`), "WORD_DOLLARS", 1.0},
	{regexp.MustCompile(`(?i)\bearn \$`), "WORD_EARN_MONEY", 1.2},
	{regexp.MustCompile(`(?i)\bdouble your\b`), "WORD_DOUBLE_YOUR", 1.0},
}

var (
	reHref          = regexp.MustCompile(`(?i)href\s*=\s*["']?(https?://[^"'\s>]+)`)
	reBareURL       = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"')]+`)
	reImgTag        = regexp.MustCompile(`(?i)<img\b[^>]*>`)
	reImgHasAlt     = regexp.MustCompile(`(?i)\balt\s*=\s*["'][^"']*[^"'\s][^"']*["']`)
	reHTMLTag       = regexp.MustCompile(`(?i)<[a-z!/][^>]*>`)
	reLiquidVar     = regexp.MustCompile(`{{[^}]*}}|{%[^%]*%}`)
	reSpintax       = regexp.MustCompile(`{[^{}]*\|[^{}]*}`)
	reExcessPunct   = regexp.MustCompile(`!{3,}|\?{3,}|\${3,}|[!?]{4,}`) // !!!, ???, $$$, mix !?!?
	reFakeRe        = regexp.MustCompile(`(?i)^\s*(re|fwd?)\s*:\s*`) // "Re:" / "Fw:" / "Fwd:"
	reCapsRun       = regexp.MustCompile(`\b[A-Z]{2,}(?:[\s-][A-Z]{2,}){2,}\b`)
	reEmoji         = regexp.MustCompile(`[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}\x{2190}-\x{21FF}\x{2B00}-\x{2BFF}]`)
	reWhitespaceRun = regexp.MustCompile(`\s+`)
	reScriptStyle   = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)>`)
	reHasDigit      = regexp.MustCompile(`\d`)
	reProperNoun    = regexp.MustCompile(`[a-z]\s+[A-Z][a-z]{2,}`) // mot Capitalisé après une minuscule = nom propre probable
)

// Score est la fonction PURE cœur du linter : zéro I/O, déterministe, instantanée.
// Elle prend un email rendu et renvoie le verdict (score borné 0-10 + règles).
func Score(in Input) Result {
	mode := in.Mode
	if mode == ModeDefault && in.ProviderClass != "" {
		mode = VeridianModeForClass(in.ProviderClass)
	}

	var rules []Rule
	add := func(name string, weight float64, msg string) {
		rules = append(rules, Rule{Name: name, Weight: weight, Message: msg})
	}

	subject := in.Subject
	body := in.Body
	// Texte "visible" : pour du HTML on dé-balise grossièrement afin de
	// raisonner sur le texte réel (ratios, longueur, MAJUSCULES).
	visibleText := body
	if in.IsHTML {
		visibleText = stripHTML(body)
	}
	visibleLen := len(strings.TrimSpace(visibleText))

	// --- 🔴 Structurel : liens ---
	links := countLinks(body)
	switch {
	case mode == ModeStrict && links > 0:
		// Gros providers : pénalité PAR lien au 1er contact.
		add("LINK_IN_STRICT_MODE", wLinkStrict*float64(min(links, 3)),
			"Liens présents : les gros providers (Google/Microsoft) pénalisent fortement les liens au premier contact cold. Idéal = 0 lien.")
	case links >= 4:
		add("LINKS_MANY", wLinksRatioHigh+0.5,
			"Beaucoup de liens : un cold 1-to-1 devrait en contenir 0 ou 1. Trop de liens = signal bulk.")
	case links >= 2 && visibleLen < 400:
		add("LINKS_RATIO_HIGH", wLinksRatioHigh,
			"Ratio liens/texte élevé : plusieurs liens dans un corps court ressemble à du spam. Réduire à 0-1 lien.")
	}

	// Tracking sur domaine ≠ From.
	if in.FromDomain != "" {
		if mismatch := trackingDomainMismatch(body, in.FromDomain); mismatch != "" {
			add("TRACKING_DOMAIN_MISMATCH", wTrackingDomainMismatch,
				"Lien vers un domaine ("+mismatch+") différent du domaine d'envoi ("+in.FromDomain+") : mismatch perçu comme anti-spam. Aligner le tracking sur le sous-domaine d'envoi.")
		}
	}

	// Tracking pixel/redirect présent en mode strict.
	if mode == ModeStrict && hasTrackingArtifacts(body) {
		add("TRACKING_IN_STRICT_MODE", wTrackingStrict,
			"Tracking (pixel d'ouverture / redirect de clic) détecté vers un gros provider : recommandé OFF en cold vers Google/Microsoft.")
	}

	// --- 🔴 Structurel : longueur du corps ---
	switch {
	case visibleLen < 120:
		add("BODY_TOO_SHORT", wBodyTooShort,
			"Corps trop court : un message d'une ligne ('check this out') est un signal spam. Écrire un message personnalisé substantiel.")
	case visibleLen > 3000:
		add("BODY_TOO_LONG", wBodyTooLong,
			"Corps très long : un cold efficace est court et ciblé. Raccourcir.")
	}

	// --- 🔴 Structurel : HTML spécifique ---
	if in.IsHTML {
		if mode == ModeStrict {
			add("HTML_IN_STRICT_MODE", wHTMLInStrict,
				"HTML en cold vers un gros provider : le plain text pur passe mieux au premier contact. Préférer un mail texte.")
		}
		// MIME_HTML_ONLY : HTML sans partie texte fournie (heuristique : on
		// n'a qu'un body HTML, pas de version texte). On le signale toujours en
		// HTML — c'est à l'envoi de fournir un multipart text+HTML.
		add("MIME_HTML_ONLY_RISK", wHTMLNoText,
			"HTML sans version texte alternative : envoyer en multipart text+HTML (sinon règle SA MIME_HTML_ONLY).")

		imgCount := len(reImgTag.FindAllString(body, -1))
		if imgCount > 0 {
			// HTML_IMAGE_ONLY : beaucoup d'images, presque pas de texte.
			if visibleLen < 100 {
				add("HTML_IMAGE_ONLY", wImageOnly,
					"HTML quasi sans texte avec image(s) : règle SA HTML_IMAGE_ONLY (très pénalisante). Ajouter du vrai texte.")
			} else if imgCount*200 > visibleLen { // ~200 chars de texte attendus par image
				add("IMAGE_RATIO_HIGH", wImageRatioHigh,
					"Trop d'images par rapport au texte : viser un ratio texte/image sain.")
			}
			// Images sans alt.
			noAlt := imagesWithoutAlt(body)
			if noAlt > 0 {
				w := wImageNoAlt * float64(min(noAlt, 4)) // plafonné
				add("IMAGE_NO_ALT", w,
					"Image(s) sans attribut alt : nuit à l'accessibilité et à la délivrabilité. Ajouter un alt descriptif.")
			}
		}
	} else if mode == ModeStrict {
		// Plain text en mode strict = exactement ce que veulent les gros
		// providers → bonus.
		add("PLAIN_TEXT_BONUS", wPlainTextBonus,
			"Plain text pur vers un gros provider : profil idéal pour le premier contact cold.")
	}

	// --- 🟡 Contenu : mots spammy ---
	haystack := subject + "\n" + visibleText
	for _, sw := range veridianSpammyWords {
		if sw.pattern.MatchString(haystack) {
			add("SPAMMY_"+sw.name, sw.weight,
				"Terme/expression spammy détecté : '"+humanizeRuleName(sw.name)+"'. Reformuler.")
		}
	}

	// --- 🟡 Contenu : sujet ---
	switch {
	case strings.TrimSpace(subject) == "":
		add("SUBJECT_EMPTY", wSubjectEmpty,
			"Sujet vide : un mail sans objet est suspect. Écrire un objet court et personnalisé.")
	default:
		if isAllCaps(subject) {
			add("ALL_CAPS_SUBJECT", wAllCapsSubject,
				"Sujet tout en MAJUSCULES (SA SUBJ_ALL_CAPS) : très pénalisant. Écrire en casse normale.")
		}
		if len([]rune(subject)) > 70 {
			add("SUBJECT_TOO_LONG", wSubjectTooLong,
				"Sujet trop long (>70 caractères) : tronqué dans la plupart des clients. Raccourcir.")
		}
		if reFakeRe.MatchString(subject) {
			add("FAKE_RE_SUBJECT", wFakeRe,
				"Faux 'Re:'/'Fwd:' sur un premier contact : trompe le destinataire, signal manipulateur. Supprimer.")
		}
		if emojiCount(subject) >= 3 {
			add("SUBJECT_EMOJI_BURST", wEmojiBurst,
				"Rafale d'emojis dans le sujet : signal promotionnel/bulk. Réduire à 0-1 emoji.")
		}
	}

	// Ponctuation excessive (sujet + corps).
	if reExcessPunct.MatchString(subject) || reExcessPunct.MatchString(visibleText) {
		add("EXCESSIVE_PUNCTUATION", wExcessivePunct,
			"Ponctuation excessive (!!!, ???, $$$) : signal spam classique. Une seule marque suffit.")
	}

	// Longue séquence MAJUSCULES dans le corps.
	if reCapsRun.MatchString(visibleText) {
		add("BODY_CAPS_RUN", wAllCapsBodyChunk,
			"Longue séquence en MAJUSCULES dans le corps : perçu comme criard/spam. Écrire en casse normale.")
	}

	// --- 🟢 Personnalisation / fraîcheur ---
	if reLiquidVar.MatchString(subject) || reLiquidVar.MatchString(body) {
		add("LIQUID_UNRESOLVED", wLiquidUnresolved,
			"Variable Liquid non résolue ({{ ... }} ou {% ... %}) visible dans le rendu : bug — le destinataire verra le code brut. Vérifier les données de contact.")
	}
	if reSpintax.MatchString(subject) || reSpintax.MatchString(body) {
		add("SPINTAX_UNRESOLVED", wSpintaxUnresolved,
			"Spintax non résolu ({A|B}) visible dans le rendu : le moteur n'a pas substitué la variante. Bug à corriger.")
	}
	// Personnalisation : aucune trace de prénom/société interpolés ET aucune
	// variable Liquid (déjà résolue OU absente). Heuristique : on considère
	// "personnalisé" si le corps contient un marqueur de personnalisation
	// AVANT rendu (variable Liquid restée — déjà couverte ci-dessus) OU si le
	// rendu mentionne quelque chose de spécifique. Faute de signal fiable
	// post-rendu, on flague seulement le cas "ni Liquid, ni rien de spécifique"
	// pour un corps très générique court — léger.
	if !reLiquidVar.MatchString(body) && looksGeneric(visibleText) {
		add("NO_PERSONALIZATION", wNoPersonalization,
			"Aucune trace de personnalisation : un cold générique ressemble à du bulk. Personnaliser (prénom, société, accroche spécifique).")
	}

	// --- Agrégation ---
	score := 0.0
	for _, r := range rules {
		score += r.Weight
	}
	if score < 0 {
		score = 0
	}
	if score > 10 {
		score = 10
	}
	score = round1(score)

	sortRulesByWeightDesc(rules)

	res := Result{
		Score:   score,
		IsRisky: score > RiskThreshold,
		Mode:    mode.String(),
		Rules:   rules,
	}
	res.Summary = buildSummary(res)
	return res
}

// --- Helpers (purs) ---

// stripHTML retire grossièrement les balises et décode quelques entités pour
// estimer le texte VISIBLE. Pas un parseur HTML complet (inutile pour un
// linter heuristique) : suffisant pour les ratios et la longueur.
func stripHTML(html string) string {
	// Retire script/style et leur contenu (sinon comptés comme "texte").
	html = reScriptStyle.ReplaceAllString(html, " ")
	text := reHTMLTag.ReplaceAllString(html, " ")
	text = strings.NewReplacer(
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&apos;", "'",
	).Replace(text)
	return reWhitespaceRun.ReplaceAllString(text, " ")
}

// countLinks compte les liens cliquables : href= dans du HTML, ou URLs nues en
// plain text. Dédоublonne grossièrement par déduplication des matches href ET
// bare (un href est aussi une URL nue) en prenant le max raisonnable.
func countLinks(body string) int {
	hrefs := reHref.FindAllString(body, -1)
	if len(hrefs) > 0 {
		return len(hrefs)
	}
	return len(reBareURL.FindAllString(body, -1))
}

// trackingDomainMismatch retourne le 1er domaine de lien qui n'appartient pas
// au domaine d'envoi (ni sous-domaine), ou "" si tous alignés / aucun lien.
// Les liens mailto/tel sont ignorés.
func trackingDomainMismatch(body, fromDomain string) string {
	from := strings.ToLower(strings.TrimSpace(fromDomain))
	if from == "" {
		return ""
	}
	urls := reHref.FindAllStringSubmatch(body, -1)
	if len(urls) == 0 {
		for _, u := range reBareURL.FindAllString(body, -1) {
			urls = append(urls, []string{u, u})
		}
	}
	for _, m := range urls {
		host := urlHost(m[1])
		if host == "" {
			continue
		}
		if host == from || strings.HasSuffix(host, "."+from) {
			continue
		}
		return host
	}
	return ""
}

// urlHost extrait le host d'une URL http(s) (lowercase, sans port).
func urlHost(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(u), "https://"), "http://")
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.IndexByte(u, ':'); i >= 0 {
		u = u[:i]
	}
	return u
}

// hasTrackingArtifacts détecte un pixel d'ouverture (img 1x1 / chemin /t/) ou
// un redirect de clic (/r/) typiques du tracking Notifuse.
func hasTrackingArtifacts(body string) bool {
	low := strings.ToLower(body)
	if strings.Contains(low, "/t/") || strings.Contains(low, "/r/") {
		return true
	}
	// Pixel 1x1 classique.
	if regexp.MustCompile(`(?i)<img[^>]*(width\s*=\s*["']?1["']?|height\s*=\s*["']?1["']?)`).MatchString(body) {
		return true
	}
	return false
}

// imagesWithoutAlt compte les <img> dépourvus d'attribut alt non vide.
func imagesWithoutAlt(body string) int {
	n := 0
	for _, tag := range reImgTag.FindAllString(body, -1) {
		if !reImgHasAlt.MatchString(tag) {
			n++
		}
	}
	return n
}

// isAllCaps : true si le texte contient au moins quelques lettres et qu'elles
// sont (quasi) toutes en MAJUSCULES. On exige >=4 lettres pour éviter de
// flagger un acronyme isolé ("OK", "FR").
func isAllCaps(s string) bool {
	letters, uppers := 0, 0
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			letters++
		} else if r >= 'A' && r <= 'Z' {
			letters++
			uppers++
		}
	}
	if letters < 4 {
		return false
	}
	return float64(uppers)/float64(letters) >= 0.9
}

// looksGeneric : heuristique "template bulk générique" — corps court ET
// dépourvu de tout marqueur qui suggère une personnalisation/spécificité
// (chiffres, nom propre capitalisé en milieu de phrase, etc.). Volontairement
// conservateur (poids faible) : on ne veut pas crier "générique" à tort.
func looksGeneric(text string) bool {
	t := strings.TrimSpace(text)
	if len([]rune(t)) > 350 {
		return false // un corps substantiel n'est pas "générique court"
	}
	// Indices de spécificité : un chiffre (date, durée, montant) ⇒ probablement
	// pas un template générique.
	if reHasDigit.MatchString(t) {
		return false
	}
	// Présence d'au moins un mot capitalisé APRÈS une minuscule (nom propre /
	// société en milieu de phrase) ⇒ probablement personnalisé.
	if reProperNoun.MatchString(t) {
		return false
	}
	return true
}

func emojiCount(s string) int { return len(reEmoji.FindAllString(s, -1)) }

func humanizeRuleName(name string) string {
	s := strings.TrimPrefix(name, "WORD_")
	s = strings.ReplaceAll(s, "_", " ")
	return strings.ToLower(s)
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// sortRulesByWeightDesc trie en place par poids décroissant (les pénalités les
// plus lourdes en tête), départage par nom pour un rendu déterministe.
func sortRulesByWeightDesc(rules []Rule) {
	for i := 1; i < len(rules); i++ {
		for j := i; j > 0; j-- {
			a, b := rules[j-1], rules[j]
			if a.Weight < b.Weight || (a.Weight == b.Weight && a.Name > b.Name) {
				rules[j-1], rules[j] = rules[j], rules[j-1]
			} else {
				break
			}
		}
	}
}

// buildSummary produit un verdict humain en une phrase.
func buildSummary(r Result) string {
	switch {
	case r.Score <= 2:
		return "Bon profil de délivrabilité (score " + floatStr(r.Score) + "/10) — peu de signaux spam."
	case r.Score <= RiskThreshold:
		return "Profil correct mais perfectible (score " + floatStr(r.Score) + "/10) — quelques signaux à corriger."
	default:
		return "RISQUE spam élevé (score " + floatStr(r.Score) + "/10, seuil " + floatStr(RiskThreshold) + ") — corriger les règles listées avant envoi."
	}
}

func floatStr(f float64) string {
	// Évite "5" → "5.0" pour la lisibilité ; tronque à 1 décimale.
	whole := int(f)
	frac := int(round1(f-float64(whole)) * 10)
	if frac == 0 {
		return itoa(whole)
	}
	return itoa(whole) + "." + itoa(frac)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
