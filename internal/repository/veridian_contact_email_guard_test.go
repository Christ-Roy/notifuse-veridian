package repository

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingContactRepo enregistre ce que le garde laisse passer jusqu'au vrai dépôt.
type recordingContactRepo struct {
	domain.ContactRepository
	upserted []string
	bulk     [][]string
	read     []string
}

func (r *recordingContactRepo) GetContactByEmail(_ context.Context, _ string, email string) (*domain.Contact, error) {
	r.read = append(r.read, email)
	return &domain.Contact{Email: email}, nil
}

func (r *recordingContactRepo) UpsertContact(_ context.Context, _ string, c *domain.Contact) (bool, error) {
	r.upserted = append(r.upserted, c.Email)
	return true, nil
}

func (r *recordingContactRepo) BulkUpsertContacts(_ context.Context, _ string, cs []*domain.Contact) ([]domain.BulkUpsertResult, error) {
	var es []string
	for _, c := range cs {
		es = append(es, c.Email)
	}
	r.bulk = append(r.bulk, es)
	return nil, nil
}

// Voie directe (inscription publique, événement, préférences) : corrigée sur place, ou refusée.
func TestVeridianContactEmailGuard_DirectWritesAreCanonicalOrRefused(t *testing.T) {
	inner := &recordingContactRepo{}
	g := NewVeridianContactEmailGuard(inner)
	c := &domain.Contact{Email: "%20legi@legiavocats.eu"}
	_, err := g.UpsertContact(context.Background(), "ws", c)
	require.NoError(t, err)
	assert.Equal(t, []string{"legi@legiavocats.eu"}, inner.upserted)
	assert.Equal(t, "legi@legiavocats.eu", c.Email, "l'appelant voit l'adresse rangée (même pointeur)")

	_, err = g.UpsertContact(context.Background(), "ws", &domain.Contact{Email: "a%20b@example.fr"})
	assert.Error(t, err)
	assert.Len(t, inner.upserted, 1, "l'adresse ambiguë n'atteint jamais la base")
}

func TestVeridianContactEmailGuard_BulkIsAllOrNothing(t *testing.T) {
	inner := &recordingContactRepo{}
	g := NewVeridianContactEmailGuard(inner)
	_, err := g.BulkUpsertContacts(context.Background(), "ws", []*domain.Contact{
		{Email: "06%0d%0aemail%3ahotel.des.lacs@netcourrier.com"}, {Email: "ok@example.fr"},
	})
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"hotel.des.lacs@netcourrier.com", "ok@example.fr"}}, inner.bulk)

	_, err = g.BulkUpsertContacts(context.Background(), "ws", []*domain.Contact{{Email: "ok@example.fr"}, {Email: "un@x.fr\r\ndeux@x.fr"}})
	assert.Error(t, err)
	assert.Len(t, inner.bulk, 1, "un lot avec une adresse abîmée n'est pas écrit à moitié")
}

// Les lectures passent telles quelles : une adresse abîmée déjà en base reste joignable par sa
// clé stockée, le temps de la corriger ou de la mettre en quarantaine.
func TestNewVeridianContactEmailGuard_ReadsPassThroughUntouched(t *testing.T) {
	inner := &recordingContactRepo{}
	g := NewVeridianContactEmailGuard(inner)
	c, err := g.GetContactByEmail(context.Background(), "ws", "%20legi@legiavocats.eu")
	require.NoError(t, err)
	assert.Equal(t, "%20legi@legiavocats.eu", c.Email)
	assert.Equal(t, []string{"%20legi@legiavocats.eu"}, inner.read)
}
