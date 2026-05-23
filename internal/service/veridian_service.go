package service

// === Veridian patch ===
// VeridianService implemente les operations Hub-driven : provisioning,
// suspension, reprise, soft-delete, lecture de status, generation de magic
// link cross-app, hard wipe (test/admin).
//
// Le Hub appelle ces operations via les endpoints /api/tenants/* proteges
// par middleware HMAC (voir veridian_hmac.go). Le service coordonne :
//   - WorkspaceServiceInterface (CRUD workspace, transfer ownership)
//   - WorkspaceRepository       (lecture members sans check auth — utilise
//                                par WipeTestTenants pour resoudre l'owner
//                                a partir d'un workspace owner-natif)
//   - UserServiceInterface       (signin pour magic code, lookup user)
//   - UserRepository             (creation user owner si absent, sessions
//                                  pour bypass des guards d'auth)
//   - VeridianPlanRepository     (persistance plan + status + quota)
//   - WebhookEmitter             (events tenant.* pousses vers le Hub)
//
// Sessions virtuelles (ctxAsRoot / ctxAsUser) :
//
//	Plusieurs ops upstream (CreateWorkspace, AddUserToWorkspace, CreateAPIKey,
//	TransferOwnership, RemoveUserFromWorkspace, DeleteWorkspace) exigent un
//	user authentifie + permissions specifiques (root, owner, member). Le Hub
//	signe HMAC mais n'a pas de session humaine. On contourne en creant des
//	sessions courtes (rootSessionTTL=5min) :
//	  - ctxAsRoot   : session pour ROOT_EMAIL — utilise pour creer un
//	                  workspace fraichement (root devient owner par defaut
//	                  via CreateWorkspace), AddUserToWorkspace, CreateAPIKey,
//	                  TransferOwnership.
//	  - ctxAsUser   : session pour un user arbitraire (utilise pour le
//	                  tenant user owner apres TransferOwnership : appel
//	                  RemoveUserFromWorkspace pour virer root, et
//	                  DeleteWorkspace dans WipeTestTenants).
//	Sessions nettoyees via cleanupSession(sessionID) en defer.
//
// Owner-natif : depuis le commit f43ce239, chaque tenant Veridian-managed a
// un seul owner (le tenant user) — root est retire post-CreateWorkspace via
// TransferOwnership + RemoveUserFromWorkspace. Voir transferOwnershipToTenant.
//
// Voir veridian-platform/notifuse/README.md.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/google/uuid"
)

// rootSessionTTL est la duree de vie d'une session root virtuelle creee
// pour bypasser les guards d'auth pendant un appel Veridian. Court pour
// limiter le risque si une operation crashe avant cleanup.
const rootSessionTTL = 5 * time.Minute

// veridianPurgeDelay est la duree apres laquelle un tenant soft-deleted
// est purge definitivement par cron. Pendant cette fenetre, toute tentative
// de re-provisioning du meme tenant_id est rejetee (ErrTenantSoftDeleted)
// pour eviter qu'un re-signup accidentel n'herite des donnees du precedent
// owner. Decision produit Veridian — voir veridian-platform/notifuse/README.md.
const veridianPurgeDelay = 30 * 24 * time.Hour

// === Veridian patch ===
// ErrTenantSoftDeleted est retourne par Provision si le tenant_id correspond
// a une ligne veridian_plan avec deleted_at != NULL. Le handler doit mapper
// ce sentinel vers HTTP 409 Conflict.
var ErrTenantSoftDeleted = errors.New("tenant is soft-deleted, awaiting purge — re-provisioning blocked")

// === Veridian patch ===
// ErrOwnerMismatch est retourne par Provision quand un workspace existe deja
// pour le tenant_id donne mais que l'owner humain enregistre dans
// user_workspaces (role=owner, type=user) a un email different de
// input.OwnerEmail. Le handler doit mapper ce sentinel vers HTTP 409 Conflict.
//
// Pourquoi : contrat §5.1 exige "Conflit owner : si tenant_id existe avec un
// owner_email different → 409 Conflict, jamais d'ecrasement silencieux".
// Sans ce check, un attaquant connaissant un tenant_id pourrait re-provisionner
// avec son propre email et recevoir un magic_link valide vers le workspace
// (apres regeneration du magic_link cote idempotent, cf. ticket Hub
// 2026-05-18-confirm-provision-idempotence).
var ErrOwnerMismatch = errors.New("workspace exists with different owner")

// === Veridian patch ===
// ErrPlanImmune est retourne par UpdatePlan quand le plan existant a un
// plan_source immunise (lifetime_site_vitrine, lifetime_partner, internal,
// manual) et que l'appelant Hub essaie de le passer en plan_source=stripe.
// Protection contre les downgrades automatiques venant des webhooks Stripe :
// un cron Stripe ne doit jamais ecraser un plan offert a la main.
//
// Le handler mappe ce sentinel vers HTTP 409 Conflict + code plan_locked
// (CONTRAT-HUB sec. 3.3 + sec. 5.10).
//
// Note : on autorise les transitions inverses (stripe → lifetime_*) et les
// transitions entre sources immunes (lifetime_* → manual, etc.) — c'est
// uniquement la collision "automation Stripe ecrase un plan offert" qu'on
// bloque.
var ErrPlanImmune = errors.New("plan is immune to automatic downgrades (lifetime/manual/internal)")

// === Veridian patch ===
// ErrPurgeNotEligible est retourne par Purge quand la condition
// purge_eligible_at < NOW n'est pas remplie (soit tenant pas soft-deleted,
// soit fenetre 30j pas encore ecoulee). Le handler mappe ce sentinel vers
// HTTP 409 Conflict + code purge_not_eligible. CONTRAT-HUB sec. 5.8.
var ErrPurgeNotEligible = errors.New("tenant not yet eligible for purge")

// === Veridian patch — Couche 4 Bounce OAuth Hub (CONTRAT-HUB §6bis.8.3) ===
// ErrUserNotInApp est retourne par IssueMagicLinkForHub quand l'user existe
// cote Hub mais n'a aucun workspace dans cette app Notifuse. Le handler mappe
// ce sentinel vers HTTP 400 + {"error": "user_not_in_app", "hint": "..."}.
// Le Hub redirige alors vers /dashboard?app=notifuse&hint=signup. PAS
// d'auto-creation cote app (anti-pattern §6bis.2).
var ErrUserNotInApp = errors.New("user has no workspace in this app")

// veridianService est l'implementation par defaut de domain.VeridianService.
type veridianService struct {
	workspaceService domain.WorkspaceServiceInterface
	workspaceRepo    domain.WorkspaceRepository // === Veridian patch === acces direct sans check auth (utilise par WipeTestTenants pour resoudre l'owner d'un workspace post-feature owner-natif, ou root n'est plus member)
	userService      domain.UserServiceInterface
	userRepo         domain.UserRepository
	planRepo         domain.VeridianPlanRepository
	emitter          domain.WebhookEmitter
	defaultPlan      string
	rootEmail        string
	apiEndpoint      string
	hubSecret        string // === Veridian patch === pour signer les auto_login_url
	logger           logger.Logger
	// === Veridian patch — Lot K (2026-05-21) ===
	// apiKeyGraceRepo est OPTIONNEL : si nil, RotateAPIKey retourne
	// ErrAPIKeyGraceRepoNotConfigured. Injecté post-construction via
	// ConfigureAPIKeyGraceSupport (cf. veridian_rotate_transfer_service.go).
	apiKeyGraceRepo domain.VeridianAPIKeyGraceRepository

	// === Veridian patch — 2026-05-23 ===
	// templateService + transactionalNotificationService sont OPTIONNELS :
	// si nil, le seed du template invitation-prospection au Provision() est
	// silencieusement skippé (best-effort, jamais bloquant). Injectés
	// post-construction via ConfigureSeedTemplatesSupport
	// (cf. veridian_seed_templates.go).
	templateService                  domain.TemplateService
	transactionalNotificationService *TransactionalNotificationService
}

// NewVeridianService construit un VeridianService.
// rootEmail doit correspondre a config.RootEmail (le user root cote Notifuse).
// apiEndpoint est l'URL publique de Notifuse (ex https://notifuse.app.veridian.site)
// utilisee pour construire l'URL de magic link.
func NewVeridianService(
	workspaceService domain.WorkspaceServiceInterface,
	workspaceRepo domain.WorkspaceRepository,
	userService domain.UserServiceInterface,
	userRepo domain.UserRepository,
	planRepo domain.VeridianPlanRepository,
	emitter domain.WebhookEmitter,
	defaultPlan string,
	rootEmail string,
	apiEndpoint string,
	hubSecret string,
	log logger.Logger,
) domain.VeridianService {
	if defaultPlan == "" {
		defaultPlan = "free"
	}
	return &veridianService{
		workspaceService: workspaceService,
		workspaceRepo:    workspaceRepo,
		userService:      userService,
		userRepo:         userRepo,
		planRepo:         planRepo,
		emitter:          emitter,
		defaultPlan:      defaultPlan,
		rootEmail:        rootEmail,
		apiEndpoint:      apiEndpoint,
		hubSecret:        hubSecret,
		logger:           log,
	}
}

// ctxAsRoot cree une session courte pour le user root et retourne un ctx
// portant les claims (UserIDKey, UserTypeKey, SessionIDKey) attendus par
// AuthService.AuthenticateUserFromContext. La session ID retournee doit
// etre passee a cleanupSession en defer pour supprimer la session.
//
// Conserve pour compat : etait utilise avant la factorisation ctxAsUser.
// Returns (ctx, sessionID, rootUserID, error) — rootUserID utile aux callers
// qui veulent transferer l'ownership a un autre user (TransferOwnership exige
// l'ID du current owner, qui est root pour les workspaces qu'on cree nous-memes).
func (s *veridianService) ctxAsRoot(ctx context.Context) (context.Context, string, string, error) {
	if s.rootEmail == "" {
		return ctx, "", "", errors.New("veridian: ROOT_EMAIL not configured")
	}
	rootUser, err := s.userRepo.GetUserByEmail(ctx, s.rootEmail)
	if err != nil {
		return ctx, "", "", fmt.Errorf("veridian: get root user (%s): %w", s.rootEmail, err)
	}
	newCtx, sessionID, err := s.ctxAsUser(ctx, rootUser.ID)
	return newCtx, sessionID, rootUser.ID, err
}

// ctxAsUser cree une session courte pour un user arbitraire (root ou tenant
// owner) et retourne un ctx pre-rempli avec les claims requis par
// AuthService.AuthenticateUserFromContext + AuthenticateUserForWorkspace.
// Le ctx contient SystemCallKey pour bypass les guards Veridian-managed dans
// les services internes (notamment WorkspaceService.CreateWorkspace).
//
// La session ID retournee doit etre passee a cleanupSession en defer pour
// supprimer la session apres usage.
func (s *veridianService) ctxAsUser(ctx context.Context, userID string) (context.Context, string, error) {
	session := &domain.Session{
		ID:        uuid.New().String(),
		UserID:    userID,
		ExpiresAt: time.Now().UTC().Add(rootSessionTTL),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.userRepo.CreateSession(ctx, session); err != nil {
		return ctx, "", fmt.Errorf("veridian: create user session for %s: %w", userID, err)
	}

	userCtx := context.WithValue(ctx, domain.UserIDKey, userID)
	userCtx = context.WithValue(userCtx, domain.UserTypeKey, string(domain.UserTypeUser))
	userCtx = context.WithValue(userCtx, domain.SessionIDKey, session.ID)
	// === Veridian patch ===
	// Mark this as a system-internal call so guards in WorkspaceService
	// (block interactive workspace creation in Veridian-managed mode) skip
	// us. Only legitimate HTTP callers — which never have this key — are
	// refused.
	userCtx = context.WithValue(userCtx, domain.SystemCallKey, true)
	return userCtx, session.ID, nil
}

func (s *veridianService) cleanupSession(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}
	if err := s.userRepo.DeleteSession(ctx, sessionID); err != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"session_id": sessionID,
			"error":      err.Error(),
		}).Warn("veridian: failed to delete session")
	}
}

// Provision cree (ou reutilise) le workspace, l'owner, l'API key et le plan
// veridian pour un nouveau tenant. Idempotent : si le tenant existe deja
// (workspace + plan presents), retourne {created: false} avec les valeurs
// existantes (sans regenerer l'API key — le caller doit garder la
// premiere). Si le workspace existe mais pas le plan, on cree le plan
// uniquement (cas de migration manuelle).
func (s *veridianService) Provision(ctx context.Context, input domain.ProvisionInput) (*domain.ProvisionResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.OwnerEmail == "" {
		return nil, errors.New("owner_email required")
	}

	plan := strings.TrimSpace(input.Plan)
	if plan == "" {
		plan = s.defaultPlan
	}

	// === Veridian patch === Refus delete → re-provision tant que pas purge.
	// Si veridian_plan.deleted_at != NULL, on rejette avec ErrTenantSoftDeleted
	// (mappe en 409 Conflict cote handler). Decision produit : eviter qu'un
	// re-signup accidentel n'herite des donnees du precedent owner.
	existingPlan, planErr := s.planRepo.Get(ctx, input.TenantID)
	if planErr == nil && existingPlan != nil && existingPlan.DeletedAt != nil {
		purgeAt := existingPlan.DeletedAt.Add(veridianPurgeDelay)
		return nil, fmt.Errorf("%w (deleted_at=%s, purge_at=%s)",
			ErrTenantSoftDeleted,
			existingPlan.DeletedAt.Format(time.RFC3339),
			purgeAt.Format(time.RFC3339))
	}

	// Idempotence : si workspace + plan existent deja, on retourne les
	// valeurs existantes sans re-provisionner. L'API key et le magic link
	// ne sont PAS regeneres ici (le Hub doit avoir conserve la premiere).
	// === Veridian patch === GetWorkspace upstream check les permissions du
	// caller — il faut donc utiliser un ctx root pour le lookup d'idempotence,
	// sinon on retourne nil et on retombe sur create workspace -> already exists.
	idempCtx, idempSessionID, _, idempErr := s.ctxAsRoot(ctx)
	var existingWorkspace *domain.Workspace
	if idempErr == nil {
		existingWorkspace, _ = s.workspaceService.GetWorkspace(idempCtx, input.TenantID)
		s.cleanupSession(ctx, idempSessionID)
	}
	if existingWorkspace != nil && planErr == nil && existingPlan != nil {
		// === Veridian patch === Cas idempotent (contrat §5.1) :
		//   - owner reel != input.OwnerEmail → ErrOwnerMismatch (mappe 409)
		//     pour eviter qu'un appel re-provision genere un magic_link valide
		//     vers un workspace que le caller ne controle pas.
		//   - owner reel == input.OwnerEmail → response idempotente avec
		//     magic_link + auto_login_url FRAIS (TTL 15 min). APIKey vide :
		//     le Hub a conserve la premiere lors de la creation.
		members, listErr := s.workspaceRepo.GetWorkspaceUsersWithEmail(ctx, input.TenantID)
		if listErr != nil {
			return nil, fmt.Errorf("idempotent: list workspace members: %w", listErr)
		}
		var ownerEmailReal, ownerUserID string
		for _, m := range members {
			if m == nil || m.Role != "owner" {
				continue
			}
			// Filtrer api_key (Type via lookupUserType : api_key user a aussi
			// role=owner sur certains workspaces pre-feature owner-natif).
			userType, _ := s.lookupUserType(ctx, m.UserID)
			if userType != domain.UserTypeUser {
				continue
			}
			ownerEmailReal = m.Email
			ownerUserID = m.UserID
			break
		}
		// Si aucun owner humain identifie (cas legacy : 9 tenants prod repares
		// via attach-owner ou workspaces pre-Provision), on ne peut pas
		// trancher → on tombe sur le chemin "creation complete" qui
		// reattachera proprement le user input via Add+Transfer.
		if ownerEmailReal != "" {
			if !strings.EqualFold(strings.TrimSpace(ownerEmailReal), strings.TrimSpace(input.OwnerEmail)) {
				return nil, ErrOwnerMismatch
			}
			// Owner match : regenerer magic_link + auto_login_url frais.
			magicLink, _, mlErr := s.buildMagicLink(ctx, input.TenantID, input.OwnerEmail)
			if mlErr != nil && s.logger != nil {
				s.logger.WithFields(map[string]interface{}{
					"tenant_id": input.TenantID,
					"email":     input.OwnerEmail,
					"error":     mlErr.Error(),
				}).Warn("veridian: failed to build magic link in idempotent return")
			}
			autoLoginURL, _, autoErr := domain.BuildAutoLoginURL(s.apiEndpoint, s.hubSecret, input.TenantID, input.OwnerEmail)
			if autoErr != nil && s.logger != nil {
				s.logger.WithFields(map[string]interface{}{
					"tenant_id": input.TenantID,
					"email":     input.OwnerEmail,
					"error":     autoErr.Error(),
				}).Warn("veridian: failed to build auto-login url in idempotent return")
			}
			return &domain.ProvisionResponse{
				WorkspaceID:  input.TenantID,
				OwnerUserID:  ownerUserID,
				APIKey:       "",
				APIKeyEmail:  "",
				MagicLink:    magicLink,
				AutoLoginURL: autoLoginURL,
				Plan:         existingPlan.Plan,
				Created:      false,
			}, nil
		}
		// Fallthrough : pas d'owner humain identifie → on continue sur le
		// chemin nominal (create workspace est skip si existant, et le user
		// input sera attache via AddUserToWorkspace + TransferOwnership).
	}

	// 1. S'assurer que l'owner user existe.
	owner, err := s.userService.GetUserByEmail(ctx, input.OwnerEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("get owner by email: %w", err)
		}
		owner = nil
	}
	if owner == nil {
		owner = &domain.User{
			ID:        uuid.New().String(),
			Email:     input.OwnerEmail,
			Type:      domain.UserTypeUser,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.userRepo.CreateUser(ctx, owner); err != nil {
			return nil, fmt.Errorf("create owner user: %w", err)
		}
	}

	// 2. Bypass d'auth : injection ctx root.
	rootCtx, rootSessionID, rootUserID, err := s.ctxAsRoot(ctx)
	if err != nil {
		return nil, err
	}
	defer s.cleanupSession(ctx, rootSessionID)

	// 3. Creer le workspace si absent.
	workspaceName := input.WorkspaceName
	if workspaceName == "" {
		workspaceName = input.TenantID
	}
	if existingWorkspace == nil {
		_, err := s.workspaceService.CreateWorkspace(
			rootCtx,
			input.TenantID,
			workspaceName,
			"",
			"",
			"",
			"UTC",
			domain.FileManagerSettings{},
			"en",
			[]string{"en"},
		)
		if err != nil {
			return nil, fmt.Errorf("create workspace: %w", err)
		}
	}

	// === Veridian patch ===
	// 4. Ajouter le tenant user comme member du workspace. C'est une etape
	// intermediaire pour TransferOwnership (step 6), qui exige que newOwner
	// soit deja membre. role=owner direct etait l'ancien comportement, mais
	// laissait root co-owner en parallele.
	addErr := s.workspaceService.AddUserToWorkspace(
		rootCtx,
		input.TenantID,
		owner.ID,
		"member",
		domain.FullPermissions,
	)
	if addErr != nil && !strings.Contains(addErr.Error(), "already") {
		return nil, fmt.Errorf("add tenant user as member: %w", addErr)
	}

	// 5. Creer une API key tenant AVANT de retirer root (step 6). CreateAPIKey
	// upstream exige que le caller soit member/owner du workspace — root l'est
	// encore a ce stade. Prefix unique par tenant pour eviter les collisions
	// "user already exists" (Notifuse derive un email du prefix).
	apiKeyPrefix := "veridian-api-" + input.TenantID
	apiKeyToken, apiKeyEmail, err := s.workspaceService.CreateAPIKey(rootCtx, input.TenantID, apiKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}

	// 5b. === Veridian patch === Verrouiller le user api_key fraichement cree
	// contre la suppression UI (Team Settings). Sans ce flag, le client peut
	// supprimer ce user via Team -> Remove member -> magic-link Hub casse
	// silencieux. Idempotent (re-provision OK). Non-fatal : si l'UPDATE foire
	// (race, DB momentanement KO), on log mais on continue le provisioning —
	// la detection cote Hub via /health api_key_valid prendra le relais.
	if markErr := s.userRepo.MarkVeridianManaged(ctx, apiKeyEmail); markErr != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id": input.TenantID,
			"api_email": apiKeyEmail,
			"error":     markErr.Error(),
		}).Warn("veridian: failed to mark api_key user as veridian_managed (non-fatal)")
	}

	// 6. Owner natif : transferer l'ownership a `owner.ID` puis retirer root
	// du workspace, pour que le tenant user soit owner unique. Voir
	// transferOwnershipToTenant pour les details. Best-effort : si la
	// sequence echoue (re-provision idempotent, etc.), on log mais on ne
	// bloque pas le provisioning car l'API key est deja cree.
	s.transferOwnershipToTenant(ctx, rootCtx, input.TenantID, owner.ID, rootUserID)

	// 7. Inserer / mettre a jour la ligne veridian_plan.
	// Quota : si input.Quotas.MonthlyEmails est fourni par le Hub, c'est la
	// source de verite (CONTRAT-HUB sec. 5.17). Sinon fallback hardcoded
	// domain.QuotaForPlan(plan) pour back-compat des appels Hub legacy.
	monthlyEmailQuota := domain.QuotaForPlan(plan)
	if input.Quotas != nil && input.Quotas.MonthlyEmails != nil {
		monthlyEmailQuota = *input.Quotas.MonthlyEmails
	}
	now := time.Now().UTC()
	planRow := &domain.VeridianPlan{
		WorkspaceID:         input.TenantID,
		Plan:                plan,
		PlanSource:          input.PlanSource, // peut etre "" — repo COALESCE default 'stripe'
		Status:              domain.PlanStatusActive,
		MonthlyEmailQuota:   monthlyEmailQuota,
		EmailsSentThisMonth: 0,
		LastResetAt:         now,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := s.planRepo.Upsert(ctx, planRow); err != nil {
		return nil, fmt.Errorf("upsert veridian_plan: %w", err)
	}

	// 8. Generer un magic link signin (fallback : saisie code) + auto-login
	// URL self-contained (preferred : bouton "Open Notifuse" Hub sans saisie).
	magicLink, _, err := s.buildMagicLink(ctx, input.TenantID, input.OwnerEmail)
	if err != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id": input.TenantID,
			"email":     input.OwnerEmail,
			"error":     err.Error(),
		}).Warn("veridian: failed to build magic link during provision")
	}

	autoLoginURL, _, autoErr := domain.BuildAutoLoginURL(s.apiEndpoint, s.hubSecret, input.TenantID, input.OwnerEmail)
	if autoErr != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id": input.TenantID,
			"email":     input.OwnerEmail,
			"error":     autoErr.Error(),
		}).Warn("veridian: failed to build auto-login url (HUB_API_SECRET missing?)")
	}

	// 9. Notifier le Hub via webhook (best-effort).
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantProvisioned, input.TenantID, map[string]interface{}{
			"plan":          plan,
			"owner_user_id": owner.ID,
			"owner_email":   input.OwnerEmail,
		})
	}

	// 10. Marquer le sync Hub réussi (best-effort, ne bloque pas le provisioning).
	s.touchHubSync(ctx, input.TenantID)

	// 11. === Veridian patch 2026-05-23 === Seed du template transactionnel
	// invitation-prospection (cf. veridian_seed_templates.go). Best-effort,
	// idempotent, jamais bloquant. Skip silencieux si services non câblés.
	s.seedInvitationProspectionTemplate(ctx, input.TenantID)

	return &domain.ProvisionResponse{
		WorkspaceID:  input.TenantID,
		OwnerUserID:  owner.ID,
		APIKey:       apiKeyToken,
		APIKeyEmail:  apiKeyEmail,
		MagicLink:    magicLink,
		AutoLoginURL: autoLoginURL,
		Plan:         plan,
		Created:      true,
	}, nil
}

// transferOwnershipToTenant rend le tenant user owner unique du workspace
// en composant 2 fonctions natives upstream :
//
//	a) TransferOwnership(workspaceID, tenantUserID, rootUserID) — promote le
//	   tenant user en owner et demote root en member, atomiquement.
//	b) RemoveUserFromWorkspace(rootUserID) depuis ctx tenant user, qui est
//	   maintenant owner et donc autorise a retirer root.
//
// Resultat final dans user_workspaces : 1 seul row pour ce workspace
// (tenant user, role=owner). Root n'est plus co-owner — referme le trou
// de securite documente dans todo/apps/notifuse/TODO.md.
//
// Best-effort : si TransferOwnership echoue (re-provision idempotent ou le
// tenant user est deja owner), on log mais on ne propage pas l'erreur, car
// le provisioning est deja state-changing (workspace cree, API key emise).
// Si Remove echoue, idem : root reste member non-owner, c'est l'etat
// upstream classique sans la garantie owner-natif renforcee.
//
// Pre-conditions : le tenant user (owner.ID) doit etre member du workspace
// (cf. step 4 de Provision : AddUserToWorkspace role=member).
func (s *veridianService) transferOwnershipToTenant(ctx, rootCtx context.Context, workspaceID, tenantUserID, rootUserID string) {
	if tenantUserID == rootUserID {
		// Cas degenere : provisionner Notifuse pour le user root lui-meme.
		// Pas de transfer a faire.
		return
	}

	if err := s.workspaceService.TransferOwnership(rootCtx, workspaceID, tenantUserID, rootUserID); err != nil {
		// Tolerant : si tenant user est deja owner (re-provision idempotent),
		// TransferOwnership echoue car newOwner doit etre member. C'est OK.
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"workspace_id": workspaceID,
				"new_owner":    tenantUserID,
				"error":        err.Error(),
			}).Warn("veridian: TransferOwnership skipped (likely re-provision idempotent)")
		}
		return
	}

	// Transfer reussi : tenant user est owner, root est member.
	// On retire root via un ctx tenant user authentifie (root n'est plus
	// owner et ne peut plus appeler RemoveUserFromWorkspace).
	tenantCtx, tenantSessionID, sessErr := s.ctxAsUser(ctx, tenantUserID)
	if sessErr != nil {
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"workspace_id": workspaceID,
				"error":        sessErr.Error(),
			}).Warn("veridian: failed to create tenant session for root removal")
		}
		return
	}
	defer s.cleanupSession(ctx, tenantSessionID)

	if err := s.workspaceService.RemoveUserFromWorkspace(tenantCtx, workspaceID, rootUserID); err != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"root_id":      rootUserID,
			"error":        err.Error(),
		}).Warn("veridian: failed to remove root from workspace (non-fatal)")
	}
}

// UpdatePlan change le plan d'un tenant existant. Recalcule le quota
// mensuel a partir du PlanQuotas. Renvoie un UpdatePlanResponse avec audit
// trail previous_plan (CONTRAT-HUB sec. 5.2).
//
// Immunite plan offert (CONTRAT-HUB sec. 3.3) : si le plan_source existant
// est immune (lifetime_*, internal, manual) et que l'appelant essaie de le
// passer en plan_source=stripe (downgrade Stripe webhook), on refuse avec
// ErrPlanImmune. Le handler mappe vers 409 plan_locked.
//
// PlanSource vide dans input = "garder l'existant" (cf. repo COALESCE) — ce
// n'est PAS interprete comme une demande stripe.
func (s *veridianService) UpdatePlan(ctx context.Context, input domain.UpdatePlanInput) (*domain.UpdatePlanResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.Plan == "" {
		return nil, errors.New("plan required")
	}
	// === CONTRAT-BILLING v2 §3.3 — enum plan_source (v2 + legacy v1) ===
	// IsValidPlanSourceV2 accepte les 4 valeurs v2 + les valeurs legacy.
	// Le handler valide aussi en amont — ce check est une défense en
	// profondeur pour les callers qui invoqueraient le service directement.
	if !domain.IsValidPlanSourceV2(input.PlanSource) {
		return nil, fmt.Errorf("invalid plan_source %q", input.PlanSource)
	}

	// Read current state for previous_plan + immunity check.
	existing, err := s.planRepo.Get(ctx, input.TenantID)
	if err != nil {
		return nil, err // sql.ErrNoRows propage tel quel (handler mappe → 404)
	}

	// === CONTRAT-BILLING v2 §3.4.4 — immunité plan offert ===
	// Garde-fou : un plan_source immune (grant_manual + valeurs legacy
	// équivalentes : manual, lifetime_*, internal) ne peut PAS être écrasé
	// par une source auto :
	//   - stripe          (subscription Stripe payante)
	//   - stripe_trial    (activation trial state machine)
	//   - downgrade_auto  (sub Stripe expirée / trial expiré)
	//
	// Seul un `update-plan` plan_source=grant_manual (ou valeur legacy
	// équivalente) peut écraser un plan offert — un admin Hub a toujours
	// le dernier mot.
	if domain.IsImmuneV2(existing.PlanSource) && domain.IsAutoDowngradeSource(input.PlanSource) {
		return nil, ErrPlanImmune
	}

	// Quota override §5.17 : si le Hub envoie input.Quotas.MonthlyEmails,
	// utiliser cette valeur. Sinon fallback hardcoded QuotaForPlan(plan).
	quota := domain.QuotaForPlan(input.Plan)
	if input.Quotas != nil && input.Quotas.MonthlyEmails != nil {
		quota = *input.Quotas.MonthlyEmails
	}
	if err := s.planRepo.UpdatePlan(ctx, input.TenantID, input.Plan, quota, input.PlanSource); err != nil {
		return nil, err
	}

	// Compute effective plan_source apres ecriture : si input vide, on a
	// preserve l'existant (cf. repo COALESCE), sinon c'est input.PlanSource.
	effectiveSource := existing.PlanSource
	if input.PlanSource != "" {
		effectiveSource = input.PlanSource
	}

	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantPlanChanged, input.TenantID, map[string]interface{}{
			"plan":          input.Plan,
			"previous_plan": existing.Plan,
			"plan_source":   string(effectiveSource),
			"quota":         quota,
		})
	}

	// Marquer le sync Hub réussi (best-effort).
	s.touchHubSync(ctx, input.TenantID)

	return &domain.UpdatePlanResponse{
		TenantID:     input.TenantID,
		Plan:         input.Plan,
		PreviousPlan: existing.Plan,
		PlanSource:   effectiveSource,
		AppliedAt:    time.Now().UTC(),
	}, nil
}

// Suspend bloque les envois pour un tenant (paywall middleware retourne 402).
func (s *veridianService) Suspend(ctx context.Context, input domain.SuspendInput) error {
	if input.TenantID == "" {
		return errors.New("tenant_id required")
	}
	if err := s.planRepo.Suspend(ctx, input.TenantID, input.Reason); err != nil {
		return err
	}
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantSuspended, input.TenantID, map[string]interface{}{
			"reason": input.Reason,
		})
	}
	// Marquer le sync Hub réussi (best-effort).
	s.touchHubSync(ctx, input.TenantID)
	return nil
}

// Resume reactive un tenant suspended.
func (s *veridianService) Resume(ctx context.Context, input domain.ResumeInput) error {
	if input.TenantID == "" {
		return errors.New("tenant_id required")
	}
	if err := s.planRepo.Resume(ctx, input.TenantID); err != nil {
		return err
	}
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantResumed, input.TenantID, nil)
	}
	// Marquer le sync Hub réussi (best-effort).
	s.touchHubSync(ctx, input.TenantID)
	return nil
}

// SoftDelete marque le tenant deleted (CONTRAT-HUB sec. 5.7-5.8). Set
// deleted_at + purge_eligible_at = NOW + 30j + lifecycle_reason. La purge
// effective est faite par un trigger cote Hub apres ce delai.
//
// Emet 2 webhooks pour back-compat : tenant.soft_deleted (nouveau, contractuel)
// ET tenant.deleted (legacy, conserve tant que d'autres consommateurs Hub
// n'ont pas migre).
//
// reason "" toleree pour back-compat handler DELETE legacy qui n'envoie pas
// de body. Pour le nouvel endpoint POST /soft-delete, reason est typiquement
// rempli (audit GDPR).
func (s *veridianService) SoftDelete(ctx context.Context, input domain.SoftDeleteInput) (*domain.SoftDeleteResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if err := s.planRepo.SoftDelete(ctx, input.TenantID, input.Reason); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	purgeEligibleAt := now.Add(veridianPurgeDelay)

	if s.emitter != nil {
		// === Lot I (V40) === Payloads en RFC3339 strings pour conformité
		// contrat Hub v1.4 (cf. veridian-hub/lib/notifuse/types.ts —
		// TenantDeletedEventData.deleted_at: string). Go sérialise time.Time
		// en JSON RFC3339 par défaut mais on explicite ici pour stabilité du
		// contrat indépendamment du marshaller (tests Hub assert sur string).
		payload := map[string]interface{}{
			"deleted_at":        now.Format(time.RFC3339),
			"purge_eligible_at": purgeEligibleAt.Format(time.RFC3339),
		}
		if input.Reason != "" {
			payload["reason"] = input.Reason
		}
		s.emitter.Emit(ctx, domain.EventTenantSoftDeleted, input.TenantID, payload)
		// back-compat : continuer a emettre tenant.deleted pour les consommateurs
		// Hub legacy qui ne connaissent pas encore tenant.soft_deleted. Payload
		// minimal (deleted_at uniquement) pour parité avec TenantDeletedEventData.
		s.emitter.Emit(ctx, domain.EventTenantDeleted, input.TenantID, map[string]interface{}{
			"deleted_at": now.Format(time.RFC3339),
		})
	}

	// Marquer le sync Hub réussi (best-effort).
	s.touchHubSync(ctx, input.TenantID)

	return &domain.SoftDeleteResponse{
		TenantID:        input.TenantID,
		Status:          string(domain.PlanStatusDeleted),
		DeletedAt:       now,
		PurgeEligibleAt: purgeEligibleAt,
	}, nil
}

// === Veridian patch ===
// ErrTenantNotSoftDeleted est retourne par Restore quand le tenant n'est pas
// dans l'etat soft_deleted (rien a restaurer). Le handler mappe → 409.
var ErrTenantNotSoftDeleted = errors.New("tenant is not soft-deleted, nothing to restore")

// === Veridian patch — attach-member (2026-05-21) ===
// ErrTenantSuspended est retourne par AttachMember quand le tenant est suspendu.
// Le handler mappe vers 423 Locked : pas d'ajout de membre sur un compte bloque.
var ErrTenantSuspended = errors.New("tenant is suspended — member attachment refused")

// ErrUserRoleConflict est retourne par AttachMember en cas de race condition
// (deux appels concurrents tentent d'assigner des roles incompatibles apres
// le lookup idempotence). Race tres rare en pratique — le cas normal (role
// different detecte proprement) provoque un UPDATE, pas un 409. Le handler
// mappe → 409.
var ErrUserRoleConflict = errors.New("user role conflict (race condition during attach)")

// Restore annule un soft-delete (CONTRAT-HUB sec. 5.7-5.8). Le tenant repasse
// en active. Refuse si le tenant n'est pas soft-deleted (ErrTenantNotSoftDeleted).
//
// Emet tenant.restored.
func (s *veridianService) Restore(ctx context.Context, input domain.RestoreInput) (*domain.RestoreResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}

	// Garde : refuser si pas soft-deleted (sinon on ecraserait restored_at
	// d'une restoration precedente sur un tenant actif — inattendu cote Hub).
	existing, err := s.planRepo.Get(ctx, input.TenantID)
	if err != nil {
		return nil, err
	}
	if existing.DeletedAt == nil {
		return nil, ErrTenantNotSoftDeleted
	}

	if err := s.planRepo.Restore(ctx, input.TenantID, input.Reason); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if s.emitter != nil {
		// === Lot I (V40) === Payload RFC3339 string (cf. SoftDelete).
		payload := map[string]interface{}{"restored_at": now.Format(time.RFC3339)}
		if input.Reason != "" {
			payload["reason"] = input.Reason
		}
		s.emitter.Emit(ctx, domain.EventTenantRestored, input.TenantID, payload)
	}

	// Marquer le sync Hub réussi (best-effort).
	s.touchHubSync(ctx, input.TenantID)

	return &domain.RestoreResponse{
		TenantID:   input.TenantID,
		Status:     string(domain.PlanStatusActive),
		RestoredAt: now,
	}, nil
}

// Purge supprime DEFINITIVEMENT un tenant (hard delete). Refuse si
// purge_eligible_at > NOW (ErrPurgeNotEligible). Exige confirm == "PURGE"
// pour proteger contre les appels accidentels (safeguard contrat sec. 5.8).
//
// Sequence : (1) hard delete workspace (DB postgres dediee + user owner via
// WipeTestTenants helper), (2) hard delete veridian_plan row, (3) emit
// tenant.purged.
//
// reason est obligatoire (audit GDPR : on doit savoir pourquoi un tenant a
// ete supprime definitivement).
func (s *veridianService) Purge(ctx context.Context, input domain.PurgeInput) (*domain.PurgeResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.Reason == "" {
		return nil, errors.New("reason required for purge (GDPR audit)")
	}
	if input.Confirm != "PURGE" {
		return nil, errors.New("confirm must equal \"PURGE\" to proceed (safeguard)")
	}

	// Garde : refuser si purge_eligible_at > NOW.
	existing, err := s.planRepo.Get(ctx, input.TenantID)
	if err != nil {
		return nil, err
	}
	if existing.PurgeEligibleAt == nil {
		// Tenant pas en soft-delete → pas eligible. Distinct du sentinel
		// ErrPurgeNotEligible "pas encore eligible" : ici on a un "pas du
		// tout en process de purge". On reuse le meme sentinel + un
		// message clair, le handler mappe vers 409 dans les deux cas.
		return nil, fmt.Errorf("%w: tenant must be soft-deleted first", ErrPurgeNotEligible)
	}
	if time.Now().UTC().Before(*existing.PurgeEligibleAt) {
		return nil, fmt.Errorf("%w: not before %s", ErrPurgeNotEligible, existing.PurgeEligibleAt.Format(time.RFC3339))
	}

	// Hard delete via le helper WipeTestTenants existant qui sait deja faire
	// (1) DROP DB workspace, (2) DELETE veridian_plan row, (3) DELETE user
	// owner si veridian-managed. C'est exactement la sequence Purge contrat.
	if _, err := s.WipeTestTenants(ctx, domain.WipeTestTenantsInput{
		TenantIDs: []string{input.TenantID},
	}); err != nil {
		return nil, fmt.Errorf("hard delete tenant: %w", err)
	}

	now := time.Now().UTC()
	if s.emitter != nil {
		// === Lot I (V40) === Payload RFC3339 string (cf. SoftDelete).
		s.emitter.Emit(ctx, domain.EventTenantPurged, input.TenantID, map[string]interface{}{
			"purged_at": now.Format(time.RFC3339),
			"reason":    input.Reason,
		})
	}

	return &domain.PurgeResponse{
		TenantID: input.TenantID,
		Status:   "purged",
		PurgedAt: now,
	}, nil
}

// veridianTouchDebounce : duree minimale entre 2 Touch effectifs pour eviter
// d'ecraser inutilement last_touched_at et generer du trafic webhook.
const veridianTouchDebounce = 24 * time.Hour

// Touch met a jour le heartbeat anti-soft-delete (last_touched_at). Debounce
// 24h : si le tenant a deja ete touche dans les dernieres 24h, no-op
// silencieux (Debounced=true dans la response). CONTRAT-HUB sec. 5.7-5.8.
//
// Emet tenant.touched UNIQUEMENT si pas debounced.
func (s *veridianService) Touch(ctx context.Context, tenantID string) (*domain.TouchResponse, error) {
	if tenantID == "" {
		return nil, errors.New("tenant_id required")
	}

	existing, err := s.planRepo.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if existing.LastTouchedAt != nil && now.Sub(*existing.LastTouchedAt) < veridianTouchDebounce {
		return &domain.TouchResponse{
			TenantID:  tenantID,
			TouchedAt: *existing.LastTouchedAt,
			Debounced: true,
		}, nil
	}

	if err := s.planRepo.Touch(ctx, tenantID); err != nil {
		return nil, err
	}

	if s.emitter != nil {
		// === Lot I (V40) === Payload RFC3339 string (cf. SoftDelete).
		// soft_delete_eligible_at = last_touched_at + heartbeat threshold côté Hub
		// (le Hub a sa propre constante GraceDeleteEligibleAfter). On envoie juste
		// touched_at, le Hub déduit ; pas de duplication d'info contractuelle.
		s.emitter.Emit(ctx, domain.EventTenantTouched, tenantID, map[string]interface{}{
			"touched_at": now.Format(time.RFC3339),
		})
	}

	// Marquer le sync Hub réussi (best-effort).
	s.touchHubSync(ctx, tenantID)

	return &domain.TouchResponse{
		TenantID:  tenantID,
		TouchedAt: now,
		Debounced: false,
	}, nil
}

// UsageSummary agrege l'utilisation effective d'un tenant (CONTRAT-HUB
// sec. 5.8). MVP : retourne les compteurs du plan (messages_sent_this_month
// utilise comme proxy de messages_sent_30d tant que IncrementEmailsSent
// n'est pas wired — cf. todo/2026-05-19-webhooks-manquants.md).
//
// contacts_count = 0 pour l'instant (necessiterait un appel cross-DB sur
// la base workspace dediee — pas en MVP).
func (s *veridianService) UsageSummary(ctx context.Context, tenantID string) (*domain.UsageSummaryResponse, error) {
	if tenantID == "" {
		return nil, errors.New("tenant_id required")
	}

	plan, err := s.planRepo.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	resp := &domain.UsageSummaryResponse{
		TenantID:        tenantID,
		MessagesSent30d: plan.EmailsSentThisMonth, // proxy MVP, voir doc fonction
		Plan:            plan.Plan,
		Status:          plan.Status,
		ContactsCount:   0, // MVP : non implementé (cross-DB query couteux)
		GeneratedAt:     time.Now().UTC(),
	}
	if plan.LastTouchedAt != nil {
		resp.LastActivityAt = plan.LastTouchedAt
	}
	return resp, nil
}

// === Veridian patch ===
// defaultSafetyClientPrefixes : prefixes de tenants qui ne sont JAMAIS effaces
// par WipeTestTenants, meme avec un wildcard. Defense en profondeur si le
// HUB_API_SECRET fuit ou si un test a un prefix trop large.
// Ces prefixes correspondent aux clients reels + tenants internes Veridian.
// Cf. todo/2026-05-20-e2e-cleanup-discipline-canary-safety.md
var defaultSafetyClientPrefixes = []string{
	// Clients réels (prod + staging)
	"apicalinfo",
	"robinix",
	"lyon",
	"loyer",
	"veridiansite",
	"antjacquet",
	"darysisowath",
	"guilhemjacquet",
	"ismailelmouaddab",
	// === Veridian patch 2026-05-20 === Canary witness tenants (long-lived)
	// canaryfree, canarypro, canaryenterprise — baseline tenants utilisés
	// avant chaque promote prod pour détecter régressions schéma/migration.
	"canary",
	// Workspaces personnels Robert (immune accidental wipe)
	"robertbrunon",
	"robertstagingtest",
	"brunon5robert",
	"rbrunon",
	"truy",
}

// WipeTestTenants supprime DEFINITIVEMENT (hard delete) les tenants matchant
// un prefix ou une liste explicite. Utilise par la CI / admin platform pour
// nettoyer les tenants e2e accumules entre les runs.
//
// Pour chaque tenant a supprimer (cf wipeOneTenant) :
//   1. Resoudre l'owner du workspace via workspaceRepo direct (post-feature
//      owner-natif, root n'est plus member et ne peut pas appeler
//      DeleteWorkspace).
//   2. WorkspaceService.DeleteWorkspace depuis ctx owner → DROP DATABASE.
//   3. PlanRepo.HardDelete → DELETE row veridian_plan.
//   4. Emit event tenant.deleted (best-effort).
//
// Les tenants matchant un safety prefix sont SKIP (jamais effaces).
// ListTenants projette les tenants en 2 buckets (managed/orphans) pour
// inspection admin avant action. Read-only. Si IncludeOrphans=true, scanne
// aussi workspaceRepo.List (source de verite globale).
//
// Pas de safety prefix ici (read-only, le caller decide quoi en faire). Le
// caller doit appeler WipeTestTenants ensuite s'il veut supprimer.
//
// Limit=0 => pas de cap (mais workspaces is bounded en pratique <500).
func (s *veridianService) ListTenants(ctx context.Context, input domain.ListTenantsInput) (*domain.ListTenantsResponse, error) {
	if input.Prefix != "" {
		if strings.ContainsAny(input.Prefix, "%_") {
			return nil, fmt.Errorf("prefix cannot contain SQL wildcards (%% or _)")
		}
		if len(input.Prefix) < 3 {
			return nil, fmt.Errorf("prefix must be at least 3 chars (got %q)", input.Prefix)
		}
	}

	resp := &domain.ListTenantsResponse{
		Managed: []domain.TenantSummary{},
	}

	// 1. Managed : tenants avec veridian_plan (et prefix matching si fourni).
	planIDs, err := s.collectPlanIDs(ctx, input.Prefix)
	if err != nil {
		return nil, fmt.Errorf("list plan ids: %w", err)
	}
	managedSet := make(map[string]bool, len(planIDs))
	for _, tid := range planIDs {
		if input.Limit > 0 && len(resp.Managed) >= input.Limit {
			break
		}
		summary := domain.TenantSummary{TenantID: tid, HasPlan: true}
		if plan, planErr := s.planRepo.Get(ctx, tid); planErr == nil && plan != nil {
			summary.Plan = plan.Plan
			summary.Status = string(plan.Status)
			if plan.DeletedAt != nil && !plan.DeletedAt.IsZero() {
				summary.DeletedAt = plan.DeletedAt.UTC().Format(time.RFC3339)
			}
		}
		resp.Managed = append(resp.Managed, summary)
		managedSet[tid] = true
	}

	// 2. Orphans : workspaces sans veridian_plan (uniquement si IncludeOrphans).
	if input.IncludeOrphans && s.workspaceRepo != nil {
		workspaces, listErr := s.workspaceRepo.List(ctx)
		if listErr != nil {
			return nil, fmt.Errorf("list workspaces: %w", listErr)
		}
		resp.Orphans = []domain.TenantSummary{}
		for _, w := range workspaces {
			if w == nil || managedSet[w.ID] {
				continue
			}
			if input.Prefix != "" && !strings.HasPrefix(w.ID, input.Prefix) {
				continue
			}
			if input.Limit > 0 && len(resp.Orphans) >= input.Limit {
				break
			}
			resp.Orphans = append(resp.Orphans, domain.TenantSummary{
				TenantID: w.ID,
				HasPlan:  false,
			})
		}
	}

	resp.Total = len(resp.Managed) + len(resp.Orphans)
	return resp, nil
}

// collectPlanIDs retourne les workspace_id de veridian_plan, filtres par
// prefix si non-vide. Si prefix vide, retourne tout (cap a 500 entrees
// pour eviter une surcharge memoire sur une table qui grossit).
func (s *veridianService) collectPlanIDs(ctx context.Context, prefix string) ([]string, error) {
	if prefix != "" {
		return s.planRepo.ListByPrefix(ctx, prefix)
	}
	// Pas de prefix : tout lister via prefix vide = scan complet.
	// planRepo.ListByPrefix("") retourne nil par convention (cf. repo).
	// On utilise un prefix wildcard impossible par construction ? Non, plus
	// simple : on retourne nil et le caller assume que sans prefix on ne
	// liste pas les managed. ListTenants sans prefix + IncludeOrphans=true =
	// audit complet (workspaces only).
	return nil, nil
}

func (s *veridianService) WipeTestTenants(ctx context.Context, input domain.WipeTestTenantsInput) (*domain.WipeTestTenantsResponse, error) {
	if input.Prefix == "" && len(input.TenantIDs) == 0 {
		return nil, errors.New("prefix or tenant_ids required")
	}

	// Resoudre la liste de candidats
	candidates := input.TenantIDs
	if input.Prefix != "" {
		// Bloquer prefix '%' ou '_' raw qui matcherait tout
		if strings.ContainsAny(input.Prefix, "%_") {
			return nil, fmt.Errorf("prefix cannot contain SQL wildcards (%% or _)")
		}
		// Bloquer prefix vide ou < 3 chars (defense en profondeur)
		if len(input.Prefix) < 3 {
			return nil, fmt.Errorf("prefix must be at least 3 chars (got %q)", input.Prefix)
		}
		ids, err := s.planRepo.ListByPrefix(ctx, input.Prefix)
		if err != nil {
			return nil, fmt.Errorf("list by prefix: %w", err)
		}
		candidates = append(candidates, ids...)

		// === Veridian patch === Inclure les workspaces orphelins (presents
		// dans workspaces mais absents de veridian_plan). Indispensable pour
		// nettoyer les tenants crees par des tests qui n'ont pas passe par
		// /api/tenants/provision (donc invisibles a planRepo.ListByPrefix).
		// Source de verite : workspaceRepo.List (table workspaces upstream).
		if input.IncludeOrphans && s.workspaceRepo != nil {
			workspaces, listErr := s.workspaceRepo.List(ctx)
			if listErr != nil {
				return nil, fmt.Errorf("list workspaces: %w", listErr)
			}
			seen := make(map[string]bool, len(candidates))
			for _, c := range candidates {
				seen[c] = true
			}
			for _, w := range workspaces {
				if w == nil {
					continue
				}
				if strings.HasPrefix(w.ID, input.Prefix) && !seen[w.ID] {
					candidates = append(candidates, w.ID)
					seen[w.ID] = true
				}
			}
		}
	}

	// Determine safety prefixes
	safetyPrefixes := input.SafetyClientPrefixes
	if len(safetyPrefixes) == 0 {
		safetyPrefixes = defaultSafetyClientPrefixes
	}

	// Open ctx root pour appeler WorkspaceService.DeleteWorkspace
	rootCtx, sessionID, _, err := s.ctxAsRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("ctxAsRoot: %w", err)
	}
	defer s.cleanupSession(ctx, sessionID)

	resp := &domain.WipeTestTenantsResponse{
		Wiped:   []string{},
		Skipped: []string{},
		Errors:  map[string]string{},
	}

	for _, tid := range candidates {
		// Safety check
		isSafe := false
		for _, sp := range safetyPrefixes {
			if strings.HasPrefix(tid, sp) {
				isSafe = true
				break
			}
		}
		if isSafe {
			resp.Skipped = append(resp.Skipped, tid)
			continue
		}

		if err := s.wipeOneTenant(ctx, rootCtx, tid); err != nil {
			resp.Errors[tid] = err.Error()
			continue
		}
		resp.Wiped = append(resp.Wiped, tid)
	}

	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"wiped":   len(resp.Wiped),
			"skipped": len(resp.Skipped),
			"errors":  len(resp.Errors),
		}).Info("veridian: WipeTestTenants completed")
	}
	return resp, nil
}

// wipeOneTenant supprime definitivement un tenant : DROP database workspace
// + DELETE row veridian_plan + emit event tenant.deleted. Rendu helper pour
// eviter le defer-in-loop (la session tenant ouverte pour DeleteWorkspace
// est nettoyee a la fin de cette fonction, pas a la fin de WipeTestTenants).
//
// rootCtx est le ctx avec session root active, utilise comme fallback si on
// n'arrive pas a creer une session tenant (pre-feature owner-natif, ou
// erreur transitoire).
func (s *veridianService) wipeOneTenant(ctx, rootCtx context.Context, tid string) error {
	// 1. Resoudre l'owner du workspace via repo direct (pas de check auth).
	// Le workspace a maintenant le tenant user comme seul owner (root a ete
	// retire par Provision via TransferOwnership). DeleteWorkspace exige que
	// le caller soit owner — on doit donc utiliser un ctx du tenant user.
	deleteCtx := rootCtx
	if s.workspaceRepo != nil {
		members, _ := s.workspaceRepo.GetWorkspaceUsersWithEmail(ctx, tid)
		var ownerUserID string
		for _, m := range members {
			if m != nil && m.Role == "owner" {
				ownerUserID = m.UserID
				break
			}
		}
		if ownerUserID != "" {
			ownerCtx, ownerSessionID, sessErr := s.ctxAsUser(ctx, ownerUserID)
			if sessErr == nil {
				deleteCtx = ownerCtx
				defer s.cleanupSession(ctx, ownerSessionID)
			} else if s.logger != nil {
				s.logger.WithFields(map[string]interface{}{
					"tenant_id": tid,
					"owner_id":  ownerUserID,
					"error":     sessErr.Error(),
				}).Warn("veridian: failed to create owner session for delete, falling back to root ctx")
			}
		}
	}

	// 2. DeleteWorkspace upstream → DROP DATABASE workspace dedie.
	// Workspace deja absent = OK (peut arriver si planRepo a une row sans workspace).
	if err := s.workspaceService.DeleteWorkspace(deleteCtx, tid); err != nil {
		if !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("delete workspace: %w", err)
		}
	}

	// 3. HardDelete → DELETE veridian_plan row.
	if err := s.planRepo.HardDelete(ctx, tid); err != nil {
		return fmt.Errorf("hard delete plan: %w", err)
	}

	// 4. Notifier le Hub (best effort).
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantDeleted, tid, map[string]interface{}{
			"hard_wipe": true,
		})
	}
	return nil
}

// GetStatus retourne le snapshot complet du tenant (plan + quota + status).
func (s *veridianService) GetStatus(ctx context.Context, tenantID string) (*domain.StatusResponse, error) {
	if tenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	p, err := s.planRepo.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return &domain.StatusResponse{
		TenantID:            p.WorkspaceID,
		Status:              p.Status,
		Plan:                p.Plan,
		MonthlyEmailQuota:   p.MonthlyEmailQuota,
		EmailsSentThisMonth: p.EmailsSentThisMonth,
		QuotaRemaining:      p.QuotaRemaining(),
		SuspendedAt:         p.SuspendedAt,
		SuspendedReason:     p.SuspendedReason,
		DeletedAt:           p.DeletedAt,
	}, nil
}

// GenerateMagicLink construit une URL signin signed-in pour un user d'un
// workspace. Pre-condition : le user doit deja exister et etre membre du
// workspace, sinon ErrUserNotFound. Le code est valide 15 min.
func (s *veridianService) GenerateMagicLink(ctx context.Context, workspaceID, userEmail string) (*domain.MagicLinkResponse, error) {
	if workspaceID == "" {
		return nil, errors.New("workspace_id required")
	}
	if userEmail == "" {
		return nil, errors.New("user_email required")
	}

	// Verifier l'existence du user (sinon SignIn renvoie une erreur
	// generique qu'on remappe en 404 cote handler).
	user, err := s.userService.GetUserByEmail(ctx, userEmail)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, &domain.ErrUserNotFound{Message: "user not found"}
	}

	link, expiresAt, err := s.buildMagicLink(ctx, workspaceID, userEmail)
	if err != nil {
		return nil, err
	}

	// === Veridian patch === auto-login URL en plus du magic link.
	autoLoginURL, _, autoErr := domain.BuildAutoLoginURL(s.apiEndpoint, s.hubSecret, workspaceID, userEmail)
	if autoErr != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"email":        userEmail,
			"error":        autoErr.Error(),
		}).Warn("veridian: failed to build auto-login url")
	}

	return &domain.MagicLinkResponse{
		MagicLink:    link,
		AutoLoginURL: autoLoginURL,
		ExpiresAt:    expiresAt,
	}, nil
}

// buildMagicLink appelle GenerateMagicCodeForVeridian (privileged) et
// construit l'URL `<API_ENDPOINT>/console/signin?email=X&code=Y`.
//
// === Veridian patch ===
// Contrairement a UserService.SignIn upstream qui n'expose le code en clair
// qu'en mode dev/test (et envoie un email en prod), GenerateMagicCodeForVeridian
// retourne TOUJOURS le code en clair. Le caller est cense avoir prouve son
// authority en amont (HMAC Hub pour Provision, API key tenant pour
// GenerateMagicLink). Permet au Hub de livrer un magic link self-contained
// au user, sans dependre du SMTP Notifuse.
func (s *veridianService) buildMagicLink(ctx context.Context, workspaceID, email string) (string, time.Time, error) {
	code, expiresAt, err := s.userService.GenerateMagicCodeForVeridian(ctx, email, workspaceID)
	if err != nil {
		return "", time.Time{}, err
	}

	endpoint := strings.TrimRight(s.apiEndpoint, "/")
	q := url.Values{}
	q.Set("email", email)
	q.Set("code", code)
	return fmt.Sprintf("%s/console/signin?%s", endpoint, q.Encode()), expiresAt.UTC(), nil
}

// === Veridian patch ===
// AttachOwner répare un workspace existant en y attachant un user humain
// comme owner. Idempotent : safe à appeler plusieurs fois.
//
// Pourquoi ce endpoint existe :
//
//	Les workspaces créés avant la feature Hub-Veridian (avril 2026 et
//	antérieurs) n'ont jamais eu d'appel à Provision. Leur owner enregistré
//	dans user_workspaces est le user "natif Notifuse" qui les a créés via
//	l'UI (typiquement le premier user de l'instance, root ou équivalent).
//
//	Quand le Hub appelle ensuite /api/workspaces.generateMagicLink avec
//	user_email = "vrai owner humain", la console Notifuse retourne un JWT
//	valide pour cet email — mais GetUserWorkspaces(user_id) renvoie [] car
//	il n'est pas dans user_workspaces. Résultat : la console redirige sur
//	/console/workspace/create au lieu d'ouvrir le workspace.
//
// Algorithme :
//
//	1. Trouver/créer le user humain owner_email (type=user).
//	2. Lire l'état actuel : déjà attaché ? déjà owner ?
//	3. Si pas attaché → AddUserToWorkspace(role=member, FullPermissions).
//	4. Si pas owner → TransferOwnership(workspaceID, newOwner=humain, currentOwner=existingOwner).
//	   On résout currentOwnerID en regardant la row user_workspaces.role=owner.
//	5. Optionnel : si l'ancien owner est "root" (≠ owner_email) et qu'on l'a
//	   demoté à member par TransferOwnership, on le retire pour finir l'owner-
//	   natif comme dans Provision. Best-effort.
func (s *veridianService) AttachOwner(ctx context.Context, input domain.AttachOwnerInput) (*domain.AttachOwnerResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.OwnerEmail == "" {
		return nil, errors.New("owner_email required")
	}

	// Step 0 : vérifier que le workspace existe AVANT toute autre op.
	// Sans ce check, un tenant_id inexistant remontait HTTP 500 ("user is
	// not a member of the workspace") au lieu de 404 — l'agent Hub a flag
	// ce comportement le 2026-05-18 (ticket from-notifuse). Le check
	// explicite garantit la sémantique 404 du contrat README.
	if _, wsErr := s.workspaceRepo.GetByID(ctx, input.TenantID); wsErr != nil {
		if errors.Is(wsErr, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		// Pas not-found mais erreur DB → propage.
		msg := strings.ToLower(wsErr.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no rows") {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("lookup workspace: %w", wsErr)
	}

	// Step 1 : trouver/créer le user humain.
	owner, err := s.userService.GetUserByEmail(ctx, input.OwnerEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("get owner by email: %w", err)
		}
		owner = nil
	}
	if owner == nil {
		owner = &domain.User{
			ID:        uuid.New().String(),
			Email:     input.OwnerEmail,
			Type:      domain.UserTypeUser,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.userRepo.CreateUser(ctx, owner); err != nil {
			return nil, fmt.Errorf("create owner user: %w", err)
		}
	}

	// Step 2 : lire l'état actuel pour décider idempotence.
	// workspaceRepo.GetUserWorkspace renvoie sql.ErrNoRows si pas attaché.
	alreadyAttached := false
	alreadyOwner := false
	existing, lookupErr := s.workspaceRepo.GetUserWorkspace(ctx, owner.ID, input.TenantID)
	if lookupErr == nil && existing != nil {
		alreadyAttached = true
		if existing.Role == "owner" {
			alreadyOwner = true
		}
	} else if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		// Toute autre erreur (DB down, etc.) → fail. ErrNoRows = pas attaché.
		// Notifuse upstream wrap "not found" dans plusieurs formats selon la
		// version : "not found", "is not a member" (validation côté repo).
		// On accepte ces patterns comme équivalents à "pas attaché" — c'est
		// exactement ce que AttachOwner doit gérer (user existe mais pas
		// encore dans user_workspaces). Vérifié en staging 2026-05-18.
		msg := strings.ToLower(lookupErr.Error())
		notAttached := strings.Contains(msg, "not found") ||
			strings.Contains(msg, "is not a member") ||
			strings.Contains(msg, "no rows")
		if !notAttached {
			return nil, fmt.Errorf("lookup current attachment: %w", lookupErr)
		}
	}

	// Si déjà owner, rien à faire (idempotence).
	if alreadyOwner {
		return &domain.AttachOwnerResponse{
			TenantID:         input.TenantID,
			OwnerEmail:       input.OwnerEmail,
			UserID:           owner.ID,
			Attached:         true,
			AlreadyAttached:  true,
			OwnerTransferred: false,
		}, nil
	}

	// Step 3 : résoudre l'owner actuel AVANT toute autre op.
	// Depuis la feature owner-natif (commit f43ce239), root n'est plus
	// dans les workspaces Veridian-managed. Donc on ne peut PAS utiliser
	// ctxAsRoot pour AddUserToWorkspace — le caller doit être l'owner
	// actuel du workspace (qui est lui dans user_workspaces).
	// Bug détecté en staging 2026-05-18 : "failed to authenticate user:
	// user is not a member of the workspace" sur attach d'un nouvel owner
	// alors que root a déjà été virer par Provision.
	members, listErr := s.workspaceRepo.GetWorkspaceUsersWithEmail(ctx, input.TenantID)
	if listErr != nil {
		return nil, fmt.Errorf("list workspace members: %w", listErr)
	}
	var currentOwnerID, currentOwnerEmail string
	for _, m := range members {
		if m.Role == "owner" && m.UserID != owner.ID {
			currentOwnerID = m.UserID
			currentOwnerEmail = m.Email
			break
		}
	}
	if currentOwnerID == "" {
		// Pas d'owner identifié (workspace orphelin) — on tombe sur root
		// comme caller fallback. Si root n'est pas membre non plus,
		// AddUserToWorkspace va fail et on remontera l'erreur.
		rootUser, rootErr := s.userRepo.GetUserByEmail(ctx, s.rootEmail)
		if rootErr != nil {
			return nil, fmt.Errorf("resolve fallback caller (root): %w", rootErr)
		}
		currentOwnerID = rootUser.ID
	}

	// Step 4 : créer la session en tant que l'owner actuel (qui est dans
	// le workspace). C'est lui qui sera le "caller" pour les ops upstream.
	callerCtx, callerSessionID, err := s.ctxAsUser(ctx, currentOwnerID)
	if err != nil {
		return nil, fmt.Errorf("ctxAsUser current owner %s: %w", currentOwnerID, err)
	}
	defer s.cleanupSession(ctx, callerSessionID)

	// Step 5 : si pas attaché, AddUserToWorkspace(role=member, FullPermissions).
	if !alreadyAttached {
		if addErr := s.workspaceService.AddUserToWorkspace(
			callerCtx,
			input.TenantID,
			owner.ID,
			"member",
			domain.FullPermissions,
		); addErr != nil && !strings.Contains(addErr.Error(), "already") {
			return nil, fmt.Errorf("add user to workspace: %w", addErr)
		}
	}

	// Step 6 : TransferOwnership(workspaceID, newOwner=humain, currentOwner=existingOwner).
	// callerCtx = ctx authentifié en tant que currentOwner qui peut donc
	// initier le transfer vers lui-même (no-op) ou vers le new owner.
	transferred := false
	if transferErr := s.workspaceService.TransferOwnership(
		callerCtx,
		input.TenantID,
		owner.ID,
		currentOwnerID,
	); transferErr != nil {
		// Si l'erreur est "déjà owner" (race condition idempotence) on continue,
		// sinon on fail.
		if !strings.Contains(strings.ToLower(transferErr.Error()), "already") {
			return nil, fmt.Errorf("transfer ownership to %s: %w", input.OwnerEmail, transferErr)
		}
	} else {
		transferred = true
	}

	// Step 7 : si l'ancien owner était le user root, on le retire pour finir
	// owner-natif (parité avec Provision). Best-effort, non bloquant.
	if transferred && s.rootEmail != "" {
		rootUser, rootErr := s.userRepo.GetUserByEmail(ctx, s.rootEmail)
		if rootErr == nil && rootUser != nil && rootUser.ID == currentOwnerID {
			tenantCtx, tenantSessionID, sessErr := s.ctxAsUser(ctx, owner.ID)
			if sessErr == nil {
				if rmErr := s.workspaceService.RemoveUserFromWorkspace(tenantCtx, input.TenantID, currentOwnerID); rmErr != nil && s.logger != nil {
					s.logger.WithFields(map[string]interface{}{
						"workspace_id": input.TenantID,
						"root_id":      currentOwnerID,
						"error":        rmErr.Error(),
					}).Warn("veridian AttachOwner: failed to remove root after transfer (non-fatal)")
				}
				s.cleanupSession(ctx, tenantSessionID)
			}
		}
	}

	// Step 8 : émettre tenant.owner_changed si un transfer a effectivement eu lieu.
	// Best-effort (best-effort par design dans webhookEmitter — pas bloquant).
	if transferred && s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantOwnerChanged, input.TenantID, map[string]interface{}{
			"new_owner_email":   input.OwnerEmail,
			"new_owner_user_id": owner.ID,
			"old_owner_email":   currentOwnerEmail,
			"old_owner_user_id": currentOwnerID,
		})
	}

	// Marquer le sync Hub réussi (best-effort).
	s.touchHubSync(ctx, input.TenantID)

	return &domain.AttachOwnerResponse{
		TenantID:         input.TenantID,
		OwnerEmail:       input.OwnerEmail,
		UserID:           owner.ID,
		Attached:         true,
		AlreadyAttached:  alreadyAttached,
		OwnerTransferred: transferred,
	}, nil
}

// === Veridian patch ===
// Health renvoie l'état réel observable du tenant (livrable 3 contrat
// intégrations Hub). Composition d'une lecture veridian_plan + workspace +
// membres. Critère métier `magic_link_capable` :
//
//	false si :
//	  - workspace absent (404)
//	  - tenant soft-deleted (DeletedAt != nil)
//	  - status = suspended
//	  - aucun owner humain (type=user, role=owner) attaché
//	  - aucune API key valide attachée (type=api_key, role=member)
//
//	true sinon.
//
// Le Hub appelle ce endpoint en cron 1×/h via HMAC. Si magic_link_capable
// passe à false sur un tenant prod actif → alerte → repair via AttachOwner.
func (s *veridianService) Health(ctx context.Context, tenantID string) (*domain.TenantHealthResponse, error) {
	if tenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	resp := &domain.TenantHealthResponse{
		TenantID:    tenantID,
		WorkspaceID: tenantID,
		CheckedAt:   time.Now().UTC(),
	}

	// 1. Workspace doit exister — c'est la source de vérité du 404.
	// Avant 2026-05-18, on déduisait l'existence du tenant via planRepo.Get.
	// Bug : les 9 workspaces prod créés avant la table `veridian_plan` n'ont
	// pas de row plan → 404 fantôme alors que le workspace existe et marche.
	// Désormais : 404 réservé au cas "workspace absent". Plan absent = legacy.
	if _, wsErr := s.workspaceRepo.GetByID(ctx, tenantID); wsErr != nil {
		if errors.Is(wsErr, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("get workspace: %w", wsErr)
	}

	// 2. Lire veridian_plan pour status + plan + deleted_at. Absent OK
	// (legacy workspace) → resp.Plan + resp.Status restent vides ; le Hub
	// interprète `plan=""` comme "tenant pre-Hub, à enroller dans un plan".
	plan, err := s.planRepo.Get(ctx, tenantID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get plan: %w", err)
	}
	if plan != nil {
		resp.Plan = plan.Plan
		resp.Status = plan.Status
		if plan.DeletedAt != nil {
			resp.Status = domain.PlanStatusDeleted
		}
	}

	// 3. Lire les membres du workspace pour identifier owner humain + api_key.
	// On utilise GetWorkspaceUsersWithEmail (déjà utilisé par AttachOwner).
	members, listErr := s.workspaceRepo.GetWorkspaceUsersWithEmail(ctx, tenantID)
	if listErr != nil {
		return nil, fmt.Errorf("list workspace members: %w", listErr)
	}
	resp.MembersCount = len(members)

	// 4. Chercher l'owner humain et au moins une api_key.
	for _, m := range members {
		userType, _ := s.lookupUserType(ctx, m.UserID)
		if m.Role == "owner" && userType == domain.UserTypeUser {
			resp.OwnerAttached = true
			resp.OwnerEmail = m.Email
			resp.OwnerUserID = m.UserID
		}
		if userType == domain.UserTypeAPIKey {
			resp.APIKeyValid = true
		}
	}

	// 5. magic_link_capable = owner humain attaché + api key valide +
	//    pas suspended ni deleted.
	resp.MagicLinkCapable = resp.OwnerAttached && resp.APIKeyValid &&
		resp.Status != domain.PlanStatusSuspended &&
		resp.Status != domain.PlanStatusDeleted

	return resp, nil
}

// lookupUserType renvoie le type du user (user, api_key). Wrapper sur
// userRepo.GetUserByID — best-effort, retourne "" sur erreur (interprété
// comme "type inconnu" donc ni owner humain ni api_key).
func (s *veridianService) lookupUserType(ctx context.Context, userID string) (domain.UserType, error) {
	u, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil || u == nil {
		return "", err
	}
	return u.Type, nil
}

// GetLimits retourne les limites + dimensions feature pour un tenant
// (V37 pricing dimensions). Source de verite pour le paywall middleware
// (lot 4) et l'endpoint /api/veridian/limits (lot 7).
//
// Strategie :
//   - On lit le row complet (Get inclut deja les 9 colonnes V37 depuis le lot 2)
//   - On expose les valeurs DB telles quelles dans la reponse
//   - Si toutes les dimensions V37 sont a zero ET les booleens features
//     a false (= row antedeluvien jamais re-upsert apres la migration V37
//     dont le backfill aurait merdu sur ce tenant en particulier), on
//     retombe sur domain.LimitsForPlan(p.Plan) pour ne pas casser le caller
//     avec un Plan = "free" mais MaxContacts = 0
//
// sql.ErrNoRows propage tel quel (handler mappe → 404).
func (s *veridianService) GetLimits(ctx context.Context, tenantID string) (*domain.LimitsResponse, error) {
	if tenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	p, err := s.planRepo.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	limits := domain.PlanLimits{
		MonthlyEmailQuota:      p.MonthlyEmailQuota,
		MaxContacts:            p.MaxContacts,
		MaxSeats:               p.MaxSeats,
		MaxOAuthAccounts:       p.MaxOAuthAccounts,
		MaxCustomDomains:       p.MaxCustomDomains,
		MaxActiveSequences:     p.MaxActiveSequences,
		FeatureABTesting:       p.FeatureABTesting,
		FeatureBrandingRemoved: p.FeatureBrandingRemoved,
		FeatureWhiteLabel:      p.FeatureWhiteLabel,
		HistoryRetentionDays:   p.HistoryRetentionDays,
	}

	// Fallback safe : row antedeluvien dont les dimensions V37 sont toutes
	// a zero/false (cas tenant créé pre-V37 jamais re-upsert). On retombe
	// sur LimitsForPlan(p.Plan) pour ne pas exposer un "free avec 0
	// contacts max" qui bloquerait toute action. Note : pour Free, le
	// fallback retourne aussi MaxCustomDomains=0 et FeatureWhiteLabel=false
	// — donc le check pessimiste reste toujours strict.
	if isEmptyLimits(limits) {
		limits = domain.LimitsForPlan(p.Plan)
	}

	return &domain.LimitsResponse{
		TenantID:    p.WorkspaceID,
		Plan:        p.Plan,
		PlanSource:  p.PlanSource,
		Status:      p.Status,
		Limits:      limits,
		GeneratedAt: time.Now().UTC(),
	}, nil
}

// isEmptyLimits retourne true si toutes les dimensions V37 d'une struct
// PlanLimits sont a zero + booleens false. Sert au fallback GetLimits.
// Note : duplique repo.isZeroPricingDimensions mais en operant sur
// PlanLimits (vs VeridianPlan). Constitution §1 : duplication tolerable
// pour 9 lignes evidentes (vs creer un sous-package juste pour ca).
func isEmptyLimits(l domain.PlanLimits) bool {
	return l.MaxContacts == 0 &&
		l.MaxSeats == 0 &&
		l.MaxOAuthAccounts == 0 &&
		l.MaxCustomDomains == 0 &&
		l.MaxActiveSequences == 0 &&
		!l.FeatureABTesting &&
		!l.FeatureBrandingRemoved &&
		!l.FeatureWhiteLabel &&
		l.HistoryRetentionDays == 0
}

// === Veridian patch — hub-attach-member (2026-05-21, refactor v1.5 §5.22.4 2026-05-23) ===
// AttachMember attache un user Hub invite au workspace Notifuse d'un tenant.
//
// Algorithme :
//  1. Vérifier que le workspace existe (404 safe).
//  2. Vérifier que le tenant n'est pas suspendu (ErrTenantSuspended → 423).
//     Le check plan peut echouer (tenant pre-Hub sans plan row) — OK on continue.
//  3. Lookup user Notifuse par EMAIL (source de verite §3.7). Si absent →
//     creer user avec uuid.New() Notifuse natif (PAS HubUserID qui n'est pas
//     forcement UUID strict). Le binding hub_user_id est stocke en colonne
//     dediee via BackfillHubUserID (best-effort §3.7).
//  4. Lookup user_workspaces (user_id, workspace_id).
//     - Si present meme role → 200 already_member=true (idempotent).
//     - Si present role DIFFERENT → 200 already_member=true avec role LOCAL
//       INCHANGE (§5.22.4 : le Hub n'est PAS autoritatif sur les roles
//       internes, JAMAIS de UPDATE remove+re-add — downgrade silencieux
//       interdit). Log info "role conflict ignored, local role kept".
//     - Si absent → INSERT via AddUserToWorkspace (role mappe vers 'member').
//  5. Générer login_url auto-login (pattern BuildAutoLoginURL).
//  6. Retourner {attached, already_member, workspace_id, role, login_url}.
//
// Idempotence stricte : 2e call avec memes params = 200 already_member=true.
// Sécurité : le check tenant_not_found est APRES HMAC (handler) pour éviter
// l'énumération de tenantId. Pas de log du body complet (contient hub_user_email).
func (s *veridianService) AttachMember(ctx context.Context, input domain.AttachMemberInput) (*domain.AttachMemberResponse, error) {
	if input.TenantID == "" {
		return nil, errors.New("tenant_id required")
	}
	if input.HubUserID == "" {
		return nil, errors.New("hub_user_id required")
	}
	if input.HubUserEmail == "" {
		return nil, errors.New("hub_user_email required")
	}
	if !input.Role.IsValid() {
		return nil, fmt.Errorf("invalid role %q: must be owner|admin|member", input.Role)
	}

	// Step 1 : vérifier que le workspace existe (source de vérité pour 404).
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

	// Step 2 : vérifier que le tenant n'est pas suspendu.
	// Le plan peut etre absent (tenant pre-Hub) → on continue sans bloquer.
	plan, planErr := s.planRepo.Get(ctx, input.TenantID)
	if planErr == nil && plan != nil {
		if blocked, reason := plan.IsBlocked(); blocked {
			if plan.Status == domain.PlanStatusSuspended {
				return nil, fmt.Errorf("%w: %s", ErrTenantSuspended, reason)
			}
			// Soft-deleted : on refuse aussi (le tenant est en cours de purge).
			return nil, fmt.Errorf("%w: tenant deleted", ErrTenantSuspended)
		}
	}

	// Step 3 : trouver/créer le user Notifuse, identifié par son EMAIL.
	// On lookup d'abord par email (source de vérité d'identité cross-app,
	// cf. CONTRAT-HUB §3.7 — l'email canonique est la clé, pas le hub_user_id).
	//
	// ⚠️ La colonne users.id Notifuse est de type UUID strict en DB. Le
	// hub_user_id reçu du Hub (ex: "user_abc123") n'est PAS forcément un UUID
	// → on génère un UUID Notifuse natif, exactement comme Provision (cf. la
	// création de l'owner plus haut dans ce fichier). Le lien avec le Hub se
	// fait par l'email, pas par un id partagé. invitation_id reste tracé en
	// audit log uniquement.
	member, err := s.userService.GetUserByEmail(ctx, input.HubUserEmail)
	if err != nil {
		var notFound *domain.ErrUserNotFound
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("get user by email: %w", err)
		}
		member = nil
	}
	if member == nil {
		hubID := input.HubUserID // capture pour pointer (struct field *string)
		member = &domain.User{
			ID:        uuid.New().String(),
			Email:     input.HubUserEmail,
			Type:      domain.UserTypeUser,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
			HubUserID: &hubID,
		}
		if err := s.userRepo.CreateUser(ctx, member); err != nil {
			return nil, fmt.Errorf("create member user: %w", err)
		}
	} else {
		// User existant : backfill best-effort du binding Hub (V46 §3.7).
		// Non bloquant : si le binding existe deja avec une valeur differente
		// (ErrHubUserIDMismatch), on log warn et on continue — l'email reste
		// la cle canonique cross-app, le binding hub_user_id est informationnel.
		if bfErr := s.userRepo.BackfillHubUserID(ctx, member.ID, input.HubUserID); bfErr != nil {
			if s.logger != nil {
				level := "warn"
				if errors.Is(bfErr, domain.ErrHubUserIDMismatch) {
					level = "warn_mismatch"
				}
				s.logger.WithFields(map[string]interface{}{
					"user_id":     member.ID,
					"hub_user_id": input.HubUserID,
					"tenant_id":   input.TenantID,
					"error":       bfErr.Error(),
					"level":       level,
				}).Warn("veridian AttachMember: backfill hub_user_id failed (non-blocking, email remains canonical join key §3.7)")
			}
		}
	}

	// Mapper le role Hub → role interne workspace Notifuse.
	// Notifuse upstream n'a QUE 2 roles workspace : 'owner' et 'member'
	// (workspace_service.AddUserToWorkspace refuse tout autre). Le Hub
	// envoie owner|admin|member — 'admin' n'existe pas côté Notifuse.
	// Conformément au CONTRAT-HUB §3.5 ("le Hub n'est PAS autoritatif sur
	// les rôles internes app"), on mappe : owner→member, admin→member.
	// Le endpoint attach-member ne crée jamais un owner (un workspace a déjà
	// son owner via provision/attach-owner). Tous les invités = 'member'.
	targetRole := "member"

	// Step 4 : lookup user_workspaces pour décider idempotence / no-downgrade.
	// CONTRAT-HUB §5.22.4 : le Hub n'est PAS autoritatif sur les roles
	// internes Notifuse. Si le user est deja membre (quel que soit son role
	// local) → on retourne 200 already_member=true en conservant le role
	// local intact. JAMAIS de UPDATE remove+re-add (downgrade silencieux
	// d'un admin local interdit). Donc PAS besoin de tracer `oldRole` pour
	// emettre tenant.member_role_changed depuis ici — on ne change jamais
	// le role via AttachMember. Le webhook role_changed reste reserve aux
	// flows UI Team Settings (futur lot).
	existing, lookupErr := s.workspaceRepo.GetUserWorkspace(ctx, member.ID, input.TenantID)
	if lookupErr == nil && existing != nil {
		if s.logger != nil && existing.Role != targetRole {
			s.logger.WithFields(map[string]interface{}{
				"tenant_id":     input.TenantID,
				"hub_user_id":   input.HubUserID,
				"local_role":    existing.Role,
				"requested":     targetRole,
				"invitation_id": input.InvitationID,
			}).Info("veridian AttachMember: role conflict ignored, local role kept (§5.22.4)")
		}
		loginURL, _, _ := domain.BuildAutoLoginURL(s.apiEndpoint, s.hubSecret, input.TenantID, input.HubUserEmail)
		if s.logger != nil {
			s.logger.WithFields(map[string]interface{}{
				"tenant_id":     input.TenantID,
				"hub_user_id":   input.HubUserID,
				"role":          existing.Role,
				"invitation_id": input.InvitationID,
			}).Info("veridian AttachMember: already_member idempotent return")
		}
		return &domain.AttachMemberResponse{
			Attached:      true,
			AlreadyMember: true,
			WorkspaceID:   input.TenantID,
			Role:          existing.Role, // role LOCAL souverain, jamais ecrase
			LoginURL:      loginURL,
		}, nil
	} else if lookupErr != nil {
		msg := strings.ToLower(lookupErr.Error())
		notAttached := strings.Contains(msg, "not found") ||
			strings.Contains(msg, "is not a member") ||
			strings.Contains(msg, "no rows") ||
			errors.Is(lookupErr, sql.ErrNoRows)
		if !notAttached {
			return nil, fmt.Errorf("lookup user workspace: %w", lookupErr)
		}
	}

	// Résoudre l'owner actuel du workspace pour construire un ctx caller valide.
	// Depuis owner-natif, root n'est plus dans les workspaces managed. On doit
	// appeler AddUserToWorkspace depuis le ctx d'un owner existant du workspace.
	members, listErr := s.workspaceRepo.GetWorkspaceUsersWithEmail(ctx, input.TenantID)
	if listErr != nil {
		return nil, fmt.Errorf("list workspace members: %w", listErr)
	}
	var callerOwnerID string
	for _, m := range members {
		if m != nil && m.Role == "owner" {
			callerOwnerID = m.UserID
			break
		}
	}
	if callerOwnerID == "" {
		// Fallback root si pas d'owner trouvé (workspace orphelin).
		rootUser, rootErr := s.userRepo.GetUserByEmail(ctx, s.rootEmail)
		if rootErr != nil {
			return nil, fmt.Errorf("resolve fallback caller (root): %w", rootErr)
		}
		callerOwnerID = rootUser.ID
	}

	callerCtx, callerSessionID, err := s.ctxAsUser(ctx, callerOwnerID)
	if err != nil {
		return nil, fmt.Errorf("ctxAsUser workspace owner: %w", err)
	}
	defer s.cleanupSession(ctx, callerSessionID)

	// INSERT pur via AddUserToWorkspace. Pas de remove+re-add §5.22.4 :
	// si on arrive ici, c'est que le user N'EST PAS deja membre (sinon on
	// serait sorti plus haut avec already_member=true). Le bloc RemoveUser
	// historique a ete supprime au profit du no-downgrade strict §5.22.4.
	if addErr := s.workspaceService.AddUserToWorkspace(
		callerCtx,
		input.TenantID,
		member.ID,
		targetRole,
		domain.FullPermissions,
	); addErr != nil {
		// "already" indique une race condition idempotente → on continue.
		if !strings.Contains(strings.ToLower(addErr.Error()), "already") {
			return nil, fmt.Errorf("add user to workspace: %w", addErr)
		}
	}

	// Step 5 : générer login_url auto-login (pattern provision). Best-effort.
	loginURL, _, autoErr := domain.BuildAutoLoginURL(s.apiEndpoint, s.hubSecret, input.TenantID, input.HubUserEmail)
	if autoErr != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id":   input.TenantID,
			"hub_user_id": input.HubUserID,
			"error":       autoErr.Error(),
		}).Warn("veridian AttachMember: failed to build auto-login url (HUB_API_SECRET missing?)")
	}

	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id":     input.TenantID,
			"hub_user_id":   input.HubUserID,
			"role":          targetRole,
			"invitation_id": input.InvitationID,
		}).Info("veridian AttachMember: member attached")
	}

	// Webhook app → Hub (CONTRAT-HUB §5.18.4 + §7.1) : member_added pur.
	// Avec le refactor §5.22.4, si on arrive ici c'est que le user n'etait
	// PAS deja membre (sinon short-circuit already_member plus haut sans
	// emission webhook). Donc emit unique tenant.member_added, JAMAIS
	// role_changed depuis AttachMember (le role local est souverain et ne
	// peut etre mute que via l'UI Team Settings, futur lot dedie).
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantMemberAdded, input.TenantID, map[string]interface{}{
			"user_email":    input.HubUserEmail,
			"role":          targetRole,
			"hub_user_id":   input.HubUserID,
			"app_user_id":   member.ID,
			"actor":         "hub",
			"invitation_id": input.InvitationID, // audit cross-app (§5.22.5)
		})
	}

	// Marquer le sync Hub réussi (best-effort).
	s.touchHubSync(ctx, input.TenantID)

	return &domain.AttachMemberResponse{
		Attached:      true,
		AlreadyMember: false,
		WorkspaceID:   input.TenantID,
		Role:          targetRole,
		LoginURL:      loginURL,
	}, nil
}

// touchHubSync marque last_hub_sync_at = NOW pour le tenant (best-effort,
// non bloquant). Appelé en queue de chaque mutation Hub→Notifuse pour mesurer
// la fraîcheur du lien Hub→Notifuse (résilience billing V39).
//
// Si l'appel échoue, on log warn et on retourne silencieusement : la mutation
// principale a déjà réussi et on ne veut pas la rendre échouée à cause d'un
// bookkeeping secondaire.
func (s *veridianService) touchHubSync(ctx context.Context, workspaceID string) {
	if err := s.planRepo.TouchHubSync(ctx, workspaceID); err != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"error":        err.Error(),
		}).Warn("veridian: touch hub sync failed (non-fatal, last_hub_sync_at not updated)")
	}
}

// Compile-time check.
var _ domain.VeridianService = (*veridianService)(nil)

// errPlanNotFound est exporte pour permettre au handler de renvoyer un 404.
var errPlanNotFound = sql.ErrNoRows

// === Veridian patch — Hub discovery cross-app (2026-05-20) ===
// LookupByEmail retourne les workspaces Notifuse associes a un email Hub.
// Appele par POST /api/users/by-email (HMAC Hub).
//
// Semantique non-error : retourne always DiscoveryResponse. Si l'email est
// inconnu : found=false + workspaces=[]. Vraies erreurs DB propagees normalement.
func (s *veridianService) LookupByEmail(ctx context.Context, email string) (*domain.DiscoveryResponse, error) {
	resp := &domain.DiscoveryResponse{
		UserEmail:  email,
		Workspaces: []domain.DiscoveryWorkspace{},
	}

	// 1. Lookup user par email.
	user, err := s.userRepo.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resp.Found = false
			return resp, nil
		}
		errMsg := err.Error()
		if errMsg != "" && (strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "no rows")) {
			resp.Found = false
			return resp, nil
		}
		return nil, fmt.Errorf("lookup user by email: %w", err)
	}

	// Exclure les users de type api_key (non-humains, internes Veridian).
	if user.Type != domain.UserTypeUser {
		resp.Found = false
		return resp, nil
	}

	resp.Found = true

	// 2. Lire les workspaces du user.
	userWorkspaces, err := s.workspaceRepo.GetUserWorkspaces(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("get user workspaces: %w", err)
	}

	for _, uw := range userWorkspaces {
		// Lire workspace name (tolerant : si absent, fallback workspace_id).
		wsName := uw.WorkspaceID
		ws, wsErr := s.workspaceRepo.GetByID(ctx, uw.WorkspaceID)
		if wsErr == nil && ws != nil {
			wsName = ws.Name
		}

		// Lire plan (tolerant : absent = legacy tenant sans plan row).
		plan := ""
		planRow, planErr := s.planRepo.Get(ctx, uw.WorkspaceID)
		if planErr == nil && planRow != nil {
			plan = planRow.Plan
		}

		fallbackURL := s.apiEndpoint + "/console/signin"
		if s.apiEndpoint == "" {
			fallbackURL = "https://notifuse.app.veridian.site/console/signin"
		}

		resp.Workspaces = append(resp.Workspaces, domain.DiscoveryWorkspace{
			WorkspaceID:      uw.WorkspaceID,
			WorkspaceName:    wsName,
			Role:             uw.Role,
			Plan:             plan,
			MagicLinkCapable: true,
			FallbackURL:      fallbackURL,
		})
	}

	return resp, nil
}

// === Veridian patch — Couche 4 Bounce OAuth Hub (CONTRAT-HUB §6bis.8.3) ===
//
// IssueMagicLinkForHub genere une URL self-contained auto-login pour un user
// authentifie cote Hub (post-OAuth Google/Microsoft). Appele par le Hub en
// HMAC apres bounce OAuth reussi.
//
// Algorithme :
//  1. Lookup user par email (source de verite identite cross-app, cf §3.7).
//     User inconnu (sql.ErrNoRows / ErrUserNotFound) ou type != user
//     → ErrUserNotInApp (handler => 400 user_not_in_app).
//  2. Lister les workspaces du user. Aucun workspace → ErrUserNotInApp.
//  3. Picker le dernier actif (MAX(UpdatedAt) sur user_workspaces). Convention
//     Notifuse : pas de last_seen_at dedie, UpdatedAt sert de proxy "derniere
//     activite sur cette adhesion" (re-add, role change, etc.).
//  4. BuildAutoLoginURL avec hubSecret → token HMAC self-contained TTL 60s.
//
// Pas d'auto-creation de workspace (anti-pattern §6bis.2). Le Hub gere la
// redirection signup-app cote Hub via /dashboard?app=...&hint=signup.
//
// hubUserID n'est pas resolveur d'identite (Notifuse users.id est UUID strict,
// hub_user_id peut etre "user_abc"). Conserve pour log/audit uniquement.
func (s *veridianService) IssueMagicLinkForHub(ctx context.Context, input domain.IssueMagicLinkInput) (*domain.IssueMagicLinkResponse, error) {
	if input.Email == "" {
		return nil, errors.New("email required")
	}
	if input.HubUserID == "" {
		return nil, errors.New("hub_user_id required")
	}

	// Step 1 : lookup user par email.
	user, err := s.userRepo.GetUserByEmail(ctx, input.Email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotInApp
		}
		errMsg := err.Error()
		if errMsg != "" && (strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "no rows")) {
			return nil, ErrUserNotInApp
		}
		return nil, fmt.Errorf("lookup user by email: %w", err)
	}
	if user == nil {
		return nil, ErrUserNotInApp
	}
	// Exclure les users non-humains (type=api_key) — coherent avec LookupByEmail.
	if user.Type != domain.UserTypeUser {
		return nil, ErrUserNotInApp
	}

	// Step 2 : lister workspaces.
	userWorkspaces, err := s.workspaceRepo.GetUserWorkspaces(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("get user workspaces: %w", err)
	}
	if len(userWorkspaces) == 0 {
		return nil, ErrUserNotInApp
	}

	// Step 3 : pick le dernier actif (MAX UpdatedAt). Si tie / vide, prend
	// le premier — deterministe pour les tests.
	latest := userWorkspaces[0]
	for _, uw := range userWorkspaces[1:] {
		if uw.UpdatedAt.After(latest.UpdatedAt) {
			latest = uw
		}
	}

	// Step 4 : BuildAutoLoginURL (token HMAC self-contained, TTL 60s).
	// Le Hub valide host *.veridian.site + https (cf. bounce-apps.ts).
	autoLoginURL, _, err := domain.BuildAutoLoginURL(s.apiEndpoint, s.hubSecret, latest.WorkspaceID, input.Email)
	if err != nil {
		// HUB_API_SECRET non configure → 500 cote handler.
		return nil, fmt.Errorf("build auto-login url: %w", err)
	}

	return &domain.IssueMagicLinkResponse{
		MagicLinkURL: autoLoginURL,
	}, nil
}
