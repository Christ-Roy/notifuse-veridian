package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
	httpHandler "github.com/Notifuse/notifuse/internal/http"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/hub_discovery"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Point UNIQUE de câblage Veridian (lot L0, 29/10/2026). app.go l'appelle d'une seule ligne, en fin
// d'InitHandlers : tout le reste du code Veridian s'accroche ici, jamais dans app.go (fichier gelé,
// voir docs/veridian/MODULARITE.md).
//
// Ordre : (1) migrations Veridian manquantes (registre internal/migrations/veridian, suivi par
// numéro), (2) Wire de chaque capacité enregistrée (internal/veridian/<module>, par ordre de nom),
// (3) Start/Stop différés : Start une fois le serveur HTTP créé, Stop à l'annulation du contexte
// d'arrêt de l'application.

// WireVeridian câble toutes les capacités Veridian sur l'application initialisée.
func WireVeridian(a *App) error {
	if err := a.veridianInitHandlers(); err != nil {
		return err
	}
	if err := wireVeridian(a, veridianmig.Default, platform.Default); err != nil {
		return err
	}
	go func() {
		if waitServerCreated(a.GetShutdownContext(), a) {
			a.veridianStartBackground()
		}
	}()
	return nil
}

func wireVeridian(a *App, migs *veridianmig.Registry, caps *platform.Registry) error {
	if len(migs.List()) > 0 {
		if a.db == nil || a.veridianMockDB {
			// Assemblage de test (WithMockDB ou sans base) : pas de base réelle à migrer. En production
			// InitDB pose toujours a.db et veridianMockDB reste faux. Les migrations ont leurs propres tests.
			a.logger.Warn("veridian migrations: assemblage sans base réelle, migrations Veridian non jouées")
		} else {
			rep, err := migs.RunAll(context.Background(), a.db, workspaceSource{repo: a.workspaceRepo}, a.logger)
			if err != nil {
				return fmt.Errorf("veridian migrations: %w", err)
			}
			if len(rep.Failures) > 0 {
				// Journalisé en erreur par RunAll ; le démarrage continue (base orpheline ou injoignable).
				a.logger.WithField("failures", len(rep.Failures)).Error("veridian migrations: workspaces en échec, rejoués au prochain démarrage")
			}
		}
	}

	host := &veridianHost{a: a}
	capabilities := caps.List()
	for _, c := range capabilities {
		if err := safeWire(c, host); err != nil {
			return fmt.Errorf("veridian capability %q: %w", c.Name, err)
		}
		a.logger.WithField("capability", c.Name).Info("Veridian capability wired")
	}
	if len(capabilities) > 0 {
		go runLifecycle(a, host, capabilities)
	}
	return nil
}

func safeWire(c platform.Capability, h platform.Host) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return c.Wire(h)
}

// runLifecycle démarre les capacités quand le serveur existe et les arrête à l'arrêt de l'application.
func runLifecycle(a *App, h platform.Host, caps []platform.Capability) {
	ctx := a.GetShutdownContext()
	if !waitServerCreated(ctx, a) {
		return
	}
	for _, c := range caps {
		if c.Start == nil {
			continue
		}
		go func(c platform.Capability) {
			defer func() {
				if r := recover(); r != nil {
					a.logger.WithField("capability", c.Name).WithField("panic", fmt.Sprint(r)).Error("Veridian capability Start panicked")
				}
			}()
			if err := c.Start(ctx, h); err != nil {
				a.logger.WithField("capability", c.Name).WithField("error", err).Error("Veridian capability Start failed")
			}
		}(c)
	}
	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, c := range caps {
		if c.Stop == nil {
			continue
		}
		if err := c.Stop(stopCtx); err != nil {
			a.logger.WithField("capability", c.Name).WithField("error", err).Warn("Veridian capability Stop failed")
		}
	}
}

// waitServerCreated attend que Start() ait créé le serveur HTTP (sondage : le canal serverStarted
// d'App est refermé puis recréé par Start(), il ne se prête pas à une attente avant Start).
func waitServerCreated(ctx context.Context, a *App) bool {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		if a.IsServerCreated() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
}

// workspaceSource adapte le dépôt de workspaces au runner de migrations.
type workspaceSource struct{ repo domain.WorkspaceRepository }

func (s workspaceSource) IDs(ctx context.Context) ([]string, error) {
	if s.repo == nil {
		return nil, nil
	}
	list, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list))
	for _, w := range list {
		ids = append(ids, w.ID)
	}
	return ids, nil
}

func (s workspaceSource) Conn(ctx context.Context, id string) (*sql.DB, error) {
	return s.repo.GetConnection(ctx, id)
}

// veridianHost : adaptateur App -> platform.Host.
type veridianHost struct{ a *App }

var _ platform.Host = (*veridianHost)(nil)

func (h *veridianHost) Config() *config.Config { return h.a.config }
func (h *veridianHost) Logger() logger.Logger  { return h.a.logger }
func (h *veridianHost) DB() *sql.DB            { return h.a.db }
func (h *veridianHost) Mux() *http.ServeMux    { return h.a.mux }

func (h *veridianHost) JWTSecret() func() ([]byte, error) {
	return func() ([]byte, error) {
		if len(h.a.config.Security.JWTSecret) == 0 {
			return nil, fmt.Errorf("JWT secret not configured")
		}
		return h.a.config.Security.JWTSecret, nil
	}
}

func (h *veridianHost) RequireAuth() func(http.Handler) http.Handler {
	return middleware.NewAuthMiddleware(h.JWTSecret()).RequireAuth()
}

func (h *veridianHost) AuthService() *service.AuthService { return h.a.authService }

func (h *veridianHost) WorkspaceRepository() domain.WorkspaceRepository { return h.a.workspaceRepo }
func (h *veridianHost) ContactRepository() domain.ContactRepository     { return h.a.contactRepo }
func (h *veridianHost) MessageHistoryRepository() domain.MessageHistoryRepository {
	return h.a.messageHistoryRepo
}
func (h *veridianHost) EmailQueueRepository() domain.EmailQueueRepository { return h.a.emailQueueRepo }
func (h *veridianHost) EmailService() *service.EmailService               { return h.a.emailService }
func (h *veridianHost) TemplateService() domain.TemplateService           { return h.a.templateService }
func (h *veridianHost) WebhookEmitter() domain.WebhookEmitter             { return h.a.veridianWebhookEmitter }
func (h *veridianHost) IMAPPoller() *queue.VeridianIMAPPollerService      { return h.a.veridianIMAPPoller }
func (h *veridianHost) EmailQueueWorker() *queue.EmailQueueWorker         { return h.a.emailQueueWorker }
func (h *veridianHost) ShutdownContext() context.Context                  { return h.a.GetShutdownContext() }

// ============================================================================================
// Câblage historique déplacé de app.go (lot L0 jalon 2, 29/10/2026). Contenu identique, aux
// mêmes étapes d'initialisation : veridianInitRepositories en fin d'InitRepositories,
// veridianInitServices en fin d'InitServices, veridianInitHandlers au début de WireVeridian
// (fin d'InitHandlers), veridianStartBackground quand le serveur est créé.
// ============================================================================================

// veridianState : l'état Veridian de l'App. Embarqué dans App : a.veridianXxx continue de
// fonctionner, aucun appelant à modifier.
type veridianState struct {
	veridianMockDB             bool // posé par WithMockDB : assemblage de test, les migrations Veridian ne sont pas jouées
	veridianPlanRepo           domain.VeridianPlanRepository
	veridianIdempotencyRepo    domain.VeridianIdempotencyRepository
	veridianAPIKeyGraceRepo    domain.VeridianAPIKeyGraceRepository  // Lot K — grace period rotate-api-key
	agentInstallTokenRepo      domain.AgentInstallTokenRepository    // mission "API & agents" (2026-10-03)
	veridianFrozenMemberRepo   domain.VeridianFrozenMemberRepository // §5.21 — freeze member per-user
	veridianIMAPUIDSeenRepo    domain.VeridianIMAPUIDSeenRepository  // Lot 1 cold — idempotence poller IMAP (V50)
	veridianContactReplyRepo   domain.VeridianContactReplyRepository // Lot 3 cold — signal durable stop-on-reply (V51)
	veridianService            domain.VeridianService
	veridianWebhookEmitter     domain.WebhookEmitter
	veridianPaywallCache       *middleware.PaywallCache                   // partage middleware paywall + handler invalidate
	veridianFrozenCache        *middleware.FrozenMemberCache              // partage middleware frozen + handler freeze/unfreeze invalidate
	veridianPricingSync        *service.VeridianPricingSyncService        // catalogue pricing Hub (lot O 2026-05-21)
	veridianTestTenantsCleanup *service.VeridianTestTenantsCleanupService // cron auto-cleanup orphans staging (2026-05-24)
	veridianIMAPPoller         *queue.VeridianIMAPPollerService           // Lot 1 cold — poller IMAP self-service (réception bounces/réponses)
	veridianReplyService       *service.VeridianReplyService              // Lot 3 cold — détection réponse + exit séquence (stop-on-reply)
}

// veridianInitRepositories : dépôts Veridian et décorateur de quota (fin d'InitRepositories).
func (a *App) veridianInitRepositories() {
	// === Veridian patch === Wrap messageHistoryRepo with quota increment
	// decorator. Le decorator incrémente veridian_plan.emails_sent_this_month
	// apres chaque Create reussi → paywall middleware peut enforcer le quota
	// mensuel. Sans cette decoration, le compteur reste a 0 et le paywall ne
	// bloque jamais sur le quota (bug détecté 2026-05-20).
	// veridianPlanRepo est créé plus bas dans la fonction (ligne ~450) — on
	// le crée maintenant en avance pour pouvoir wrapper.
	a.veridianPlanRepo = repository.NewVeridianPlanRepository(a.db)
	a.messageHistoryRepo = repository.NewVeridianMessageHistoryDecorator(a.messageHistoryRepo, a.veridianPlanRepo, a.logger)

	// === Veridian patches ===
	// veridianPlanRepo est deja initialise plus haut (avant le decorator
	// messageHistoryRepo). On garde juste l'init du repo idempotency ici.
	a.veridianIdempotencyRepo = repository.NewVeridianIdempotencyRepository(a.db)

	// === Veridian patch — Lot K (2026-05-21) === Grace period repo pour
	// rotate-api-key (CONTRAT-HUB §5.15). Voir migration V41 + service
	// VeridianAPIKeyGraceCleanupService.
	a.veridianAPIKeyGraceRepo = repository.NewVeridianAPIKeyGraceRepository(a.db)

	// === Veridian patch — mission "API & agents" (2026-10-03) === Repo pour
	// la table agent_install_tokens (migration V59) : jetons d'installation
	// à usage unique du parcours "Brancher mon agent".
	a.agentInstallTokenRepo = repository.NewAgentInstallTokenRepository(a.db)

	// === Veridian patch — Freeze member per-user (CONTRAT-HUB §5.21, 2026-05-25) ===
	// Repo Postgres pour la table veridian_frozen_members (migration V47).
	// Utilise par : (a) service.FreezeMember/UnfreezeMember, (b) middleware
	// paywall per-user pour decider 402 user_frozen vs reads obfusques.
	a.veridianFrozenMemberRepo = repository.NewVeridianFrozenMemberRepository(a.db)

	// === Veridian patch — Lot 1 sprint cold (2026-06-15) === Repo Postgres pour
	// l'idempotence du poller IMAP self-service (table système
	// veridian_imap_uid_seen, migration V50). Consommé par le poller pour ne
	// jamais re-dispatcher un UID déjà traité aux lots downstream (bounce-loop,
	// stop-on-reply). Cf. internal/domain/veridian_imap_integration.go.
	a.veridianIMAPUIDSeenRepo = repository.NewVeridianIMAPUIDSeenRepository(a.db)

	// === Veridian patch — Lot 3 sprint cold (2026-06-15) === Repo Postgres pour
	// le signal durable stop-on-reply (table workspace veridian_contact_reply,
	// migration V51). Source de vérité "ce contact a répondu", consommée par le
	// gate d'exit de séquence (Lot 9) via ColdReplyChecker et par l'exit actif des
	// automations. Cf. internal/domain/veridian_contact_reply.go.
	a.veridianContactReplyRepo = repository.NewVeridianContactReplyRepository(a.workspaceRepo)
}

// veridianInitServices : services, collaborateurs de worker et d'exécuteur, émetteur webhook (fin d'InitServices).
func (a *App) veridianInitServices(automationExecutor *service.AutomationExecutor) {
	// === Veridian patch — mission "API & agents" (2026-10-03) ===
	a.workspaceService.SetAgentInstallTokenRepository(a.agentInstallTokenRepo)

	a.emailQueueWorker.SetAutomationSendGuard(
		a.automationRepo,
		a.contactListRepo,
		a.veridianContactReplyRepo,
	)

	// Veridian: rendu au depilage (le contenu des automations suit le modele courant).
	a.emailQueueWorker.SetQueuedEmailRenderer(service.NewVeridianQueueEmailRenderer(
		a.templateRepo,
		a.listRepo,
		a.contactRepo,
		a.automationRepo,
		a.config.APIEndpoint,
	))

	// Veridian (fiche 62, lot 1) : journal des decisions d'envoi (niveau par workspace,
	// "transitions" par defaut). Best-effort, retention opportuniste, aucune tache planifiee.
	a.emailQueueWorker.SetDecisionLog(repository.NewVeridianSendDecisionRepository(a.workspaceRepo))

	// === Veridian patch — Lot 3 sprint cold (2026-06-15) === Service stop-on-reply.
	// Détecte les réponses prospect (consommées via le poller IMAP, branché plus bas)
	// et pose le signal 'replied' + exit actif des automations. Il EST aussi le
	// ColdReplyChecker du Lot 9 : injecté dans l'executor via SetColdReplyChecker pour
	// que le gate d'exit de séquence sorte un contact dès qu'il a répondu, y compris
	// au milieu d'un délai de relance. Cf. internal/service/veridian_reply_service.go.
	a.veridianReplyService = service.NewVeridianReplyService(
		a.veridianContactReplyRepo,
		a.messageHistoryRepo,
		a.contactRepo,
		a.automationRepo,
		a.contactTimelineRepo,
		a.logger,
	)

	// Branche le checker stop-on-reply (Lot 3) sur le gate d'exit cold (Lot 9).
	automationExecutor.SetColdReplyChecker(a.veridianReplyService)
	// Veridian lot 0 : le nœud webhook déchiffre son secret avec la passphrase serveur.
	automationExecutor.SetWebhookSecretKey(a.config.Security.SecretKey)
	// Veridian fix 2026-09-29 (todo/done/2026-09-29-automation-advance-on-send-only.md) :
	// le noeud email ne fait plus avancer le contact a l'enqueue - il reste parque
	// (status=sending) jusqu'a ce que le worker de la file confirme l'issue reelle
	// de l'envoi. Ces callbacks ferment la boucle : avance seulement si le SMTP a
	// vraiment accepte, sortie avec raison sur rejet definitif / retries epuises,
	// aucun changement si le gate reporte (cap/warmup/fenetre) - le contact reste
	// parque, la file retente seule en arriere-plan.
	a.emailQueueWorker.SetCallbacks(automationExecutor.HandleEmailSent, automationExecutor.HandleEmailFailed)

	// === Veridian patches ===
	a.veridianWebhookEmitter = service.NewVeridianWebhookEmitter(
		a.config.HubWebhookURL,
		a.config.HubWebhookSecret,
		a.logger,
	)
	// === Veridian patch — events comportementaux cold↔web (2026-06-17) ===
	// Le Hub a livré un réconciliateur de scoring prospect (ingestProspectEvent) qui
	// attend email.opened/clicked/replied. On injecte l'emitter (créé ci-dessus) dans
	// les services qui détiennent la donnée d'engagement : EmailService (open/click via
	// les pixels /t/ et redirects /r/) et VeridianReplyService (reply via le poller IMAP).
	// DI optionnelle nil-safe : emitter noop si HUB_WEBHOOK_URL/SECRET absents.
	a.emailService.SetVeridianWebhookEmitter(a.veridianWebhookEmitter)
	if a.veridianReplyService != nil {
		a.veridianReplyService.SetVeridianWebhookEmitter(a.veridianWebhookEmitter)
	}
	// V38 : injecter le webhook emitter dans le plan repo pour que
	// IncrementEmailsSent puisse émettre tenant.activity_threshold_reached
	// quand le seuil 5 mails est franchi. Le repo est créé en ligne 435
	// avant le service (besoin de décorateur messageHistoryRepo), mais
	// l'emitter est créé ici — on le patch via WithWebhookEmitter.
	a.veridianPlanRepo = repository.WithWebhookEmitter(a.veridianPlanRepo, a.veridianWebhookEmitter, a.logger)
	a.veridianService = service.NewVeridianService(
		a.workspaceService,
		a.workspaceRepo,
		a.userService,
		a.userRepo,
		a.veridianPlanRepo,
		a.veridianWebhookEmitter,
		a.config.VeridianDefaultPlan,
		a.config.RootEmail,
		a.config.APIEndpoint,
		a.config.HubAPISecret,
		a.logger,
	)

	// === Veridian patch — Lot K (2026-05-21) === Injecter le grace repo
	// post-construction (CONTRAT-HUB §5.15 rotate-api-key). Sans ça,
	// RotateAPIKey retournera ErrAPIKeyGraceRepoNotConfigured.
	if a.veridianAPIKeyGraceRepo != nil {
		if err := service.ConfigureAPIKeyGraceSupport(a.veridianService, a.veridianAPIKeyGraceRepo); err != nil {
			a.logger.WithField("error", err.Error()).Warn("ConfigureAPIKeyGraceSupport failed — rotate-api-key endpoint disabled")
		}
	}

	// === Veridian patch — Freeze member per-user (2026-05-25) === Injecter
	// le frozen_member repo post-construction (CONTRAT-HUB §5.21). Sans ça,
	// FreezeMember/UnfreezeMember retournent ErrFrozenMemberRepoNotConfigured.
	if a.veridianFrozenMemberRepo != nil {
		if err := service.ConfigureFrozenMemberSupport(a.veridianService, a.veridianFrozenMemberRepo); err != nil {
			a.logger.WithField("error", err.Error()).Warn("ConfigureFrozenMemberSupport failed — freeze/unfreeze-member endpoints disabled")
		}
	}

	// === Veridian patch — 2026-05-23 === Injecter templateService +
	// transactionalNotificationService pour le seed du template
	// invitation-prospection au Provision (cf. veridian_seed_templates.go).
	// Best-effort : si non câblé, le seed est skippé silencieusement.
	if a.templateService != nil && a.transactionalNotificationService != nil {
		if err := service.ConfigureSeedTemplatesSupport(a.veridianService, a.templateService, a.transactionalNotificationService); err != nil {
			a.logger.WithField("error", err.Error()).Warn("ConfigureSeedTemplatesSupport failed — invitation-prospection seed disabled")
		}
	}
}

// veridianInitHandlers : handlers et routes Veridian (fin d'InitHandlers).
func (a *App) veridianInitHandlers() error {
	getJWTSecret := func() ([]byte, error) {
		if len(a.config.Security.JWTSecret) == 0 {
			return nil, fmt.Errorf("JWT secret not configured")
		}
		return a.config.Security.JWTSecret, nil
	}

	// === Veridian patch — mission "API & agents" (2026-10-03) === Routes
	// publiques /agent/* (install.sh, CLI, skill) + échange de jeton
	// d'installation. apiHost derive de APIEndpoint (jamais du Host client).
	agentAPIHost := strings.TrimPrefix(strings.TrimPrefix(a.config.APIEndpoint, "https://"), "http://")
	if idx := strings.Index(agentAPIHost, "/"); idx != -1 {
		agentAPIHost = agentAPIHost[:idx]
	}
	agentHandler := httpHandler.NewAgentHandler(
		a.workspaceService,
		a.logger,
		a.config.AgentCLIBinaryPath,
		agentAPIHost,
	)
	agentHandler.RegisterRoutes(a.mux)

	// === Veridian patch ===
	// On route les endpoints analytics via le wrapper veridian pour aligner le
	// error-shape sur le standard {"error": "<string>"} (l'upstream renvoyait
	// {"error": true, "message": ...}). Cf. veridian_analytics_handler.go +
	// ticket todo/2026-06-17-analytics-handler-error-shape-non-standard.md.
	// Le handler upstream NewAnalyticsHandler n'est PLUS enregistré dans le mux
	// (le wrapper prend les mêmes routes /api/analytics.query + .schemas — les
	// enregistrer tous les deux ferait paniquer le mux sur un pattern dupliqué).
	analyticsHandler := httpHandler.NewVeridianAnalyticsHandler(
		a.analyticsService,
		getJWTSecret,
		a.logger,
	)
	analyticsHandler.RegisterRoutes(a.mux)

	// === Veridian patches ===
	// 6 endpoints /api/tenants/* proteges par middleware HMAC Hub.
	// Si HUB_API_SECRET vide, RegisterRoutes attache quand meme les routes
	// mais le middleware renverra 503 (mode self-hosted, Hub absent).
	//
	// PaywallCache partage : injecte ici dans VeridianHandler ET passe au
	// middleware paywall en Start(). Permet a /api/veridian/admin/cache/invalidate
	// de purger une entree sans attendre le TTL (60s). Critique pour reduire
	// le wall-clock e2e CI paywall (gain ~5min/run).
	a.veridianPaywallCache = middleware.NewPaywallCache()
	// === Veridian patch — Freeze member per-user (2026-05-25) === Cache
	// partage middleware frozen + handler freeze/unfreeze. Permet
	// l'invalidation immediate post-mutation sans attendre le TTL 60s
	// (critique pour les E2E qui freeze puis ecrit immediatement).
	a.veridianFrozenCache = middleware.NewFrozenMemberCache()
	veridianHandler := httpHandler.NewVeridianHandler(a.veridianService, a.logger)
	veridianHandler.SetPaywallCache(a.veridianPaywallCache)
	veridianHandler.SetFrozenCache(a.veridianFrozenCache)
	veridianHandler.SetIdempotencyRepo(a.veridianIdempotencyRepo)

	// === Veridian patch — lot O (2026-05-21) ===
	// Cache pricing Hub : fetch + cron 1h, log warn divergence. Best-effort
	// (si Hub down, on garde le cache precedent et on continue avec les
	// valeurs hardcodees `domain.DefaultPlanLimits`). Le service est demarre
	// plus bas (cf. bloc "Start Veridian pricing sync"). Le handler debug
	// /api/veridian/admin/pricing-cache expose le snapshot du cache.
	//
	// L'URL est derivee de HUB_BASE_URL (host public Veridian = app.veridian.site
	// en prod, hub.staging.veridian.site en staging) pour rester alignee sur le
	// reste du cablage Hub (invitation client, webhooks). Avant le 2026-06-13 on
	// passait "" -> le service tombait sur un default `hub.veridian.site` qui
	// n'existe PAS en DNS public -> `VeridianPricingSync: fetch failed` en boucle,
	// cache jamais rafraichi (cf. todo/done pricing-sync-dns-hub-failed). Si
	// HUB_BASE_URL est vide (self-hosted sans Hub), pricingURL reste "" et le
	// service applique son default constant (corrige).
	pricingURL := ""
	if a.config.HubBaseURL != "" {
		pricingURL = strings.TrimRight(a.config.HubBaseURL, "/") + "/api/pricing/plans"
	}
	a.veridianPricingSync = service.NewVeridianPricingSyncService(pricingURL, nil, a.logger, 0)
	veridianHandler.SetPricingSync(a.veridianPricingSync)

	// === Veridian patch — 2026-05-24 — cron auto-cleanup orphans staging ===
	// Garde-fou strict : ne tourne QUE si cfg.Environment == "staging". Sur
	// prod / dev / demo / vide, NewVeridianTestTenantsCleanupService est
	// instancie quand meme (pour que le handler stats puisse retourner
	// enabled:false), mais Start() est un no-op silencieux. Cf.
	// todo/2026-05-24-staging-db-pool-orphan-cleanup-auto.md.
	a.veridianTestTenantsCleanup = service.NewVeridianTestTenantsCleanupService(
		a.veridianService,
		a.workspaceRepo,
		a.logger,
		a.config.Environment,
		0, // 0 = default 30 min
	)
	veridianHandler.SetTestTenantsCleanup(a.veridianTestTenantsCleanup)

	// === Veridian patch — 2026-06-15 === Endpoint de test cold lifecycle
	// (POST /api/veridian/admin/cold-simulate). STAGING-ONLY (l'env est passé
	// au handler qui refuse en 503 hors staging). Branche le VRAI service
	// stop-on-reply (a.veridianReplyService) + le repo message_history réel +
	// le workspace repo (secret key pour chiffrer message_data au seed). Les
	// E2E cold-lifecycle.spec.ts frappent cet endpoint pour valider stop-on-reply
	// et le cap destinataire sans envoi de mail ni IMAP réel.
	veridianHandler.SetColdSimulate(
		a.veridianReplyService,
		a.messageHistoryRepo,
		a.workspaceRepo,
		a.config.Environment,
	)

	// === Veridian patch — 2026-06-18 — DROP FORCE de rattrapage + GC orphelins ===
	// (todo/2026-06-17-orphan-workspaces-staging-db-starvation.md)
	// (1) Active le DROP DATABASE ... WITH (FORCE) de rattrapage dans wipeOneTenant
	//     (le DROP upstream sans FORCE échoue sur la race de connexions worker →
	//     689 bases orphelines staging). Préfixe = config.Database.Prefix. Actif
	//     PARTOUT (le rattrapage n'est jamais destructif sur une base à record).
	// (2) Endpoint GC STAGING-ONLY POST/GET /api/veridian/admin/gc-orphan-workspace-dbs
	//     qui DROP les bases physiques sans record (FORCE, séquentiel, exclut canary).
	//     Le handler gate sur a.config.Environment == "staging" (503 sinon).
	// Détection par type-assertion (le service concret *veridianService est
	// non-exporté ; ces capacités sont des méthodes veridian_*).
	if cleaner, ok := a.veridianService.(interface{ ConfigureWorkspaceDBCleanup(string) }); ok {
		cleaner.ConfigureWorkspaceDBCleanup(a.config.Database.Prefix)
	}
	if gcRunner, ok := a.veridianService.(httpHandler.VeridianOrphanDBGCRunner); ok {
		veridianHandler.SetOrphanDBGC(gcRunner, a.config.Environment)
	}
	// Route admin email-integration.verify (HMAC) : transport reel vers un 'to' libre.
	veridianHandler.SetEmailIntegrationVerifier(a.emailService)

	// === Veridian patch — Lot 1 sprint cold (2026-06-15) — poller IMAP ===
	// BRIQUE FONDATRICE. Poll les boîtes IMAP configurées (Integration de type
	// "imap" par workspace) et dispatche les messages neufs aux consumers
	// enregistrés par les lots downstream (bounce-loop, stop-on-reply) via
	// RegisterConsumer. Best-effort, démarré dans Start().
	//
	// 🔌 LOTS 2/3 — POINT D'ENREGISTREMENT : ajouter ICI, juste après la
	// création, vos appels :
	//     a.veridianIMAPPoller.RegisterConsumer(<votreConsumer>)
	// Tant qu'AUCUN consumer n'est enregistré, Start() est un no-op volontaire
	// (poller une boîte pour dispatcher à personne = travail inutile). Le poller
	// s'active automatiquement dès qu'un consumer est branché ici.
	a.veridianIMAPPoller = queue.NewVeridianIMAPPollerService(
		a.workspaceRepo,
		a.veridianIMAPUIDSeenRepo,
		a.logger,
		0, // 0 = default tick 30s
		0, // 0 = default lookback 7j
	)

	// Lot 2 cold — bounce-loop : détecte les NDR Postfix rapatriés par le poller
	// IMAP et déclenche la suppression du contact via la chaîne webhook SMTP
	// existante (ProcessWebhook → ClassifyBounce → MarkEmailsAsBounced). Son
	// enregistrement ACTIVE le poller (no-op sans consumer).
	veridianBounceConsumer := service.NewVeridianBounceConsumer(
		a.inboundWebhookEventService,
		a.workspaceRepo,
		a.logger,
	)
	a.veridianIMAPPoller.RegisterConsumer(veridianBounceConsumer)

	// Lot 3 cold — stop-on-reply : détecte qu'un prospect a répondu (match fort par
	// Message-ID, fallback expéditeur, NDR exclus via pkg/veridian_ndr) et stoppe sa
	// cadence (signal durable + exit actif des automations + gate Lot 9). Best-effort,
	// idempotent. Son enregistrement (comme le bounce-loop) garde le poller actif.
	veridianReplyConsumer := service.NewVeridianReplyConsumer(a.veridianReplyService, a.logger)
	a.veridianIMAPPoller.RegisterConsumer(veridianReplyConsumer)

	// === Veridian patch — Mail provider choice (V48) SUPPRIMÉ 2026-05-31 ===
	// Le pipeline "envoi via Hub Mail Gateway" (mail-provider-choice + proxy
	// mail-accounts) a été retiré : il créait une dépendance Hub sur l'envoi
	// (aberration vs règle d'or stand-alone) et n'était jamais câblé à l'envoi
	// réel (email_service.go ignore MailProviderChoice). L'envoi passe
	// uniquement par le provider configuré par workspace (Settings >
	// Integrations, natif upstream : SMTP/SES/...). Cf. memory
	// project_mail_sending_standalone_decision.

	veridianHandler.RegisterRoutes(a.mux, a.config.HubAPISecret)

	// Endpoint generateMagicLink (auth API key tenant Notifuse).
	veridianMagicHandler := httpHandler.NewVeridianMagicHandler(
		a.veridianService,
		a.workspaceRepo,
		getJWTSecret,
		a.logger,
	)
	veridianMagicHandler.SetManagedKeyChecker(func(ctx context.Context, userID string) (bool, error) {
		u, err := a.userService.GetUserByID(ctx, userID)
		if err != nil || u == nil {
			return false, err
		}
		return u.VeridianManaged, nil
	})
	veridianMagicHandler.RegisterRoutes(a.mux)

	// === Veridian patch === Endpoint /veridian/auto-login (token HMAC dans URL).
	// Page HTML qui stocke le JWT dans localStorage et redirect /console.
	veridianAutoLoginHandler := httpHandler.NewVeridianAutoLoginHandler(
		a.userService,
		a.config.HubAPISecret,
		a.config.APIEndpoint,
		a.logger,
	)
	veridianAutoLoginHandler.RegisterRoutes(a.mux)

	// === Veridian patch lot 2026-05-23 — Hub Discovery client ===
	// Endpoint GET /api/veridian/hub-discovery/me : la console l'appelle
	// post-login (best-effort) pour decouvrir si l'email du user est
	// connu cote Hub et pre-charger les liens cross-app du dashboard.
	// Si HUB_API_SECRET vide -> client disabled, repond hub_available=false.
	// Le call Hub est non-bloquant (timeout 2s, fail-safe). Cf. ticket
	// todo/2026-05-23-call-hub-discovery-by-email.md + CONTRAT-HUB §6.5.
	hubDiscoveryClient := hub_discovery.NewClient(hub_discovery.Config{
		BaseURL: a.config.HubBaseURL, // vide -> DefaultHubBaseURL (app.veridian.site) ; surcharge staging via HUB_BASE_URL
		Secret:  a.config.HubAPISecret,
		Logger:  a.logger,
	})
	veridianHubDiscoveryHandler := httpHandler.NewVeridianHubDiscoveryHandler(
		hubDiscoveryClient,
		a.userService,
		getJWTSecret,
		a.logger,
	)
	veridianHubDiscoveryHandler.RegisterRoutes(a.mux)

	// === Veridian patch — R1 breakdown contacts par classe de provider (2026-06-14) ===
	// Endpoint POST+GET /api/veridian/contacts.providerBreakdown : compte les
	// contacts par classe de provider destinataire (google/microsoft/yahoo_aol/
	// freemail_fr/corporate) pour dimensionner le throttle cold outbound. Auth
	// JWT console + permission contacts:read (gardien dans le service). Cf.
	// todo/2026-06-14-tunnel-vente-ui-controle-et-roadmap.md (R1).
	veridianContactBreakdownRepo := repository.NewVeridianContactBreakdownRepository(a.workspaceRepo)
	veridianContactBreakdownService := service.NewVeridianContactBreakdownService(
		veridianContactBreakdownRepo,
		a.authService,
		a.logger,
	)
	veridianContactBreakdownHandler := httpHandler.NewVeridianContactBreakdownHandler(
		veridianContactBreakdownService,
		getJWTSecret,
		a.logger,
	)
	veridianContactBreakdownHandler.RegisterRoutes(a.mux)

	// === Veridian patch — KPI reply rate (taux de réponse cold, 2026-06-16) ===
	// Endpoint POST+GET /api/veridian/messages.replyStats : compte les contacts
	// ayant répondu (signal stop-on-reply Lot 3, table veridian_contact_reply) sur
	// la fenêtre [start, end] du dashboard. En cold outreach le reply rate est LE
	// KPI #1 (conversion réelle). Le ratio replied/sent est calculé côté front avec
	// le count_sent déjà chargé par EmailMetricsChart. Auth JWT console + permission
	// contacts:read (gardien dans le service, comme le breakdown R1 — le reply est
	// une donnée contact). PAS de greffe dans l'analytics : la donnée vit dans une
	// table séparée. Cf. todo/2026-06-16-kpi-reply-rate-dashboard.md.
	veridianReplyStatsService := service.NewVeridianReplyStatsService(
		a.veridianContactReplyRepo,
		a.authService,
		a.logger,
	)
	veridianReplyStatsHandler := httpHandler.NewVeridianReplyStatsHandler(
		veridianReplyStatsService,
		getJWTSecret,
		a.logger,
	)
	veridianReplyStatsHandler.RegisterRoutes(a.mux)

	// === Veridian patch, lot 5 (08/10/2026) : agrégats du tableau de bord de prospection ===
	// GET|POST /api/veridian/prospection.stats : réponses par séquence et par liste,
	// avancement des séquences, stock par liste. Lecture seule (contacts:read et
	// message_history:read vérifiés dans le service).
	veridianProspectionStatsService := service.NewVeridianProspectionStatsService(
		repository.NewVeridianProspectionStatsRepository(a.workspaceRepo),
		a.authService,
		a.logger,
	)
	httpHandler.NewVeridianProspectionStatsHandler(
		veridianProspectionStatsService,
		getJWTSecret,
		a.logger,
	).RegisterRoutes(a.mux)

	// === Veridian — lecture des inscriptions d'automation (27/10/2026) ===
	// GET /api/automations.enrollments : contacts inscrits, statut, noeud courant, prochaine date (curseur).
	httpHandler.NewVeridianAutomationEnrollmentsListHandler(
		service.NewVeridianAutomationEnrollmentService(
			repository.NewVeridianAutomationEnrollmentRepository(a.workspaceRepo),
			a.authService,
			a.logger,
		),
		getJWTSecret,
		a.logger,
	).RegisterRoutes(a.mux)

	// === Veridian — file d'envoi : lister et annuler (27/10/2026) ===
	// GET /api/veridian/queue.entries, POST /api/veridian/queue.cancel (journal decisions.list).
	httpHandler.NewVeridianQueueEntriesHandler(
		service.NewVeridianQueueEntriesService(
			repository.NewVeridianQueueEntriesRepository(a.workspaceRepo, nil),
			a.authService,
			a.logger,
		),
		getJWTSecret,
		a.logger,
	).RegisterRoutes(a.mux)

	// === Veridian patch, fiche 62 lot 1 (10/10/2026) : « pourquoi ca n'envoie pas » ===
	// GET|POST /api/veridian/queue.explain (entrees de file groupees par automation, noeud,
	// raison, profil ; --entry = detail gate par gate), GET|POST /api/veridian/decisions.list
	// (journal des decisions), POST /api/veridian/queue.recompute (remise a zero bornee).
	// Permissions automations:read / automations:write verifiees dans le service.
	httpHandler.NewVeridianQueueExplainHandler(
		service.NewVeridianQueueExplainService(
			repository.NewVeridianQueueExplainRepository(a.workspaceRepo),
			repository.NewVeridianSendDecisionRepository(a.workspaceRepo),
			a.workspaceRepo,
			a.authService,
			a.logger,
		),
		getJWTSecret,
		a.logger,
	).RegisterRoutes(a.mux)

	// === Veridian patch — fusible de réputation, API de lecture (2026-09-29) ===
	// Endpoint POST+GET /api/veridian/messages.reputationStatus : signal
	// "visible dans l'interface ou l'API" du fusible qui gèle automatiquement une
	// infra dont le taux de bounce dur dépasse 3%/7j ou qui reçoit une plainte
	// (cf. internal/service/queue/veridian_reputation_gate.go). Auth JWT console +
	// permission message_history:read (gardien dans le service, même posture que
	// le reply rate). Cf. internal/domain/veridian_reputation_status.go.
	veridianReputationStatusService := service.NewVeridianReputationStatusService(
		a.messageHistoryRepo,
		a.workspaceRepo,
		a.authService,
		a.logger,
	)
	veridianReputationStatusHandler := httpHandler.NewVeridianReputationStatusHandler(
		veridianReputationStatusService,
		getJWTSecret,
		a.logger,
	)
	veridianReputationStatusHandler.RegisterRoutes(a.mux)

	// === Veridian patch — KPI engagement par classe de provider (dashboard cold, 2026-06-16) ===
	// Endpoint POST+GET /api/veridian/messages.engagementByClass : agrège
	// sent/delivered/bounced/opened/clicked PAR CLASSE de provider destinataire
	// sur la fenêtre de dates, pour repérer une classe qui se dégrade (bounce
	// rate Microsoft qui monte = signal d'arrêt AVANT de griller le domaine). La
	// classe n'est PAS une dimension de message_history (Lot 4) → le repo agrège
	// par DOMAINE en SQL, le service mappe domaine → classe en Go (réutilise
	// veridian_provider_class.go, zéro CASE SQL). Auth JWT console + permission
	// contacts:read (gardien dans le service, comme breakdown R1 / reply stats).
	// Cf. todo/2026-06-16-kpi-engagement-par-classe-provider.md.
	veridianEngagementByClassRepo := repository.NewVeridianEngagementByClassRepository(a.workspaceRepo)
	veridianEngagementByClassService := service.NewVeridianEngagementByClassService(
		veridianEngagementByClassRepo,
		a.authService,
		a.logger,
	)
	veridianEngagementByClassHandler := httpHandler.NewVeridianEngagementByClassHandler(
		veridianEngagementByClassService,
		getJWTSecret,
		a.logger,
	)
	veridianEngagementByClassHandler.RegisterRoutes(a.mux)

	// Exact daily usage per sending integration/profile (V56). The console reads
	// this endpoint; it never estimates quota consumption client-side.
	veridianEmailProfileUsageRepo := repository.NewVeridianEmailProfileUsageRepository(a.workspaceRepo)
	veridianEmailProfileUsageService := service.NewVeridianEmailProfileUsageService(
		veridianEmailProfileUsageRepo, a.workspaceRepo, a.authService, a.logger,
	)
	veridianEmailProfileUsageHandler := httpHandler.NewVeridianEmailProfileUsageHandler(
		veridianEmailProfileUsageService, getJWTSecret, a.logger,
	)
	veridianEmailProfileUsageHandler.RegisterRoutes(a.mux)

	// Lot 2 « verite d'un profil » (08/10/2026) : GET|POST
	// /api/veridian/emailProfiles.overview. Tous les profils email (commerciaux et
	// transactionnels) avec EffectivePlan, la fonction partagee avec le worker.
	veridianEmailProfileOverviewRepo := repository.NewVeridianEmailProfileOverviewRepository(a.workspaceRepo)
	veridianEmailProfileOverviewService := service.NewVeridianEmailProfileOverviewService(
		veridianEmailProfileOverviewRepo, a.messageHistoryRepo, a.workspaceRepo, a.authService, a.logger,
	)
	httpHandler.NewVeridianEmailProfileOverviewHandler(
		veridianEmailProfileOverviewService, getJWTSecret, a.logger,
	).RegisterRoutes(a.mux)

	// Lot 3 « page Profils d envoi » (08/10/2026) : POST /api/veridian/emailProfiles.create,
	// profil SMTP (+ IMAP lie) cree en une seule ecriture, reserve au proprietaire.
	httpHandler.NewVeridianEmailProfileCreateHandler(
		service.NewVeridianEmailProfileCreateService(a.workspaceRepo, a.authService, a.config.Security.SecretKey, a.logger),
		getJWTSecret, a.logger,
	).RegisterRoutes(a.mux)

	// Lot 4 « separation transactionnel / commercial » (08/10/2026) : POST
	// /api/veridian/emailProfiles.setUsage|pause|resume. L'usage d'un profil
	// (commercial en rotation, transactionnel, hors service) et sa pause, valides
	// cote serveur et ecrits en une fois. Droit : ecriture sur le workspace.
	veridianProfilePolicyRepo, _ := a.emailQueueRepo.(domain.EmailIntegrationPolicyQueueRepository)
	httpHandler.NewVeridianEmailProfileAdminHandler(
		service.NewVeridianEmailProfileAdminService(a.workspaceRepo, a.authService, veridianProfilePolicyRepo, a.logger),
		getJWTSecret, a.logger,
	).RegisterRoutes(a.mux)

	// === Veridian patch — linter de délivrabilité (spam score) cold (2026-06-15) ===
	// Endpoint POST+GET /api/veridian/templates.deliverabilityScore : score 0-10
	// (façon SpamAssassin) + règles déclenchées avec poids, sur un template cold
	// RENDU (Liquid+spintax résolus). Moteur = package pur pkg/veridian_deliverability
	// (zéro I/O, instantané). Modes strict (Google/MS) / lenient (petits providers)
	// déduits de la classe destinataire. Auth JWT console + permission templates:read
	// (gardien dans le service). Cf.
	// todo/2026-06-15-linter-deliverabilite-spam-score-templates.md.
	veridianDeliverabilityScoreService := service.NewVeridianDeliverabilityScoreService(
		a.authService,
		a.logger,
	)
	veridianDeliverabilityScoreHandler := httpHandler.NewVeridianDeliverabilityScoreHandler(
		veridianDeliverabilityScoreService,
		getJWTSecret,
		a.logger,
	)
	veridianDeliverabilityScoreHandler.RegisterRoutes(a.mux)

	// === Veridian patch — automations.enroll (sprint AI-first API, 2026-06-15) ===
	// Endpoint POST /api/automations.enroll : enrôle un/des contact(s) au node
	// d'entrée d'une automation LIVE par API (maillon manquant du pilotage
	// AI-first). Réutilise la fonction SQL automation_enroll_contact via
	// AutomationRepository.EnrollContact (même chemin que le trigger timeline) —
	// zéro réimplémentation de l'executor. Auth JWT console + permission
	// automations:write (gardien dans le service). Idempotent : contact déjà actif
	// = no-op "already_active".
	veridianAutomationEnrollService := service.NewVeridianAutomationEnrollService(
		a.automationRepo,
		a.authService,
		a.logger,
	)
	veridianAutomationEnrollHandler := httpHandler.NewVeridianAutomationEnrollHandler(
		veridianAutomationEnrollService,
		getJWTSecret,
		a.logger,
	)
	veridianAutomationEnrollHandler.RegisterRoutes(a.mux)

	// === Veridian patch — Mail accounts proxy SUPPRIMÉ 2026-05-31 ===
	// Retiré avec le pipeline mail-provider (cf. ci-dessus). La gestion des
	// comptes d'envoi se fait via Settings > Integrations (provider local par
	// workspace), pas via un proxy vers le Hub.

	// === Veridian patch — Hub invitation flow (2026-05-23) ===
	// Endpoint POST /api/veridian/workspaces.inviteMember : delegue
	// l'invitation cross-app au Hub. Quand owner Notifuse clique "Inviter
	// membre" en mode managed, le front appelle ce handler au lieu de
	// l'endpoint upstream /api/workspaces.inviteMember. Cf. todo/
	// 2026-05-20-hub-invitation-flow-multi-membre.md.
	//
	// managedMode := (HUB_API_SECRET != "" && HUB_INVITATION_SECRET_NOTIFUSE != "").
	// Si l'un manque, l'endpoint renvoie 503 avec un message explicite.
	hubInvitationManaged := a.config.HubAPISecret != "" && a.config.HubInvitationSecretNotifuse != ""
	hubInvitationClient := service.NewVeridianHubInvitationClient(
		a.config.HubBaseURL,
		a.config.HubInvitationSecretNotifuse,
		nil, // default timeout
		a.logger,
	)
	veridianInviteHandler := httpHandler.NewVeridianInviteMemberHandler(
		hubInvitationClient,
		a.authService,
		a.userService,
		httpHandler.NewSQLHubUserIDResolver(a.db),
		getJWTSecret,
		a.logger,
		hubInvitationManaged,
	)
	veridianInviteHandler.RegisterRoutes(a.mux)
	if !hubInvitationManaged {
		a.logger.WithFields(map[string]interface{}{
			"hub_api_secret_set":             a.config.HubAPISecret != "",
			"hub_invitation_secret_notifuse": a.config.HubInvitationSecretNotifuse != "",
		}).Warn("Veridian hub invitation endpoint disabled (self-hosted or secret missing)")
	}

	return nil
}

// veridianStartBackground : tâches de fond Veridian historiques (démarrées dans Start() avant le jalon 2).
func (a *App) veridianStartBackground() {
	// === Veridian patch 2026-05-20 — cron cleanup idempotency keys ===
	// Purge quotidienne des entrées expirées de veridian_idempotency_keys
	// (CONTRAT-HUB §5.11). DeleteExpired existait mais aucun cron upstream
	// ne l'appelait. Branché AVANT le rollout massif du header Idempotency-Key
	// côté Hub pour éviter dette qui devient urgente.
	// Cf. todo/2026-05-19-cron-cleanup-idempotency-keys.md
	if a.veridianIdempotencyRepo != nil {
		cleanup := service.NewVeridianIdempotencyCleanupService(
			a.veridianIdempotencyRepo,
			a.logger,
			24*time.Hour,
		)
		cleanup.Start(a.GetShutdownContext())
		a.logger.Info("Veridian idempotency cleanup scheduler started (24h interval)")
	}

	// === Veridian patch lot O — 2026-05-21 — pricing sync Hub ===
	// Fetch boot + cron 1h du catalogue pricing canonique expose par le Hub
	// (GET app.veridian.site/api/pricing/plans). Best-effort : si Hub down,
	// on garde le cache precedent et l'app continue de tourner avec ses
	// valeurs hardcodees `domain.DefaultPlanLimits`. Reconcile (log warn si
	// divergence) tourne a chaque fetch reussi.
	// Cf. todo/2026-05-21-consume-hub-pricing-api.md
	if a.veridianPricingSync != nil {
		a.veridianPricingSync.Start(a.GetShutdownContext())
		a.logger.Info("Veridian pricing sync scheduler started (1h interval)")
	}

	// === Veridian patch 2026-05-24 — cron auto-cleanup orphans staging ===
	// Start() est un no-op silencieux + log info "disabled" si Environment
	// != "staging". Le check garde-fou est dans le service, pas ici, pour
	// que le handler stats reste exploitable en prod (audit "enabled:false").
	// Cf. todo/2026-05-24-staging-db-pool-orphan-cleanup-auto.md
	if a.veridianTestTenantsCleanup != nil {
		a.veridianTestTenantsCleanup.Start(a.GetShutdownContext())
	}

	// === Veridian patch 2026-06-15 — poller IMAP self-service (Lot 1 cold) ===
	// Démarre le polling des boîtes IMAP configurées. Best-effort : Start() est
	// un no-op silencieux si les deps sont nil. Disabled en demo (pas de réseau
	// sortant vers des boîtes tierces). Cf.
	// internal/service/queue/veridian_imap_poller.go.
	if a.veridianIMAPPoller != nil && !a.config.IsDemo() {
		a.veridianIMAPPoller.Start(a.GetShutdownContext())
		a.logger.Info("Veridian IMAP poller scheduler started")
	}
}

// veridianWrapHandler : filtres Veridian de la chaîne HTTP (paywall, assets, soft-delete, gel de
// membre, autorisation workspace, journal d'accès), du plus interne au plus externe, posés avant le
// middleware d'arrêt gracieux. Le rate-limit global et les en-têtes de sécurité restent dans
// App.Start (garde-fou check-rate-limit-coverage.sh, ordre avec le traçage).
func (a *App) veridianWrapHandler(handler http.Handler) http.Handler {
	// === Veridian patch ===
	// Path-filtered paywall : intercepte les envois sur /api/transactional.send
	// et /api/broadcasts.{create,schedule,sendToIndividual}, laisse passer le
	// reste sans inspection. Place tot dans la chaine pour rejeter avant le
	// auth middleware si plan suspended ou quota depasse.
	handler = middleware.VeridianPaywallPathFilterWithCache(a.veridianPaywallCache, a.veridianPlanRepo, a.logger)(handler)

	// === Veridian patch — perf-ui-baseline (ticket 2026-05-22) ===
	// Optimise le serving des assets console (/console/assets/*) : gzip à
	// la volée + cache mémoire + Cache-Control immutable sur les assets
	// hashés. Sans ce filtre, le bundle Vite de ~6.3 MB est servi brut et
	// sans cache par le http.FileServer upstream. N'intercepte QUE
	// /console/assets/* — tout le reste passe direct. Voir
	// internal/http/veridian_static_handler.go.
	handler = httpHandler.VeridianStaticAssetsFilter(a.logger)(handler)

	// === Lot J 2026-05-21 — Mode dégradé soft-deleted (CONTRAT-HUB §5.9) ===
	// Wrappe le handler GLOBAL : si tenant soft-deleted (deleted_at != NULL),
	//   - GET/HEAD/OPTIONS : la response JSON est obfusquée (33% en clair)
	//   - POST/PUT/PATCH/DELETE : 402 + tenant_soft_deleted body
	// Exempts : /api/veridian/*, /api/tenants/*, /api/health, /api/version,
	// /api/auth/* — voir middleware.veridianSoftDeletedExemptPrefixes.
	//
	// Placé APRÈS le paywall path filter dans la chaîne d'application (donc
	// EXÉCUTÉ AVANT dans le flow request) pour que la réponse 402 standard
	// tenant_soft_deleted prime sur les autres décisions de gating (suspended,
	// HubSyncDead, feature gate). Cache partagé avec le paywall pour
	// économiser les round-trips DB sur les routes communes.
	handler = middleware.VeridianSoftDeletedFilterWithCache(a.veridianPaywallCache, a.veridianPlanRepo, a.logger)(handler)

	// === Freeze member per-user (CONTRAT-HUB §5.21, 2026-05-25) ===
	// Wrappe le handler GLOBAL : si user frozen sur le workspace courant,
	//   - GET/HEAD/OPTIONS : reads obfusques (header X-User-Frozen=true)
	//   - POST/PUT/PATCH/DELETE : 402 user_frozen body + unfreeze_url
	// Exempts : /api/veridian/*, /api/tenants/*, /api/health, /api/version,
	// /api/auth/* (HMAC + systeme — pas concernes par freeze user-side).
	//
	// Place APRES soft-deleted dans la chaine d'application (donc EXECUTE
	// AVANT dans le flow request) : soft-deleted prime sur frozen (un
	// tenant deleted court-circuite le freeze user). Cache decouple
	// (FrozenMemberCache, cle (workspace, user)) pour eviter de polluer
	// le PaywallCache existant cle workspace seul.
	//
	// frozenRepo nil = passthrough (mode self-hosted ou freeze pas configure).
	if a.veridianFrozenMemberRepo != nil {
		getJWTSecret := func() ([]byte, error) {
			if len(a.config.Security.JWTSecret) == 0 {
				return nil, fmt.Errorf("JWT secret not configured")
			}
			return a.config.Security.JWTSecret, nil
		}
		handler = middleware.VeridianFrozenMemberFilterWithCache(
			a.veridianFrozenCache,
			a.veridianFrozenMemberRepo,
			getJWTSecret,
			a.logger,
		)(handler)
	}

	// === Veridian (fiche 67) : autorisation workspace des routes sans garde
	// (webhookSubscriptions.*, segments.*, timeline.list). Au plus pres du mux.
	authzJWTSecret := func() ([]byte, error) {
		if len(a.config.Security.JWTSecret) == 0 {
			return nil, fmt.Errorf("JWT secret not configured")
		}
		return a.config.Security.JWTSecret, nil
	}
	handler = httpHandler.VeridianWorkspaceAuthzMiddleware(a.authService, authzJWTSecret)(handler)
	// Journal d'acces API (une ligne JSON par requete /api/*, stdout -> Loki).
	handler = httpHandler.VeridianAccessLogMiddleware(a.logger, func(ctx context.Context, userID string) ([]string, error) {
		uws, err := a.workspaceRepo.GetUserWorkspaces(ctx, userID)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(uws))
		for _, uw := range uws {
			ids = append(ids, uw.WorkspaceID)
		}
		return ids, nil
	})(handler)
	return handler
}
