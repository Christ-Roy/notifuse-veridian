package domain

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/asaskevich/govalidator"
)

// VER-220 (22/11/2026) : l'adresse d'un contact est normalisée et validée à l'ENTRÉE, pour
// toutes les voies (API contacts.upsert et contacts.import, import CSV de la console, synchro
// Prospection, inscription publique, événements) : Contact.Validate et le garde du dépôt
// (repository.VeridianContactEmailGuard) appellent cette fonction.
//
// Pourquoi : govalidator.IsEmail accepte « % » (permis par la RFC dans la partie locale) ; des
// adresses extraites de pages web sont entrées telles quelles. Relevé en prod : 51 adresses
// « 06%0d%0aemail%3ahotel…@… » (fin de téléphone, saut de ligne, « email: », export Prospection
// du 30/10) et 2 « %20legi@… » (import CSV du 06/10). Elles partaient en séquence et rebondissaient.
//
// Ce qui se corrige, parce que l'adresse voulue est sans ambiguïté :
//   - un encodage d'URL (%XX), décodé une fois ;
//   - plusieurs lignes dont UNE SEULE porte un « @ » : on garde celle-là ;
//   - espaces et caractères invisibles (espace insécable, zéro chasse, BOM) aux bords ;
//   - une étiquette en tête : « mailto: », « email: », « e-mail : », « courriel: », « mail: » ;
//   - la casse.
//
// Ce qui se refuse (erreur « invalid email format: … ») : tout ce qui reste ambigu ou abîmé
// après cela, espace ou invisible au milieu, « % » restant, deux adresses, contrôle, arobase
// double, domaine sans point, encodage invalide.
func VeridianCanonicalContactEmail(raw string) (string, error) {
	s := raw
	if veridianPctEncoded.MatchString(s) {
		dec, err := url.PathUnescape(s)
		if err != nil || !utf8.ValidString(dec) {
			return "", veridianBadEmail("encodage d'URL invalide")
		}
		s = dec
	}
	var withAt []string
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' }) {
		if strings.Contains(line, "@") {
			withAt = append(withAt, line)
		}
	}
	switch len(withAt) {
	case 0:
		return "", veridianBadEmail("aucune adresse")
	case 1:
		s = withAt[0]
	default:
		return "", veridianBadEmail("plusieurs adresses")
	}
	s = strings.TrimFunc(s, veridianEdgeTrim)
	s = veridianEmailLabel.ReplaceAllString(s, "")
	s = strings.ToLower(strings.TrimFunc(s, veridianEdgeTrim))

	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			return "", veridianBadEmail("espace dans l'adresse")
		case unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r):
			return "", veridianBadEmail("caractère de contrôle ou invisible")
		case r == '%':
			return "", veridianBadEmail("caractère encodé")
		}
	}
	at := strings.Count(s, "@")
	if at != 1 {
		return "", veridianBadEmail("il faut exactement un @")
	}
	domain := s[strings.IndexByte(s, '@')+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", veridianBadEmail("domaine invalide")
	}
	if !govalidator.IsEmail(s) {
		return "", veridianBadEmail("format refusé")
	}
	return s, nil
}

var (
	veridianPctEncoded = regexp.MustCompile(`%[0-9A-Fa-f]{2}`)
	veridianEmailLabel = regexp.MustCompile(`(?i)^(mailto|e-?mail|courriel|mail)\s*:\s*`)
)

// veridianEdgeTrim : espaces (dont insécable) et invisibles de format (zéro chasse, BOM, U+2060).
func veridianEdgeTrim(r rune) bool { return unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) }

func veridianBadEmail(why string) error { return fmt.Errorf("invalid email format: %s", why) }
