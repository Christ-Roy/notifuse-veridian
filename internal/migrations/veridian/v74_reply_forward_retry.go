package veridian

// V74 (fiche 78, 11/11/2026) : robustesse de la redirection des reponses. La table veridian_reply_forward
// (V69) gagne de quoi rejouer et reporter une copie sans la perdre : contenu a envoyer (efface une fois
// la copie partie ou abandonnee), nombre de tentatives, echeance de la prochaine tentative et instant ou la
// copie a pris une place sous le plafond journalier.
// Additive : ADD COLUMN IF NOT EXISTS de colonnes NULLABLES (metadonnee seule, aucune reecriture de table,
// aucun index). Le tag precedent ignore ces colonnes.
func init() {
	Register(Spec{
		Number: 74, Lot: "L1d", Name: "reply_forward_retry",
		Workspace: []string{
			`ALTER TABLE veridian_reply_forward ADD COLUMN IF NOT EXISTS payload TEXT`,
			`ALTER TABLE veridian_reply_forward ADD COLUMN IF NOT EXISTS attempts INTEGER DEFAULT 0`,
			`ALTER TABLE veridian_reply_forward ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ`,
			`ALTER TABLE veridian_reply_forward ADD COLUMN IF NOT EXISTS slot_at TIMESTAMPTZ`,
		},
	})
}
