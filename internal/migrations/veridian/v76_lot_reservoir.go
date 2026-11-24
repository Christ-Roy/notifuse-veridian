package veridian

// V76 (24/11/2026, fiche 85) : lots d'activation nommés par l'appelant. Rejouer un lot (délai du Hub, programme relancé) rend
// le résultat d'origine au lieu de faire entrer le lot suivant dans la séquence. Table neuve, additive.
func init() {
	Register(Spec{
		Number: 76, Lot: "Reserve", Name: "lot_reservoir",
		Workspace: []string{
			`CREATE TABLE IF NOT EXISTS veridian_reservoir_batch (
				automation_id VARCHAR(36) NOT NULL,
				batch_id      VARCHAR(100) NOT NULL,
				emails        TEXT[] NOT NULL DEFAULT '{}',
				selected      INTEGER NOT NULL DEFAULT 0,
				created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (automation_id, batch_id)
			)`,
		},
	})
}
