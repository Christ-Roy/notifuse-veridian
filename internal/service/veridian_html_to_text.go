package service

import (
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
//   - extrait le texte visible (jamais le contenu de <script>/<style>/<head>) ;
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

func veridianWalkHTMLNode(n *html.Node, sb *strings.Builder) {
	switch n.Type {
	case html.TextNode:
		sb.WriteString(n.Data)
		return
	case html.ElementNode:
		if _, skip := veridianSkipElements[n.DataAtom]; skip {
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
