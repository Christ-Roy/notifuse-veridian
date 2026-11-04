package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Fiche 79 : verification d'un profil d'envoi par son propre utilisateur.
//
// POST /api/veridian/emailProfiles.verify {workspace_id, integration_id, to} (JWT). Meme acte que la
// route admin HMAC (vrai test de transport vers `to`, puis VeridianTransportVerifiedAt seulement si le
// transport accepte), avec les garde-fous d'un appelant non fiable : membre du workspace
// (AuthenticateUserForWorkspace : la cle du workspace A est refusee sur B), ecriture sur la ressource
// "workspace" (cle en lecture seule refusee), integration lue DANS ce workspace uniquement, et un
// plafond de VerifyMaxPerMinute verifications par minute et par workspace (le test envoie un vrai mail).

// ErrVeridianVerifyRateLimited : plafond atteint pour ce workspace (HTTP 429).
var ErrVeridianVerifyRateLimited = errors.New("too many verification attempts, retry in a minute")

const veridianVerifyMaxPerMinute = 5

// VeridianEmailProfileVerifier : capacite deleguee a *EmailService (route admin existante).
type VeridianEmailProfileVerifier interface {
	VeridianAdminVerifyEmailIntegration(ctx context.Context, workspaceID, integrationID, to string) error
}

type VeridianEmailProfileVerifyService struct {
	auth     domain.AuthService
	verifier VeridianEmailProfileVerifier
	now      func() time.Time
	mu       sync.Mutex
	recent   map[string][]time.Time
}

func NewVeridianEmailProfileVerifyService(auth domain.AuthService, verifier VeridianEmailProfileVerifier) *VeridianEmailProfileVerifyService {
	return &VeridianEmailProfileVerifyService{auth: auth, verifier: verifier, now: time.Now, recent: map[string][]time.Time{}}
}

func (s *VeridianEmailProfileVerifyService) allow(workspaceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-time.Minute)
	kept := s.recent[workspaceID][:0]
	for _, t := range s.recent[workspaceID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= veridianVerifyMaxPerMinute {
		s.recent[workspaceID] = kept
		return false
	}
	s.recent[workspaceID] = append(kept, s.now())
	return true
}

// VerifyEmailProfile : ne renvoie jamais de secret ; une erreur de transport est opaque cote HTTP.
func (s *VeridianEmailProfileVerifyService) VerifyEmailProfile(ctx context.Context, workspaceID, integrationID, to string) error {
	workspaceID, integrationID, to = strings.TrimSpace(workspaceID), strings.TrimSpace(integrationID), strings.TrimSpace(to)
	if workspaceID == "" || integrationID == "" || to == "" {
		return domain.NewValidationError("workspace_id, integration_id and to are required")
	}
	ctx, _, uw, err := s.auth.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to authenticate user: %w", err)
	}
	if err := veridianIntegrationWriteAllowed(uw, domain.IntegrationTypeEmail); err != nil {
		return err
	}
	if !s.allow(workspaceID) {
		return ErrVeridianVerifyRateLimited
	}
	return s.verifier.VeridianAdminVerifyEmailIntegration(ctx, workspaceID, integrationID, to)
}
