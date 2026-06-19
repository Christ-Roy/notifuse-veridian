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

// veridianDBExistenceChecker : capacité (optionnelle) de vérifier si la base
// physique d'un workspace est encore dans pg_database. Sert au wipe à confirmer
// qu'un DROP FORCE de rattrapage a bien supprimé la base quand le DROP upstream a
// raté sur la race. Implémentée par *repository.workspaceRepository.
type veridianDBExistenceChecker interface {
	VeridianWorkspaceDBExists(ctx context.Context, workspaceID string) (bool, error)
}

// veridianSystemRecordDeleter : capacité (optionnelle) de supprimer les ROWS
// SYSTÈME d'un workspace (workspaces + user_workspaces + workspace_invitations)
// SANS toucher à la base physique. C'est la brique du wipe « record-first » : on
// supprime le record system AVANT le DROP pour couper la ré-élection du ws par le
// worker round-robin (qui lit `workspaces` via List()), donc la recréation de la
// base par une task segment-queue en vol. Implémentée par
// *repository.workspaceRepository (méthode dans veridian_workspace_drop.go),
// détectée par type-assertion → pas d'import du package repository depuis service.
type veridianSystemRecordDeleter interface {
	VeridianDeleteWorkspaceSystemRecord(ctx context.Context, workspaceID string) error
}

// deleteWorkspaceSystemRecordBestEffort supprime le record system du workspace AVANT
// le DROP (wipe record-first). Retourne true si la suppression a RÉELLEMENT eu lieu
// (capacité présente + pas d'erreur) — le caller s'en sert pour savoir si la
// ré-élection worker est coupée et donc s'il peut traiter une erreur ultérieure de
// DeleteWorkspace comme bénigne. Best-effort : capacité absente → false (on retombe
// sur le comportement upstream seul) ; erreur → loggée + false (le DROP de
// rattrapage reste tenté, et le record restant sera re-nettoyé au prochain wipe/GC).
func (s *veridianService) deleteWorkspaceSystemRecordBestEffort(ctx context.Context, workspaceID string) bool {
	deleter, ok := s.workspaceRepo.(veridianSystemRecordDeleter)
	if !ok {
		return false
	}
	if err := deleter.VeridianDeleteWorkspaceSystemRecord(ctx, workspaceID); err != nil {
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"tenant_id": workspaceID,
				"error":     err.Error(),
			}).Warn("veridian: system-record delete (record-first) failed (continuing to force-drop)")
		}
		return false
	}
	return true
}

// workspaceDBStillExists retourne true si la base physique du workspace est encore
// présente. Best-effort : si le repo n'expose pas la capacité, ou si la requête
// échoue, on retourne false (on ne bloque PAS le wipe sur une incertitude — le
// record est déjà supprimé, et le DROP FORCE idempotent peut être rejoué au
// prochain GC). Conservateur uniquement quand on a une réponse CLAIRE "la base
// est toujours là".
func (s *veridianService) workspaceDBStillExists(ctx context.Context, workspaceID string) bool {
	checker, ok := s.workspaceRepo.(veridianDBExistenceChecker)
	if !ok {
		return false
	}
	exists, err := checker.VeridianWorkspaceDBExists(ctx, workspaceID)
	if err != nil {
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"tenant_id": workspaceID,
				"error":     err.Error(),
			}).Warn("veridian: db existence check failed (treating as dropped, best-effort)")
		}
		return false
	}
	return exists
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
