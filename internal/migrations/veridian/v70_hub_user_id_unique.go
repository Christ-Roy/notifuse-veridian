package veridian

// V70 (chantier identite, 06/11/2026) : index unique partiel sur users.hub_user_id, exige par le
// CONTRAT-HUB §3.7 (un hub_user_id ne lie qu un seul user local). V46 l avait laisse au code, qui
// ne protege que la ligne qu il met a jour, pas un second user. Mesure prod du 06/11 : 240 lignes,
// 3 liens, aucun doublon, donc pas d echec possible et un verrou tres court.
// Additive et idempotente. Le tag precedent tourne sur ce schema.
func init() {
	Register(Spec{
		Number: 70, Lot: "Identite", Name: "hub_user_id_unique",
		System: []string{
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_hub_user_id_unique
		ON users (hub_user_id)
		WHERE hub_user_id IS NOT NULL`,
		},
	})
}
