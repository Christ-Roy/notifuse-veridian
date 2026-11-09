package veridian

// V73 (09/11/2026) : identifiant du tenant au Hub (hub_tenant_id), appris par update-plan. Le MCP derriere la
// porte du Hub choisit l'espace par ce claim `tenant_id` (CONTRAT-MCP) : Notifuse nomme ses espaces par slug.
// Additive, nullable : le tag precedent tourne sur ce schema.
func init() {
	Register(Spec{
		Number: 73, Lot: "Identite", Name: "hub_tenant_id",
		System: []string{
			`ALTER TABLE veridian_plan ADD COLUMN IF NOT EXISTS hub_tenant_id VARCHAR(64) NULL`,
			`CREATE INDEX IF NOT EXISTS idx_veridian_plan_hub_tenant_id ON veridian_plan (hub_tenant_id) WHERE hub_tenant_id IS NOT NULL`,
		},
	})
}
