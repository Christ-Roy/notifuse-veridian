package veridian

// V82 (25/11/2026, fiche 95) : administrateurs de plateforme posés propriétaires EN PLUS du client. La marque dit qui n'est pas le
// client, garde la trace d'un retrait, et empêche qu'un retrait soit remis tout seul. Table neuve, additive.
func init() {
	Register(Spec{
		Number: 82, Lot: "Droits", Name: "admin_plateforme",
		System: []string{
			`CREATE TABLE IF NOT EXISTS veridian_platform_admin (
				workspace_id  VARCHAR(20) NOT NULL,
				user_id       UUID NOT NULL,
				hub_user_id   VARCHAR(64) NOT NULL,
				previous_role VARCHAR(20) NOT NULL DEFAULT '',
				granted_by    VARCHAR(120) NOT NULL,
				reason        VARCHAR(300) NOT NULL,
				granted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				removed_at    TIMESTAMPTZ NULL,
				removed_by    UUID NULL,
				PRIMARY KEY (workspace_id, user_id)
			)`,
		},
	})
}
