package veridian

// V72 (08/11/2026) : statut d'essai envoye par le Hub avec le plan (CONTRAT-BILLING v2 §3, trial_status et
// trial_ends_at). Notifuse le STOCKE et le rend dans la decouverte by-email ; aucune lecture seule n'est
// appliquee (drapeau VERIDIAN_TRIAL_READONLY_ENFORCED, coupe par defaut, decision de Robert).
// Additive, nullable : le tag precedent tourne sur ce schema.
func init() {
	Register(Spec{
		Number: 72, Lot: "Identite", Name: "trial_status",
		System: []string{
			`ALTER TABLE veridian_plan ADD COLUMN IF NOT EXISTS trial_status VARCHAR(16) NULL`,
			`ALTER TABLE veridian_plan ADD COLUMN IF NOT EXISTS trial_ends_at TIMESTAMPTZ NULL`,
		},
	})
}
