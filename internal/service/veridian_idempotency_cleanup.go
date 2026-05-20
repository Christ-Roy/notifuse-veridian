package service

import (
	"context"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianIdempotencyCleanupService est un cron Veridian custom qui purge
// périodiquement les entrées expirées de la table veridian_idempotency_keys.
//
// Why : CONTRAT-HUB §5.11 (Idempotency-Key header). La méthode
// `DeleteExpired(ctx)` du repo est implémentée mais aucun cron upstream ne
// l'appelle. Sans purge, la table grossit ~1MB/jour à 10k req/jour (cf.
// todo/2026-05-19-cron-cleanup-idempotency-keys.md). Aujourd'hui pas
// critique car le Hub n'envoie pas encore le header (middleware passthrough)
// mais on branche AVANT le rollout massif pour éviter une dette qui devient
// urgente.
//
// Pattern : goroutine + time.NewTicker (identique à TelemetryService.
// StartDailyScheduler ligne 209). Pas d'inscription au TaskScheduler upstream
// qui est lié à task_service.ExecutePendingTasks (workflow tasks DB), pas un
// scheduler généraliste.
//
// Frequency : 24h. Run immédiat au démarrage (1 fois) pour rattraper si l'app
// était down longtemps, puis tous les 24h.
type VeridianIdempotencyCleanupService struct {
	repo     domain.VeridianIdempotencyRepository
	logger   logger.Logger
	interval time.Duration
}

// NewVeridianIdempotencyCleanupService crée le service. Interval par défaut
// 24h via le wrapper, override possible pour les tests.
func NewVeridianIdempotencyCleanupService(
	repo domain.VeridianIdempotencyRepository,
	logger logger.Logger,
	interval time.Duration,
) *VeridianIdempotencyCleanupService {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &VeridianIdempotencyCleanupService{
		repo:     repo,
		logger:   logger,
		interval: interval,
	}
}

// Start lance la goroutine de cleanup. Retourne immédiatement.
//
// Lifecycle : la goroutine s'arrête quand ctx est cancelled (typiquement
// app.shutdownCtx via app.GetShutdownContext()). Pas de defer Stop() à
// appeler explicitement — ctx.Done() suffit.
//
// Run pattern : exécution immédiate puis tick périodique. Si la première
// exec fail, on log et on continue (pas de panic, pas de retry — le tick
// suivant fera le boulot).
func (s *VeridianIdempotencyCleanupService) Start(ctx context.Context) {
	if s.repo == nil {
		s.logger.Warn("VeridianIdempotencyCleanup: repo nil, scheduler skipped")
		return
	}

	go func() {
		s.runOnce(ctx)

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				s.logger.Info("VeridianIdempotencyCleanup: context cancelled, stopping")
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
}

// runOnce appelle DeleteExpired une fois et log le résultat. Best-effort :
// les erreurs sont loggées mais ne stoppent pas le scheduler.
func (s *VeridianIdempotencyCleanupService) runOnce(ctx context.Context) {
	start := time.Now()
	deleted, err := s.repo.DeleteExpired(ctx)
	elapsed := time.Since(start)

	if err != nil {
		s.logger.WithFields(map[string]interface{}{
			"error":   err.Error(),
			"elapsed": elapsed.String(),
		}).Error("VeridianIdempotencyCleanup: DeleteExpired failed")
		return
	}

	s.logger.WithFields(map[string]interface{}{
		"deleted": deleted,
		"elapsed": elapsed.String(),
	}).Info("VeridianIdempotencyCleanup: purged expired entries")
}
