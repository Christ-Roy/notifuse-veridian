package domain

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeridianReputationProfileFromContextIsEmptyByDefault(t *testing.T) {
	assert.Equal(t, "", VeridianReputationProfileFromContext(context.Background()))
	assert.Equal(t, "", VeridianReputationProfileFromContext(nil))
}

func TestVeridianReputationProfileContext(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, ctx, WithVeridianReputationProfile(ctx, "  "), "un profil vide ne change pas le contexte")
	scoped := WithVeridianReputationProfile(ctx, " profil-1 ")
	assert.Equal(t, "profil-1", VeridianReputationProfileFromContext(scoped))
	assert.Equal(t, "", VeridianReputationProfileFromContext(ctx), "le contexte d'origine est intact")
}
