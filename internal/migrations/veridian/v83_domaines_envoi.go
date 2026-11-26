package veridian

// V83 (26/11/2026, VER-166, fiche 97) : domaines d'envoi authentifiés d'un espace. Une ligne par espace et par domaine : sélecteur DKIM,
// clé publique (base64 DER, celle qu'on publie), clé privée chiffrée par SECRET_KEY (jamais en clair, jamais rendue par l'API), statut
// et dernier relevé DNS. Table système : la signature à l'envoi retrouve la clé par (espace, domaine du From). Additive.
func init() {
	Register(Spec{
		Number: 83, Lot: "DomAuth", Name: "domaines_envoi",
		System: []string{
			`CREATE TABLE IF NOT EXISTS veridian_sending_domain (
				id                    VARCHAR(36) PRIMARY KEY,
				workspace_id          VARCHAR(20) NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
				domain                VARCHAR(253) NOT NULL,
				selector              VARCHAR(63) NOT NULL,
				public_key            TEXT NOT NULL,
				encrypted_private_key TEXT NOT NULL,
				status                VARCHAR(20) NOT NULL DEFAULT 'pending',
				checks                JSONB NOT NULL DEFAULT '[]',
				created_by            VARCHAR(80) NOT NULL DEFAULT '',
				created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				last_checked_at       TIMESTAMPTZ NULL,
				verified_at           TIMESTAMPTZ NULL
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_veridian_sending_domain_ws_domain ON veridian_sending_domain (workspace_id, domain)`,
		},
	})
}
