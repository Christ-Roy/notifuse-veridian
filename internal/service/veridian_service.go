package service

// === Veridian patch ===
// VeridianService implemente les operations Hub-driven : provisioning,
// suspension, reprise, soft-delete, lecture de status, generation de magic
// link cross-app.
//
// Le Hub appelle ces operations via les endpoints /api/tenants/* proteges
// par middleware HMAC (voir veridian_hmac.go). Le service coordonne :
//   - WorkspaceServiceInterface (creation workspace, API key, ajout owner)
//   - UserServiceInterface       (signin pour magic code, lookup user)
//   - UserRepository             (creation user owner si absent, sessions
//                                  root pour bypass des guards d'auth)
//   - VeridianPlanRepository     (persistance plan + status + quota)
//   - WebhookEmitter             (events tenant.* pousses vers le Hub)
//
// Pourquoi le ctx-as-root :
//   WorkspaceService.CreateWorkspace exige un user root authentifie dans
//   le contexte. AddUserToWorkspace + CreateAPIKey exigent un owner
//   authentifie. Le Hub n'a pas de session humaine — la requete est
//   signee HMAC. On contourne en creant une session courte (5 min) pour
//   le user root et en l'injectant dans le ctx pour le temps de l'appel.
//   La session est ensuite supprimee.
//
// Voir veridian-platform/notifuse/README.md.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

// veridianService est l'implementation par defaut de domain.VeridianService.
type veridianService struct {
	workspaceService domain.WorkspaceServiceInterface
	userService      domain.UserServiceInterface
	userRepo         domain.UserRepository
	planRepo         domain.VeridianPlanRepository
	emitter          domain.WebhookEmitter
	defaultPlan      string
	rootEmail        string
	apiEndpoint      string
	hubSecret        string // === Veridian patch === pour signer les auto_login_url
	logger           logger.Logger
}

// NewVeridianService construit un VeridianService.
// rootEmail doit correspondre a config.RootEmail (le user root cote Notifuse).
// apiEndpoint est l'URL publique de Notifuse (ex https://notifuse.app.veridian.site)
// utilisee pour construire l'URL de magic link.
func NewVeridianService(
	workspaceService domain.WorkspaceServiceInterface,
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
// etre passee a cleanupRootSession en defer pour supprimer la session.
func (s *veridianService) ctxAsRoot(ctx context.Context) (context.Context, string, error) {
	if s.rootEmail == "" {
		return ctx, "", errors.New("veridian: ROOT_EMAIL not configured")
	}
	rootUser, err := s.userRepo.GetUserByEmail(ctx, s.rootEmail)
	if err != nil {
		return ctx, "", fmt.Errorf("veridian: get root user (%s): %w", s.rootEmail, err)
	}

	session := &domain.Session{
		ID:        uuid.New().String(),
		UserID:    rootUser.ID,
		ExpiresAt: time.Now().UTC().Add(rootSessionTTL),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.userRepo.CreateSession(ctx, session); err != nil {
		return ctx, "", fmt.Errorf("veridian: create root session: %w", err)
	}

	rootCtx := context.WithValue(ctx, domain.UserIDKey, rootUser.ID)
	rootCtx = context.WithValue(rootCtx, domain.UserTypeKey, string(domain.UserTypeUser))
	rootCtx = context.WithValue(rootCtx, domain.SessionIDKey, session.ID)
	return rootCtx, session.ID, nil
}

func (s *veridianService) cleanupRootSession(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}
	if err := s.userRepo.DeleteSession(ctx, sessionID); err != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"session_id": sessionID,
			"error":      err.Error(),
		}).Warn("veridian: failed to delete root session")
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
	idempCtx, idempSessionID, idempErr := s.ctxAsRoot(ctx)
	var existingWorkspace *domain.Workspace
	if idempErr == nil {
		existingWorkspace, _ = s.workspaceService.GetWorkspace(idempCtx, input.TenantID)
		s.cleanupRootSession(ctx, idempSessionID)
	}
	if existingWorkspace != nil && planErr == nil && existingPlan != nil {
		// Tenant deja provisionne : on retourne sans toucher.
		owner, _ := s.userService.GetUserByEmail(ctx, input.OwnerEmail)
		ownerID := ""
		if owner != nil {
			ownerID = owner.ID
		}
		return &domain.ProvisionResponse{
			WorkspaceID: input.TenantID,
			OwnerUserID: ownerID,
			APIKey:      "",
			APIKeyEmail: "",
			MagicLink:   "",
			Plan:        existingPlan.Plan,
			Created:     false,
		}, nil
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
	rootCtx, rootSessionID, err := s.ctxAsRoot(ctx)
	if err != nil {
		return nil, err
	}
	defer s.cleanupRootSession(ctx, rootSessionID)

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

	// 4. Ajouter l'owner Veridian comme owner du workspace (le root y est
	// deja owner via CreateWorkspace).
	if err := s.workspaceService.AddUserToWorkspace(
		rootCtx,
		input.TenantID,
		owner.ID,
		"owner",
		domain.FullPermissions,
	); err != nil {
		// Si owner deja membre, on ne bloque pas le provisioning.
		if !strings.Contains(err.Error(), "already") {
			return nil, fmt.Errorf("add owner to workspace: %w", err)
		}
	}

	// 5. Creer une API key tenant (utilisee par le Hub pour piloter le
	// workspace, ex generateMagicLink). Prefix unique par tenant car Notifuse
	// stocke l'API key user avec un email base sur le prefix — le meme prefix
	// pour deux workspaces distincts cree un conflit "user already exists".
	apiKeyPrefix := "veridian-api-" + input.TenantID
	apiKeyToken, apiKeyEmail, err := s.workspaceService.CreateAPIKey(rootCtx, input.TenantID, apiKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}

	// 6. Inserer / mettre a jour la ligne veridian_plan.
	now := time.Now().UTC()
	planRow := &domain.VeridianPlan{
		WorkspaceID:         input.TenantID,
		Plan:                plan,
		Status:              domain.PlanStatusActive,
		MonthlyEmailQuota:   domain.QuotaForPlan(plan),
		EmailsSentThisMonth: 0,
		LastResetAt:         now,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := s.planRepo.Upsert(ctx, planRow); err != nil {
		return nil, fmt.Errorf("upsert veridian_plan: %w", err)
	}

	// 7. Generer un magic link pour l'owner (fallback : signin avec saisie code).
	magicLink, _, err := s.buildMagicLink(ctx, input.TenantID, input.OwnerEmail)
	if err != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id": input.TenantID,
			"email":     input.OwnerEmail,
			"error":     err.Error(),
		}).Warn("veridian: failed to build magic link during provision")
	}

	// 7b. === Veridian patch === Generer auto_login_url (URL self-contained
	// signe HMAC qui logge directement le user via /veridian/auto-login).
	// C'est l'URL qu'utilise le bouton "Open Notifuse" du Hub, sans saisie.
	autoLoginURL, _, autoErr := httpHandlerBuildAutoLoginURL(s.apiEndpoint, s.hubSecret, input.TenantID, input.OwnerEmail)
	if autoErr != nil && s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"tenant_id": input.TenantID,
			"email":     input.OwnerEmail,
			"error":     autoErr.Error(),
		}).Warn("veridian: failed to build auto-login url (HUB_API_SECRET missing?)")
	}

	// 8. Notifier le Hub.
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantProvisioned, input.TenantID, map[string]interface{}{
			"plan":          plan,
			"owner_user_id": owner.ID,
			"owner_email":   input.OwnerEmail,
		})
	}

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

// UpdatePlan change le plan d'un tenant existant. Recalcule le quota
// mensuel a partir du PlanQuotas.
func (s *veridianService) UpdatePlan(ctx context.Context, input domain.UpdatePlanInput) error {
	if input.TenantID == "" {
		return errors.New("tenant_id required")
	}
	if input.Plan == "" {
		return errors.New("plan required")
	}
	quota := domain.QuotaForPlan(input.Plan)
	if err := s.planRepo.UpdatePlan(ctx, input.TenantID, input.Plan, quota); err != nil {
		return err
	}
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantPlanChanged, input.TenantID, map[string]interface{}{
			"plan":  input.Plan,
			"quota": quota,
		})
	}
	return nil
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
	return nil
}

// SoftDelete marque le tenant deleted. La purge effective est faite par cron 30j.
func (s *veridianService) SoftDelete(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return errors.New("tenant_id required")
	}
	if err := s.planRepo.SoftDelete(ctx, tenantID); err != nil {
		return err
	}
	if s.emitter != nil {
		s.emitter.Emit(ctx, domain.EventTenantDeleted, tenantID, nil)
	}
	return nil
}

// === Veridian patch ===
// defaultSafetyClientPrefixes : prefixes de tenants qui ne sont JAMAIS effaces
// par WipeTestTenants, meme avec un wildcard. Defense en profondeur si le
// HUB_API_SECRET fuit ou si un test a un prefix trop large.
// Ces prefixes correspondent aux clients reels staging documentes dans CLAUDE.md.
var defaultSafetyClientPrefixes = []string{
	"apicalinfo",
	"robinix",
	"lyon",
	"loyer",
	"veridiansite",
}

// WipeTestTenants supprime DEFINITIVEMENT (hard delete) les tenants matchant
// un prefix ou une liste explicite. Utilise par la CI / admin platform pour
// nettoyer les tenants e2e accumules entre les runs.
//
// Pour chaque tenant a supprimer :
//   1. WorkspaceService.DeleteWorkspace via ctx root → DROP DATABASE upstream
//   2. PlanRepo.HardDelete → DELETE veridian_plan row
//   3. Emit event tenant.deleted
//
// Les tenants matchant un safety prefix sont SKIP (jamais effaces).
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
	}

	// Determine safety prefixes
	safetyPrefixes := input.SafetyClientPrefixes
	if len(safetyPrefixes) == 0 {
		safetyPrefixes = defaultSafetyClientPrefixes
	}

	// Open ctx root pour appeler WorkspaceService.DeleteWorkspace
	rootCtx, sessionID, err := s.ctxAsRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("ctxAsRoot: %w", err)
	}
	defer s.cleanupRootSession(ctx, sessionID)

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

		// 1. WorkspaceService.DeleteWorkspace upstream → DROP DATABASE
		if err := s.workspaceService.DeleteWorkspace(rootCtx, tid); err != nil {
			// Workspace deja absent = OK (peut arriver si planRepo a une row sans workspace)
			if !strings.Contains(err.Error(), "not found") {
				resp.Errors[tid] = "delete workspace: " + err.Error()
				continue
			}
		}

		// 2. PlanRepo.HardDelete → DELETE veridian_plan row
		if err := s.planRepo.HardDelete(ctx, tid); err != nil {
			resp.Errors[tid] = "hard delete plan: " + err.Error()
			continue
		}

		// 3. Emit event
		if s.emitter != nil {
			s.emitter.Emit(ctx, domain.EventTenantDeleted, tid, map[string]interface{}{
				"hard_wipe": true,
			})
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
	autoLoginURL, _, autoErr := httpHandlerBuildAutoLoginURL(s.apiEndpoint, s.hubSecret, workspaceID, userEmail)
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

// Compile-time check.
var _ domain.VeridianService = (*veridianService)(nil)

// errPlanNotFound est exporte pour permettre au handler de renvoyer un 404.
var errPlanNotFound = sql.ErrNoRows

// === Veridian patch ===
// autoLoginTokenTTL est la fenetre temporelle d'un token auto-login.
// 60s : court pour limiter replay si l'URL fuit, suffisant pour un click.
// La valeur DOIT matcher AutoLoginTokenTTL dans internal/http/veridian_autologin_handler.go
// (les deux sont independants pour eviter les import cycles).
const autoLoginTokenTTL = 60 * time.Second

// httpHandlerBuildAutoLoginURL genere une URL self-contained vers
// /veridian/auto-login?token=<base64(payload).<hex(hmac)>.
//
// Mirroir de BuildAutoLoginURL dans internal/http/veridian_autologin_handler.go
// pour eviter un import cycle (le service ne peut pas importer http).
// Si on modifie ici, il faut aussi modifier la-bas — la verification cote
// handler doit utiliser exactement le meme schema.
func httpHandlerBuildAutoLoginURL(apiEndpoint, hubSecret, workspaceID, email string) (string, time.Time, error) {
	if hubSecret == "" {
		return "", time.Time{}, fmt.Errorf("HUB_API_SECRET not configured")
	}
	now := time.Now()
	expiresAt := now.Add(autoLoginTokenTTL)
	payload := struct {
		WorkspaceID string `json:"w"`
		Email       string `json:"e"`
		IssuedAt    int64  `json:"i"`
		ExpiresAt   int64  `json:"x"`
	}{
		WorkspaceID: workspaceID,
		Email:       email,
		IssuedAt:    now.UnixMilli(),
		ExpiresAt:   expiresAt.UnixMilli(),
	}
	rawJSON, err := json.Marshal(payload)
	if err != nil {
		return "", time.Time{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(rawJSON)
	mac := hmac.New(sha256.New, []byte(hubSecret))
	mac.Write([]byte(encoded))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := encoded + "." + sig

	url := strings.TrimRight(apiEndpoint, "/") + "/veridian/auto-login?token=" + token
	return url, expiresAt, nil
}
