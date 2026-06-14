// Package veridian_spintax implémente la résolution de syntaxe "spintax" pour le
// cold outbound Veridian.
//
// Contexte cold : envoyer 500 mails au HTML identique mot pour mot crée une
// signature de spam triviale, immédiatement détectée par les filtres bayésiens
// et les empreintes de contenu (fuzzy hashing type Nilsimsa/ssdeep). Le cold
// professionnel varie le corps via spintax : `{option A|option B|option C}`.
// Chaque destinataire reçoit une variante, ce qui casse l'empreinte commune.
//
// Propriétés clés de cette implémentation :
//
//   - DÉTERMINISTE PAR DESTINATAIRE. La variante choisie pour chaque groupe est
//     une fonction pure du seed (l'email du contact) et de la POSITION du groupe
//     dans le texte. Conséquences voulues :
//
//   - re-render identique → même sortie (debug, idempotence, audit) ;
//
//   - deux destinataires différents → variantes potentiellement différentes ;
//
//   - deux groupes différents dans le même mail varient indépendamment.
//
//   - NESTING. `{Bonjour {Monsieur|Madame}|Salut}` est géré récursivement :
//     l'option choisie au niveau externe est elle-même résolue.
//
//   - LIQUID PRÉSERVÉ. Les variables `{{ ... }}` et les tags `{% ... %}` ne sont
//     JAMAIS altérés ni interprétés comme du spintax. Le parseur ne touche QUE
//     les accolades SIMPLES `{...|...}`. Une variable Liquid imbriquée dans une
//     option spintax (`{Bonjour {{contact.first_name}}|Salut}`) est recopiée
//     intacte si l'option est choisie, et reste rendable par Liquid ensuite.
//
//   - NO-OP STRICT. Un texte sans spintax (aucun groupe `{...|...}` valide)
//     ressort STRICTEMENT identique à l'entrée. Non-régression totale pour tous
//     les templates existants : appeler ResolveSpintax sur du contenu normal ne
//     le modifie pas d'un octet.
//
//   - ROBUSTE. Accolades déséquilibrées, EOF au milieu d'un groupe, Liquid
//     non fermé : best-effort, jamais de panic. Le contenu non résolvable est
//     recopié tel quel.
package veridian_spintax

import (
	"hash/fnv"
	"strings"
)

// maxNestingDepth borne la profondeur de récursion de résolution. Garde-fou
// défensif contre un contenu pathologiquement imbriqué (`{a|{a|{a|...}}}`) qui
// pourrait faire grossir la pile. Bien au-delà de tout usage cold légitime
// (un template humain dépasse rarement 2-3 niveaux). Au-delà, le contenu du
// niveau trop profond est recopié verbatim (best-effort, jamais de panic).
const maxNestingDepth = 64

// ResolveSpintax résout la syntaxe spintax de input en choisissant, pour chaque
// groupe `{a|b|c}`, une option déterminée par seed et la position du groupe.
//
// seed = l'identité du destinataire (typiquement son email). Pour un même seed,
// la sortie est stable. Un seed vide reste accepté (toutes les variantes sont
// alors choisies de façon déterministe à partir du seed vide).
//
// Si input ne contient aucun groupe spintax valide, la sortie est égale à
// l'entrée (no-op strict).
func ResolveSpintax(input string, seed string) string {
	// Fast-path no-op : pas d'accolade ouvrante du tout → rien à faire. Évite
	// d'allouer quoi que ce soit pour l'immense majorité des templates sans
	// spintax.
	if !strings.ContainsRune(input, '{') {
		return input
	}

	p := &parser{
		input:    input,
		seedHash: hashSeed(seed),
	}
	var b strings.Builder
	b.Grow(len(input))
	p.parseInto(&b, 0, len(input), 0)
	return b.String()
}

// parser porte l'état de résolution. groupIndex est incrémenté à chaque groupe
// spintax rencontré (dans l'ordre de lecture gauche→droite, y compris les
// groupes imbriqués), pour que des groupes distincts varient indépendamment.
type parser struct {
	input    string
	seedHash uint64
	// groupIndex : compteur global de groupes spintax résolus. Mélangé au seed
	// pour dériver le choix de chaque groupe.
	groupIndex uint64
}

// parseInto résout récursivement la portion input[start:end] et écrit le
// résultat dans b. Le contenu hors groupes spintax est recopié verbatim ; les
// blocs Liquid `{{...}}` / `{%...%}` sont recopiés verbatim ; les groupes
// `{...|...}` sont résolus. depth = profondeur de récursion courante (borne
// défensive maxNestingDepth).
func (p *parser) parseInto(b *strings.Builder, start, end, depth int) {
	// Garde-fou anti-récursion : au-delà de la borne, recopier le reste verbatim
	// sans tenter de résoudre (le contenu trop profond est rendu tel quel plutôt
	// que de risquer un stack overflow sur input adverse).
	if depth > maxNestingDepth {
		b.WriteString(p.input[start:end])
		return
	}

	i := start
	for i < end {
		c := p.input[i]

		if c == '{' {
			// Bloc Liquid `{{ ... }}` ou `{% ... %}` : recopier intact, ne PAS
			// traiter comme spintax. copyLiquidVar/copyLiquidTag renvoient l'index
			// après le bloc (ou -1 si non fermé → la `{` retombe en accolade simple).
			if next := i + 1; next < end {
				switch p.input[next] {
				case '{':
					if j := p.copyLiquidVar(b, i, end); j >= 0 {
						i = j
						continue
					}
				case '%':
					if j := p.copyLiquidTag(b, i, end); j >= 0 {
						i = j
						continue
					}
				}
			}

			// Accolade simple : tenter de matcher un groupe spintax `{...|...}`.
			// findGroup renvoie l'index de la `}` fermante du groupe (ou -1) et si
			// un `|` existe au niveau top (vrai groupe spintax vs littéral `{x}`).
			if closeIdx, hasPipe := p.findGroup(i, end); closeIdx >= 0 {
				if hasPipe {
					p.resolveGroup(b, i+1, closeIdx, depth)
					i = closeIdx + 1
					continue
				}
				// Accolade équilibrée mais SANS séparateur `|` au niveau top :
				// ce n'est pas du spintax (ex: `{ texte }` ou `{}`). On recopie
				// l'accolade ouvrante verbatim et on continue le scan à
				// l'intérieur (un groupe spintax peut être imbriqué plus loin,
				// même si le niveau courant n'en est pas un).
				b.WriteByte('{')
				i++
				continue
			}

			// Accolade ouvrante sans fermante équilibrée (déséquilibrée / EOF) :
			// recopier verbatim, best-effort, pas de panic.
			b.WriteByte('{')
			i++
			continue
		}

		b.WriteByte(c)
		i++
	}
}

// copyLiquidVar recopie un bloc `{{ ... }}` commençant à i (input[i:i+2]=="{{").
// Renvoie l'index après la `}}` fermante, ou -1 si le bloc n'est pas fermé avant
// end (auquel cas l'appelant traite la `{` comme un caractère ordinaire).
func (p *parser) copyLiquidVar(b *strings.Builder, i, end int) int {
	close := strings.Index(p.input[i:end], "}}")
	if close < 0 {
		return -1
	}
	stop := i + close + 2
	b.WriteString(p.input[i:stop])
	return stop
}

// copyLiquidTag recopie un bloc `{% ... %}` commençant à i (input[i:i+2]=="{%").
// Renvoie l'index après la `%}` fermante, ou -1 si non fermé avant end.
func (p *parser) copyLiquidTag(b *strings.Builder, i, end int) int {
	close := strings.Index(p.input[i:end], "%}")
	if close < 0 {
		return -1
	}
	stop := i + close + 2
	b.WriteString(p.input[i:stop])
	return stop
}

// findGroup, partant d'une `{` simple à openIdx, cherche la `}` fermante
// correspondante en respectant le nesting des accolades simples ET en sautant
// les blocs Liquid `{{...}}` / `{%...%}` (dont les accolades ne comptent pas
// dans l'équilibrage spintax).
//
// Renvoie (closeIdx, hasTopLevelPipe) :
//   - closeIdx = index de la `}` fermante au niveau de openIdx, ou -1 si jamais
//     équilibrée avant end.
//   - hasTopLevelPipe = true s'il existe au moins un `|` au niveau de nesting de
//     ce groupe (donc un vrai groupe spintax). Un `|` à l'intérieur d'un
//     sous-groupe imbriqué ne compte pas pour le niveau courant.
func (p *parser) findGroup(openIdx, end int) (int, bool) {
	depth := 0
	hasPipe := false
	i := openIdx
	for i < end {
		c := p.input[i]
		switch c {
		case '{':
			// Saut des blocs Liquid : leurs accolades ne participent pas à
			// l'équilibrage spintax.
			if next := i + 1; next < end {
				if p.input[next] == '{' {
					if close := strings.Index(p.input[i:end], "}}"); close >= 0 {
						i = i + close + 2
						continue
					}
				} else if p.input[next] == '%' {
					if close := strings.Index(p.input[i:end], "%}"); close >= 0 {
						i = i + close + 2
						continue
					}
				}
			}
			depth++
			i++
		case '}':
			depth--
			if depth == 0 {
				return i, hasPipe
			}
			i++
		case '|':
			if depth == 1 {
				hasPipe = true
			}
			i++
		default:
			i++
		}
	}
	return -1, false
}

// resolveGroup résout un groupe spintax dont le contenu (entre accolades, exclu)
// est input[innerStart:innerEnd]. Il split les options sur les `|` de niveau top,
// choisit une option de façon déterministe (seed + index du groupe), puis résout
// récursivement l'option choisie (nesting).
func (p *parser) resolveGroup(b *strings.Builder, innerStart, innerEnd, depth int) {
	// Réserver l'index AVANT de descendre dans les sous-groupes, pour que la
	// numérotation suive l'ordre de lecture (le groupe externe a un index plus
	// petit que ses enfants).
	idx := p.groupIndex
	p.groupIndex++

	options := p.splitOptions(innerStart, innerEnd)
	// splitOptions garantit len >= 1 (le `|` top-level existe puisque findGroup
	// a renvoyé hasPipe=true, donc len >= 2 ; on reste défensif).
	choice := pickOption(p.seedHash, idx, len(options))
	opt := options[choice]
	p.parseInto(b, opt.start, opt.end, depth+1)
}

// span est un intervalle [start, end) dans input.
type span struct {
	start, end int
}

// splitOptions découpe input[innerStart:innerEnd] sur les `|` de niveau top
// (depth 0 relatif au contenu du groupe), en sautant les sous-groupes imbriqués
// et les blocs Liquid. Renvoie les intervalles de chaque option.
func (p *parser) splitOptions(innerStart, innerEnd int) []span {
	var options []span
	depth := 0
	segStart := innerStart
	i := innerStart
	for i < innerEnd {
		c := p.input[i]
		switch c {
		case '{':
			if next := i + 1; next < innerEnd {
				if p.input[next] == '{' {
					if close := strings.Index(p.input[i:innerEnd], "}}"); close >= 0 {
						i = i + close + 2
						continue
					}
				} else if p.input[next] == '%' {
					if close := strings.Index(p.input[i:innerEnd], "%}"); close >= 0 {
						i = i + close + 2
						continue
					}
				}
			}
			depth++
			i++
		case '}':
			if depth > 0 {
				depth--
			}
			i++
		case '|':
			if depth == 0 {
				options = append(options, span{segStart, i})
				segStart = i + 1
			}
			i++
		default:
			i++
		}
	}
	options = append(options, span{segStart, innerEnd})
	return options
}

// hashSeed produit un hash 64 bits stable du seed. FNV-1a : rapide, sans
// allocation, parfaitement déterministe (pas de randomisation de seed map Go).
func hashSeed(seed string) uint64 {
	h := fnv.New64a()
	// Préfixe de domaine pour éviter toute collision de sémantique si le même
	// hash était réutilisé ailleurs, et stabiliser l'espace de hash.
	_, _ = h.Write([]byte("veridian-spintax\x00"))
	_, _ = h.Write([]byte(seed))
	return h.Sum64()
}

// pickOption choisit un index dans [0, n) de façon déterministe à partir du hash
// du seed et de l'index du groupe. Mélange seedHash et groupIndex via une étape
// de type splitmix64 pour décorréler les groupes successifs (sinon des groupes
// d'indices proches sur un même seed tendraient à choisir la même position).
func pickOption(seedHash, groupIndex uint64, n int) int {
	if n <= 1 {
		return 0
	}
	x := seedHash ^ (groupIndex+1)*0x9E3779B97F4A7C15
	// finaliseur splitmix64
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int(x % uint64(n))
}
