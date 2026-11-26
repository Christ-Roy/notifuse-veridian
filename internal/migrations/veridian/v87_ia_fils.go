package veridian

// V87 (26/11/2026, VER-203, CONTRAT-HUB §5.24, fiche 99) : les fils de l'assistant « Mon IA », rangés dans l'app. Un fil = une session du Hub
// (UUID), une personne (hub_user_id) et une organisation (hub_tenant_id) ; les messages sont stockés tels quels (JSON, texte exact).
// Additive : une table neuve, supprimée avec son espace (ON DELETE CASCADE) ; l'image précédente l'ignore.
func init() {
	Register(Spec{
		Number: 87, Lot: "MonIA", Name: "ia_fils",
		System: []string{
			`CREATE TABLE IF NOT EXISTS veridian_ia_fils (
				session_id    UUID PRIMARY KEY,
				workspace_id  VARCHAR(20) NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				hub_user_id   TEXT NOT NULL,
				hub_tenant_id TEXT NOT NULL,
				messages      JSON NOT NULL,
				message_count INTEGER NOT NULL DEFAULT 0,
				created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE INDEX IF NOT EXISTS idx_veridian_ia_fils_ws_user ON veridian_ia_fils (workspace_id, hub_user_id)`,
		},
	})
}
