package veridian_spintax

import "strings"

// CountVariants compte le NOMBRE TOTAL de variantes distinctes qu'un template
// spintax peut produire (produit du nombre d'options à chaque groupe, nesting
// inclus). PUR, déterministe, zéro allocation hors parcours.
//
// But (anti-hash, Lot 2026-06-15) : un template à 1 variante (aucun spintax, ou
// un seul littéral) envoyé en gros volume vers une classe produit 500× le même
// hash → collision garantie. CountVariants permet au linter délivrabilité
// d'AVERTIR en amont quand la variété est insuffisante par rapport au volume
// cible, sans bloquer.
//
// Règles, alignées sur ResolveSpintax (même parseur de structure) :
//   - `{A|B}` → 2 ; `{A|B}{C|D}` → 4 (groupes top-level : produit).
//   - nesting `{Bonjour {Monsieur|Madame}|Salut}` : l'option externe choisie
//     "Bonjour {Monsieur|Madame}" a elle-même 2 sous-variantes → ce groupe vaut
//     2 (option Monsieur/Madame) + 1 (option Salut) = 3 variantes.
//   - pas de spintax (aucun groupe `{...|...}` valide) → 1.
//   - Liquid `{{ }}` / `{% %}` ignoré (jamais compté comme spintax).
//   - accolades déséquilibrées / `{x}` sans `|` → littéral (n'augmente pas le
//     compte). best-effort, jamais de panic.
//
// Overflow : sur un template pathologique (des dizaines de gros groupes), le
// produit pourrait dépasser int. On PLAFONNE à countVariantsCap (le linter n'a
// besoin que de "beaucoup > volume", pas de la valeur exacte). Au plafond, on
// arrête de multiplier (la valeur reste >= cap, suffisant pour le verdict).
func CountVariants(template string) int {
	if !strings.ContainsRune(template, '{') {
		return 1
	}
	c := &variantCounter{input: template}
	n := c.count(0, len(template), 0)
	if n < 1 {
		return 1
	}
	return n
}

// countVariantsCap plafonne le produit pour éviter l'overflow int sur un
// template adverse. 1e9 est largement au-dessus de tout volume cold réel : si un
// template peut produire ≥ 1 milliard de variantes, il est "assez varié" quel
// que soit le volume.
const countVariantsCap = 1_000_000_000

// variantCounter parcourt la structure spintax SANS résoudre (il ne choisit pas
// d'option, il compte). Réutilise la même logique de découpage que parser
// (Liquid sauté, nesting, `|` top-level) pour garantir que le compte
// correspond exactement à l'espace que ResolveSpintax peut produire.
type variantCounter struct {
	input string
}

// count retourne le nombre de variantes de la portion input[start:end].
// Le nombre de variantes d'une SÉQUENCE = produit des variantes de chacun de
// ses groupes (le texte littéral entre groupes vaut 1). depth borne la récursion.
func (c *variantCounter) count(start, end, depth int) int {
	if depth > maxNestingDepth {
		return 1
	}
	total := 1
	i := start
	for i < end {
		ch := c.input[i]
		if ch == '{' {
			// Bloc Liquid : sauté, ne compte pas.
			if next := i + 1; next < end {
				if c.input[next] == '{' {
					if close := strings.Index(c.input[i:end], "}}"); close >= 0 {
						i = i + close + 2
						continue
					}
				} else if c.input[next] == '%' {
					if close := strings.Index(c.input[i:end], "%}"); close >= 0 {
						i = i + close + 2
						continue
					}
				}
			}
			// Groupe spintax équilibré ?
			if closeIdx, hasPipe := c.findGroup(i, end); closeIdx >= 0 {
				if hasPipe {
					// Variantes de ce groupe = SOMME des variantes de ses options.
					groupVariants := c.countGroup(i+1, closeIdx, depth)
					total = capMul(total, groupVariants)
					i = closeIdx + 1
					continue
				}
				// `{x}` sans `|` au top : littéral, on saute juste la `{` (le
				// contenu interne peut contenir un groupe imbriqué → continue à
				// l'intérieur comme ResolveSpintax).
				i++
				continue
			}
			i++
			continue
		}
		i++
	}
	return total
}

// countGroup compte les variantes d'un groupe (contenu entre accolades, exclu) :
// SOMME des variantes de chaque option séparée par `|` au niveau top.
func (c *variantCounter) countGroup(innerStart, innerEnd, depth int) int {
	options := c.splitOptions(innerStart, innerEnd)
	sum := 0
	for _, opt := range options {
		sum = capAdd(sum, c.count(opt.start, opt.end, depth+1))
	}
	if sum < 1 {
		return 1
	}
	return sum
}

// findGroup : même logique d'équilibrage que parser.findGroup (Liquid sauté,
// nesting des accolades simples, détection d'un `|` top-level). Dupliqué ici en
// méthode du counter pour rester dans le même fichier sans toucher parser.
func (c *variantCounter) findGroup(openIdx, end int) (int, bool) {
	depth := 0
	hasPipe := false
	i := openIdx
	for i < end {
		ch := c.input[i]
		switch ch {
		case '{':
			if next := i + 1; next < end {
				if c.input[next] == '{' {
					if close := strings.Index(c.input[i:end], "}}"); close >= 0 {
						i = i + close + 2
						continue
					}
				} else if c.input[next] == '%' {
					if close := strings.Index(c.input[i:end], "%}"); close >= 0 {
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

// splitOptions : même logique que parser.splitOptions (split sur les `|` de
// niveau top, Liquid et sous-groupes sautés).
func (c *variantCounter) splitOptions(innerStart, innerEnd int) []span {
	var options []span
	depth := 0
	segStart := innerStart
	i := innerStart
	for i < innerEnd {
		ch := c.input[i]
		switch ch {
		case '{':
			if next := i + 1; next < innerEnd {
				if c.input[next] == '{' {
					if close := strings.Index(c.input[i:innerEnd], "}}"); close >= 0 {
						i = i + close + 2
						continue
					}
				} else if c.input[next] == '%' {
					if close := strings.Index(c.input[i:innerEnd], "%}"); close >= 0 {
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

// capMul / capAdd plafonnent le produit/la somme à countVariantsCap pour éviter
// l'overflow int sur un template adverse (la valeur exacte n'importe pas
// au-delà du plafond).
func capMul(a, b int) int {
	if a >= countVariantsCap || b >= countVariantsCap {
		return countVariantsCap
	}
	p := a * b
	if p >= countVariantsCap || p < 0 {
		return countVariantsCap
	}
	return p
}

func capAdd(a, b int) int {
	s := a + b
	if s >= countVariantsCap || s < 0 {
		return countVariantsCap
	}
	return s
}
