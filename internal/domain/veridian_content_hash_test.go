package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVeridianContentHash(t *testing.T) {
	t.Run("même (subject, body) → même hash (déterministe)", func(t *testing.T) {
		h1 := VeridianContentHash("Bonjour", "Corps du mail")
		h2 := VeridianContentHash("Bonjour", "Corps du mail")
		assert.Equal(t, h1, h2)
		assert.Len(t, h1, 32, "SHA-256 tronqué 128 bits = 32 hex")
	})

	t.Run("casse et whitespace n'affectent pas (normalisation)", func(t *testing.T) {
		h1 := VeridianContentHash("Bonjour Monde", "Voici   le   corps")
		h2 := VeridianContentHash("BONJOUR   monde", "voici le corps")
		h3 := VeridianContentHash("\tBonjour\n  Monde \n", "Voici\nle\tcorps")
		assert.Equal(t, h1, h2)
		assert.Equal(t, h1, h3)
	})

	t.Run("sujet différent → hash différent", func(t *testing.T) {
		assert.NotEqual(t,
			VeridianContentHash("Sujet A", "même corps"),
			VeridianContentHash("Sujet B", "même corps"))
	})

	t.Run("corps différent → hash différent", func(t *testing.T) {
		assert.NotEqual(t,
			VeridianContentHash("même sujet", "corps A"),
			VeridianContentHash("même sujet", "corps B"))
	})

	t.Run("séparateur sujet/corps : déplacement de frontière → hash différent", func(t *testing.T) {
		// "AB" + "" vs "A" + "B" ne doivent PAS produire le même hash.
		assert.NotEqual(t,
			VeridianContentHash("AB", ""),
			VeridianContentHash("A", "B"))
	})
}

func TestVeridianAntiHashEnabledFor(t *testing.T) {
	ptr := func(b bool) *bool { return &b }

	tests := []struct {
		name        string
		metaEnabled bool
		metaOK      bool
		infra       *bool
		workspace   *bool
		coldDefault bool
		want        bool
	}{
		{"broadcast présent true prime tout", true, true, ptr(false), ptr(false), false, true},
		{"broadcast présent false prime tout", false, true, ptr(true), ptr(true), true, false},
		{"infra utilisé si broadcast absent", false, false, ptr(true), ptr(false), false, true},
		{"infra false prime workspace", false, false, ptr(false), ptr(true), true, false},
		{"workspace utilisé si broadcast+infra absents", false, false, nil, ptr(true), false, true},
		{"défaut cold si rien défini", false, false, nil, nil, true, true},
		{"défaut off si rien défini et coldDefault false", false, false, nil, nil, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VeridianAntiHashEnabledFor(tt.metaEnabled, tt.metaOK, tt.infra, tt.workspace, tt.coldDefault)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVeridianAntiHashWindow(t *testing.T) {
	tests := []struct {
		name                              string
		metaHours, infraHours, wsHours    int
		want                              time.Duration
	}{
		{"aucun → défaut 72h", 0, 0, 0, VeridianDefaultAntiHashWindow},
		{"broadcast prime", 24, 48, 96, 24 * time.Hour},
		{"infra si broadcast absent", 0, 48, 96, 48 * time.Hour},
		{"workspace si broadcast+infra absents", 0, 0, 96, 96 * time.Hour},
		{"valeur négative ignorée → défaut", -5, 0, 0, VeridianDefaultAntiHashWindow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VeridianAntiHashWindow(tt.metaHours, tt.infraHours, tt.wsHours))
		})
	}
}

func TestVeridianAntiHashEnabledFromMetadata(t *testing.T) {
	t.Run("nil metadata → not present", func(t *testing.T) {
		_, ok := VeridianAntiHashEnabledFromMetadata(nil)
		assert.False(t, ok)
	})
	t.Run("clé absente → not present", func(t *testing.T) {
		_, ok := VeridianAntiHashEnabledFromMetadata(MapOfAny{"x": 1})
		assert.False(t, ok)
	})
	t.Run("true présent", func(t *testing.T) {
		v, ok := VeridianAntiHashEnabledFromMetadata(MapOfAny{VeridianAntiHashMetadataKeyEnabled: true})
		assert.True(t, ok)
		assert.True(t, v)
	})
	t.Run("false présent (désactivé explicite)", func(t *testing.T) {
		v, ok := VeridianAntiHashEnabledFromMetadata(MapOfAny{VeridianAntiHashMetadataKeyEnabled: false})
		assert.True(t, ok)
		assert.False(t, v)
	})
	t.Run("non-booléen → not present", func(t *testing.T) {
		_, ok := VeridianAntiHashEnabledFromMetadata(MapOfAny{VeridianAntiHashMetadataKeyEnabled: "yes"})
		assert.False(t, ok)
	})
}

func TestVeridianAntiHashWindowHoursFromMetadata(t *testing.T) {
	t.Run("nil → 0", func(t *testing.T) {
		assert.Zero(t, VeridianAntiHashWindowHoursFromMetadata(nil))
	})
	t.Run("absent → 0", func(t *testing.T) {
		assert.Zero(t, VeridianAntiHashWindowHoursFromMetadata(MapOfAny{"x": 1}))
	})
	t.Run("float64 round-trip", func(t *testing.T) {
		assert.Equal(t, 48, VeridianAntiHashWindowHoursFromMetadata(MapOfAny{VeridianAntiHashMetadataKeyWindowHours: float64(48)}))
	})
	t.Run("int natif", func(t *testing.T) {
		assert.Equal(t, 24, VeridianAntiHashWindowHoursFromMetadata(MapOfAny{VeridianAntiHashMetadataKeyWindowHours: 24}))
	})
	t.Run("<=0 → 0 (non configuré)", func(t *testing.T) {
		assert.Zero(t, VeridianAntiHashWindowHoursFromMetadata(MapOfAny{VeridianAntiHashMetadataKeyWindowHours: float64(0)}))
		assert.Zero(t, VeridianAntiHashWindowHoursFromMetadata(MapOfAny{VeridianAntiHashMetadataKeyWindowHours: -3}))
	})
}
