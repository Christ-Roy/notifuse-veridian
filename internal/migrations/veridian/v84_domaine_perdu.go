package veridian

// V84 (26/11/2026, VER-166 suite, fiche 97) : un domaine d'envoi qui PERD sa vérification (clé DKIM retirée ou changée dans le DNS,
// constatée par la revérification à l'usage) garde la date du constat et son motif (verdict DKIM : missing ou mismatch), pour que le
// client le voie dans la console. Additive : l'image précédente ignore ces colonnes.
func init() {
	Register(Spec{
		Number: 84, Lot: "DomAuth", Name: "domaine_perdu",
		System: []string{
			`ALTER TABLE veridian_sending_domain ADD COLUMN IF NOT EXISTS lost_at TIMESTAMPTZ NULL`,
			`ALTER TABLE veridian_sending_domain ADD COLUMN IF NOT EXISTS lost_reason VARCHAR(20) NOT NULL DEFAULT ''`,
		},
	})
}
