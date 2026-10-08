package domain

// Veridian fork, lot 4 (08/10/2026) : type d'un message d'envoi.
//
// Deux familles qui n'ont pas les memes regles. Le COMMERCIAL (campagnes,
// sequences) passe par la file, la rotation des profils, les plafonds, la chauffe,
// la fenetre d'envoi et le fusible de reputation. Le TRANSACTIONNEL (API d'envoi,
// relais SMTP, modeles de categorie transactionnelle) part par le seul profil
// transactionnel, sans aucune de ces portes : un mot de passe oublie part toujours.
// message_history.veridian_message_type porte la distinction (V61) ; NULL = commercial.
const (
	VeridianMessageTypeCommercial    = "commercial"
	VeridianMessageTypeTransactional = "transactional"
)

// VeridianIsTransactionalCategory dit si un modele de cette categorie est un mail
// transactionnel quand une sequence l'envoie.
func VeridianIsTransactionalCategory(category string) bool {
	return TemplateCategory(category) == TemplateCategoryTransactional
}
