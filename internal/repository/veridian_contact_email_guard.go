package repository

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
)

// VeridianContactEmailGuard (VER-220, 22/11/2026) : dernière porte avant l'écriture d'un contact.
// Contact.Validate normalise déjà l'adresse sur les voies du service (API, import), mais trois
// voies écrivent directement dans le dépôt sans le passer : l'inscription publique à une liste
// (list_service), la création de contact par un événement (custom_event_service) et le centre
// de préférences (notification_center_service). Le garde y applique la même règle,
// domain.VeridianCanonicalContactEmail : l'adresse est corrigée sur place (l'appelant garde le
// même pointeur) ou l'écriture est refusée. Toutes les autres méthodes passent telles quelles.
type VeridianContactEmailGuard struct {
	domain.ContactRepository
}

// NewVeridianContactEmailGuard enveloppe le dépôt des contacts.
func NewVeridianContactEmailGuard(inner domain.ContactRepository) domain.ContactRepository {
	return &VeridianContactEmailGuard{ContactRepository: inner}
}

func (g *VeridianContactEmailGuard) UpsertContact(ctx context.Context, workspaceID string, contact *domain.Contact) (bool, error) {
	if contact != nil {
		email, err := domain.VeridianCanonicalContactEmail(contact.Email)
		if err != nil {
			return false, fmt.Errorf("contact %q refusé: %w", contact.Email, err)
		}
		contact.Email = email
	}
	return g.ContactRepository.UpsertContact(ctx, workspaceID, contact)
}

// BulkUpsertContacts : le lot vient du service, déjà passé par Contact.Validate. Une adresse
// abîmée ici est une voie nouvelle qui contourne la validation : le lot entier est refusé,
// jamais écrit à moitié en silence.
func (g *VeridianContactEmailGuard) BulkUpsertContacts(ctx context.Context, workspaceID string, contacts []*domain.Contact) ([]domain.BulkUpsertResult, error) {
	for _, c := range contacts {
		if c == nil {
			continue
		}
		email, err := domain.VeridianCanonicalContactEmail(c.Email)
		if err != nil {
			return nil, fmt.Errorf("lot refusé, contact %q: %w", c.Email, err)
		}
		c.Email = email
	}
	return g.ContactRepository.BulkUpsertContacts(ctx, workspaceID, contacts)
}
