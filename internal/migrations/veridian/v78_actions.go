package veridian

// V78 (24/11/2026, INTEGRATIONS-NATIVES §2.3, fiche 87) : actions demandées par une autre app au nom d'un utilisateur. Jetons déjà vus (un jeton
// ne sert qu'une fois) et gestes déjà faits (un rejeu rend le premier résultat), avec leur origine. Tables système neuves, additives.
func init() {
	Register(Spec{
		Number: 78, Lot: "Actions", Name: "actions_inter_apps",
		System: []string{
			`CREATE TABLE IF NOT EXISTS veridian_action_jti (
				jti     VARCHAR(128) PRIMARY KEY,
				seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE INDEX IF NOT EXISTS idx_veridian_action_jti_seen ON veridian_action_jti (seen_at)`,
			`CREATE TABLE IF NOT EXISTS veridian_action_done (
				workspace_id VARCHAR(64) NOT NULL,
				app          VARCHAR(64) NOT NULL,
				idem_key     VARCHAR(128) NOT NULL,
				action       VARCHAR(64) NOT NULL,
				request_hash VARCHAR(64) NOT NULL,
				hub_user_id  VARCHAR(64) NOT NULL,
				jti          VARCHAR(128) NOT NULL,
				result       JSONB NULL,
				created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				done_at      TIMESTAMPTZ NULL,
				PRIMARY KEY (workspace_id, app, idem_key)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_veridian_action_done_created ON veridian_action_done (created_at)`,
		},
	})
}
