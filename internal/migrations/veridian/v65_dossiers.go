package veridian

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/internal/veridian/folders"
)

// V65 (lot L2, 29/10/2026) : dossiers natifs pour les segments, les listes et les modeles.
//   - tables veridian_folders et veridian_folder_items (neuves, additives) ;
//   - conversion des noms `A / B / C` existants en arbre, SANS modifier un seul nom (les clients
//     qui lisent encore le nom ne voient aucune difference) ; idempotente.
//
// Le DDL et la conversion vivent dans internal/veridian/folders (une seule source, testee).
func init() {
	Register(Spec{
		Number:    65,
		Lot:       "L2",
		Name:      "dossiers",
		Workspace: folders.SchemaStatements,
		WorkspaceFunc: func(ctx context.Context, workspaceID string, db Executor) error {
			q, ok := db.(folders.Querier)
			if !ok {
				return fmt.Errorf("V65 : le runner doit fournir une transaction lisible")
			}
			_, err := folders.MigrateWorkspace(ctx, q)
			return err
		},
	})
}
