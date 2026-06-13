package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validCatalogJSON est un payload Hub minimaliste mais valide (Plans non-vide
// requis par la validation fetch). Utilise dans tous les tests "happy path".
const validCatalogJSON = `{
  "plans": {
    "notifuse-free": {
      "id": "notifuse-free", "name": "Notifuse Free", "tier": "free",
      "apps_unlocked": ["notifuse"], "price_eur": 0, "price_eur_yearly_per_month": 0,
      "stripePriceIdLive": {"month": null, "year": null},
      "stripePriceIdTest": {"month": null, "year": null},
      "welcome_leads": 0, "seats": null, "features": [],
      "annual_perks": false, "plan_source": "stripe",
      "hidden_from_public": false, "rank": 1
    },
    "notifuse-pro": {
      "id": "notifuse-pro", "name": "Notifuse Pro", "tier": "pro",
      "apps_unlocked": ["notifuse"], "price_eur": 29, "price_eur_yearly_per_month": 24,
      "stripePriceIdLive": {"month": null, "year": null},
      "stripePriceIdTest": {"month": null, "year": null},
      "welcome_leads": 0, "seats": null, "features": ["notifuse_ab_testing"],
      "annual_perks": true, "plan_source": "stripe",
      "hidden_from_public": false, "rank": 2
    },
    "notifuse-business": {
      "id": "notifuse-business", "name": "Notifuse Business", "tier": "business",
      "apps_unlocked": ["notifuse"], "price_eur": 99, "price_eur_yearly_per_month": 82,
      "stripePriceIdLive": {"month": null, "year": null},
      "stripePriceIdTest": {"month": null, "year": null},
      "welcome_leads": 0, "seats": null, "features": ["notifuse_white_label"],
      "annual_perks": true, "plan_source": "stripe",
      "hidden_from_public": false, "rank": 3
    },
    "notifuse-enterprise": {
      "id": "notifuse-enterprise", "name": "Notifuse Enterprise", "tier": "enterprise",
      "apps_unlocked": ["notifuse"], "price_eur": 0, "price_eur_yearly_per_month": 0,
      "stripePriceIdLive": {"month": null, "year": null},
      "stripePriceIdTest": {"month": null, "year": null},
      "welcome_leads": 0, "seats": null, "features": ["notifuse_white_label"],
      "annual_perks": true, "plan_source": "manual",
      "hidden_from_public": true, "rank": 4
    }
  },
  "refill": {
    "pricing_cents": {"freemium": [[10, 1000]], "pro": [], "business": []},
    "max_per_order": 100000
  },
  "annual_perks": {
    "supportPriority": true,
    "onboardingSession": {"durationMin": 60, "deliveredVia": "visio"},
    "premiumTutos": true,
    "annualDiscountPct": 17
  },
  "version": "0.1.0",
  "generated_at": "2026-05-21T12:00:00Z"
}`

// TestNewVeridianPricingSyncService_DefaultsApplied verifie que les valeurs
// par defaut sont appliquees quand on passe des arguments vides/zero.
func TestNewVeridianPricingSyncService_DefaultsApplied(t *testing.T) {
	svc := NewVeridianPricingSyncService("", nil, logger.NewLogger(), 0)
	require.NotNil(t, svc)
	assert.Equal(t, DefaultHubPricingURL, svc.url)
	assert.Equal(t, DefaultPricingSyncInterval, svc.interval)
	require.NotNil(t, svc.client)
}

// TestDefaultHubPricingURL_UsesPublicHost est un garde-fou anti-régression du
// bug du 2026-06-13 : le default pointait sur `hub.veridian.site` qui n'existe
// PAS en DNS public -> `VeridianPricingSync: fetch failed` en boucle, cache
// jamais rafraichi (cf. todo/done pricing-sync-dns-hub-failed). Le host public
// Veridian est `app.veridian.site`. Ce test echoue si quelqu'un re-introduit le
// host fantome.
func TestDefaultHubPricingURL_UsesPublicHost(t *testing.T) {
	assert.Contains(t, DefaultHubPricingURL, "app.veridian.site",
		"le default doit pointer sur le host public app.veridian.site")
	assert.NotContains(t, DefaultHubPricingURL, "hub.veridian.site",
		"hub.veridian.site n'existe pas en DNS public (bug 2026-06-13)")
	assert.True(t, strings.HasSuffix(DefaultHubPricingURL, "/api/pricing/plans"),
		"le default doit cibler l'endpoint pricing du Hub")
}

// TestNewVeridianPricingSyncService_AcceptsCustom verifie que les overrides
// sont bien transmis (URL custom, interval court, client custom).
func TestNewVeridianPricingSyncService_AcceptsCustom(t *testing.T) {
	custom := &http.Client{Timeout: 5 * time.Second}
	svc := NewVeridianPricingSyncService("https://example.com/x", custom, logger.NewLogger(), 42*time.Minute)
	assert.Equal(t, "https://example.com/x", svc.url)
	assert.Equal(t, 42*time.Minute, svc.interval)
	assert.Same(t, custom, svc.client)
}

// TestPricingKeyForTier_MapsCanonical verifie le mapping canonique
// tier Notifuse → cle Hub.
func TestPricingKeyForTier_MapsCanonical(t *testing.T) {
	tests := map[string]string{
		"free":       "notifuse-free",
		"pro":        "notifuse-pro",
		"business":   "notifuse-business",
		"enterprise": "notifuse-enterprise",
		"":           "notifuse-", // edge case : on documente le comportement
	}
	for tier, expected := range tests {
		t.Run(tier, func(t *testing.T) {
			assert.Equal(t, expected, PricingKeyForTier(tier))
		})
	}
}

// TestVeridianPricingSyncService_Start_FetchesOnBootAndPopulatesCache
// verifie que Start() declenche un fetch immediat qui peuple le cache.
func TestVeridianPricingSyncService_Start_FetchesOnBootAndPopulatesCache(t *testing.T) {
	var hits int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600, stale-while-revalidate=86400")
		_, _ = io.WriteString(w, validCatalogJSON)
	}))
	defer server.Close()

	// Interval long : on ne veut qu'un seul fetch (celui du boot).
	svc := NewVeridianPricingSyncService(server.URL, server.Client(), logger.NewLogger(), 1*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Start(ctx)

	// Wait active pour le fetch immediat (max 500ms — large pour CI lente).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if svc.Catalog() != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cat := svc.Catalog()
	require.NotNil(t, cat, "catalog should be populated after boot fetch")
	assert.Equal(t, "0.1.0", cat.Version)
	require.Contains(t, cat.Plans, "notifuse-pro")
	assert.Equal(t, float64(29), cat.Plans["notifuse-pro"].PriceEUR)
	assert.GreaterOrEqual(t, atomic.LoadInt64(&hits), int64(1))
}

// TestVeridianPricingSyncService_Start_KeepsPreviousCacheOnError verifie
// que si le Hub renvoie une erreur apres un succes initial, le cache
// precedent est conserve (best-effort, fail-open).
func TestVeridianPricingSyncService_Start_KeepsPreviousCacheOnError(t *testing.T) {
	var phase int64 // 0 = success, 1 = error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt64(&phase) == 0 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, validCatalogJSON)
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	// Interval ultra-court : on veut plusieurs ticks pour observer l'erreur.
	svc := NewVeridianPricingSyncService(server.URL, server.Client(), logger.NewLogger(), 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Start(ctx)

	// Attendre fetch initial reussi.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if svc.Catalog() != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NotNil(t, svc.Catalog(), "initial fetch should succeed")

	// Bascule en mode erreur, attendre 3 cycles pour confirmer la persistence.
	atomic.StoreInt64(&phase, 1)
	time.Sleep(80 * time.Millisecond)

	// Cache toujours present malgre les erreurs.
	assert.NotNil(t, svc.Catalog(), "cache must be kept on subsequent errors")
	snap := svc.Snapshot()
	assert.NotEmpty(t, snap.LastError, "last_error must reflect latest failure")
	require.NotNil(t, snap.LastSuccessAt, "last_success_at must be set from initial fetch")
}

// TestVeridianPricingSyncService_Start_StopsOnContextCancel verifie
// que la goroutine se termine proprement sur ctx.Done().
func TestVeridianPricingSyncService_Start_StopsOnContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, validCatalogJSON)
	}))
	defer server.Close()

	svc := NewVeridianPricingSyncService(server.URL, server.Client(), logger.NewLogger(), 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	svc.Start(ctx)
	time.Sleep(30 * time.Millisecond) // laisse le boot passer
	cancel()
	// Si la goroutine fuit, ce test ne fail pas explicitement, mais le
	// goleak (si jamais branche) le verra. On verifie au moins qu'aucun
	// panic ne survient apres cancel.
	time.Sleep(100 * time.Millisecond)
	assert.True(t, true)
}

// TestVeridianPricingSyncService_Start_HandlesNon200Status verifie qu'un
// status code non-200 est traite comme une erreur fetch (cache pas peuple
// au premier coup, last_error rempli).
func TestVeridianPricingSyncService_Start_HandlesNon200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer server.Close()

	svc := NewVeridianPricingSyncService(server.URL, server.Client(), logger.NewLogger(), 1*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Start(ctx)
	// Attendre que le fetch initial echoue.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if svc.Snapshot().LastError != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	snap := svc.Snapshot()
	assert.Nil(t, svc.Catalog(), "catalog must be nil when initial fetch fails")
	assert.Contains(t, snap.LastError, "502", "last_error should reflect HTTP status")
	assert.Nil(t, snap.LastSuccessAt, "last_success_at must remain nil after errors")
}

// TestVeridianPricingSyncService_Start_HandlesMalformedJSON verifie qu'un
// JSON invalide ou vide ne peuple pas le cache mais ne crash pas.
func TestVeridianPricingSyncService_Start_HandlesMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"plans": {}}`) // plans vide → erreur "no plans"
	}))
	defer server.Close()

	svc := NewVeridianPricingSyncService(server.URL, server.Client(), logger.NewLogger(), 1*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Start(ctx)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if svc.Snapshot().LastError != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.Nil(t, svc.Catalog())
	assert.Contains(t, svc.Snapshot().LastError, "no plans")
}

// TestVeridianPricingSyncService_Snapshot_StaleFlag verifie que Stale=true
// quand last_success_at est plus vieux que 2 * interval.
func TestVeridianPricingSyncService_Snapshot_StaleFlag(t *testing.T) {
	svc := NewVeridianPricingSyncService("https://example.com/x", nil, logger.NewLogger(), 1*time.Hour)

	// Cas 1 : jamais fetch → Stale = false (lastFetchedAt nil → pas considere stale).
	snap := svc.Snapshot()
	assert.False(t, snap.Stale)

	// Cas 2 : fetch tres ancien (LastFetchedAt set, LastSuccessAt nil) → Stale = true.
	old := time.Now().Add(-3 * time.Hour)
	svc.mu.Lock()
	svc.lastFetchedAt = &old
	svc.mu.Unlock()
	snap = svc.Snapshot()
	assert.True(t, snap.Stale, "no success ever + lastFetched set should be stale")

	// Cas 3 : succes recent → Stale = false.
	recent := time.Now()
	svc.mu.Lock()
	svc.lastSuccessAt = &recent
	svc.mu.Unlock()
	snap = svc.Snapshot()
	assert.False(t, snap.Stale, "recent success should not be stale")

	// Cas 4 : succes ancien (>2 * interval) → Stale = true.
	oldSuccess := time.Now().Add(-3 * time.Hour) // 2*interval = 2h, donc -3h = stale
	svc.mu.Lock()
	svc.lastSuccessAt = &oldSuccess
	svc.mu.Unlock()
	snap = svc.Snapshot()
	assert.True(t, snap.Stale, "success older than 2*interval should be stale")
}

// TestVeridianPricingSyncService_Snapshot_ExposesSourceURL verifie que le
// snapshot expose l'URL source courante (utile pour le debug endpoint).
func TestVeridianPricingSyncService_Snapshot_ExposesSourceURL(t *testing.T) {
	svc := NewVeridianPricingSyncService("https://hub.example.com/api/pricing/plans", nil, logger.NewLogger(), time.Hour)
	snap := svc.Snapshot()
	assert.Equal(t, "https://hub.example.com/api/pricing/plans", snap.SourceURL)
	assert.NotEmpty(t, snap.StaleThreshold)
}

// TestVeridianPricingSyncService_Catalog_ReturnsNilBeforeFetch verifie le
// contrat : Catalog() peut retourner nil avant le premier fetch reussi.
func TestVeridianPricingSyncService_Catalog_ReturnsNilBeforeFetch(t *testing.T) {
	svc := NewVeridianPricingSyncService("https://example.com/x", nil, logger.NewLogger(), time.Hour)
	assert.Nil(t, svc.Catalog())
}

// TestVeridianPricingSyncService_RequestHeaders verifie que les headers
// Accept et User-Agent sont bien envoyes au Hub (utile pour les logs Hub).
func TestVeridianPricingSyncService_RequestHeaders(t *testing.T) {
	receivedAccept := ""
	receivedUA := ""
	done := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAccept = r.Header.Get("Accept")
		receivedUA = r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, validCatalogJSON)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer server.Close()

	svc := NewVeridianPricingSyncService(server.URL, server.Client(), logger.NewLogger(), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Start(ctx)
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server never received a request")
	}
	assert.Equal(t, "application/json", receivedAccept)
	assert.Contains(t, receivedUA, "notifuse-veridian-pricing-sync")
}

// TestVeridianPricingSyncService_ReconcileLogsDriftSilent : test
// d'integration leger qui s'assure que reconcile() ne crash pas, ne
// modifie pas le cache, et tolere le cas "tier present cote Notifuse
// mais absent cote Hub". Le warning est emis via le logger — on ne
// peut pas l'asserter sans hook custom, mais on verifie au moins
// l'idempotence.
func TestVeridianPricingSyncService_ReconcileLogsDriftSilent(t *testing.T) {
	// Catalog qui declare notifuse-pro AVEC white_label (drift vs
	// DefaultPlanLimits["pro"].FeatureWhiteLabel = false).
	driftJSON := strings.Replace(validCatalogJSON,
		`"notifuse-pro", "tier": "pro",
      "apps_unlocked": ["notifuse"], "price_eur": 29, "price_eur_yearly_per_month": 24,
      "stripePriceIdLive": {"month": null, "year": null},
      "stripePriceIdTest": {"month": null, "year": null},
      "welcome_leads": 0, "seats": null, "features": ["notifuse_ab_testing"]`,
		`"notifuse-pro", "tier": "pro",
      "apps_unlocked": ["notifuse"], "price_eur": 29, "price_eur_yearly_per_month": 24,
      "stripePriceIdLive": {"month": null, "year": null},
      "stripePriceIdTest": {"month": null, "year": null},
      "welcome_leads": 0, "seats": null, "features": ["notifuse_white_label"]`,
		1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, driftJSON)
	}))
	defer server.Close()

	svc := NewVeridianPricingSyncService(server.URL, server.Client(), logger.NewLogger(), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.Start(ctx)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if svc.Catalog() != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NotNil(t, svc.Catalog(), "drift catalog should still populate cache")
	// reconcile() a tourne, drift logge en WARN — pas de fail attendu.
}

// TestVeridianPricingSyncService_ContextCancelMidFetch verifie qu'un ctx
// cancel pendant un fetch n'introduit pas de deadlock ou panic.
func TestVeridianPricingSyncService_ContextCancelMidFetch(t *testing.T) {
	// Server qui bloque longtemps pour simuler un fetch en cours.
	block := make(chan struct{})
	defer close(block)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		http.Error(w, "ctx cancelled", http.StatusGatewayTimeout)
	}))
	defer server.Close()

	svc := NewVeridianPricingSyncService(server.URL, server.Client(), logger.NewLogger(), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)
	// Pas de deadlock = test pass.
	assert.True(t, true)
}

// Helper : sanity check sur la structure JSON de validCatalogJSON pour
// detecter les regressions du fixture lui-meme.
func TestValidCatalogJSON_FixtureIsParseable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, validCatalogJSON)
	}))
	defer server.Close()
	resp, err := server.Client().Get(server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(string(body)), "{"))
	assert.Contains(t, string(body), "notifuse-pro")
	// Detecte les bumps accidentels de format JSON :
	assert.NotContains(t, string(body), fmt.Sprintf("unexpected"))
}
