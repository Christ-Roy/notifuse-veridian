package service

// === Veridian patch — Lot K (2026-05-21) ===
//
// Cron qui scanne periodiquement veridian_api_key_grace et revoque les
// api_keys dont revoke_at <= NOW. CONTRAT-HUB §5.15.
//
// Frequence : 1×/min (interval par defaut). Choix justifie :
//   - Grace contractuelle : 5 minutes. Latency cron 1min = revocation
//     effective dans [5min, 6min]. Acceptable cote contrat.
//   - Cost : SELECT + DELETE legers sur table transient (<100 rows).
//
// Pattern aligne sur VeridianIdempotencyCleanupService.

import (
	"context"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianAPIKeyGraceCleanupService est un cron qui revoque les api_keys
// expirees (RotateAPIKey grace period termine).
type VeridianAPIKeyGraceCleanupService struct {
	svc      domain.VeridianService
	logger   logger.Logger
	interval time.Duration
}

// NewVeridianAPIKeyGraceCleanupService cree le scheduler. Interval par
// defaut 1 minute via le wrapper, override possible pour tests.
//
// Le service doit avoir ete configure avec ConfigureAPIKeyGraceSupport
// (sinon RunAPIKeyGraceCleanupOnce est un no-op silencieux).
func NewVeridianAPIKeyGraceCleanupService(
	svc domain.VeridianService,
	log logger.Logger,
	interval time.Duration,
) *VeridianAPIKeyGraceCleanupService {
	if interval <= 0 {
		interval = 1 * time.Minute
	}
	return &VeridianAPIKeyGraceCleanupService{
		svc:      svc,
		logger:   log,
		interval: interval,
	}
}

// Start lance la goroutine de cleanup. Retourne immediatement.
//
// Lifecycle : la goroutine s'arrete quand ctx est cancelled (typiquement
// app.shutdownCtx via app.GetShutdownContext()).
//
// Run pattern : exec immediate puis tick periodique. Pas de retry — le tick
// suivant rejoue.
func (s *VeridianAPIKeyGraceCleanupService) Start(ctx context.Context) {
	if s.svc == nil {
		if s.logger != nil {
			s.logger.Warn("VeridianAPIKeyGraceCleanup: svc nil, scheduler skipped")
		}
		return
	}

	go func() {
		s.runOnce(ctx)

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				if s.logger != nil {
					s.logger.Info("VeridianAPIKeyGraceCleanup: context cancelled, stopping")
				}
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
}

// runOnce appelle RunAPIKeyGraceCleanupOnce. Best-effort.
func (s *VeridianAPIKeyGraceCleanupService) runOnce(ctx context.Context) {
	// Type assertion : la methode RunAPIKeyGraceCleanupOnce n'est PAS sur
	// l'interface domain.VeridianService (utilitaire interne au service).
	// On l'appelle via assertion.
	impl, ok := s.svc.(interface {
		RunAPIKeyGraceCleanupOnce(context.Context) (int, map[string]string)
	})
	if !ok {
		if s.logger != nil {
			s.logger.Warn("VeridianAPIKeyGraceCleanup: svc does not expose RunAPIKeyGraceCleanupOnce, skip")
		}
		return
	}

	start := time.Now()
	revoked, errs := impl.RunAPIKeyGraceCleanupOnce(ctx)
	elapsed := time.Since(start)

	fields := map[string]interface{}{
		"revoked": revoked,
		"elapsed": elapsed.String(),
	}
	if len(errs) > 0 {
		fields["errors"] = errs
		if s.logger != nil {
			s.logger.WithFields(fields).Warn("VeridianAPIKeyGraceCleanup: some revocations failed (will retry next tick)")
		}
		return
	}
	if revoked > 0 && s.logger != nil {
		s.logger.WithFields(fields).Info("VeridianAPIKeyGraceCleanup: revoked expired api_keys")
	}
}
