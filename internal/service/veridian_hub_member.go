package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Une personne du Hub est son `hub_user_id` (CONTRAT-HUB 5.19.7, fiche 89, 25/11/2026). Le Hub sait changer l'adresse principale de quelqu'un, et une
// adresse se recycle : la chercher par l'adresse laisserait l'accès à un retiré, ou toucherait quelqu'un d'autre. L'adresse ne sert qu'à créer une copie.

// ErrHubUserIDRequired : l'adresse seule désigne un compte relié au Hub, il faut son hub_user_id.
var ErrHubUserIDRequired = errors.New("hub_user_id is required: this account is bound to the Hub")

// ErrOwnerNotMember : le futur propriétaire doit déjà être membre de l'espace (CONTRAT-HUB 5.19.7).
var ErrOwnerNotMember = errors.New("user is not a member of this workspace")

// hubMember : le compte de cette personne, par son hub_user_id SEUL. nil, nil : elle n'a pas de compte ici.
func (s *veridianService) hubMember(ctx context.Context, hubUserID string) (*domain.User, error) {
	if hubUserID == "" {
		return nil, errors.New("hub_user_id required")
	}
	user, err := s.userRepo.GetUserByHubUserID(ctx, hubUserID)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if errors.As(err, &notFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get user by hub_user_id: %w", err)
	}
	if user != nil && user.Type != domain.UserTypeUser {
		return nil, nil
	}
	return user, nil
}

// hubMemberOrCreate : le compte de cette personne, sinon une copie neuve et reliée. Une adresse déjà portée ici n'est jamais une fusion : reliée
// seulement si le compte est sans lien ET que le Hub atteste l'adresse, refus sinon.
func (s *veridianService) hubMemberOrCreate(ctx context.Context, hubUserID, email string, proven bool, source string) (*domain.User, error) {
	user, err := s.hubMember(ctx, hubUserID)
	if err != nil || user != nil {
		return user, err
	}
	if email == "" {
		return nil, ErrUserNotInApp
	}
	holder, err := s.userService.GetUserByEmail(ctx, email)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("get user by email: %w", err)
		}
		holder = nil
	}
	if holder != nil {
		if holder.Type != domain.UserTypeUser || hubUserIDOf(holder) != "" || !proven {
			return nil, ErrHubUserIDConflict
		}
		if err := s.bindHubUserID(ctx, holder.ID, hubUserID, source); err != nil {
			return nil, ErrHubUserIDConflict
		}
		return holder, nil
	}
	id := hubUserID
	user = &domain.User{ID: uuid.New().String(), Email: email, Type: domain.UserTypeUser, HubUserID: &id, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := s.userRepo.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("create member user: %w", err)
	}
	return user, nil
}

// hubOwner : le propriétaire visé par provision ou attach-owner, nil s'il faut le créer. Le Hub donne le hub_user_id ; un script d'opérateur
// (attach-owner) peut encore ne donner que l'adresse. Un compte sans lien trouvé par l'adresse sert, le lien reste à bindProvisionOwner.
func (s *veridianService) hubOwner(ctx context.Context, hubUserID, email string) (*domain.User, error) {
	if hubUserID != "" {
		owner, err := s.hubMember(ctx, hubUserID)
		if err != nil || owner != nil {
			return owner, err
		}
	}
	owner, err := s.userService.GetUserByEmail(ctx, email)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if errors.As(err, &notFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get owner by email: %w", err)
	}
	if owner != nil && hubUserID != "" && hubUserIDOf(owner) != "" {
		return nil, ErrHubUserIDConflict
	}
	return owner, nil
}

// hubOwnerMatches : ce propriétaire est-il la personne demandée ? Par hub_user_id quand les deux le portent ; l'adresse ne sert qu'à un compte sans lien
// ou à un appel sans hub_user_id.
func hubOwnerMatches(owner *domain.User, hubUserID, email string) bool {
	if bound := hubUserIDOf(owner); hubUserID != "" && bound != "" {
		return bound == hubUserID
	}
	return normalizedEqual(owner.Email, email)
}

func hubUserIDOf(u *domain.User) string {
	if u == nil || u.HubUserID == nil {
		return ""
	}
	return *u.HubUserID
}

// LookupMember : by-email quand le Hub donne le hub_user_id. L'adresse n'est plus lue.
func (s *veridianService) LookupMember(ctx context.Context, hubUserID string) (*domain.DiscoveryResponse, error) {
	user, err := s.hubMember(ctx, hubUserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return &domain.DiscoveryResponse{Workspaces: []domain.DiscoveryWorkspace{}}, nil
	}
	return s.LookupByEmail(ctx, user.Email)
}

func normalizedEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// magicLinkMember : qui un lien de connexion d'espace ouvre. ErrUserNotFound si la personne est inconnue ou n'est pas membre : une clé d'espace
// n'ouvre jamais la session de quelqu'un d'un autre espace.
func (s *veridianService) magicLinkMember(ctx context.Context, workspaceID, hubUserID, email string) (*domain.User, error) {
	var user *domain.User
	var err error
	if hubUserID != "" {
		user, err = s.hubMember(ctx, hubUserID)
	} else {
		user, err = s.userService.GetUserByEmail(ctx, email)
	}
	if err != nil {
		return nil, err
	}
	if user == nil || user.Type == domain.UserTypeAPIKey {
		return nil, &domain.ErrUserNotFound{Message: "user not found"}
	}
	if hubUserID == "" && hubUserIDOf(user) != "" {
		return nil, ErrHubUserIDRequired // une personne du Hub ne se désigne plus par son adresse ; reste un compte sans lien (canari, opérateur)
	}
	uw, err := s.workspaceRepo.GetUserWorkspace(ctx, user.ID, workspaceID)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(msg, "not found") || strings.Contains(msg, "is not a member") || strings.Contains(msg, "no rows") {
			return nil, &domain.ErrUserNotFound{Message: "user is not a member of this workspace"}
		}
		return nil, fmt.Errorf("lookup user workspace: %w", err)
	}
	if uw == nil {
		return nil, &domain.ErrUserNotFound{Message: "user is not a member of this workspace"}
	}
	return user, nil
}
