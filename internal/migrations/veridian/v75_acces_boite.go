package veridian

// V75 (23/11/2026, fiche 84) : accès d'une boîte louée. Table système : la connexion IMAP ou SMTP n'a que l'adresse et le
// mot de passe pour retrouver le workspace. Le mot de passe n'est jamais stocké (empreinte SHA-256 d'un secret aléatoire
// de 160 bits). Table workspace : drapeaux IMAP par profil, séparés de « traité ». Additive.
func init() {
	Register(Spec{
		Number: 75, Lot: "Boites", Name: "acces_boite",
		System: []string{
			`CREATE TABLE IF NOT EXISTS veridian_box_access (
				id           VARCHAR(36) PRIMARY KEY,
				workspace_id VARCHAR(64) NOT NULL,
				profile_id   VARCHAR(64) NOT NULL,
				address      VARCHAR(255) NOT NULL,
				label        VARCHAR(120) NOT NULL DEFAULT '',
				secret_hash  VARCHAR(64) NOT NULL,
				can_send     BOOLEAN NOT NULL DEFAULT TRUE,
				daily_cap    INTEGER NOT NULL DEFAULT 200,
				sent_day     DATE NULL,
				sent_count   INTEGER NOT NULL DEFAULT 0,
				created_by   VARCHAR(80) NOT NULL DEFAULT '',
				created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				last_used_at TIMESTAMPTZ NULL,
				revoked_at   TIMESTAMPTZ NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_veridian_box_access_address ON veridian_box_access (address) WHERE revoked_at IS NULL`,
			`CREATE INDEX IF NOT EXISTS idx_veridian_box_access_ws ON veridian_box_access (workspace_id, created_at)`,
		},
		Workspace: []string{
			`CREATE TABLE IF NOT EXISTS veridian_mail_box_flag (
				profile_id VARCHAR(64) NOT NULL,
				message_id TEXT NOT NULL,
				flags      TEXT[] NOT NULL DEFAULT '{}',
				hidden     BOOLEAN NOT NULL DEFAULT FALSE,
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (profile_id, message_id)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_vmm_profile_seq ON veridian_mail_message (profile_id, seq)`,
		},
	})
}
