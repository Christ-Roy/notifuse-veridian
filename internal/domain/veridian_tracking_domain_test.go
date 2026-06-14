package domain

import (
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
)

const (
	globalEndpoint    = "https://notifuse.app.veridian.site"
	workspaceEndpoint = "https://track.workspace-custom.example"
	infraDomainBare   = "track.agences-veridian.fr"
	infraDomainURL    = "https://track.agences-veridian.fr"
)

// TestVeridianResolveTrackingEndpoint couvre la cascade complète infra > endpoint
// (déjà résolu workspace > global en amont) + la non-régression stricte.
func TestVeridianResolveTrackingEndpoint(t *testing.T) {
	tests := []struct {
		name             string
		provider         *EmailProvider
		resolvedEndpoint string
		want             string
	}{
		{
			// NON-RÉGRESSION : provider nil (intégration legacy) → endpoint inchangé.
			name:             "provider nil → endpoint inchangé",
			provider:         nil,
			resolvedEndpoint: globalEndpoint,
			want:             globalEndpoint,
		},
		{
			// NON-RÉGRESSION : pas de tracking domain sur l'infra, l'endpoint reçu
			// est le global → fallback strict sur le global.
			name:             "infra sans tracking domain, fallback global",
			provider:         &EmailProvider{Kind: EmailProviderKindSMTP},
			resolvedEndpoint: globalEndpoint,
			want:             globalEndpoint,
		},
		{
			// NON-RÉGRESSION : pas de tracking domain infra, mais l'endpoint reçu est
			// déjà le CustomEndpointURL workspace (résolu en amont) → on le préserve.
			name:             "infra sans tracking domain, fallback workspace",
			provider:         &EmailProvider{Kind: EmailProviderKindSMTP},
			resolvedEndpoint: workspaceEndpoint,
			want:             workspaceEndpoint,
		},
		{
			// Tracking domain infra fourni en domaine nu → normalisé https:// et PRIME
			// sur l'endpoint global.
			name:             "infra tracking domain (nu) prime sur global",
			provider:         &EmailProvider{VeridianTrackingDomain: infraDomainBare},
			resolvedEndpoint: globalEndpoint,
			want:             infraDomainURL,
		},
		{
			// Tracking domain infra PRIME aussi sur un CustomEndpointURL workspace
			// (l'infra est plus spécifique que le workspace).
			name:             "infra tracking domain prime sur workspace",
			provider:         &EmailProvider{VeridianTrackingDomain: infraDomainBare},
			resolvedEndpoint: workspaceEndpoint,
			want:             infraDomainURL,
		},
		{
			// Tracking domain infra fourni comme URL complète → utilisé tel quel.
			name:             "infra tracking domain (URL complète)",
			provider:         &EmailProvider{VeridianTrackingDomain: infraDomainURL},
			resolvedEndpoint: globalEndpoint,
			want:             infraDomainURL,
		},
		{
			// Slash final retiré pour éviter le double slash dans "%s/t/%s".
			name:             "trailing slash retiré",
			provider:         &EmailProvider{VeridianTrackingDomain: "https://track.agences-veridian.fr/"},
			resolvedEndpoint: globalEndpoint,
			want:             infraDomainURL,
		},
		{
			// http:// explicite préservé (utile en E2E staging derrière proxy local).
			name:             "schéma http explicite préservé",
			provider:         &EmailProvider{VeridianTrackingDomain: "http://localhost:8080"},
			resolvedEndpoint: globalEndpoint,
			want:             "http://localhost:8080",
		},
		{
			// Espaces autour de la valeur → trimmés.
			name:             "valeur avec espaces trimmée",
			provider:         &EmailProvider{VeridianTrackingDomain: "  track.agences-veridian.fr  "},
			resolvedEndpoint: globalEndpoint,
			want:             infraDomainURL,
		},
		{
			// Valeur blanche (espaces seuls) → considérée vide → fallback.
			name:             "valeur blanche → fallback",
			provider:         &EmailProvider{VeridianTrackingDomain: "   "},
			resolvedEndpoint: globalEndpoint,
			want:             globalEndpoint,
		},
		{
			// HTTPS:// en majuscule reconnu (pas de double préfixe).
			name:             "schéma HTTPS majuscule reconnu",
			provider:         &EmailProvider{VeridianTrackingDomain: "HTTPS://track.agences-veridian.fr"},
			resolvedEndpoint: globalEndpoint,
			want:             "HTTPS://track.agences-veridian.fr",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := VeridianResolveTrackingEndpoint(tc.provider, tc.resolvedEndpoint)
			if got != tc.want {
				t.Fatalf("VeridianResolveTrackingEndpoint() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestVeridianResolveTrackingEndpoint_LinksUseAlignedDomain prouve le résultat de
// bout en bout : l'endpoint résolu alimente les générateurs de liens réels, donc le
// pixel /t/ et le redirect /r/ pointent vers le sous-domaine aligné au domaine
// d'envoi quand l'infra le configure, et vers le domaine global sinon.
func TestVeridianResolveTrackingEndpoint_LinksUseAlignedDomain(t *testing.T) {
	const (
		wid = "ws-123"
		mid = "ws-123_msg-abc"
		ts  = int64(1700000000)
		dst = "https://example.com/landing"
	)

	t.Run("infra alignée → liens sur le sous-domaine du domaine d'envoi", func(t *testing.T) {
		provider := &EmailProvider{VeridianTrackingDomain: infraDomainBare}
		endpoint := VeridianResolveTrackingEndpoint(provider, globalEndpoint)

		pixel := notifuse_mjml.GenerateHTMLOpenTrackingPixel(wid, mid, endpoint, ts)
		if !strings.Contains(pixel, infraDomainURL+"/t/") {
			t.Fatalf("pixel doit pointer vers %s/t/, got: %s", infraDomainURL, pixel)
		}
		if strings.Contains(pixel, globalEndpoint) {
			t.Fatalf("pixel ne doit PAS pointer vers le domaine global, got: %s", pixel)
		}

		redirect := notifuse_mjml.GenerateEmailRedirectionEndpoint(wid, mid, endpoint, dst, ts)
		if !strings.HasPrefix(redirect, infraDomainURL+"/r/") && !strings.HasPrefix(redirect, infraDomainURL+"/visit") {
			t.Fatalf("redirect doit pointer vers %s (/r/ ou /visit fallback), got: %s", infraDomainURL, redirect)
		}
		if strings.Contains(redirect, globalEndpoint) {
			t.Fatalf("redirect ne doit PAS pointer vers le domaine global, got: %s", redirect)
		}
	})

	t.Run("sans infra → liens sur le domaine global (non-régression)", func(t *testing.T) {
		provider := &EmailProvider{Kind: EmailProviderKindSMTP}
		endpoint := VeridianResolveTrackingEndpoint(provider, globalEndpoint)

		pixel := notifuse_mjml.GenerateHTMLOpenTrackingPixel(wid, mid, endpoint, ts)
		if !strings.Contains(pixel, globalEndpoint+"/t/") {
			t.Fatalf("sans infra, le pixel doit rester sur le domaine global, got: %s", pixel)
		}

		redirect := notifuse_mjml.GenerateEmailRedirectionEndpoint(wid, mid, endpoint, dst, ts)
		if !strings.HasPrefix(redirect, globalEndpoint) {
			t.Fatalf("sans infra, le redirect doit rester sur le domaine global, got: %s", redirect)
		}
	})

	t.Run("provider nil → liens sur le domaine global (non-régression)", func(t *testing.T) {
		endpoint := VeridianResolveTrackingEndpoint(nil, globalEndpoint)
		pixel := notifuse_mjml.GenerateHTMLOpenTrackingPixel(wid, mid, endpoint, ts)
		if !strings.Contains(pixel, globalEndpoint+"/t/") {
			t.Fatalf("provider nil → pixel sur domaine global attendu, got: %s", pixel)
		}
	})
}
