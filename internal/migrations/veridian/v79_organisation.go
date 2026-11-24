package veridian

// V79 (25/11/2026, VER-252, fiche 88) : l'organisation poussée par le Hub (marque, type, modules, réglages), dans l'ordre de sa version.
// Table système neuve, additive. Nom et logo sont en plus recopiés dans les champs natifs de l'espace.
func init() {
	Register(Spec{
		Number: 79, Lot: "Orga", Name: "organisation_hub",
		System: []string{
			`CREATE TABLE IF NOT EXISTS veridian_organisation (
				workspace_id  VARCHAR(64) PRIMARY KEY,
				hub_tenant_id VARCHAR(64) NOT NULL,
				version       BIGINT NOT NULL,
				corps         JSONB NOT NULL DEFAULT '{}'::jsonb,
				updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
		},
	})
}
