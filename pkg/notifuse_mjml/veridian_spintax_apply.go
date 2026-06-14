package notifuse_mjml

import (
	"github.com/Notifuse/notifuse/pkg/veridian_spintax"
)

// veridianApplySpintax résout la syntaxe spintax `{a|b|c}` du contenu MJML/HTML
// avec une graine déterministe (l'email du destinataire, porté par
// CompileTemplateRequest.VeridianSpintaxSeed).
//
// Appelé dans CompileTemplate APRÈS le rendu Liquid (les `{{ }}` / `{% %}` sont
// déjà remplacés par leurs valeurs) et AVANT le preprocessing XML + rendu MJML.
// À ce stade le spintax restant ne peut être que des accolades simples : le
// resolver les résout, et toute variable Liquid déjà rendue est préservée.
//
// Seed vide = no-op STRICT (sortie = entrée). Garantit la non-régression totale
// de tous les templates upstream : sans seed posée, aucune transformation.
func veridianApplySpintax(content string, seed string) string {
	if seed == "" {
		return content
	}
	return veridian_spintax.ResolveSpintax(content, seed)
}
