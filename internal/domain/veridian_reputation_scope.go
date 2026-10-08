package domain

import (
	"context"
	"strings"
)

type veridianReputationProfileKey struct{}

// WithVeridianReputationProfile marque un contexte : les compteurs de reputation
// lus avec ce contexte sont ceux de CE profil d'envoi (lot 4, 08/10/2026), pas
// ceux de tout le domaine emetteur. Lecture par le repository des messages.
func WithVeridianReputationProfile(ctx context.Context, profileID string) context.Context {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return ctx
	}
	return context.WithValue(ctx, veridianReputationProfileKey{}, profileID)
}

// VeridianReputationProfileFromContext rend l'identifiant du profil pose par
// WithVeridianReputationProfile, vide sinon.
func VeridianReputationProfileFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(veridianReputationProfileKey{}).(string)
	return v
}
