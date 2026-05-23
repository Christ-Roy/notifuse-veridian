package service

// === Veridian patch — v1.3 Multi-membre cross-app (2026-05-19) ===
//
// Implementations SyncMember (§5.18), RemoveMember (§5.19), RestoreMember
// (§5.20) du contrat Hub. Voir todo/2026-05-19-v13-multi-membre-cross-app.md.
//
// Pattern :
//   - SyncMember : mirror de l'attach-member workspace-level (§5.22) mais
//     en endpoint tenant-level. Pour Notifuse 1 tenant = 1 workspace, donc
//     les 2 endpoints se rejoignent au niveau service.
//   - RemoveMember : hard delete user_workspaces row via workspaceRepo
//     direct (HMAC bypass auth). Le user reste en table users pour audit.
//     Refuse si target est owner (ErrCannotRemoveOwner).
//   - RestoreMember : re-add user au workspace avec role=member. Cree le user
//     s'il a ete physiquement supprime entre-temps (defensif).
//
// freeze/unfreeze (§5.21) non livre : exige un mecanisme paywall per-user
// que Notifuse n'a pas (paywall middleware tenant-level). Ticket de suivi
// requis pour activer une fois le quota seats live cote Hub. Cf. note Option B
// dans le ticket todo/2026-05-19-v13-multi-membre-cross-app.md : le default
// conservateur §5.21.4 (Hub bloque les invitations au-dela du seat-limit)
// suffit comme protection minimale tant que le Hub n'emet pas
// `tenant.member_frozen` cross-app.
//
// Webhooks app → Hub (§5.18.4 + §7.1) : SyncMember/RestoreMember emettent
// `tenant.member_added`, RemoveMember emet `tenant.member_removed`. L'event
// `tenant.member_role_changed` est emis cote AttachMember
// (veridian_service.go) quand un role est promu via Hub invitation — seul
// site Notifuse qui modifie un role existant (SyncMember est additif
// uniquement, donc pas concerne).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/google/uuid"
)

// ErrCannotRemoveOwner est retourne par RemoveMember quand le target est
// l'owner du workspace. CONTRAT-HUB §5.19.1 (handler => 409 cannot_remove_owner).
var ErrCannotRemoveOwner = errors.New("cannot remove workspace owner — use transfer-owner instead")

// SyncMember propage un membre Hub → Notifuse. CONTRAT-HUB §5.18.3.
//
// Algorithme :
//   1. Verifier workspace existe (404 si non — sql.ErrNoRows).
//   2. Lookup user Notifuse par EMAIL (source de verite identite, §3.7).
//   3. Si absent → CreateUser (type=user, UUID natif Notifuse, password vide).
//   4. Lookup user_workspaces (user_id, workspace_id) :
//      - Si present + role owner → garder owner (additif, jamais downgrade).
//      - Si present + role member → idempotent, retourner synced=true.
//      - Si absent → AddUserToWorkspace(role=member) via ctx caller owner.
//   5. Touch hub_sync (best-effort).
//
// Idempotence stricte : 2e call sans changement = 200 synced=true.
func (s *veridianService) SyncMember(ctx context.Context, input domain.SyncMemberInput) (*domain.SyncMemberResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.UserEmail == "" {
		return nil, errors.New("user_email required")
	}
	if input.HubUserID == "" {
		return nil, errors.New("hub_user_id required")
	}
	if !input.Role.IsValid() {
		return nil, fmt.Errorf("invalid role %q: must be member|admin", input.Role)
	}

	// Step 1 : workspace existe ?
	if _, wsErr := s.workspaceRepo.GetByID(ctx, input.TenantID); wsErr != nil {
		if errors.Is(wsErr, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		msg := strings.ToLower(wsErr.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no rows") {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("lookup workspace: %w", wsErr)
	}

	// Step 2 : lookup user par EMAIL.
	member, err := s.userService.GetUserByEmail(ctx, input.UserEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("get user by email: %w", err)
		}
		member = nil
	}

	// Step 3 : creer si absent.
	if member == nil {
		member = &domain.User{
			ID:        uuid.New().String(),
			Email:     input.UserEmail,
			Type:      domain.UserTypeUser,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.userRepo.CreateUser(ctx, member); err != nil {
			return nil, fmt.Errorf("create member user: %w", err)
		}
	}

	// Step 4 : lookup user_workspaces.
	// Notifuse n'a que owner/member en role workspace upstream. Tous les
	// invites du Hub deviennent `member` (cf. §3.5 : Hub non-autoritatif sur
	// les roles internes app). Si le user est DEJA owner, on garde owner
	// (additif, jamais de downgrade).
	existing, lookupErr := s.workspaceRepo.GetUserWorkspace(ctx, member.ID, input.TenantID)
	if lookupErr == nil && existing != nil {
		// Idempotent : deja membre, on garde son role effectif.
		appRole := existing.Role
		s.touchHubSync(ctx, input.TenantID)
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"tenant_id":   input.TenantID,
				"hub_user_id": input.HubUserID,
				"app_role":    appRole,
			}).Info("veridian SyncMember: already_member idempotent return")
		}
		return &domain.SyncMemberResponse{
			TenantID:  input.TenantID,
			UserEmail: input.UserEmail,
			Synced:    true,
			AppUserID: member.ID,
			AppRole:   appRole,
		}, nil
	}
	if lookupErr != nil {
		msg := strings.ToLower(lookupErr.Error())
		notAttached := strings.Contains(msg, "not found") ||
			strings.Contains(msg, "is not a member") ||
			strings.Contains(msg, "no rows") ||
			errors.Is(lookupErr, sql.ErrNoRows)
		if !notAttached {
			return nil, fmt.Errorf("lookup user workspace: %w", lookupErr)
		}
	}

	// Pas membre → resolveur ctx caller owner pour AddUserToWorkspace upstream.
	callerOwnerID, err := s.resolveWorkspaceCaller(ctx, input.TenantID)
	if err != nil {
		return nil, err
	}
	callerCtx, callerSessionID, err := s.ctxAsUser(ctx, callerOwnerID)
	if err != nil {
		return nil, fmt.Errorf("ctxAsUser workspace owner: %w", err)
	}
	defer s.cleanupSession(ctx, callerSessionID)

	if addErr := s.workspaceService.AddUserToWorkspace(
		callerCtx,
		input.TenantID,
		member.ID,
		"member",
		domain.FullPermissions,
	); addErr != nil {
		// "already" indique une race condition idempotente.
		if !strings.Contains(strings.ToLower(addErr.Error()), "already") {
			return nil, fmt.Errorf("add user to workspace: %w", addErr)
		}
	}

	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id":     input.TenantID,
			"hub_user_id":   input.HubUserID,
			"app_user_id":   member.ID,
			"app_role":      "member",
			"role_from_hub": string(input.Role),
		}).Info("veridian SyncMember: member attached")
	}

	// Webhook app → Hub (CONTRAT-HUB §7.1) : tenant.member_added emit uniquement
	// sur nouveau attach (idempotent replay ci-dessus a deja return sans
	// passer ici). Best-effort, non bloquant (goroutine + retry interne).
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantMemberAdded, input.TenantID, map[string]interface{}{
			"user_email":  input.UserEmail,
			"role":        "member",
			"hub_user_id": input.HubUserID,
			"app_user_id": member.ID,
			"actor":       "hub", // §5.18.3 : appele par script admin Hub
		})
	}

	s.touchHubSync(ctx, input.TenantID)

	return &domain.SyncMemberResponse{
		TenantID:  input.TenantID,
		UserEmail: input.UserEmail,
		Synced:    true,
		AppUserID: member.ID,
		AppRole:   "member",
	}, nil
}

// RemoveMember retire un membre du workspace. CONTRAT-HUB §5.19.2.
//
// Algorithme :
//   1. Verifier workspace existe (404 si non).
//   2. Lookup user par email. Si absent → 200 idempotent (rien a retirer).
//   3. Lookup user_workspaces. Si absent → 200 idempotent.
//   4. Si role=owner → ErrCannotRemoveOwner (handler 409).
//   5. workspaceRepo.RemoveUserFromWorkspace (raw DB, bypass auth via HMAC).
//   6. Touch hub_sync (best-effort).
//
// Hard delete user_workspaces row : le user lui-meme reste en DB pour audit
// + restauration (restore-member re-cree la row si besoin). Sa data creee
// (templates, broadcasts) reste attachee au tenant.
//
// Note semantique : le contrat parle de "soft delete user_workspaces.deleted_at",
// mais Notifuse n'a pas cette colonne v1.3. Hard delete + restore en re-INSERT
// preserve le comportement metier (le user perd l'acces, peut etre restaure)
// sans imposer une migration DB destructive. Une colonne deleted_at peut etre
// ajoutee ulterieurement si on a besoin de tracer l'historique des retraits.
func (s *veridianService) RemoveMember(ctx context.Context, input domain.RemoveMemberInput) (*domain.RemoveMemberResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.UserEmail == "" {
		return nil, errors.New("user_email required")
	}

	// Step 1 : workspace existe ?
	if _, wsErr := s.workspaceRepo.GetByID(ctx, input.TenantID); wsErr != nil {
		if errors.Is(wsErr, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		msg := strings.ToLower(wsErr.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no rows") {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("lookup workspace: %w", wsErr)
	}

	// Step 2 : lookup user par email.
	member, err := s.userService.GetUserByEmail(ctx, input.UserEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if errors.As(err, &notFound) {
			// User inconnu = idempotent, rien a retirer.
			s.touchHubSync(ctx, input.TenantID)
			return &domain.RemoveMemberResponse{
				TenantID:  input.TenantID,
				UserEmail: input.UserEmail,
				RemovedAt: time.Now().UTC(),
			}, nil
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	if member == nil {
		s.touchHubSync(ctx, input.TenantID)
		return &domain.RemoveMemberResponse{
			TenantID:  input.TenantID,
			UserEmail: input.UserEmail,
			RemovedAt: time.Now().UTC(),
		}, nil
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
			// User existe mais pas membre = idempotent.
			s.touchHubSync(ctx, input.TenantID)
			return &domain.RemoveMemberResponse{
				TenantID:  input.TenantID,
				UserEmail: input.UserEmail,
				RemovedAt: time.Now().UTC(),
			}, nil
		}
		return nil, fmt.Errorf("lookup user workspace: %w", lookupErr)
	}
	if existing == nil {
		s.touchHubSync(ctx, input.TenantID)
		return &domain.RemoveMemberResponse{
			TenantID:  input.TenantID,
			UserEmail: input.UserEmail,
			RemovedAt: time.Now().UTC(),
		}, nil
	}

	// Step 4 : garde-fou owner.
	if existing.Role == "owner" {
		return nil, ErrCannotRemoveOwner
	}

	// Step 5 : hard delete user_workspaces row (raw DB, bypass auth via HMAC).
	if rmErr := s.workspaceRepo.RemoveUserFromWorkspace(ctx, member.ID, input.TenantID); rmErr != nil {
		return nil, fmt.Errorf("remove user from workspace: %w", rmErr)
	}

	removedAt := time.Now().UTC()
	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id":  input.TenantID,
			"user_email": input.UserEmail,
			"user_id":    member.ID,
			"reason":     input.Reason,
		}).Info("veridian RemoveMember: member removed from workspace")
	}

	// Webhook app → Hub (CONTRAT-HUB §7.1) : tenant.member_removed emit sur
	// hard delete reussi uniquement (pas sur les short-circuits idempotents
	// ci-dessus, ni sur ErrCannotRemoveOwner). Best-effort, non bloquant.
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantMemberRemoved, input.TenantID, map[string]interface{}{
			"user_email":  input.UserEmail,
			"reason":      input.Reason,
			"app_user_id": member.ID,
			"actor":       "hub", // §5.19.2 : appele par script admin Hub
		})
	}

	s.touchHubSync(ctx, input.TenantID)

	return &domain.RemoveMemberResponse{
		TenantID:  input.TenantID,
		UserEmail: input.UserEmail,
		RemovedAt: removedAt,
	}, nil
}

// RestoreMember annule un remove-member precedent. CONTRAT-HUB §5.20.
//
// Algorithme :
//   1. Verifier workspace existe (404 si non).
//   2. Lookup/create user par email (si user supprime entre-temps).
//   3. Lookup user_workspaces : si deja membre → idempotent 200.
//   4. Sinon → AddUserToWorkspace(role=member) via ctx caller owner.
//   5. Touch hub_sync (best-effort).
//
// Note : le role original avant remove n'est pas trace (pas de colonne
// deleted_at sur user_workspaces). On restaure toujours en role=member,
// conformement au §3.5 (Hub non-autoritatif). Si l'user etait owner, c'est
// qu'on a tenter un transfer-owner avant remove — workflow distinct.
func (s *veridianService) RestoreMember(ctx context.Context, input domain.RestoreMemberInput) (*domain.RestoreMemberResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.UserEmail == "" {
		return nil, errors.New("user_email required")
	}

	// Step 1 : workspace existe ?
	if _, wsErr := s.workspaceRepo.GetByID(ctx, input.TenantID); wsErr != nil {
		if errors.Is(wsErr, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		msg := strings.ToLower(wsErr.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no rows") {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("lookup workspace: %w", wsErr)
	}

	// Step 2 : lookup/create user.
	member, err := s.userService.GetUserByEmail(ctx, input.UserEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("get user by email: %w", err)
		}
		member = nil
	}
	if member == nil {
		member = &domain.User{
			ID:        uuid.New().String(),
			Email:     input.UserEmail,
			Type:      domain.UserTypeUser,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.userRepo.CreateUser(ctx, member); err != nil {
			return nil, fmt.Errorf("create member user: %w", err)
		}
	}

	// Step 3 : lookup user_workspaces.
	existing, lookupErr := s.workspaceRepo.GetUserWorkspace(ctx, member.ID, input.TenantID)
	if lookupErr == nil && existing != nil {
		// Idempotent : deja membre.
		s.touchHubSync(ctx, input.TenantID)
		return &domain.RestoreMemberResponse{
			TenantID:   input.TenantID,
			UserEmail:  input.UserEmail,
			RestoredAt: time.Now().UTC(),
		}, nil
	}
	if lookupErr != nil {
		msg := strings.ToLower(lookupErr.Error())
		notAttached := strings.Contains(msg, "not found") ||
			strings.Contains(msg, "is not a member") ||
			strings.Contains(msg, "no rows") ||
			errors.Is(lookupErr, sql.ErrNoRows)
		if !notAttached {
			return nil, fmt.Errorf("lookup user workspace: %w", lookupErr)
		}
	}

	// Step 4 : AddUserToWorkspace via owner caller ctx.
	callerOwnerID, err := s.resolveWorkspaceCaller(ctx, input.TenantID)
	if err != nil {
		return nil, err
	}
	callerCtx, callerSessionID, err := s.ctxAsUser(ctx, callerOwnerID)
	if err != nil {
		return nil, fmt.Errorf("ctxAsUser workspace owner: %w", err)
	}
	defer s.cleanupSession(ctx, callerSessionID)

	if addErr := s.workspaceService.AddUserToWorkspace(
		callerCtx,
		input.TenantID,
		member.ID,
		"member",
		domain.FullPermissions,
	); addErr != nil {
		if !strings.Contains(strings.ToLower(addErr.Error()), "already") {
			return nil, fmt.Errorf("add user to workspace: %w", addErr)
		}
	}

	restoredAt := time.Now().UTC()
	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id":  input.TenantID,
			"user_email": input.UserEmail,
			"user_id":    member.ID,
		}).Info("veridian RestoreMember: member restored to workspace")
	}

	// Webhook app → Hub (CONTRAT-HUB §7.1) : tenant.member_added emit sur
	// restore reussi uniquement (le short-circuit "deja membre" ci-dessus a
	// deja return sans passer ici). Du point de vue Hub, un restore est
	// equivalent a un add — meme event, payload `actor=hub`.
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantMemberAdded, input.TenantID, map[string]interface{}{
			"user_email":  input.UserEmail,
			"role":        "member",
			"app_user_id": member.ID,
			"actor":       "hub", // §5.20 : appele par script admin Hub
			"restored":    true,  // distingue un restore d'un add neuf
		})
	}

	s.touchHubSync(ctx, input.TenantID)

	return &domain.RestoreMemberResponse{
		TenantID:   input.TenantID,
		UserEmail:  input.UserEmail,
		RestoredAt: restoredAt,
	}, nil
}

// resolveWorkspaceCaller renvoie l'id d'un user owner du workspace, fallback
// root si aucun owner trouve (workspace orphelin). Utilise pour construire
// un ctx caller pour AddUserToWorkspace upstream qui exige role=owner.
//
// Pattern identique a AttachMember/RotateAPIKey — factorise ici pour les 2
// endpoints SyncMember/RestoreMember.
func (s *veridianService) resolveWorkspaceCaller(ctx context.Context, workspaceID string) (string, error) {
	members, listErr := s.workspaceRepo.GetWorkspaceUsersWithEmail(ctx, workspaceID)
	if listErr != nil {
		return "", fmt.Errorf("list workspace members: %w", listErr)
	}
	for _, m := range members {
		if m != nil && m.Role == "owner" && m.Type == domain.UserTypeUser {
			return m.UserID, nil
		}
	}
	// Fallback root (workspace orphelin).
	if s.rootEmail == "" {
		return "", errors.New("no owner found and ROOT_EMAIL not configured")
	}
	rootUser, rootErr := s.userRepo.GetUserByEmail(ctx, s.rootEmail)
	if rootErr != nil {
		return "", fmt.Errorf("resolve fallback caller (root): %w", rootErr)
	}
	return rootUser.ID, nil
}
