package veridian

import "github.com/Notifuse/notifuse/internal/veridian/reconcile/schema"

// V64 (lot L1b, 29/10/2026) : table des écarts du réconciliateur (veridian_reconcile_finding).
// Additive et idempotente : le tag précédent l'ignore. Le DDL vit dans reconcile/schema, comme
// pour les autres tables du module.
func init() {
	Register(Spec{Number: 64, Lot: "L1b", Name: "reconcile_finding", Workspace: schema.Statements})
}
