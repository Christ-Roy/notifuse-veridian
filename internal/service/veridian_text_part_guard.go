package service

import (
	"strings"
	"unicode"
)

// === Veridian — garde-fou « libellé de gabarit résiduel » (incident 2026-08-24) ===
//
// Contexte de l'incident : le gabarit Notifuse `ecom-t1-a` portait
// `<mj-preview>Ouverture observation</mj-preview>` — le préheader valait le NOM
// INTERNE du gabarit. MJML compile ça en un `<div style="display:none;…">` placé
// en tout premier nœud du <body>. L'aplatissement HTML→texte ne filtrait pas les
// nœuds invisibles : « Ouverture observation » est devenu la PREMIÈRE LIGNE du
// text/plain, juste avant « Bonjour, », et est parti tel quel dans 215 mails cold.
//
// Le correctif de fond est dans veridian_html_to_text.go (les nœuds invisibles ne
// remontent plus). Ce fichier est la SECONDE barrière : elle couvre aussi le
// chemin où le corps texte est fourni tel quel (TextContent / plain_text_only),
// que le correctif HTML→texte ne traverse pas du tout.
//
// Philosophie : refuser plutôt que réparer silencieusement. Un corps texte qui
// s'ouvre sur un libellé interne est un défaut de gabarit ; le réparer en douce
// laisserait le gabarit cassé en base et le libellé continuerait de fuiter
// ailleurs (aperçu boîte de réception, exports, autres canaux).

// veridianTextLeakMaxWords : au-delà, la première ligne est une phrase, pas un
// libellé de gabarit.
const veridianTextLeakMaxWords = 5

// veridianTextLeakLookahead : nombre de lignes non vides examinées après la
// première pour y trouver la salutation.
const veridianTextLeakLookahead = 5

// veridianGreetingWords : premiers mots qui identifient une salutation d'ouverture.
var veridianGreetingWords = map[string]struct{}{
	"bonjour": {}, "bonsoir": {}, "salut": {}, "coucou": {}, "re": {},
	"cher": {}, "chere": {}, "chère": {}, "chers": {}, "cheres": {}, "chères": {},
	"madame": {}, "monsieur": {}, "mesdames": {}, "messieurs": {},
	"hello": {}, "hi": {}, "hey": {}, "dear": {}, "good": {}, "greetings": {},
}

// veridianStripAccents rabat les diacritiques latins usuels pour comparer un mot
// à la liste des salutations sans dépendre de l'accentuation.
var veridianAccentFolder = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "á", "a", "ã", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "í", "i",
	"ô", "o", "ö", "o", "ó", "o", "õ", "o",
	"ù", "u", "û", "u", "ü", "u", "ú", "u",
	"ç", "c", "ñ", "n",
)

// veridianLineIsGreeting dit si une ligne s'ouvre sur une salutation
// (« Bonjour, », « Bonjour Marie, », « Hi Jean », « Chère Madame »…).
func veridianLineIsGreeting(line string) bool {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(line)))
	if len(fields) == 0 {
		return false
	}
	first := strings.Trim(veridianAccentFolder.Replace(fields[0]), ",.!?;:")
	if _, ok := veridianGreetingWords[first]; ok {
		return true
	}
	// Salutation modelée en Liquid : « {{ contact.first_name }}, » précédé d'un
	// mot de salutation absent — on reconnaît aussi « Bonjour {{ … }} » via le
	// cas ci-dessus, donc ici on ne traite que la salutation purement Liquid.
	return strings.HasPrefix(strings.TrimSpace(line), "{{") ||
		strings.HasPrefix(strings.TrimSpace(line), "{%")
}

// veridianLooksLikeTemplateLabel : la ligne ressemble-t-elle à un libellé de
// gabarit (« Ouverture observation », « Relance J+3 », « Séquence A ») plutôt
// qu'à une phrase rédigée ?
//
// Critères cumulatifs, volontairement stricts pour ne pas bloquer un vrai mail :
//   - 1 à 5 mots ;
//   - aucune ponctuation de phrase ni séparateur (. , ! ? ; : … " ' « ») ;
//   - pas de Liquid, pas d'URL, pas d'adresse mail ;
//   - uniquement des mots alphanumériques (lettres, chiffres, tiret, apostrophe,
//     « + » pour les « J+3 »).
func veridianLooksLikeTemplateLabel(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	if strings.Contains(line, "{{") || strings.Contains(line, "{%") {
		return false
	}
	if strings.Contains(line, "://") || strings.Contains(line, "@") ||
		strings.Contains(strings.ToLower(line), "www.") {
		return false
	}
	if strings.ContainsAny(line, ".,!?;:…\"'’«»()[]<>/|*_=") {
		return false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 || len(fields) > veridianTextLeakMaxWords {
		return false
	}
	for _, w := range fields {
		for _, r := range w {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '+' {
				continue
			}
			return false
		}
	}
	return true
}

// veridianTemplateLabelLeak inspecte un corps text/plain et retourne le libellé
// fautif si la PREMIÈRE ligne est un libellé de gabarit résiduel placé AVANT la
// salutation. Retourne "" si le corps est sain.
//
// L'exigence « avant la salutation » est ce qui rend le garde-fou sûr : une
// première ligne courte suivie d'une salutation est la signature exacte de la
// fuite de préheader, alors qu'un vrai mail commence par sa salutation ou par
// une phrase.
func veridianTemplateLabelLeak(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")

	var lines []string
	for _, l := range strings.Split(normalized, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < 2 {
		// Sans ligne suivante, impossible de constater « avant la salutation ».
		return ""
	}

	first := lines[0]
	if veridianLineIsGreeting(first) || !veridianLooksLikeTemplateLabel(first) {
		return ""
	}

	limit := veridianTextLeakLookahead + 1
	if limit > len(lines) {
		limit = len(lines)
	}
	for _, l := range lines[1:limit] {
		if veridianLineIsGreeting(l) {
			return first
		}
	}
	return ""
}
