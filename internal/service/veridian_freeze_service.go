package service

// === Veridian patch — Freeze member per-user (CONTRAT-HUB §5.21) ===
//
// Implementations FreezeMember / UnfreezeMember. Voir
// todo/2026-05-23-membership-freeze-per-user.md et
// domain/veridian_freeze.go.
//
// Pattern :
//
//   - FreezeMember : insert via frozenMemberRepo (UPSERT idempotent),
//     refuse si target est owner (ErrCannotFreezeOwner), emit webhook
//     tenant.member_frozen best-effort.
//   - UnfreezeMember : delete via frozenMemberRepo (idempotent), emit
//     tenant.member_unfrozen UNIQUEMENT si une row a effectivement ete
//     supprimee (pas d'emit sur replay idempotent — evite spam Hub).
//
// Pattern setter post-construction : ConfigureFrozenMemberSupport injecte
// le repo dans une instance VeridianService existante. Mode "self-hosted
// sans freeze" si le repo n'est pas configure → refuse l'op au lieu
// d'echouer en silence.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// ErrCannotFreezeOwner est retourne par FreezeMember si la target est l'owner
// du workspace. CONTRAT-HUB §5.21 garde-fou : freeze l'owner casserait toute
// la chaine d'administration du tenant. Le handler mappe ce sentinel vers
// HTTP 409 Conflict + code cannot_freeze_owner.
var ErrCannotFreezeOwner = errors.New("cannot freeze workspace owner")

// ErrFrozenMemberRepoNotConfigured est retourne par FreezeMember/UnfreezeMember
// si le repo n'a pas ete injecte via ConfigureFrozenMemberSupport. Mode
// "self-hosted sans freeze" — refuse l'op plutot que d'ignorer en silence.
var ErrFrozenMemberRepoNotConfigured = errors.New("frozen_member repo not configured")

// ErrMemberNotInWorkspace est retourne par FreezeMember/UnfreezeMember si le
// user existe mais n'est pas membre du workspace cible. Distinct de
// sql.ErrNoRows (workspace inexistant) pour permettre au handler de renvoyer
// 404 user_not_member specifique.
var ErrMemberNotInWorkspace = errors.New("user is not a member of this workspace")

// ConfigureFrozenMemberSupport injecte le repo frozen_member dans une instance
// VeridianService. Pattern aligne sur ConfigureAPIKeyGraceSupport.
// Renvoie une erreur si svc n'est pas un *veridianService (mock ou
// implementation custom).
func ConfigureFrozenMemberSupport(svc domain.VeridianService, repo domain.VeridianFrozenMemberRepository) error {
	impl, ok := svc.(*veridianService)
	if !ok {
		return errors.New("svc is not a *veridianService — frozen_member support unsupported on this implementation")
	}
	impl.frozenMemberRepo = repo
	return nil
}

// FreezeMember marque un user comme frozen sur un workspace. CONTRAT-HUB §5.21.
//
// Algorithme :
//
//  1. Verifier workspace existe (sql.ErrNoRows si non).
//  2. Lookup user par email. Si absent → ErrMemberNotInWorkspace (le user
//     doit avoir un compte Notifuse pour etre frozen).
//  3. Lookup user_workspaces. Si pas membre → ErrMemberNotInWorkspace.
//  4. Si role = owner → ErrCannotFreezeOwner (garde-fou §5.21).
//  5. Repo.Freeze (UPSERT idempotent). Retourne (row, alreadyFrozen).
//  6. Si alreadyFrozen=false → emit tenant.member_frozen best-effort.
//  7. Touch hub_sync best-effort.
//
// Idempotence : 2eme call retourne alreadyFrozen=true. Le handler decide
// 200 vs 409 (ici on prefere 409 conformement au brief team-lead).
func (s *veridianService) FreezeMember(ctx context.Context, input domain.FreezeMemberInput) (*domain.FreezeMemberResponse, bool, error) {
	if input.TenantID == "" {
		return nil, false, errors.New("tenant_id required")
	}
	if input.UserEmail == "" {
		return nil, false, errors.New("user_email required")
	}
	if s.frozenMemberRepo == nil {
		return nil, false, ErrFrozenMemberRepoNotConfigured
	}

	reason := input.Reason
	if reason == "" {
		reason = domain.FreezeReasonManual
	}

	// Step 1 : workspace existe ?
	if _, wsErr := s.workspaceRepo.GetByID(ctx, input.TenantID); wsErr != nil {
		if errors.Is(wsErr, sql.ErrNoRows) {
			return nil, false, sql.ErrNoRows
		}
		msg := strings.ToLower(wsErr.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no rows") {
			return nil, false, sql.ErrNoRows
		}
		return nil, false, fmt.Errorf("lookup workspace: %w", wsErr)
	}

	// Step 2 : lookup user par email.
	member, err := s.userService.GetUserByEmail(ctx, input.UserEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if errors.As(err, &notFound) {
			return nil, false, ErrMemberNotInWorkspace
		}
		return nil, false, fmt.Errorf("get user by email: %w", err)
	}
	if member == nil {
		return nil, false, ErrMemberNotInWorkspace
	}

	// Step 3 : lookup user_workspaces.
	existing, lookupErr := s.workspaceRepo.GetUserWorkspace(ctx, member.ID, input.TenantID)
	if lookupErr != nil {
		msg := strings.ToLower(lookupErr.Error())
		notAttached := strings.Contains(msg, "not found") ||
			strings.Contains(msg, "is not a member") ||
			strings.Contains(msg, "no rows") ||
			errors.Is(lookupErr, sql.ErrNoRows)
		if notAttached {
			return nil, false, ErrMemberNotInWorkspace
		}
		return nil, false, fmt.Errorf("lookup user workspace: %w", lookupErr)
	}
	if existing == nil {
		return nil, false, ErrMemberNotInWorkspace
	}

	// Step 4 : garde-fou owner.
	if existing.Role == "owner" {
		return nil, false, ErrCannotFreezeOwner
	}

	// Step 5 : repo Freeze (UPSERT idempotent).
	row, alreadyFrozen, freezeErr := s.frozenMemberRepo.Freeze(ctx, input.TenantID, member.ID, reason)
	if freezeErr != nil {
		return nil, false, fmt.Errorf("freeze member: %w", freezeErr)
	}

	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id":      input.TenantID,
			"user_email":     input.UserEmail,
			"app_user_id":    member.ID,
			"hub_user_id":    input.HubUserID,
			"reason":         string(reason),
			"already_frozen": alreadyFrozen,
		}).Info("veridian FreezeMember: member freeze processed")
	}

	// Step 6 : webhook (uniquement sur nouveau freeze, pas sur replay idempotent).
	if !alreadyFrozen && s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantMemberFrozen, input.TenantID, map[string]interface{}{
			"user_email":  input.UserEmail,
			"hub_user_id": input.HubUserID,
			"app_user_id": member.ID,
			"reason":      string(reason),
			"frozen_at":   row.FrozenAt.UTC().Format(time.RFC3339),
			"actor":       "hub", // §5.21 : appele par script admin Hub
		})
	}

	s.touchHubSync(ctx, input.TenantID)

	return &domain.FreezeMemberResponse{
		TenantID:  input.TenantID,
		UserEmail: input.UserEmail,
		HubUserID: input.HubUserID,
		FrozenAt:  row.FrozenAt,
		Reason:    row.Reason,
	}, alreadyFrozen, nil
}

// UnfreezeMember enleve le freeze d'un user sur un workspace. CONTRAT-HUB §5.21.
//
// Algorithme :
//
//  1. Verifier workspace existe (sql.ErrNoRows si non).
//  2. Lookup user par email. Si absent → ErrMemberNotInWorkspace.
//  3. Repo.Unfreeze (DELETE idempotent). Retourne wasFrozen.
//  4. Si wasFrozen=true → emit tenant.member_unfrozen best-effort.
//  5. Touch hub_sync best-effort.
//
// Idempotence : si user pas frozen, on retourne 200 wasFrozen=false sans
// webhook (evite spam Hub sur replay). Le brief autorise aussi 404, mais
// 200 idempotent est plus aligne sur le reste du contrat (cf. remove-member).
//
// Note : on n'exige PAS que le user soit encore membre du workspace —
// un user retire du workspace puis re-attache pourrait avoir une row freeze
// orpheline a nettoyer. L'unfreeze doit toujours pouvoir effacer une row
// freeze qui traine.
func (s *veridianService) UnfreezeMember(ctx context.Context, input domain.UnfreezeMemberInput) (*domain.UnfreezeMemberResponse, bool, error) {
	if input.TenantID == "" {
		return nil, false, errors.New("tenant_id required")
	}
	if input.UserEmail == "" {
		return nil, false, errors.New("user_email required")
	}
	if s.frozenMemberRepo == nil {
		return nil, false, ErrFrozenMemberRepoNotConfigured
	}

	// Step 1 : workspace existe ?
	if _, wsErr := s.workspaceRepo.GetByID(ctx, input.TenantID); wsErr != nil {
		if errors.Is(wsErr, sql.ErrNoRows) {
			return nil, false, sql.ErrNoRows
		}
		msg := strings.ToLower(wsErr.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no rows") {
			return nil, false, sql.ErrNoRows
		}
		return nil, false, fmt.Errorf("lookup workspace: %w", wsErr)
	}

	// Step 2 : lookup user par email.
	member, err := s.userService.GetUserByEmail(ctx, input.UserEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if errors.As(err, &notFound) {
			return nil, false, ErrMemberNotInWorkspace
		}
		return nil, false, fmt.Errorf("get user by email: %w", err)
	}
	if member == nil {
		return nil, false, ErrMemberNotInWorkspace
	}

	// Step 3 : repo Unfreeze.
	wasFrozen, unfreezeErr := s.frozenMemberRepo.Unfreeze(ctx, input.TenantID, member.ID)
	if unfreezeErr != nil {
		return nil, false, fmt.Errorf("unfreeze member: %w", unfreezeErr)
	}

	unfrozenAt := time.Now().UTC()
	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id":   input.TenantID,
			"user_email":  input.UserEmail,
			"app_user_id": member.ID,
			"hub_user_id": input.HubUserID,
			"was_frozen":  wasFrozen,
		}).Info("veridian UnfreezeMember: member unfreeze processed")
	}

	// Step 4 : webhook uniquement si un vrai unfreeze a eu lieu.
	if wasFrozen && s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantMemberUnfrozen, input.TenantID, map[string]interface{}{
			"user_email":  input.UserEmail,
			"hub_user_id": input.HubUserID,
			"app_user_id": member.ID,
			"unfrozen_at": unfrozenAt.Format(time.RFC3339),
			"actor":       "hub",
		})
	}

	s.touchHubSync(ctx, input.TenantID)

	return &domain.UnfreezeMemberResponse{
		TenantID:   input.TenantID,
		UserEmail:  input.UserEmail,
		HubUserID:  input.HubUserID,
		UnfrozenAt: unfrozenAt,
	}, wasFrozen, nil
}
