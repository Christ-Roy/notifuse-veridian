package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V43Migration aligne les types TIMESTAMP de la table `veridian_plan` :
// migre toutes les colonnes legacy (`created_at, updated_at, last_reset_at,
// suspended_at, deleted_at`) de TIMESTAMP WITHOUT TIME ZONE vers TIMESTAMP
// WITH TIME ZONE (UTC). Élimine le drift documenté dans le ticket
// 2026-05-19-aligner-types-timestamp-veridian-plan.md.
//
// Contexte du drift :
//
//	Le CREATE TABLE bootstrap (internal/database/schema/system_tables.go) et
//	V31 backfill ont créé les colonnes timestamp legacy en WITHOUT TIME ZONE
//	(idiome Notifuse upstream). Les migrations Veridian V34/V35/V38/V39
//	ajoutent ensuite restored_at, purge_eligible_at, last_touched_at,
//	activity_threshold_reached_at, last_hub_sync_at en WITH TIME ZONE
//	(idiome correct UTC + offset).
//
// Bug runtime déclenché par ce drift :
//
//	`UPDATE veridian_plan SET last_touched_at = $2, updated_at = $2` plante
//	avec `pq: inconsistent types deduced for parameter $2` car Postgres ne
//	peut pas inférer un type unique pour $2 partagé entre une colonne
//	WITHOUT TZ et une WITH TZ (cf. fix commit 995a9e0b — workaround $2/$3).
//
// Décision (Option 1 du ticket) : tout en WITH TIME ZONE.
//
//	Sémantiquement correct, élimine le bug type mismatch définitivement,
//	cohérent avec les nouvelles colonnes V34+ et avec les conventions
//	idiomatiques Postgres modernes.
//
// Safety §12 (Expand & Contract) :
//
//	ALTER COLUMN TYPE est in-place sur Postgres quand la conversion n'a pas
//	besoin de réécrire les pages (cas ici : WITHOUT TZ → WITH TZ avec
//	`AT TIME ZONE 'UTC'` est une simple réinterprétation des bytes stockés,
//	pas un rewrite). AccessExclusiveLock court (< 1s sur 10k rows max).
//	Le tag Docker précédent peut continuer à tourner sur le schéma
//	post-migration : le driver lib/pq accepte les deux types en
//	lecture/écriture transparente côté Go (time.Time round-trip).
//
// Idempotence : on guard chaque ALTER avec une vérif information_schema —
// si la colonne est déjà `timestamp with time zone`, skip. Re-run sans
// effet.
//
// Pas d'index à recréer (le seul index sur deleted_at est `WHERE deleted_at
// IS NOT NULL` — le predicate reste valide après le retype).
//
// Backfill : aucun. `AT TIME ZONE 'UTC'` réinterprète les naive timestamps
// comme UTC, ce qui correspond exactement à la sémantique implicite
// utilisée par tout le code Go (`time.Now().UTC()` partout dans
// veridian_plan_postgres.go).
type V43Migration struct{}

func (m *V43Migration) GetMajorVersion() float64 {
	return 43.0
}

func (m *V43Migration) HasSystemUpdate() bool {
	return true
}

func (m *V43Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V43Migration) ShouldRestartServer() bool {
	return false
}

// v43LegacyColumns liste les colonnes WITHOUT TIME ZONE à migrer.
// Ordre fixe pour idempotence + déterminisme des tests sqlmock.
var v43LegacyColumns = []string{
	"created_at",
	"updated_at",
	"last_reset_at",
	"suspended_at",
	"deleted_at",
}

func (m *V43Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	for _, col := range v43LegacyColumns {
		// Idempotent : check si la colonne est déjà en `timestamp with time zone`.
		// Re-run de la migration (ou migration appliquée hors flow) = no-op.
		var dataType string
		row := db.QueryRowContext(ctx, `
			SELECT data_type
			FROM information_schema.columns
			WHERE table_name = 'veridian_plan' AND column_name = $1
		`, col)
		if err := row.Scan(&dataType); err != nil {
			return fmt.Errorf("introspect veridian_plan.%s: %w", col, err)
		}
		if dataType == "timestamp with time zone" {
			// Déjà migré (re-run, ou colonne créée WITH TZ par un schéma futur).
			continue
		}

		// Postgres ne permet pas $1 paramétré pour un nom de colonne dans
		// ALTER COLUMN — concat sûr car col vient d'une whitelist en dur.
		stmt := fmt.Sprintf(
			`ALTER TABLE veridian_plan ALTER COLUMN %s TYPE TIMESTAMP WITH TIME ZONE USING %s AT TIME ZONE 'UTC'`,
			col, col,
		)
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("alter veridian_plan.%s to TIMESTAMP WITH TIME ZONE: %w", col, err)
		}
	}
	return nil
}

func (m *V43Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V43Migration{})
}
