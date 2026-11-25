package veridian

// V82 (25/11/2026, VER-263, fiche 95) : qui le Hub a désigné admin d'un espace, et le journal de chaque décision (sans clé étrangère : il survit à
// l'espace et au compte). Additive ; l'image précédente ignore ces tables et traite un admin comme un membre à tous droits.
func init() {
	Register(Spec{
		Number: 82, Lot: "Roles", Name: "admin_espace",
		System: []string{
			`CREATE TABLE IF NOT EXISTS veridian_workspace_admins (
				workspace_id VARCHAR(20) NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				granted_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				revoked_at   TIMESTAMPTZ NULL,
				PRIMARY KEY (workspace_id, user_id)
			)`,
			`CREATE TABLE IF NOT EXISTS veridian_workspace_admin_events (
				id                BIGSERIAL PRIMARY KEY,
				workspace_id      VARCHAR(20) NOT NULL,
				hub_user_id       TEXT NOT NULL,
				action            VARCHAR(20) NOT NULL,
				result            VARCHAR(40) NOT NULL,
				role_before       VARCHAR(20) NOT NULL DEFAULT '',
				role_after        VARCHAR(20) NOT NULL DEFAULT '',
				actor_hub_user_id TEXT NOT NULL,
				reason            TEXT NOT NULL,
				reinstate         BOOLEAN NOT NULL DEFAULT FALSE,
				at                TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE INDEX IF NOT EXISTS idx_veridian_workspace_admin_events_ws ON veridian_workspace_admin_events (workspace_id, at)`,
		},
	})
}
