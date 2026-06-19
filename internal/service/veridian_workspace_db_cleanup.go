package service

// === Veridian patch — 2026-06-18 — DROP FORCE de rattrapage pour le wipe ===
//
// Voir todo/2026-06-17-orphan-workspaces-staging-db-starvation.md.
//
// Le DROP de base workspace upstream (workspaceRepository.DeleteDatabase) ne fait
// PAS DROP DATABASE ... WITH (FORCE). Sur staging, le worker EmailQueueWorker poll
// tous les workspaces en round-robin et rouvre des connexions en permanence →
// entre le pg_terminate_backend et le DROP, une connexion se rouvre → le DROP
// échoue ("being accessed by other users") → la base reste. Résultat : 689 bases
// orphelines pour 110 records (observé 2026-06-18).
//
// Ce module ajoute une étape de rattrapage dans wipeOneTenant : APRÈS le
// DeleteWorkspace upstream, on DROP la base avec FORCE (atomique, PG13+). On NE
// peut PAS importer le package repository depuis service (cycle :
// automation_postgres.go importe service) → on détecte la capacité du repo par
// TYPE-ASSERTION sur l'interface étroite veridianForceDropper, implémentée par le
// *workspaceRepository concret (méthode VeridianForceDropDatabase).

import (
	"context"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// veridianForceDropper est la capacité (optionnelle) du WorkspaceRepository à
// DROP une base workspace avec FORCE. Implémentée par *repository.workspaceRepository
// (méthode dans veridian_workspace_drop.go). Détectée par type-assertion → aucun
// import du package repository depuis service, aucune modif de l'interface domain.
type veridianForceDropper interface {
	VeridianForceDropDatabase(ctx context.Context, workspaceID string, log logger.Logger) error
}

// ConfigureWorkspaceDBCleanup active le DROP FORCE de rattrapage en injectant le
// préfixe des bases workspace (config.Database.Prefix). Appelé une fois au
// câblage (app.go). Préfixe vide = rattrapage désactivé (non-régression).
func (s *veridianService) ConfigureWorkspaceDBCleanup(dbPrefix string) {
	s.dbPrefix = dbPrefix
}

// forceDropWorkspaceDBBestEffort DROP la base physique d'un workspace avec FORCE,
// en rattrapage du DROP upstream qui peut échouer sur la race de connexions.
// Best-effort total : no-op si dbPrefix non configuré ou si le repo n'expose pas
// la capacité ; un échec est loggué mais JAMAIS propagé — le wipe a déjà supprimé
// le record, le rattrapage ne sert qu'à libérer disque/scan worker.
func (s *veridianService) forceDropWorkspaceDBBestEffort(ctx context.Context, workspaceID string) {
	if s.dbPrefix == "" {
		return // rattrapage désactivé
	}
	dropper, ok := s.workspaceRepo.(veridianForceDropper)
	if !ok {
		if s.logger != nil {
			s.logger.WithField("tenant_id", workspaceID).
				Warn("veridian: force-drop rattrapage skipped (repo lacks capability)")
		}
		return
	}

	if err := dropper.VeridianForceDropDatabase(ctx, workspaceID, s.logger); err != nil {
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"tenant_id": workspaceID,
				"error":     err.Error(),
			}).Warn("veridian: force-drop rattrapage failed (record already deleted, db may remain)")
		}
	}
}
