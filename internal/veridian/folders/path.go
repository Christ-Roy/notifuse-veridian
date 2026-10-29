package folders

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Espaces au sens de JavaScript (String.prototype.trim, \s) : la console parsait les noms
// avec ces regles, la conversion doit rendre exactement les memes chemins.
const jsSpace = `[\s\x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}]`

var (
	separatorRE = regexp.MustCompile(`(?:` + jsSpace + `+/)+` + jsSpace + `+`)
	leadingRE   = regexp.MustCompile(`^(?:/` + jsSpace + `+)+`)
	trailingRE  = regexp.MustCompile(`(?:` + jsSpace + `+/)+$`)
)

func isJSSpace(r rune) bool {
	switch {
	case r == '\t', r == '\n', r == '\v', r == '\f', r == '\r', r == ' ',
		r == 0x00A0, r == 0x1680, r >= 0x2000 && r <= 0x200A,
		r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000, r == 0xFEFF:
		return true
	}
	return false
}

func trimJS(s string) string { return strings.TrimFunc(s, isJSSpace) }

// ParsePath decoupe un nom historique `A / B / C` en chemin. Reprend a l'identique
// parseSegmentPath de la console : un slash colle sans espace (« AC/DC », « 1/2 ») n'est
// pas un separateur ; separateurs repetes, de tete et de queue toleres ; un nom sans
// separateur donne un chemin a un seul element (le nom, tel quel).
func ParsePath(name string) []string {
	trimmed := trimJS(name)
	cleaned := trimJS(trailingRE.ReplaceAllString(leadingRE.ReplaceAllString(trimmed, ""), ""))
	var parts []string
	for _, p := range separatorRE.Split(cleaned, -1) {
		if p = trimJS(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return []string{trimmed}
	}
	return parts
}

// HasSeparator dit si le nom porte au moins un separateur de dossier.
func HasSeparator(name string) bool { return len(ParsePath(name)) > 1 }

// JoinPath assemble un chemin avec le separateur de compatibilite.
func JoinPath(path []string) string { return strings.Join(path, Separator) }

// CompatName est le nom stocke d'un objet de libelle `label` range sous `path`.
func CompatName(path []string, label string) string {
	if len(path) == 0 {
		return label
	}
	return JoinPath(path) + Separator + label
}

// samePath compare deux chemins sans tenir compte de la casse (l'unicite des freres l'ignore).
func samePath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

// LabelFor donne le libelle court d'un objet range sous `path`. Si le nom commence par ce
// chemin (comparaison par elements, tolerante aux espaces et a la casse), c'est le dernier
// element ; sinon le nom entier est le libelle (renommage fait ailleurs sans prefixe).
func LabelFor(name string, path []string) string {
	if len(path) == 0 {
		return name
	}
	parts := ParsePath(name)
	if len(parts) == len(path)+1 && samePath(parts[:len(path)], path) {
		return parts[len(parts)-1]
	}
	return name
}

// separatorInside detecte un separateur de dossier a l'interieur d'un nom (« A / B », « / A », « A / »).
func separatorInside(s string) bool {
	if separatorRE.MatchString(s) {
		return true
	}
	t := trimJS(s)
	return leadingRE.MatchString(t) || trailingRE.MatchString(t)
}

// ValidateName valide un nom de dossier ou un libelle d'objet : non vide, sans espaces en
// tete ou en queue, pas de separateur `/` entoure d'espaces (ce serait recreer, par accident,
// la convention de nom), longueur bornee, pas de caractere de controle.
func ValidateName(what, name string) error {
	if trimJS(name) == "" {
		return fmt.Errorf("%s : le nom ne peut pas etre vide", what)
	}
	if name != trimJS(name) {
		return fmt.Errorf("%s : le nom ne doit pas commencer ni finir par un espace", what)
	}
	if utf8.RuneCountInString(name) > MaxNameLen {
		return fmt.Errorf("%s : %d caracteres au maximum", what, MaxNameLen)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s : caractere de controle interdit", what)
		}
	}
	if separatorInside(name) {
		return fmt.Errorf("%s : « / » entoure d'espaces est interdit dans un nom (c'est l'ancienne convention de chemin : utilisez des sous-dossiers)", what)
	}
	return nil
}

// naturalCollator reproduit Intl.Collator('fr', {numeric: true, sensitivity: 'base'}) de la
// console : ordre naturel (P2 avant P10), sans casse ni accents.
var naturalCollator = collate.New(language.French, collate.Numeric, collate.IgnoreCase, collate.IgnoreDiacritics)

// NaturalLess ordonne deux libelles comme la console le faisait.
func NaturalLess(a, b string) bool { return naturalCollator.CompareString(a, b) < 0 }

// NaturalCompare : -1, 0, 1.
func NaturalCompare(a, b string) int { return naturalCollator.CompareString(a, b) }
