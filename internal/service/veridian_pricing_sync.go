package service

// === Veridian patch — lot O (2026-05-21) ===
// VeridianPricingSyncService : consomme le catalogue pricing canonique
// expose par le Hub Veridian (GET app.veridian.site/api/pricing/plans)
// et le maintient en cache memoire pour rester aligne sur la source de
// verite (veridian-infra/shared/pricing/plans.ts).
//
// Pourquoi :
//   - Le pricing Veridian est centralise dans `veridian-infra/shared/` (TS).
//   - Les apps TS (Hub, Prospection) consomment via Git submodule.
//   - Notifuse est en Go → ne peut pas importer directement le shared.
//   - Le Hub expose le catalogue en JSON pour Notifuse.
//
// Strategie :
//   - Fetch au boot + cron 1h (pattern goroutine + ticker, identique a
//     veridian_idempotency_cleanup.go).
//   - Cache memoire thread-safe (sync.RWMutex).
//   - Best-effort : si Hub down, on conserve le cache precedent et
//     on continue avec les valeurs hardcodees `domain.DefaultPlanLimits`.
//   - Reconcile : log warn si divergence detectee entre le JSON Hub
//     et `domain.DefaultPlanLimits` (drift detection, sans fail).
//
// Non-objectif (ticket P2) :
//   - Ne PAS rebrancher le paywall middleware sur le cache. Le cache
//     est observationnel : Notifuse continue d'utiliser ses valeurs
//     hardcodees pour les decisions runtime. Le cache sert :
//     1) a detecter le drift (alerting),
//     2) a alimenter un futur endpoint /api/veridian/limits enrichi,
//     3) a faciliter le pivot pricing suivant (Robert verra l'ecart).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// DefaultHubPricingURL est l'endpoint canonique du Hub (host public Veridian
// = `app.veridian.site`, PAS `hub.veridian.site` qui n'existe pas en DNS
// public — bug corrigé 2026-06-13, cf. todo/done pricing-sync-dns-hub-failed).
//
// En pratique l'URL réelle est dérivée de `HUB_BASE_URL` côté app.go
// (`{HUB_BASE_URL}/api/pricing/plans`), pour rester aligné sur le reste du
// câblage Hub (invitation client, webhooks). Ce default ne s'applique QUE si
// HUB_BASE_URL est vide (mode self-hosted sans Hub) ou en test.
const DefaultHubPricingURL = "https://app.veridian.site/api/pricing/plans"

// DefaultPricingSyncInterval est la frequence de refresh du cache. Aligne
// sur le Cache-Control max-age=3600 expose par le Hub : on rafraichit en
// meme temps que le CDN, pas plus souvent.
const DefaultPricingSyncInterval = 1 * time.Hour

// DefaultPricingHTTPTimeout : 10s. Le Hub est cacheable (1h CDN), donc
// la latence p50 < 100ms. 10s tolere un cold start CDN ou un incident
// reseau sans bloquer le boot Notifuse.
const DefaultPricingHTTPTimeout = 10 * time.Second

// PlanCatalogEntry est le miroir Go de la struct exposee par le Hub.
// Convention JSON identique au shared TS (veridian-infra/shared/pricing/plans.ts).
//
// Les champs nullable cote TS sont *T cote Go : Seats (null = illimite),
// StripePriceIds (null = pas de prix Stripe configure).
type PlanCatalogEntry struct {
	ID                     string         `json:"id"`
	Name                   string         `json:"name"`
	Tier                   string         `json:"tier"` // free | pro | business | enterprise
	AppsUnlocked           []string       `json:"apps_unlocked"`
	PriceEUR               float64        `json:"price_eur"`
	PriceEURYearlyPerMonth float64        `json:"price_eur_yearly_per_month"`
	StripePriceIDLive      StripePriceIDs `json:"stripePriceIdLive"`
	StripePriceIDTest      StripePriceIDs `json:"stripePriceIdTest"`
	WelcomeLeads           int            `json:"welcome_leads"`
	Seats                  *int           `json:"seats"` // null = illimite
	Features               []string       `json:"features"`
	AnnualPerks            bool           `json:"annual_perks"`
	PlanSource             string         `json:"plan_source"` // stripe | manual | lifetime_* | internal
	HiddenFromPublic       bool           `json:"hidden_from_public"`
	Rank                   int            `json:"rank"`
}

// StripePriceIDs : "month" et "year" peuvent etre null cote Hub.
type StripePriceIDs struct {
	Month *string `json:"month"`
	Year  *string `json:"year"`
}

// RefillCatalog miroir des prix de refill leads (Veridian Prospection).
type RefillCatalog struct {
	PricingCents map[string][][2]int `json:"pricing_cents"`
	MaxPerOrder  int                 `json:"max_per_order"`
}

// AnnualPerksCatalog miroir des perks abonnement annuel.
type AnnualPerksCatalog struct {
	SupportPriority   bool `json:"supportPriority"`
	OnboardingSession struct {
		DurationMin   int    `json:"durationMin"`
		DeliveredVia  string `json:"deliveredVia"`
	} `json:"onboardingSession"`
	PremiumTutos      bool `json:"premiumTutos"`
	AnnualDiscountPct int  `json:"annualDiscountPct"`
}

// PricingCatalog est la racine du JSON renvoye par le Hub.
type PricingCatalog struct {
	Plans       map[string]PlanCatalogEntry `json:"plans"`
	Refill      RefillCatalog               `json:"refill"`
	AnnualPerks AnnualPerksCatalog          `json:"annual_perks"`
	Version     string                      `json:"version"`
	GeneratedAt time.Time                   `json:"generated_at"`
}

// PricingCacheSnapshot est ce qui est expose au debug endpoint
// /api/veridian/admin/pricing-cache. Inclut les metadonnees de
// fraicheur du cache (last_fetched_at, last_error).
type PricingCacheSnapshot struct {
	Catalog        *PricingCatalog `json:"catalog,omitempty"`
	LastFetchedAt  *time.Time      `json:"last_fetched_at,omitempty"`
	LastSuccessAt  *time.Time      `json:"last_success_at,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	SourceURL      string          `json:"source_url"`
	Stale          bool            `json:"stale"`
	StaleThreshold string          `json:"stale_threshold"`
}

// VeridianPricingSyncService maintient un cache en memoire du catalogue
// Hub. Thread-safe via sync.RWMutex.
//
// Lifecycle : Start(ctx) lance la goroutine de refresh, ctx.Done() l'arrete.
// Pas de Stop() explicite — ctx suffit.
type VeridianPricingSyncService struct {
	url      string
	client   *http.Client
	logger   logger.Logger
	interval time.Duration

	mu             sync.RWMutex
	catalog        *PricingCatalog
	lastFetchedAt  *time.Time
	lastSuccessAt  *time.Time
	lastError      string
}

// NewVeridianPricingSyncService construit le service. url/interval/client
// utilisent les defaults si vide/nil/zero. La signature est intentionnellement
// large pour permettre l'injection en tests (httptest.NewServer + URL custom).
func NewVeridianPricingSyncService(
	url string,
	client *http.Client,
	log logger.Logger,
	interval time.Duration,
) *VeridianPricingSyncService {
	if url == "" {
		url = DefaultHubPricingURL
	}
	if client == nil {
		client = &http.Client{Timeout: DefaultPricingHTTPTimeout}
	}
	if interval <= 0 {
		interval = DefaultPricingSyncInterval
	}
	return &VeridianPricingSyncService{
		url:      url,
		client:   client,
		logger:   log,
		interval: interval,
	}
}

// Start lance la goroutine de refresh. Retourne immediatement.
// Pattern identique a VeridianIdempotencyCleanupService :
//   - exec immediate au boot pour primer le cache,
//   - puis ticker periodique.
//
// La goroutine s'arrete quand ctx est cancelled (typiquement
// app.GetShutdownContext()).
func (s *VeridianPricingSyncService) Start(ctx context.Context) {
	go func() {
		s.runOnce(ctx)

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				if s.logger != nil {
					s.logger.Info("VeridianPricingSync: context cancelled, stopping")
				}
				return
			case <-ticker.C:
				s.runOnce(ctx)
			}
		}
	}()
}

// runOnce fetche le catalogue, met a jour le cache et log le resultat.
// Best-effort : sur erreur, on conserve le cache precedent.
func (s *VeridianPricingSyncService) runOnce(ctx context.Context) {
	start := time.Now()
	catalog, err := s.fetch(ctx)
	elapsed := time.Since(start)
	now := time.Now().UTC()

	s.mu.Lock()
	s.lastFetchedAt = &now
	if err != nil {
		s.lastError = err.Error()
		s.mu.Unlock()
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"error":   err.Error(),
				"elapsed": elapsed.String(),
				"url":     s.url,
			}).Warn("VeridianPricingSync: fetch failed, keeping previous cache")
		}
		return
	}

	s.catalog = catalog
	s.lastSuccessAt = &now
	s.lastError = ""
	s.mu.Unlock()

	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"elapsed":      elapsed.String(),
			"plans_count":  len(catalog.Plans),
			"catalog_ver":  catalog.Version,
			"generated_at": catalog.GeneratedAt,
		}).Info("VeridianPricingSync: catalog refreshed")
	}

	// Reconcile : log warn si divergence avec DefaultPlanLimits. Pas de fail.
	s.reconcile(catalog)
}

// fetch effectue le GET HTTP et decode la reponse JSON.
func (s *VeridianPricingSyncService) fetch(ctx context.Context) (*PricingCatalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "notifuse-veridian-pricing-sync/1.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get %s: %w", s.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("unexpected status %d from %s: %s", resp.StatusCode, s.url, strings.TrimSpace(string(body)))
	}

	var catalog PricingCatalog
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode pricing catalog: %w", err)
	}
	if len(catalog.Plans) == 0 {
		return nil, fmt.Errorf("pricing catalog has no plans")
	}
	return &catalog, nil
}

// reconcile compare le catalogue Hub avec domain.DefaultPlanLimits et log
// un WARN si divergence est detectee. N'effectue aucun changement runtime.
//
// Convention de mapping : la cle canonique du Hub pour Notifuse est
// "notifuse-<tier>" (ex: "notifuse-pro", "notifuse-business"). Le tier
// est ce que Notifuse stocke en DB ("free", "pro", "business", "enterprise").
//
// La verification porte sur la feature white_label (seule difference
// enforced cote Notifuse post-pivot 2026-05-21) : si Hub dit
// notifuse-pro a "notifuse_white_label" dans features mais
// DefaultPlanLimits["pro"].FeatureWhiteLabel == false (ou vice-versa),
// on log warn. Toutes les autres dimensions = -1/true partout post-pivot,
// donc rien d'autre a verifier.
func (s *VeridianPricingSyncService) reconcile(catalog *PricingCatalog) {
	if catalog == nil || s.logger == nil {
		return
	}
	for tier, defaults := range domain.DefaultPlanLimits {
		key := PricingKeyForTier(tier)
		entry, ok := catalog.Plans[key]
		if !ok {
			// Plan Notifuse absent du catalogue Hub : c'est une dette catalogue
			// (Hub n'expose pas tous les plans), pas une erreur.
			s.logger.WithFields(map[string]interface{}{
				"tier":         tier,
				"expected_key": key,
			}).Warn("VeridianPricingSync: tier missing from Hub catalog")
			continue
		}
		hubHasWhiteLabel := false
		for _, f := range entry.Features {
			if f == FeatureNotifuseWhiteLabel {
				hubHasWhiteLabel = true
				break
			}
		}
		if hubHasWhiteLabel != defaults.FeatureWhiteLabel {
			s.logger.WithFields(map[string]interface{}{
				"tier":                 tier,
				"hub_key":              key,
				"hub_has_white_label":  hubHasWhiteLabel,
				"local_white_label":    defaults.FeatureWhiteLabel,
				"action":               "manual_review_required",
			}).Warn("VeridianPricingSync: white_label drift between Hub catalog and DefaultPlanLimits")
		}
	}
}

// PricingKeyForTier mappe un tier Notifuse vers la cle canonique du Hub.
// Convention shared TS (veridian-infra/shared/pricing/plans.ts) :
//   - tier "free"       → "notifuse-free"
//   - tier "pro"        → "notifuse-pro"
//   - tier "business"   → "notifuse-business"
//   - tier "enterprise" → "notifuse-enterprise"
func PricingKeyForTier(tier string) string {
	return "notifuse-" + tier
}

// FeatureNotifuseWhiteLabel est le code feature canonique cote Hub pour
// debloquer le white-label custom Notifuse (Business+ uniquement).
const FeatureNotifuseWhiteLabel = "notifuse_white_label"

// Snapshot retourne une copie immuable de l'etat courant du cache. Utilise
// par le handler debug /api/veridian/admin/pricing-cache.
//
// `Stale` = true si lastSuccessAt est plus vieux que 2 * interval (signal :
// le Hub est down depuis 2 cycles, alerter Robert via observation manuelle).
func (s *VeridianPricingSyncService) Snapshot() PricingCacheSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stale := false
	staleThreshold := 2 * s.interval
	if s.lastSuccessAt != nil {
		stale = time.Since(*s.lastSuccessAt) > staleThreshold
	} else if s.lastFetchedAt != nil {
		// Jamais fetch avec succes : forcement stale.
		stale = true
	}

	return PricingCacheSnapshot{
		Catalog:        s.catalog,
		LastFetchedAt:  s.lastFetchedAt,
		LastSuccessAt:  s.lastSuccessAt,
		LastError:      s.lastError,
		SourceURL:      s.url,
		Stale:          stale,
		StaleThreshold: staleThreshold.String(),
	}
}

// Catalog retourne le catalogue en cache (peut etre nil si premier fetch
// n'a pas reussi). Thread-safe.
func (s *VeridianPricingSyncService) Catalog() *PricingCatalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalog
}
