package queue

import (
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func floatPtr(f float64) *float64 { return &f }

// constRNG retourne toujours v (figé) pour rendre veridianApplyJitter
// déterministe dans les tests.
func constRNG(v float64) func() float64 { return func() float64 { return v } }

func TestVeridianApplyJitter(t *testing.T) {
	base := 60 * time.Second

	tests := []struct {
		name string
		pct  float64
		rng  func() float64
		want time.Duration
	}{
		{
			name: "pct 0 -> no-op exact (non-régression)",
			pct:  0,
			rng:  constRNG(0.5),
			want: base,
		},
		{
			name: "pct négatif -> no-op (défensif)",
			pct:  -0.5,
			rng:  constRNG(0.0),
			want: base,
		},
		{
			name: "rng nil -> no-op",
			pct:  0.3,
			rng:  nil,
			want: base,
		},
		{
			name: "pct 0.3 rng=0 -> delay × 0.7 (borne basse)",
			pct:  0.3,
			rng:  constRNG(0.0),
			want: time.Duration(float64(base) * 0.7),
		},
		{
			// rng() ∈ [0,1) ; on teste la limite haute via une valeur proche de 1.
			name: "pct 0.3 rng≈1 -> delay × ~1.3 (borne haute)",
			pct:  0.3,
			rng:  constRNG(0.9999999),
			want: time.Duration(float64(base) * (1 + (0.9999999*2-1)*0.3)),
		},
		{
			name: "pct 0.3 rng=0.5 -> delay inchangé (milieu)",
			pct:  0.3,
			rng:  constRNG(0.5),
			want: base,
		},
		{
			name: "pct > 0.9 clampé à 0.9, rng=0 -> delay × 0.1",
			pct:  2.0,
			rng:  constRNG(0.0),
			want: time.Duration(float64(base) * 0.1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := veridianApplyJitter(base, tt.pct, tt.rng)
			// InDelta : la formule est en float64, une troncature à la nanoseconde
			// près est attendue (ex. 0.1×60s = 5.999999999s). On tolère 1µs.
			assert.InDelta(t, int64(tt.want), int64(got), float64(time.Microsecond))
			assert.GreaterOrEqual(t, int64(got), int64(0), "jamais négatif")
		})
	}
}

func TestVeridianApplyJitter_NeverNegative(t *testing.T) {
	// Même avec le clamp à 0.9, le facteur minimal est 0.1 → jamais négatif. On
	// vérifie sur de petits délais que la troncature ne passe pas sous 0.
	for _, d := range []time.Duration{time.Second, 100 * time.Millisecond, time.Nanosecond} {
		got := veridianApplyJitter(d, 0.9, constRNG(0.0))
		assert.GreaterOrEqual(t, int64(got), int64(0))
	}
}

func TestVeridianResolveJitterPct_Cascade(t *testing.T) {
	tests := []struct {
		name      string
		workspace *domain.Workspace
		provider  *domain.EmailProvider
		entry     *domain.EmailQueueEntry
		want      float64
	}{
		{
			name:      "aucun niveau défini -> défaut cold 0.30",
			workspace: &domain.Workspace{},
			provider:  &domain.EmailProvider{},
			entry:     &domain.EmailQueueEntry{},
			want:      veridianDefaultJitterPct,
		},
		{
			name:      "tout nil -> défaut cold 0.30",
			workspace: nil,
			provider:  nil,
			entry:     &domain.EmailQueueEntry{},
			want:      veridianDefaultJitterPct,
		},
		{
			// Le piège pointeur : *0 explicite = jitter OFF, distinct de nil.
			name:      "payload *0 -> 0 (jitter OFF voulu, distinct de nil)",
			workspace: &domain.Workspace{Settings: domain.WorkspaceSettings{VeridianJitterPct: floatPtr(0.5)}},
			provider:  &domain.EmailProvider{VeridianJitterPct: floatPtr(0.4)},
			entry:     &domain.EmailQueueEntry{Payload: domain.EmailQueuePayload{VeridianJitterPct: floatPtr(0)}},
			want:      0,
		},
		{
			name:      "payload prime sur infra et workspace",
			workspace: &domain.Workspace{Settings: domain.WorkspaceSettings{VeridianJitterPct: floatPtr(0.5)}},
			provider:  &domain.EmailProvider{VeridianJitterPct: floatPtr(0.4)},
			entry:     &domain.EmailQueueEntry{Payload: domain.EmailQueuePayload{VeridianJitterPct: floatPtr(0.2)}},
			want:      0.2,
		},
		{
			name:      "infra prime sur workspace (payload nil)",
			workspace: &domain.Workspace{Settings: domain.WorkspaceSettings{VeridianJitterPct: floatPtr(0.5)}},
			provider:  &domain.EmailProvider{VeridianJitterPct: floatPtr(0.4)},
			entry:     &domain.EmailQueueEntry{},
			want:      0.4,
		},
		{
			name:      "workspace utilisé si payload+infra nil",
			workspace: &domain.Workspace{Settings: domain.WorkspaceSettings{VeridianJitterPct: floatPtr(0.5)}},
			provider:  &domain.EmailProvider{},
			entry:     &domain.EmailQueueEntry{},
			want:      0.5,
		},
		{
			name:      "provider nil (legacy) -> niveau sauté, fallback workspace",
			workspace: &domain.Workspace{Settings: domain.WorkspaceSettings{VeridianJitterPct: floatPtr(0.5)}},
			provider:  nil,
			entry:     &domain.EmailQueueEntry{},
			want:      0.5,
		},
		{
			name:      "valeur négative clampée à 0",
			workspace: &domain.Workspace{Settings: domain.WorkspaceSettings{VeridianJitterPct: floatPtr(-1)}},
			provider:  &domain.EmailProvider{},
			entry:     &domain.EmailQueueEntry{},
			want:      0,
		},
		{
			name:      "valeur > 0.9 clampée à 0.9",
			workspace: &domain.Workspace{Settings: domain.WorkspaceSettings{VeridianJitterPct: floatPtr(3)}},
			provider:  &domain.EmailProvider{},
			entry:     &domain.EmailQueueEntry{},
			want:      veridianMaxJitterPct,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := veridianResolveJitterPct(tt.workspace, tt.provider, tt.entry)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestVeridianApplyJitter_StaysWithinBounds vérifie qu'un délai nominal de 60s
// avec pct=0.3 produit, sur de nombreux tirages, des délais bornés dans
// [42s, 78s] (= ±30 %), AVANT les bornes 1s/5min appliquées par le gate.
func TestVeridianApplyJitter_StaysWithinBounds(t *testing.T) {
	nominal := 60 * time.Second
	pct := 0.30
	lo := time.Duration(float64(nominal) * 0.70) // 42s
	hi := time.Duration(float64(nominal) * 1.30) // 78s

	// Échantillonner aux extrêmes de rng() pour borner l'intervalle.
	for _, v := range []float64{0.0, 0.0001, 0.25, 0.5, 0.75, 0.999999} {
		got := veridianApplyJitter(nominal, pct, constRNG(v))
		assert.GreaterOrEqual(t, int64(got), int64(lo), "rng=%v sous la borne basse", v)
		assert.LessOrEqual(t, int64(got), int64(hi), "rng=%v au-dessus de la borne haute", v)
	}
}

// TestVeridianProviderClassGate_JitterDispersesDelay vérifie via le VRAI gate
// (rand.Float64 en prod) qu'un délai nominal de 60s avec le jitter par défaut
// (0.30) produit, sur N tirages, des délais TOUS dans [42s, 78s] et qui VARIENT
// (pas tous identiques = la régularité métronomique est bien cassée).
func TestVeridianProviderClassGate_JitterDispersesDelay(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	// 1/min → délai nominal 60s. Jitter par défaut (aucun pct configuré → 0.30).
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)
	provider := &workspace.Integrations[0].EmailProvider

	entry := veridianTestEntry("e", "x@gmail.com", domain.EmailQueuePayload{})
	// 1er appel consomme l'unique token google ; le bucket (partagé sur le worker)
	// reste ensuite vide → tous les appels suivants sont throttlés et jittés.
	_, throttled := env.worker.veridianProviderClassGate(workspace, provider, entry)
	require.False(t, throttled)

	delays := make(map[time.Duration]struct{})
	for i := 0; i < 50; i++ {
		delay, throttled := env.worker.veridianProviderClassGate(workspace, provider, entry)
		require.True(t, throttled)
		assert.GreaterOrEqual(t, delay, 42*time.Second, "jitter sous la borne basse")
		assert.LessOrEqual(t, delay, 78*time.Second, "jitter au-dessus de la borne haute")
		delays[delay] = struct{}{}
	}
	// Sur 50 tirages, on attend largement plus d'une valeur distincte (la
	// dispersion casse le rythme constant). Tolérance basse pour éviter le flake.
	assert.Greater(t, len(delays), 5, "le jitter doit produire des délais variés")
}

// TestVeridianProviderClassGate_JitterDisabledExactDelay vérifie qu'avec un
// jitter explicitement désactivé (*0 sur le workspace), le délai reste EXACTEMENT
// le nominal 60s (non-régression du comportement pré-jitter, opt-out).
func TestVeridianProviderClassGate_JitterDisabledExactDelay(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(map[string]float64{"google": 1}, 6000)
	workspace.Settings.VeridianJitterPct = floatPtr(0) // jitter OFF explicite
	provider := &workspace.Integrations[0].EmailProvider

	entry := veridianTestEntry("e", "x@gmail.com", domain.EmailQueuePayload{})
	_, throttled := env.worker.veridianProviderClassGate(workspace, provider, entry)
	require.False(t, throttled)
	delay, throttled := env.worker.veridianProviderClassGate(workspace, provider, entry)
	require.True(t, throttled)
	assert.Equal(t, 60*time.Second, delay, "jitter OFF → délai nominal exact")
}
