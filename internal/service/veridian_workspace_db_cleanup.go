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
	"fmt"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
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

// veridianWorkspaceLocker : capacité (optionnelle) de prendre le MÊME
// advisory lock Postgres nommé que EnsureWorkspaceDatabaseExists côté
// création (internal/database/workspace_lock.go) — durcissement 2026-10-04,
// audit "race entre wipe et recréation". Implémentée par
// *repository.workspaceRepository (méthode dans veridian_workspace_drop.go).
type veridianWorkspaceLocker interface {
	VeridianAcquireWorkspaceLock(ctx context.Context, workspaceID string) (release func(), err error)
}

// acquireWorkspaceWipeLock prend le verrou nommé sur workspaceID AVANT toute
// opération destructrice de wipeOneTenant (suppression du record système,
// DeleteWorkspace, force-drop). Contrairement aux autres helpers de ce
// fichier, ce n'EST PAS best-effort côté échec d'acquisition : si la
// capacité est présente (repo réel) mais que prendre le verrou échoue
// (base système injoignable, etc.), on NE SAIT PLUS dire si un
// CreateWorkspace concurrent est en train de (re)créer ce même workspace —
// continuer sans verrou serait exactement la situation que ce correctif
// ferme. Capacité absente (type de repo qui ne l'implémente pas, ex. un
// double de test) → no-op (comportement inchangé, non-régression).
func (s *veridianService) acquireWorkspaceWipeLock(ctx context.Context, workspaceID string) (func(), error) {
	locker, ok := s.workspaceRepo.(veridianWorkspaceLocker)
	if !ok {
		return func() {}, nil
	}
	release, err := locker.VeridianAcquireWorkspaceLock(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire workspace wipe lock: %w", err)
	}
	return release, nil
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

// cleanupFailedProvisionDB retire la base laissee a moitie creee par un CreateWorkspace qui a echoue
// (07/11/2026 : cinq bases sans aucune table apres des echecs de provision, "too many clients" :
// EnsureWorkspaceDatabaseExists cree la base puis son schema, un echec entre les deux laisse une
// base sans record). Garde-fous: jamais si la base existait AVANT la tentative (donnees possibles),
// jamais si le record workspace existe. Best-effort, comme le rattrapage du wipe.
func (s *veridianService) cleanupFailedProvisionDB(ctx context.Context, workspaceID string, existedBefore bool) {
	if existedBefore || s.workspaceRepo == nil {
		return
	}
	if ws, err := s.workspaceRepo.GetByID(ctx, workspaceID); err == nil && ws != nil {
		return // le record existe: ce n'est pas une base orpheline
	}
	s.forceDropWorkspaceDBBestEffort(ctx, workspaceID)
}

// pruneOrphanedOwners supprime, apres un wipe, les comptes humains du workspace qui n'ont plus AUCUN
// workspace (07/11/2026 : 79 comptes locaux sans workspace, restes des batteries, wipe ne les retirait pas).
// Jamais le root ni le co-owner par defaut, jamais un compte qui appartient encore a un autre workspace.
// Un compte machine (api_key) ne part que s'il est celui du tenant (veridian-api-<tenant>@...) : sinon
// il restait et le reprovisionnement du meme slug repondait 500 "user already exists" (09/11/2026). Best-effort : un echec est journalise sans faire echouer le wipe.
func (s *veridianService) pruneOrphanedOwners(ctx context.Context, tenantID string, members []*domain.UserWorkspaceWithEmail) {
	for _, m := range members {
		if m == nil || !(m.Type == domain.UserTypeUser || isTenantAPIKeyUser(m, tenantID)) {
			continue
		}
		if strings.EqualFold(m.Email, s.rootEmail) || (s.defaultCoOwnerEmail != "" && strings.EqualFold(m.Email, s.defaultCoOwnerEmail)) {
			continue
		}
		remaining, err := s.workspaceRepo.GetUserWorkspaces(ctx, m.UserID)
		if err != nil || len(remaining) > 0 {
			continue // incertain ou encore rattache ailleurs: on ne supprime rien
		}
		if err := s.userRepo.DeleteAllSessionsByUserID(ctx, m.UserID); err != nil {
			s.warnPrune(tenantID, m.UserID, err)
			continue
		}
		if err := s.userRepo.Delete(ctx, m.UserID); err != nil {
			s.warnPrune(tenantID, m.UserID, err)
		}
	}
}

// isTenantAPIKeyUser : vrai pour le compte machine cree au provisionnement de CE tenant.
func isTenantAPIKeyUser(m *domain.UserWorkspaceWithEmail, tenantID string) bool {
	return m.Type == domain.UserTypeAPIKey && tenantID != "" &&
		strings.HasPrefix(strings.ToLower(m.Email), "veridian-api-"+strings.ToLower(tenantID)+"@")
}

func (s *veridianService) warnPrune(tenantID, userID string, err error) {
	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{"tenant_id": tenantID, "user_id": userID, "error": err.Error()}).
			Warn("veridian: wipe could not prune orphaned owner account (best-effort)")
	}
}
