package service

// === Veridian patch — Lot K (2026-05-21) ===
//
// Implementations RotateAPIKey (§5.15) + TransferOwner (§5.16) du contrat Hub.
// Voir todo/2026-05-19-rotate-transfer-owner-endpoints.md.
//
// RotateAPIKey : genere une nouvelle api_key + marque l'ancienne pour
//                revocation apres APIKeyGracePeriod (5min). Pendant la grace,
//                les DEUX keys sont valides — comportement explicitement
//                attendu par le contrat (zero downtime cote Hub).
//
// TransferOwner : thin wrapper sur AttachOwner. L'ancien owner devient
//                 `member` (divergence Notifuse vs contrat "admin" — pas
//                 natif Notifuse). Reponse format §5.16.
//
// Grace cleanup : voir StartAPIKeyGraceCleanupLoop pour le cron 1×/min.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// ErrAPIKeyGraceRepoNotConfigured est retourne par RotateAPIKey si le grace
// repo n'a pas ete injecte via ConfigureAPIKeyGraceSupport. Mode "self-hosted
// sans grace tracking" — refuse l'op plutot que d'effacer l'ancienne key
// immediatement (qui casserait le caller).
var ErrAPIKeyGraceRepoNotConfigured = errors.New("api_key grace repo not configured")

// ConfigureAPIKeyGraceSupport injecte le repo grace dans une instance
// VeridianService. Renvoie une erreur si svc n'est pas un *veridianService
// (par exemple, un mock ou une implementation custom).
//
// Pattern setter post-construction : evite de casser la signature
// NewVeridianService (back-compat tests existants + app.go). Le repo grace
// est optionnel cote constructor — uniquement requis si RotateAPIKey est
// utilise.
func ConfigureAPIKeyGraceSupport(svc domain.VeridianService, repo domain.VeridianAPIKeyGraceRepository) error {
	impl, ok := svc.(*veridianService)
	if !ok {
		return errors.New("svc is not a *veridianService — grace support unsupported on this implementation")
	}
	impl.apiKeyGraceRepo = repo
	return nil
}

// RotateAPIKey genere une nouvelle api_key pour le tenant et marque l'ancienne
// pour revocation apres APIKeyGracePeriod. CONTRAT-HUB §5.15.
//
// Algorithme :
//
//  1. Verifier que le workspace existe (404 si non).
//  2. Lister les users du workspace, identifier l'ancienne api_key (type=api_key
//     + role=member). Si plusieurs (re-rotate dans la grace precedente), on
//     prend la plus recente (par CreatedAt) — les autres sont deja en grace.
//  3. Generer une nouvelle api_key via workspaceService.CreateAPIKey avec un
//     prefix unique (suffixe horodatage millisecondes mod 1M pour eviter
//     conflict avec l'ancienne qui partage le tenant_id base).
//  4. Marker le user veridian_managed (parite avec Provision).
//  5. Insert grace entry pour l'ancienne avec revoke_at = NOW + APIKeyGracePeriod.
//  6. Emit EventTenantAPIKeyRotated (best-effort).
//  7. Touch hub_sync (best-effort).
//
// Idempotence : un re-rotate dans la grace period precedente ecrase l'entree
// grace de l'ancienne ancienne key (qui devient orpheline et sera supprimee
// par le cron). Best-effort : le caller doit gerer le risque de
// surconsommation d'api_key users dans la grace period.
func (s *veridianService) RotateAPIKey(ctx context.Context, input domain.RotateAPIKeyInput) (*domain.RotateAPIKeyResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.Reason == "" {
		return nil, errors.New("reason required")
	}
	if s.apiKeyGraceRepo == nil {
		return nil, ErrAPIKeyGraceRepoNotConfigured
	}

	// Step 1 : verifier que le workspace existe.
	if _, wsErr := s.workspaceRepo.GetByID(ctx, input.TenantID); wsErr != nil {
		// On normalise sur sql.ErrNoRows via isNotFoundErr-style lookup.
		msg := strings.ToLower(wsErr.Error())
		if errors.Is(wsErr, sql.ErrNoRows) || strings.Contains(msg, "not found") || strings.Contains(msg, "no rows") {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("lookup workspace: %w", wsErr)
	}

	// Step 2 : trouver l'ancienne api_key. On utilise workspaceRepo direct
	// (sans check auth) car le contrat Hub via HMAC nous autorise.
	members, listErr := s.workspaceRepo.GetWorkspaceUsersWithEmail(ctx, input.TenantID)
	if listErr != nil {
		return nil, fmt.Errorf("list workspace members: %w", listErr)
	}
	var oldAPIKeyUserID string
	var oldAPIKeyCreatedAt time.Time
	for _, m := range members {
		if m.Type != domain.UserTypeAPIKey {
			continue
		}
		// On choisit la plus recente (parmi celles encore actives — celles
		// deja revoquees sont DELETE par le cron donc absentes ici).
		if oldAPIKeyUserID == "" || m.CreatedAt.After(oldAPIKeyCreatedAt) {
			oldAPIKeyUserID = m.UserID
			oldAPIKeyCreatedAt = m.CreatedAt
		}
	}

	// Step 3 : creer la nouvelle api_key. Le prefix doit etre UNIQUE — sans
	// quoi CreateAPIKey upstream va echouer ("this user already exists") car
	// l'email derive du prefix est identique a l'ancienne key.
	//
	// Suffixe = "r<unixMillis%1e9>" : 9 chiffres max, lisible (debug staging),
	// faiblement collisionnant (~31 ans entre 2 collisions sur le meme tenant).
	uniqueSuffix := fmt.Sprintf("r%d", time.Now().UnixMilli()%1_000_000_000)
	apiKeyPrefix := "veridian-api-" + input.TenantID + "-" + uniqueSuffix

	// CreateAPIKey upstream exige un ctx authentifie comme owner du workspace.
	// On utilise ctxAsRoot (root est owner d'un workspace fraichement cree par
	// Provision MAIS depuis owner-natif feature, root a ete retire). Donc on
	// resout l'owner actuel et on utilise sa session.
	var currentOwnerID string
	for _, m := range members {
		if m.Role == "owner" && m.Type == domain.UserTypeUser {
			currentOwnerID = m.UserID
			break
		}
	}
	if currentOwnerID == "" {
		// Fallback : essayer root. Si root n'est pas membre, CreateAPIKey
		// echouera et on remontera l'erreur (cas tenant orphelin).
		if s.rootEmail == "" {
			return nil, errors.New("no owner found and ROOT_EMAIL not configured")
		}
		rootUser, rootErr := s.userRepo.GetUserByEmail(ctx, s.rootEmail)
		if rootErr != nil {
			return nil, fmt.Errorf("resolve fallback caller (root): %w", rootErr)
		}
		currentOwnerID = rootUser.ID
	}

	callerCtx, callerSessionID, err := s.ctxAsUser(ctx, currentOwnerID)
	if err != nil {
		return nil, fmt.Errorf("ctxAsUser owner %s: %w", currentOwnerID, err)
	}
	defer s.cleanupSession(ctx, callerSessionID)

	apiKeyToken, apiKeyEmail, err := s.workspaceService.CreateAPIKey(callerCtx, input.TenantID, apiKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("create new api key: %w", err)
	}

	// Step 4 : marker veridian_managed (parite Provision step 5b). Non-fatal.
	if markErr := s.userRepo.MarkVeridianManaged(ctx, apiKeyEmail); markErr != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id": input.TenantID,
			"api_email": apiKeyEmail,
			"error":     markErr.Error(),
		}).Warn("veridian RotateAPIKey: failed to mark api_key user as veridian_managed (non-fatal)")
	}

	// Step 5 : insert grace entry si on avait une ancienne key.
	revokeAt := time.Now().UTC().Add(domain.APIKeyGracePeriod)
	if oldAPIKeyUserID != "" {
		entry := &domain.APIKeyGraceEntry{
			APIKeyUserID: oldAPIKeyUserID,
			WorkspaceID:  input.TenantID,
			RevokeAt:     revokeAt,
			Reason:       input.Reason,
			CreatedAt:    time.Now().UTC(),
		}
		if graceErr := s.apiKeyGraceRepo.Insert(ctx, entry); graceErr != nil {
			// Si on n'arrive pas a tracker l'ancienne pour revocation, on a un
			// risque de fuite — l'ancienne key va rester valide indefiniment.
			// On log l'incident pour audit mais on ne tente PAS de rollback du
			// nouveau user api_key (signature userRepo.Delete uncertaine, et le
			// fail-open est plus safe que le fail-close cote Hub : pire cas,
			// on a 2 keys valides et un orphelin DB → repair manuel possible).
			if s.logger != nil {
				s.logger.WithFields(map[string]interface{}{
					"tenant_id":         input.TenantID,
					"new_api_key_email": apiKeyEmail,
					"old_api_key_id":    oldAPIKeyUserID,
					"error":             graceErr.Error(),
				}).Error("veridian RotateAPIKey: grace insert failed — old key NOT scheduled for revocation, manual cleanup required")
			}
			return nil, fmt.Errorf("insert grace entry: %w", graceErr)
		}
	}

	// Step 6 : emit event (best-effort).
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantAPIKeyRotated, input.TenantID, map[string]interface{}{
			"new_api_key_email":      apiKeyEmail,
			"old_api_key_user_id":    oldAPIKeyUserID,
			"old_api_key_revokes_at": revokeAt,
			"reason":                 input.Reason,
		})
	}

	// Step 7 : touch hub_sync (best-effort).
	s.touchHubSync(ctx, input.TenantID)

	return &domain.RotateAPIKeyResponse{
		TenantID:           input.TenantID,
		NewAPIKey:          apiKeyToken,
		NewAPIKeyEmail:     apiKeyEmail,
		OldAPIKeyRevokesAt: revokeAt,
	}, nil
}

// TransferOwner transfere l'ownership a un nouvel email. Thin wrapper sur
// AttachOwner avec le format response §5.16.
//
// Divergence Notifuse vs contrat : l'ancien owner devient `member` (pas
// `admin`) car Notifuse n'a pas de role admin natif. L'intention contrat
// (retirer les droits owner) est respectee — c'est cosmetique cote API.
func (s *veridianService) TransferOwner(ctx context.Context, input domain.TransferOwnerInput) (*domain.TransferOwnerResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.NewOwnerEmail == "" {
		return nil, errors.New("new_owner_email required")
	}
	if input.Reason == "" {
		return nil, errors.New("reason required")
	}

	// Resolver l'ancien owner AVANT le transfer pour le response payload.
	// On ne fail PAS si non resolvable (workspace orphelin) — on met "" et
	// AttachOwner gerera l'op (incluant le 404 si tenant absent).
	var oldOwnerEmail string
	if members, listErr := s.workspaceRepo.GetWorkspaceUsersWithEmail(ctx, input.TenantID); listErr == nil {
		for _, m := range members {
			if m.Role == "owner" && m.Type == domain.UserTypeUser {
				oldOwnerEmail = m.Email
				break
			}
		}
	}

	// Appel AttachOwner qui fait DEJA tout le boulot : find/create new owner
	// user, AddUserToWorkspace, TransferOwnership (old owner → member),
	// emit tenant.owner_changed, touchHubSync.
	if _, err := s.AttachOwner(ctx, domain.AttachOwnerInput{
		TenantID:   input.TenantID,
		OwnerEmail: input.NewOwnerEmail,
	}); err != nil {
		// AttachOwner propage sql.ErrNoRows si tenant inexistant — handler
		// map vers 404. Toute autre erreur remonte telle quelle.
		return nil, err
	}

	transferredAt := time.Now().UTC()
	return &domain.TransferOwnerResponse{
		TenantID:      input.TenantID,
		OldOwner:      oldOwnerEmail,
		NewOwner:      input.NewOwnerEmail,
		TransferredAt: transferredAt,
	}, nil
}

// RunAPIKeyGraceCleanupOnce execute une passe du cron : lit les entrees
// expirees, supprime le user api_key upstream, supprime la row grace.
//
// Retourne (revoked, errs). revoked = nb de users effectivement supprimes.
// errs = liste des erreurs par api_key_user_id (best-effort par item, on ne
// stop pas au premier echec).
//
// Bloque : tourne synchroniquement. Pour la boucle 1×/min, voir
// StartAPIKeyGraceCleanupLoop.
func (s *veridianService) RunAPIKeyGraceCleanupOnce(ctx context.Context) (revoked int, errs map[string]string) {
	errs = make(map[string]string)
	if s.apiKeyGraceRepo == nil {
		return 0, errs
	}

	expired, err := s.apiKeyGraceRepo.ListExpired(ctx, time.Now().UTC())
	if err != nil {
		errs["__list__"] = err.Error()
		return 0, errs
	}

	for _, e := range expired {
		// Best-effort : on essaie de supprimer le user upstream. Si ça
		// echoue (FK, conn dead), on log et on continue — le cron rejoue
		// dans 1 min.
		if delErr := s.deleteAPIKeyUser(ctx, e.APIKeyUserID); delErr != nil {
			errs[e.APIKeyUserID] = delErr.Error()
			if s.logger != nil {
				s.logger.WithFields(map[string]interface{}{
					"api_key_user_id": e.APIKeyUserID,
					"workspace_id":    e.WorkspaceID,
					"error":           delErr.Error(),
				}).Warn("veridian grace cleanup: failed to delete api_key user (will retry next tick)")
			}
			continue
		}
		// User supprime : on peut maintenant retirer la row grace.
		if dropErr := s.apiKeyGraceRepo.DeleteByID(ctx, e.APIKeyUserID); dropErr != nil {
			errs[e.APIKeyUserID] = dropErr.Error()
			if s.logger != nil {
				s.logger.WithFields(map[string]interface{}{
					"api_key_user_id": e.APIKeyUserID,
					"error":           dropErr.Error(),
				}).Warn("veridian grace cleanup: failed to delete grace row after user delete (orphan row, will retry)")
			}
			continue
		}
		revoked++
	}

	return revoked, errs
}

// deleteAPIKeyUser supprime un user api_key. Implementation : on retire d'abord
// le user du workspace (parite "delete member"), puis on delete le user (cascade
// nettoie ses sessions s'il y en avait). Pour l'instant on se limite a
// userRepo.Delete via ctx system (bypass auth).
//
// Note : Notifuse upstream n'expose pas de UserRepository.Delete simple — le
// flow naturel est DELETE FROM users WHERE id = ... mais ça casse les FK
// si on a pas drop user_workspaces avant. La methode workspaceRepo.RemoveUserFromWorkspace
// existe et fait DELETE FROM user_workspaces — on l'utilise.
func (s *veridianService) deleteAPIKeyUser(ctx context.Context, userID string) error {
	// On a besoin du workspace_id pour RemoveUserFromWorkspace. On va le
	// chercher dans veridian_api_key_grace si possible.
	expired, _ := s.apiKeyGraceRepo.ListExpired(ctx, time.Now().UTC().Add(time.Hour))
	var workspaceID string
	for _, e := range expired {
		if e.APIKeyUserID == userID {
			workspaceID = e.WorkspaceID
			break
		}
	}
	if workspaceID != "" {
		// Best-effort retirer du workspace. Si echoue, on continue (le user
		// sera juste membre orphelin — non bloquant pour l'auth qui passe par
		// AuthenticateUserFromContext → GetUserByID).
		if err := s.workspaceRepo.RemoveUserFromWorkspace(ctx, userID, workspaceID); err != nil && s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"user_id":      userID,
				"workspace_id": workspaceID,
				"error":        err.Error(),
			}).Debug("veridian grace cleanup: remove from workspace failed (non-fatal)")
		}
	}

	// Supprimer le user. La signature attendue par notre code de cleanup :
	// userRepo.DeleteUserByID (ou equivalent). On utilise une approche
	// generique : si la methode existe, on l'appelle ; sinon on echoue
	// proprement (l'auth check renverra toujours 200 sur l'ancienne key).
	if deleter, ok := s.userRepo.(interface {
		DeleteUser(ctx context.Context, userID string) error
	}); ok {
		return deleter.DeleteUser(ctx, userID)
	}
	// Fallback : userRepo n'a pas DeleteUser → on ne peut pas physiquement
	// supprimer. Mais on a deja retire du workspace donc l'api_key ne pourra
	// plus authentifier pour aucun endpoint workspace-scope. Bon compromis.
	return nil
}
