package domain

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// threeSenders construit une infra à 3 senders d'IDs stables (s1<s2<s3 pour un
// ordre de rotation déterministe quel que soit l'ordre de stockage).
func threeSenders() *EmailProvider {
	return &EmailProvider{
		Kind:               EmailProviderKindSMTP,
		RateLimitPerMinute: 10,
		Senders: []EmailSender{
			{ID: "s3", Email: "c@agences-veridian.fr", Name: "C"},
			{ID: "s1", Email: "a@agences-veridian.fr", Name: "A", IsDefault: true},
			{ID: "s2", Email: "b@agences-veridian.fr", Name: "B"},
		},
	}
}

func TestVeridianSelectSender_RoundRobinPerClass(t *testing.T) {
	p := threeSenders()
	r := NewVeridianSenderRotator()

	// Round-robin déterministe par classe : ordre trié par ID = s1, s2, s3.
	want := []string{"s1", "s2", "s3", "s1", "s2", "s3"}
	for i, exp := range want {
		s := p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle)
		require.NotNil(t, s)
		assert.Equal(t, exp, s.ID, "google appel %d", i)
	}
}

func TestVeridianSelectSender_CursorsAreIndependentPerClass(t *testing.T) {
	p := threeSenders()
	r := NewVeridianSenderRotator()

	// google avance, microsoft a son propre curseur : on ne martèle pas le même
	// sender vers une classe parce qu'une autre classe a déjà tourné.
	assert.Equal(t, "s1", p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle).ID)
	assert.Equal(t, "s2", p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle).ID)
	// microsoft repart de s1 (curseur distinct).
	assert.Equal(t, "s1", p.VeridianSelectSender(r, "int-1", "", ProviderClassMicrosoft).ID)
	assert.Equal(t, "s2", p.VeridianSelectSender(r, "int-1", "", ProviderClassMicrosoft).ID)
	// google continue où il en était (s3).
	assert.Equal(t, "s3", p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle).ID)
}

func TestVeridianSelectSender_CursorsAreIndependentPerIntegration(t *testing.T) {
	p := threeSenders()
	r := NewVeridianSenderRotator()

	assert.Equal(t, "s1", p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle).ID)
	// Autre intégration = autre clé = repart de s1.
	assert.Equal(t, "s1", p.VeridianSelectSender(r, "int-2", "", ProviderClassGoogle).ID)
}

func TestVeridianSelectSender_TemplateSenderIDTakesPriority(t *testing.T) {
	p := threeSenders()
	r := NewVeridianSenderRotator()

	// Un template qui force explicitement son expéditeur garde la main : pas de
	// rotation, toujours s2.
	for i := 0; i < 5; i++ {
		s := p.VeridianSelectSender(r, "int-1", "s2", ProviderClassGoogle)
		require.NotNil(t, s)
		assert.Equal(t, "s2", s.ID, "appel %d", i)
	}
}

func TestVeridianSelectSender_UnknownTemplateSenderFallsToRotation(t *testing.T) {
	p := threeSenders()
	r := NewVeridianSenderRotator()

	// SenderID inexistant → ignoré, on retombe sur la rotation.
	s := p.VeridianSelectSender(r, "int-1", "does-not-exist", ProviderClassGoogle)
	require.NotNil(t, s)
	assert.Equal(t, "s1", s.ID)
}

func TestVeridianSelectSender_NilRotatorIsUpstream(t *testing.T) {
	p := threeSenders()
	// rotator nil = comportement upstream : sender par défaut (IsDefault = s1).
	for i := 0; i < 3; i++ {
		s := p.VeridianSelectSender(nil, "int-1", "", ProviderClassGoogle)
		require.NotNil(t, s)
		assert.Equal(t, "s1", s.ID)
	}
}

func TestVeridianSelectSender_SingleSenderNoRotation(t *testing.T) {
	p := &EmailProvider{
		Senders: []EmailSender{{ID: "only", Email: "x@a.fr", Name: "X", IsDefault: true}},
	}
	r := NewVeridianSenderRotator()
	for i := 0; i < 3; i++ {
		s := p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle)
		require.NotNil(t, s)
		assert.Equal(t, "only", s.ID)
	}
}

func TestVeridianSelectSender_NoSenderReturnsNil(t *testing.T) {
	p := &EmailProvider{Senders: nil}
	r := NewVeridianSenderRotator()
	assert.Nil(t, p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle))
}

func TestVeridianSelectSender_EmptyEmailSendersExcluded(t *testing.T) {
	// Un sender sans email ne participe pas à la rotation.
	p := &EmailProvider{
		Senders: []EmailSender{
			{ID: "s1", Email: "a@a.fr", Name: "A", IsDefault: true},
			{ID: "s2", Email: "", Name: "vide"},
			{ID: "s3", Email: "c@a.fr", Name: "C"},
		},
	}
	r := NewVeridianSenderRotator()
	// 2 éligibles seulement (s1, s3) → rotation sur 2.
	assert.Equal(t, "s1", p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle).ID)
	assert.Equal(t, "s3", p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle).ID)
	assert.Equal(t, "s1", p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle).ID)
}

func TestVeridianSelectSender_ConcurrentNoRace(t *testing.T) {
	p := threeSenders()
	r := NewVeridianSenderRotator()
	var wg sync.WaitGroup
	counts := make([]int, 3)
	var mu sync.Mutex
	indexByID := map[string]int{"s1": 0, "s2": 1, "s3": 2}

	const n = 300
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := p.VeridianSelectSender(r, "int-1", "", ProviderClassGoogle)
			mu.Lock()
			counts[indexByID[s.ID]]++
			mu.Unlock()
		}()
	}
	wg.Wait()

	total := counts[0] + counts[1] + counts[2]
	assert.Equal(t, n, total)
	// Répartition équilibrée (chaque sender ~1/3, tolérance large car concurrence).
	for i, c := range counts {
		assert.InDelta(t, n/3, c, float64(n)/3, "sender %d sur-représenté: %d", i, c)
	}
}

func TestVeridianActiveSenderCount(t *testing.T) {
	assert.Equal(t, 3, threeSenders().VeridianActiveSenderCount())
	assert.Equal(t, 0, (&EmailProvider{}).VeridianActiveSenderCount())
	p := &EmailProvider{Senders: []EmailSender{{Email: "a@a.fr"}, {Email: ""}, {Email: "c@a.fr"}}}
	assert.Equal(t, 2, p.VeridianActiveSenderCount())
}

func TestVeridianEffectiveRateLimit(t *testing.T) {
	// 3 senders à 10/min = 30/min agrégé.
	assert.Equal(t, 30, threeSenders().VeridianEffectiveRateLimit())
	// 1 sender = rate inchangé (non-régression).
	one := &EmailProvider{RateLimitPerMinute: 10, Senders: []EmailSender{{Email: "a@a.fr"}}}
	assert.Equal(t, 10, one.VeridianEffectiveRateLimit())
	// 0 sender = rate inchangé.
	zero := &EmailProvider{RateLimitPerMinute: 10}
	assert.Equal(t, 10, zero.VeridianEffectiveRateLimit())
}

func TestVeridianIsColdContext(t *testing.T) {
	t.Run("rien -> false", func(t *testing.T) {
		assert.False(t, VeridianIsColdContext(nil, nil, nil))
		assert.False(t, VeridianIsColdContext(nil, &Broadcast{}, &Workspace{}))
	})

	t.Run("tag contact -> true", func(t *testing.T) {
		c := &Contact{CustomString5: &NullableString{String: ProviderClassGoogle, IsNull: false}}
		assert.True(t, VeridianIsColdContext(c, nil, nil))
	})

	t.Run("rates broadcast -> true", func(t *testing.T) {
		b := &Broadcast{Metadata: MapOfAny{VeridianProviderClassRatesMetadataKey: map[string]any{"google": 1.0}}}
		assert.True(t, VeridianIsColdContext(nil, b, nil))
	})

	t.Run("window broadcast -> true", func(t *testing.T) {
		b := &Broadcast{Metadata: MapOfAny{VeridianSendingWindowMetadataKey: map[string]any{"start_hour": 9.0, "end_hour": 18.0}}}
		assert.True(t, VeridianIsColdContext(nil, b, nil))
	})

	t.Run("rates workspace -> true", func(t *testing.T) {
		w := &Workspace{Settings: WorkspaceSettings{VeridianProviderClassRates: map[string]float64{"google": 1}}}
		assert.True(t, VeridianIsColdContext(nil, nil, w))
	})

	t.Run("window workspace -> true", func(t *testing.T) {
		w := &Workspace{Settings: WorkspaceSettings{VeridianSendingWindow: businessHours()}}
		assert.True(t, VeridianIsColdContext(nil, nil, w))
	})
}
