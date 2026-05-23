package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V46Migration ajoute la colonne `users.hub_user_id` UUID NULLABLE pour
// materialiser le lien d'identite cross-app du CONTRAT-HUB §3.7 (grave v1.4).
//
// Pourquoi : le Hub Veridian est source de verite de l'identite utilisateur
// cross-app (`hub_app.users.id`). Chaque app downstream doit pouvoir stocker
// ce `hub_user_id` localement pour resoudre un user lors d'un attach-member /
// sync-member sans s'appuyer uniquement sur l'email, tracer le bon
// identifiant Hub dans les audit logs et webhooks app->Hub
// (`tenant.member_role_changed` §5.18.4), et preparer la jointure ferme
// cross-app si Notifuse appelle le Hub.
//
// Semantique : NULLABLE par construction — la migration ne backfille PAS
// retro-activement les rows existantes. Le backfill se fait au premier
// contact (Provision, AttachMember, futur SyncMember). Cf. CONTRAT-HUB §3.7
// "Migration legacy". Les users Notifuse crees avant cette migration auront
// `hub_user_id = NULL` jusqu'a leur prochain passage HMAC.
//
// Pas d'index dans cette migration : les migrations Notifuse tournent en
// transaction (cf. manager.executeMigration BeginTx), et la version
// CREATE INDEX CONCURRENTLY ne peut pas etre dans une TX (Postgres l'interdit
// explicitement). CREATE INDEX sans CONCURRENTLY prend un AccessExclusiveLock
// qui bloque toutes les ecritures — refuse par Constitution §12 (Expand &
// Contract). L'unicite est enforced applicativement par BackfillHubUserID
// (UPDATE conditionne sur `hub_user_id IS NULL` qui empeche le double-binding
// logique). Si le volume justifie un index, une migration hors-TX dediee
// l'ajoutera ulterieurement (pattern V33/V41 deja documente).
//
// Safety §12 (Expand & Contract) : ADD COLUMN NULLABLE = additif pur. Le
// tag Docker precedent (V45 et anterieurs) continue a tourner sur ce schema
// (il ne SELECT pas la colonne). AccessExclusiveLock court (< 1s).
//
// Idempotent : `IF NOT EXISTS` sur ADD COLUMN. Re-run sans effet.
//
// Note V44/V45 : intentionnellement sautees (reservees a d'autres tickets
// du sprint sync v1.5 en parallele). V43 a livre l'alignement TIMESTAMP.
type V46Migration struct{}

func (m *V46Migration) GetMajorVersion() float64 {
	return 46.0
}

func (m *V46Migration) HasSystemUpdate() bool {
	return true
}

func (m *V46Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V46Migration) ShouldRestartServer() bool {
	return false
}

func (m *V46Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE users
		ADD COLUMN IF NOT EXISTS hub_user_id UUID NULL
	`); err != nil {
		return fmt.Errorf("add users.hub_user_id: %w", err)
	}
	return nil
}

func (m *V46Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V46Migration{})
}
