package service

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// veridianHTMLToText dérive une représentation text/plain LISIBLE d'un corps HTML,
// pour alimenter la partie text/plain d'un multipart/alternative conforme (un vrai
// client mail — Thunderbird/Apple Mail — envoie toujours les deux représentations ;
// un HTML-only est un tell anti-spam : HTML_IMAGE_ONLY / MIME_HTML_ONLY côté
// SpamAssassin).
//
// Ce n'est PAS un renderer fidèle (pas de rendu de tableaux, pas de wrapping de
// largeur) : juste un texte propre et best-effort qui :
//   - extrait le texte VISIBLE, et uniquement lui : jamais le contenu de
//     <script>/<style>/<head>, et jamais celui d'un nœud rendu invisible
//     (display:none, visibility:hidden, opacity:0, max-height:0, attribut
//     hidden, aria-hidden="true"). Cf. incident préheader 2026-08-24 : le
//     <mj-preview> d'un gabarit MJML compile en un <div style="display:none;…">
//     premier nœud du <body> ; sans ce filtrage, le libellé interne du gabarit
//     devenait la PREMIÈRE LIGNE du text/plain, avant la salutation, et est
//     parti dans 215 mails cold ;
//   - insère des sauts de ligne aux frontières de blocs (<p>, <div>, <br>, <li>,
//     <h1..6>, <tr>, etc.) pour rester lisible ;
//   - effondre les espaces/sauts de ligne redondants ;
//   - décode les entités HTML (&amp;, &eacute;, &nbsp; …) via le parseur natif.
//
// Si l'entrée ne parse pas ou ne produit aucun texte exploitable, renvoie "" : le
// call-site retombe alors en HTML-only (non-régression stricte — on ne casse jamais
// un envoi pour ajouter une partie texte).
func veridianHTMLToText(htmlBody string) string {
	if strings.TrimSpace(htmlBody) == "" {
		return ""
	}

	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		return ""
	}

	var sb strings.Builder
	veridianWalkHTMLNode(doc, &sb)

	return veridianNormalizeText(sb.String())
}

// veridianBlockElements : éléments dont l'ouverture/fermeture doit produire un
// saut de ligne dans la sortie texte (frontières de bloc visuelles).
var veridianBlockElements = map[atom.Atom]struct{}{
	atom.P: {}, atom.Div: {}, atom.Li: {}, atom.Ul: {}, atom.Ol: {},
	atom.Tr: {}, atom.Table: {}, atom.Section: {}, atom.Article: {},
	atom.Header: {}, atom.Footer: {}, atom.Blockquote: {}, atom.Pre: {},
	atom.H1: {}, atom.H2: {}, atom.H3: {}, atom.H4: {}, atom.H5: {}, atom.H6: {},
	atom.Hr: {},
}

// veridianSkipElements : sous-arbres dont le contenu textuel ne doit JAMAIS
// apparaître dans le texte (code/style/métadonnées non visibles).
var veridianSkipElements = map[atom.Atom]struct{}{
	atom.Script: {}, atom.Style: {}, atom.Head: {}, atom.Title: {},
	atom.Noscript: {},
}

// veridianZeroLengthValues : valeurs CSS de longueur considérées comme nulles,
// quelle que soit l'unité (une longueur nulle est nulle dans toutes les unités).
func veridianCSSLengthIsZero(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	// Retire l'unité (px, pt, em, rem, %, vh, …) pour ne garder que le nombre.
	i := 0
	for i < len(v) && (v[i] == '+' || v[i] == '-' || v[i] == '.' || (v[i] >= '0' && v[i] <= '9')) {
		i++
	}
	num := v[:i]
	unit := strings.TrimSpace(v[i:])
	switch unit {
	case "", "px", "pt", "em", "rem", "ex", "ch", "vh", "vw", "cm", "mm", "in", "pc", "%":
	default:
		return false
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return false
	}
	return f == 0
}

// veridianCSSNumberIsZero : un nombre CSS sans unité (opacity) valant zéro.
func veridianCSSNumberIsZero(v string) bool {
	v = strings.TrimSuffix(strings.TrimSpace(v), "%")
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return false
	}
	return f == 0
}

// veridianStyleHidesNode analyse un attribut style inline et dit si la
// déclaration rend le nœud invisible à l'écran.
//
// Volontairement conservateur : on ne considère QUE les propriétés qui masquent
// le nœud ET tout son sous-arbre de façon non ambiguë. En particulier on ignore
// font-size:0 / line-height:0, très utilisés comme hack de gouttière dans les
// mails HTML sur des conteneurs dont les enfants redéfinissent leur taille —
// les traiter comme invisibles supprimerait du texte réellement affiché.
func veridianStyleHidesNode(style string) bool {
	for _, decl := range strings.Split(style, ";") {
		prop, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		prop = strings.ToLower(strings.TrimSpace(prop))
		value = strings.ToLower(strings.TrimSpace(value))
		// "!important" ne change pas la sémantique ici.
		value = strings.TrimSpace(strings.TrimSuffix(value, "!important"))
		value = strings.TrimSpace(strings.TrimSuffix(value, "!"))
		switch prop {
		case "display":
			if value == "none" {
				return true
			}
		case "visibility":
			if value == "hidden" || value == "collapse" {
				return true
			}
		case "opacity":
			if veridianCSSNumberIsZero(value) {
				return true
			}
		case "max-height":
			if veridianCSSLengthIsZero(value) {
				return true
			}
		}
	}
	return false
}

// veridianNodeIsInvisible : le nœud (et donc son sous-arbre) n'est pas rendu.
// Un nœud invisible ne doit JAMAIS remonter dans la partie text/plain.
func veridianNodeIsInvisible(n *html.Node) bool {
	for _, attr := range n.Attr {
		switch strings.ToLower(attr.Key) {
		case "hidden":
			// Attribut booléen HTML : sa seule présence masque le nœud.
			return true
		case "aria-hidden":
			if strings.EqualFold(strings.TrimSpace(attr.Val), "true") {
				return true
			}
		case "style":
			if veridianStyleHidesNode(attr.Val) {
				return true
			}
		}
	}
	return false
}

func veridianWalkHTMLNode(n *html.Node, sb *strings.Builder) {
	switch n.Type {
	case html.TextNode:
		sb.WriteString(n.Data)
		return
	case html.ElementNode:
		if _, skip := veridianSkipElements[n.DataAtom]; skip {
			return
		}
		// Nœud non rendu (préheader MJML, contenu ARIA masqué, pixel de suivi
		// textuel…) : on coupe tout le sous-arbre.
		if veridianNodeIsInvisible(n) {
			return
		}
		if n.DataAtom == atom.Br {
			sb.WriteByte('\n')
			return
		}
		_, isBlock := veridianBlockElements[n.DataAtom]
		if isBlock {
			sb.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			veridianWalkHTMLNode(c, sb)
		}
		if isBlock {
			sb.WriteByte('\n')
		}
		return
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			veridianWalkHTMLNode(c, sb)
		}
	}
}

// veridianNormalizeText effondre les espaces intra-ligne et limite les lignes
// vides consécutives à une seule, en supprimant les blancs de bord.
func veridianNormalizeText(raw string) string {
	// Normalise les fins de ligne.
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	// nbsp décodé par le parseur = U+00A0 → espace normal.
	raw = strings.ReplaceAll(raw, " ", " ")

	lines := strings.Split(raw, "\n")
	cleaned := make([]string, 0, len(lines))
	blankRun := 0
	for _, line := range lines {
		// Effondre les runs d'espaces/tabs intra-ligne en un seul espace.
		line = strings.TrimSpace(strings.Join(strings.Fields(line), " "))
		if line == "" {
			blankRun++
			if blankRun > 1 {
				continue
			}
			cleaned = append(cleaned, "")
			continue
		}
		blankRun = 0
		cleaned = append(cleaned, line)
	}

	out := strings.Join(cleaned, "\n")
	return strings.TrimSpace(out)
}
