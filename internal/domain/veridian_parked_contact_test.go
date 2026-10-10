package domain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOrphanRepo applique le contrat documenté : seuls les orphelins plus vieux que
// `grace` sont rendus, bornés par `limit`.
type fakeOrphanRepo struct {
	ages []time.Duration // âge de chaque orphelin
}

func (f fakeOrphanRepo) ListOrphanParked(_ context.Context, ws string, grace time.Duration, limit int) ([]VeridianParkedContact, error) {
	var out []VeridianParkedContact
	for i, age := range f.ages {
		if age < grace || len(out) >= limit {
			continue
		}
		out = append(out, VeridianParkedContact{ContactAutomationID: ws + "-" + string(rune('a'+i))})
	}
	return out, nil
}

func TestVeridianOrphanParkedRepository_OptionalInterfaceDetectedByTypeAssertion(t *testing.T) {
	// L'exécuteur détecte l'interface OPTIONNELLE par assertion de type sur le dépôt.
	var repo interface{} = fakeOrphanRepo{ages: []time.Duration{time.Hour, time.Second, 2 * time.Hour}}
	orphanRepo, ok := repo.(VeridianOrphanParkedRepository)
	require.True(t, ok)

	got, err := orphanRepo.ListOrphanParked(context.Background(), "ws", time.Minute, 10)
	require.NoError(t, err)
	assert.Len(t, got, 2, "le contact plus récent que la grâce est exclu")

	got, err = orphanRepo.ListOrphanParked(context.Background(), "ws", time.Minute, 1)
	require.NoError(t, err)
	assert.Len(t, got, 1, "limit borne")

	// Un dépôt sans la méthode ne satisfait pas l'interface (repli silencieux voulu).
	_, ok = interface{}(struct{}{}).(VeridianOrphanParkedRepository)
	assert.False(t, ok)
}

func TestVeridianParkedContact_ZeroValueIsSafeToResend(t *testing.T) {
	// La valeur zéro ne prétend ni message trouvé, ni envoyé, ni contact déjà joint :
	// une réconciliation ne doit jamais déduire un envoi d'un champ non renseigné.
	var p VeridianParkedContact
	assert.False(t, p.MessageFound)
	assert.False(t, p.MessageSent)
	assert.False(t, p.MessageFailed)
	assert.False(t, p.AlreadyContacted)
}
