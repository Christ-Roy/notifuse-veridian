package veridian

// V66 (lot L3 « politique d'envoi et stock », 29/10/2026). Additive, expand seulement :
//
//   - veridian_policy_override : surcharges de la politique par classe de fournisseur
//     destinataire, par workspace (plafond, cadence, part de chauffe, seuil de ralentissement).
//     Une ligne par classe, une colonne vide = pas de surcharge pour cette règle (le preset du
//     catalogue s'applique) ;
//   - veridian_send_priority : priorité d'envoi d'une campagne ou d'une automation (2 à 9,
//     défaut 5 = pas de ligne). Table à part plutôt qu'une colonne sur broadcasts et automations :
//     aucune écriture sur les tables upstream, le tag précédent l'ignore ;
//   - veridian_policy_settings : réglages du module en JSON (définition du stock, cible de
//     couverture), une ligne par clé.
//
// Tables neuves (vides) : les clés primaires suffisent, aucun index à créer.
func init() {
	Register(Spec{
		Number: 66,
		Lot:    "L3",
		Name:   "politique_envoi_et_stock",
		Workspace: []string{
			`CREATE TABLE IF NOT EXISTS veridian_policy_override (
				class VARCHAR(32) PRIMARY KEY,
				daily_cap INTEGER CHECK (daily_cap IS NULL OR (daily_cap BETWEEN 1 AND 1000)),
				rate_per_min NUMERIC(8,3) CHECK (rate_per_min IS NULL OR (rate_per_min BETWEEN 0.01 AND 10)),
				warmup_share NUMERIC(4,3) CHECK (warmup_share IS NULL OR (warmup_share > 0 AND warmup_share <= 1)),
				slowdown_threshold NUMERIC(5,4) CHECK (slowdown_threshold IS NULL OR (slowdown_threshold BETWEEN 0.01 AND 0.15)),
				reason VARCHAR(300),
				updated_by VARCHAR(255),
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE TABLE IF NOT EXISTS veridian_send_priority (
				source_type VARCHAR(20) NOT NULL,
				source_id VARCHAR(255) NOT NULL,
				priority SMALLINT NOT NULL CHECK (priority BETWEEN 2 AND 9),
				updated_by VARCHAR(255),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (source_type, source_id)
			)`,
			`CREATE TABLE IF NOT EXISTS veridian_policy_settings (
				key VARCHAR(64) PRIMARY KEY,
				value JSONB NOT NULL,
				updated_by VARCHAR(255),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
		},
	})
}
