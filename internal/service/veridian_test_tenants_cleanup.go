package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// === Veridian patch — 2026-05-24 — cron auto-cleanup orphans staging ===
//
// Why : sur staging, les E2E créent des tenants `tst*` qui sont normalement
// wipés par leur `afterEach`. Mais si un test plante mid-flow / runner OOM /
// timeout, le wipe est skip → orphans. Au bout de quelques heures, le pool
// PG staging sature et les nouveaux provisionnings fail
// (cf. todo/2026-05-24-staging-db-pool-orphan-cleanup-auto.md).
//
// Le workaround manuel actuel est un curl POST /api/veridian/admin/
// wipe-test-tenants avec include_orphans=true (cf. memory
// reference_admin_tenants_listing_api). Ce cron automatise ce workaround
// avec garde-fous stricts :
//   - DeployEnv == "staging" (JAMAIS prod) — check explicit au Start, no-op silencieux sinon
//   - Prefix `tst` codé en dur (impossible de wipe d'autres prefixes via ce cron)
//   - Min age 1h (laisse aux E2E le temps de finir + leur afterEach)
//   - Max 100 wipes / tick (rate-limit anti-runaway si une fuite massive arrive)
//   - Délègue à veridianService.WipeTestTenants pour réutiliser safety prefixes
//     + flow hard-delete éprouvé (workspace + DB + plan + event)
//
// Pattern copié de VeridianIdempotencyCleanupService : goroutine + ticker,
// best-effort, log warn/info, stoppe sur ctx.Done().
//
// Télémétrie : last-cleanup en mémoire (sync.Map / atomic — perte au restart
// container = acceptable, c'est best-effort observabilité staging only).

const (
	// testTenantsCleanupPrefix est le SEUL prefix wipé par ce cron. Codé en
	// dur pour éliminer tout risque qu'un opérateur le change pour un prefix
	// dangereux. Si on veut wiper un autre prefix, on passe par l'API admin
	// manuelle.
	testTenantsCleanupPrefix = "tst"

	// testTenantsCleanupMinAge : on ne wipe que les workspaces créés > 1h.
	// Laisse une marge généreuse aux E2E les plus longs (chaos-provisioning
	// ~30 min) + leur afterEach + retries.
	testTenantsCleanupMinAge = 1 * time.Hour

	// testTenantsCleanupMaxPerTick : cap dur anti-runaway. Si une fuite
	// massive d'orphans arrive (ex: 5k tenants leakés), on en wipe 100 par
	// tick (= 200/h = 4800/jour) sans saturer le DB ni la CPU.
	testTenantsCleanupMaxPerTick = 100

	// testTenantsCleanupInterval : 30 min entre 2 ticks. Frequency conforme
	// au ticket : "toutes les 30 min".
	testTenantsCleanupInterval = 30 * time.Minute

	// stagingDeployEnv est la SEULE valeur de cfg.Environment qui active ce
	// cron. Toute autre valeur (production, development, demo, "") → no-op.
	stagingDeployEnv = "staging"
)

// TestTenantsCleanupStats est la snapshot de télémétrie du cron exposée par
// l'endpoint GET /api/veridian/admin/test-tenants-stats. Tous les compteurs
// sont best-effort en mémoire (perte au restart container = acceptable).
type TestTenantsCleanupStats struct {
	TotalTestTenants      int       `json:"total_test_tenants"`
	OrphansOlderThan1h    int       `json:"orphans_older_than_1h"`
	LastAutoCleanupAt     time.Time `json:"last_auto_cleanup_at,omitempty"`
	LastCleanupWipedCount int       `json:"last_cleanup_wiped_count"`
	Enabled               bool      `json:"enabled"` // false si pas staging
}

// VeridianTestTenantsCleanupService est le cron qui purge périodiquement
// les workspaces orphelins staging matchant le prefix `tst`.
type VeridianTestTenantsCleanupService struct {
	veridianSvc  domain.VeridianService
	workspaceSvc WorkspaceLister
	logger       logger.Logger
	deployEnv    string
	interval     time.Duration

	// État de télémétrie best-effort en mémoire. Protégé par mu.
	mu              sync.RWMutex
	lastCleanupAt   time.Time
	lastWipedCount  int
}

// WorkspaceLister est la SOUS-interface minimale de WorkspaceRepository dont
// le cron a besoin. Permet de mocker sans tirer toute la surface du repo.
type WorkspaceLister interface {
	List(ctx context.Context) ([]*domain.Workspace, error)
}

// NewVeridianTestTenantsCleanupService crée le service. Interval par défaut
// 30 min si <=0 (test override). deployEnv vient de cfg.Environment.
func NewVeridianTestTenantsCleanupService(
	veridianSvc domain.VeridianService,
	workspaceSvc WorkspaceLister,
	log logger.Logger,
	deployEnv string,
	interval time.Duration,
) *VeridianTestTenantsCleanupService {
	if interval <= 0 {
		interval = testTenantsCleanupInterval
	}
	return &VeridianTestTenantsCleanupService{
		veridianSvc:  veridianSvc,
		workspaceSvc: workspaceSvc,
		logger:       log,
		deployEnv:    deployEnv,
		interval:     interval,
	}
}

// IsEnabled retourne true si le cron est activé (cfg.Environment == "staging"
// + dépendances présentes). Permet aux tests et au handler stats de tester
// le garde-fou sans démarrer la goroutine.
func (s *VeridianTestTenantsCleanupService) IsEnabled() bool {
	return s.deployEnv == stagingDeployEnv && s.veridianSvc != nil && s.workspaceSvc != nil
}

// Start lance le scheduler si et seulement si IsEnabled() == true. Sinon
// no-op silencieux + log info au boot (audit : on doit voir dans les logs
// prod "cron disabled, env=production" pour confirmer le garde-fou).
//
// Retourne immédiatement. La goroutine s'arrête quand ctx est cancelled.
func (s *VeridianTestTenantsCleanupService) Start(ctx context.Context) {
	if !s.IsEnabled() {
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"deploy_env":      s.deployEnv,
				"has_veridian":    s.veridianSvc != nil,
				"has_workspaces":  s.workspaceSvc != nil,
				"required_env":    stagingDeployEnv,
			}).Info("VeridianTestTenantsCleanup: disabled (not staging or missing deps), scheduler skipped")
		}
		return
	}

	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"interval":     s.interval.String(),
			"prefix":       testTenantsCleanupPrefix,
			"min_age":      testTenantsCleanupMinAge.String(),
			"max_per_tick": testTenantsCleanupMaxPerTick,
		}).Info("VeridianTestTenantsCleanup: staging cron started")
	}

	go func() {
		// Run immédiat pour rattraper si l'app était down depuis longtemps.
		s.runOnce(ctx)

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				if s.logger != nil {
					s.logger.Info("VeridianTestTenantsCleanup: context cancelled, stopping")
				}
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
}

// runOnce exécute un tick : liste workspaces, filtre prefix+age, cap 100,
// délègue le hard delete à veridianService.WipeTestTenants. Best-effort :
// toute erreur est logguée + scheduler continue.
func (s *VeridianTestTenantsCleanupService) runOnce(ctx context.Context) {
	start := time.Now()
	cutoff := start.Add(-testTenantsCleanupMinAge)

	workspaces, err := s.workspaceSvc.List(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"error":   err.Error(),
				"elapsed": time.Since(start).String(),
			}).Warn("VeridianTestTenantsCleanup: workspace list failed")
		}
		return
	}

	// Filtre prefix `tst` + age > 1h. On garde un counter total séparé pour
	// la télémétrie stats endpoint.
	candidates := make([]string, 0, testTenantsCleanupMaxPerTick)
	totalTest := 0
	orphansOld := 0
	for _, w := range workspaces {
		if w == nil || !strings.HasPrefix(w.ID, testTenantsCleanupPrefix) {
			continue
		}
		totalTest++
		if !w.CreatedAt.Before(cutoff) {
			// Trop récent : encore dans la fenêtre des E2E actifs.
			continue
		}
		orphansOld++
		if len(candidates) < testTenantsCleanupMaxPerTick {
			candidates = append(candidates, w.ID)
		}
	}

	// Mise à jour télémétrie même si rien à wiper (preuve que le cron tourne).
	now := time.Now().UTC()

	if len(candidates) == 0 {
		s.recordTick(now, 0)
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"total_test_tenants":   totalTest,
				"orphans_older_than_1h": orphansOld,
				"elapsed":              time.Since(start).String(),
			}).Info("VeridianTestTenantsCleanup: nothing to wipe")
		}
		return
	}

	// Délégation au service existant : applique défaut safety prefixes (canary,
	// clients réels, etc.) + flow hard delete éprouvé. On passe TenantIDs exact
	// (pas Prefix) pour ne wiper QUE notre sélection filtrée par age + cap.
	resp, wipeErr := s.veridianSvc.WipeTestTenants(ctx, domain.WipeTestTenantsInput{
		TenantIDs: candidates,
	})
	if wipeErr != nil {
		// Le service a refusé l'input (ex: prefix vide ET tenant_ids vide → ne
		// peut pas arriver ici puisqu'on a vérifié len > 0). Log + scheduler
		// continue.
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"error":      wipeErr.Error(),
				"candidates": len(candidates),
				"elapsed":    time.Since(start).String(),
			}).Warn("VeridianTestTenantsCleanup: WipeTestTenants failed")
		}
		s.recordTick(now, 0)
		return
	}

	wiped := 0
	skipped := 0
	errs := 0
	if resp != nil {
		wiped = len(resp.Wiped)
		skipped = len(resp.Skipped)
		errs = len(resp.Errors)
	}
	s.recordTick(now, wiped)

	if s.logger != nil {
		fields := map[string]interface{}{
			"total_test_tenants":   totalTest,
			"orphans_older_than_1h": orphansOld,
			"candidates":           len(candidates),
			"wiped":                wiped,
			"skipped":              skipped,
			"errors":               errs,
			"elapsed":              time.Since(start).String(),
		}
		// Warn (pas Info) parce que ticket exige "log warn chaque tick avec
		// count + last cleanup timestamp" — facilite l'observabilité staging.
		s.logger.WithFields(fields).Warn("VeridianTestTenantsCleanup: tick completed")
	}
}

// recordTick met à jour la télémétrie sous lock.
func (s *VeridianTestTenantsCleanupService) recordTick(at time.Time, wiped int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCleanupAt = at
	s.lastWipedCount = wiped
}

// Stats renvoie un snapshot best-effort de l'état du cron + une mesure live
// de total_test_tenants / orphans_older_than_1h. Appelé par le handler
// GET /api/veridian/admin/test-tenants-stats. Si !IsEnabled retourne
// Enabled:false + le reste à zéro.
func (s *VeridianTestTenantsCleanupService) Stats(ctx context.Context) (*TestTenantsCleanupStats, error) {
	s.mu.RLock()
	lastAt := s.lastCleanupAt
	lastWiped := s.lastWipedCount
	s.mu.RUnlock()

	stats := &TestTenantsCleanupStats{
		LastAutoCleanupAt:     lastAt,
		LastCleanupWipedCount: lastWiped,
		Enabled:               s.IsEnabled(),
	}

	// Si dépendances absentes, on retourne stats partielles sans planter.
	if s.workspaceSvc == nil {
		return stats, nil
	}

	workspaces, err := s.workspaceSvc.List(ctx)
	if err != nil {
		// Best-effort : on rend stats partielles (compteurs live à 0).
		return stats, err
	}

	cutoff := time.Now().Add(-testTenantsCleanupMinAge)
	for _, w := range workspaces {
		if w == nil || !strings.HasPrefix(w.ID, testTenantsCleanupPrefix) {
			continue
		}
		stats.TotalTestTenants++
		if w.CreatedAt.Before(cutoff) {
			stats.OrphansOlderThan1h++
		}
	}
	return stats, nil
}
