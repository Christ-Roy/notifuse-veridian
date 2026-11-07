package veridian

// V71 (chantier identite, 07/11/2026) : users.hub_deleted_at, posé par POST /api/hub/users/sync
// {deleted:true} (CONTRAT-HUB §3.8). Le compte reste en base (aucune donnée supprimée), ses sessions
// sont coupées et plus aucun lien de connexion ne lui est émis. Additive, nullable : le tag précédent
// tourne sur ce schéma.
func init() {
	Register(Spec{
		Number: 71, Lot: "Identite", Name: "hub_deleted_at",
		System: []string{
			`ALTER TABLE users ADD COLUMN IF NOT EXISTS hub_deleted_at TIMESTAMPTZ NULL`,
		},
	})
}
