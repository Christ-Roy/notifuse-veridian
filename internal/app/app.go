package app

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/database"
	"github.com/Notifuse/notifuse/internal/domain"
	httpHandler "github.com/Notifuse/notifuse/internal/http"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/migrations"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/service/broadcast"
	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/pkg/cache"
	pkgDatabase "github.com/Notifuse/notifuse/pkg/database"
	"github.com/Notifuse/notifuse/pkg/hub_discovery"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/mailer"
	"github.com/Notifuse/notifuse/pkg/ratelimiter"
	"github.com/Notifuse/notifuse/pkg/smtp_bridge"
	"github.com/Notifuse/notifuse/pkg/tracing"

	"contrib.go.opencensus.io/integrations/ocsql"
)

// AppInterface defines the interface for the App
type AppInterface interface {
	Initialize() error
	Start() error
	Shutdown(ctx context.Context) error

	// Getters for app components accessed in tests
	GetConfig() *config.Config
	GetLogger() logger.Logger
	GetMux() *http.ServeMux
	GetDB() *sql.DB
	GetMailer() mailer.Mailer

	// Repository getters for testing
	GetUserRepository() domain.UserRepository
	GetWorkspaceRepository() domain.WorkspaceRepository
	GetContactRepository() domain.ContactRepository
	GetListRepository() domain.ListRepository
	GetTemplateRepository() domain.TemplateRepository
	GetBroadcastRepository() domain.BroadcastRepository
	GetMessageHistoryRepository() domain.MessageHistoryRepository
	GetContactListRepository() domain.ContactListRepository
	GetTransactionalNotificationRepository() domain.TransactionalNotificationRepository
	GetTelemetryRepository() domain.TelemetryRepository
	GetEmailQueueRepository() domain.EmailQueueRepository
	GetTaskRepository() domain.TaskRepository

	// Service getters for testing
	GetAuthService() interface{} // Returns *service.AuthService but defined as interface{} to avoid import cycle
	GetTransactionalNotificationService() domain.TransactionalNotificationService
	GetEmailQueueWorker() *queue.EmailQueueWorker
	GetAutomationScheduler() *service.AutomationScheduler
	GetTaskScheduler() *service.TaskScheduler

	// Server status methods
	IsServerCreated() bool
	WaitForServerStart(ctx context.Context) bool

	// Methods for initialization steps
	InitDB() error
	InitMailer() error
	InitTracing() error
	InitRepositories() error
	InitServices() error
	InitHandlers() error

	// Graceful shutdown methods
	SetShutdownTimeout(timeout time.Duration)
	GetActiveRequestCount() int64
	GetShutdownContext() context.Context
}

// App encapsulates the application dependencies and configuration
type App struct {
	config      *config.Config
	logger      logger.Logger
	db          *sql.DB
	mailer      mailer.Mailer
	eventBus    domain.EventBus
	isInstalled bool // Indicates if setup wizard has been completed

	// Repositories
	userRepo                      domain.UserRepository
	workspaceRepo                 domain.WorkspaceRepository
	authRepo                      domain.AuthRepository
	settingRepo                   domain.SettingRepository
	contactRepo                   domain.ContactRepository
	listRepo                      domain.ListRepository
	contactListRepo               domain.ContactListRepository
	templateRepo                  domain.TemplateRepository
	broadcastRepo                 domain.BroadcastRepository
	taskRepo                      domain.TaskRepository
	transactionalNotificationRepo domain.TransactionalNotificationRepository
	messageHistoryRepo            domain.MessageHistoryRepository
	inboundWebhookEventRepo       domain.InboundWebhookEventRepository
	telemetryRepo                 domain.TelemetryRepository
	analyticsRepo                 domain.AnalyticsRepository
	contactTimelineRepo           domain.ContactTimelineRepository
	segmentRepo                   domain.SegmentRepository
	contactSegmentQueueRepo       domain.ContactSegmentQueueRepository
	blogCategoryRepo              domain.BlogCategoryRepository
	blogPostRepo                  domain.BlogPostRepository
	blogThemeRepo                 domain.BlogThemeRepository
	customEventRepo               domain.CustomEventRepository
	webhookSubscriptionRepo       domain.WebhookSubscriptionRepository
	webhookDeliveryRepo           domain.WebhookDeliveryRepository
	automationRepo                domain.AutomationRepository
	emailQueueRepo                domain.EmailQueueRepository

	// === Veridian patches ===
	veridianPlanRepo         domain.VeridianPlanRepository
	veridianIdempotencyRepo  domain.VeridianIdempotencyRepository
	veridianAPIKeyGraceRepo  domain.VeridianAPIKeyGraceRepository // Lot K — grace period rotate-api-key
	veridianFrozenMemberRepo domain.VeridianFrozenMemberRepository // §5.21 — freeze member per-user
	veridianIMAPUIDSeenRepo  domain.VeridianIMAPUIDSeenRepository  // Lot 1 cold — idempotence poller IMAP (V50)
	veridianContactReplyRepo domain.VeridianContactReplyRepository // Lot 3 cold — signal durable stop-on-reply (V51)
	veridianService          domain.VeridianService
	veridianWebhookEmitter   domain.WebhookEmitter
	veridianPaywallCache     *middleware.PaywallCache              // partage middleware paywall + handler invalidate
	veridianFrozenCache      *middleware.FrozenMemberCache         // partage middleware frozen + handler freeze/unfreeze invalidate
	veridianPricingSync      *service.VeridianPricingSyncService   // catalogue pricing Hub (lot O 2026-05-21)
	veridianTestTenantsCleanup *service.VeridianTestTenantsCleanupService // cron auto-cleanup orphans staging (2026-05-24)
	veridianIMAPPoller       *queue.VeridianIMAPPollerService      // Lot 1 cold — poller IMAP self-service (réception bounces/réponses)
	veridianReplyService     *service.VeridianReplyService         // Lot 3 cold — détection réponse + exit séquence (stop-on-reply)

	// Services
	authService                      *service.AuthService
	userService                      *service.UserService
	workspaceService                 *service.WorkspaceService
	contactService                   *service.ContactService
	listService                      *service.ListService
	contactListService               *service.ContactListService
	templateService                  *service.TemplateService
	templateBlockService             *service.TemplateBlockService
	emailService                     *service.EmailService
	broadcastService                 *service.BroadcastService
	taskService                      *service.TaskService
	transactionalNotificationService *service.TransactionalNotificationService
	systemNotificationService        *service.SystemNotificationService
	inboundWebhookEventService       *service.InboundWebhookEventService
	webhookRegistrationService       *service.WebhookRegistrationService
	messageHistoryService            *service.MessageHistoryService
	notificationCenterService        *service.NotificationCenterService
	demoService                      *service.DemoService
	telemetryService                 *service.TelemetryService
	analyticsService                 *service.AnalyticsService
	contactTimelineService           domain.ContactTimelineService
	segmentService                   *service.SegmentService
	blogService                      *service.BlogService
	settingService                   *service.SettingService
	setupService                     *service.SetupService
	supabaseService                  *service.SupabaseService
	taskScheduler                    *service.TaskScheduler
	dnsVerificationService           *service.DNSVerificationService
	customEventService               *service.CustomEventService
	webhookSubscriptionService       *service.WebhookSubscriptionService
	webhookDeliveryWorker            *service.WebhookDeliveryWorker
	automationService                *service.AutomationService
	automationScheduler              *service.AutomationScheduler
	llmService                       *service.LLMService
	emailQueueWorker                 *queue.EmailQueueWorker
	dataFeedFetcher                  broadcast.DataFeedFetcher
	// providers
	postmarkService  *service.PostmarkService
	mailgunService   *service.MailgunService
	mailjetService   *service.MailjetService
	sparkPostService *service.SparkPostService
	sesService       *service.SESService
	sendGridService  *service.SendGridService

	// Cache
	blogCache cache.Cache // Dedicated cache for blog rendering

	// HTTP handlers
	mux    *http.ServeMux
	server *http.Server

	// Rate limiter (global, namespace-based)
	rateLimiter *ratelimiter.RateLimiter

	// SMTP bridge server
	smtpBridgeHandlerService *service.SMTPBridgeHandlerService
	smtpBridgeServer         interface {
		Start() error
		Shutdown(context.Context) error
	}

	// Server synchronization
	serverMu      sync.RWMutex
	serverStarted chan struct{}

	// Graceful shutdown management
	shutdownCtx     context.Context
	shutdownCancel  context.CancelFunc
	activeRequests  int64          // atomic counter for active HTTP requests
	requestWg       sync.WaitGroup // wait group for active requests
	shutdownTimeout time.Duration  // configurable shutdown timeout
}

// AppOption defines a functional option for configuring the App
type AppOption func(*App)

// WithMockDB configures the app to use a mock database
func WithMockDB(db *sql.DB) AppOption {
	return func(a *App) {
		a.db = db
	}
}

// WithMockMailer configures the app to use a mock mailer
// Note: If Initialize() or InitMailer() is called after setting a mock,
// the mock will be replaced with a real mailer. To keep the mock, either:
// 1. Don't call Initialize()/InitMailer(), OR
// 2. Set the mock again after calling Initialize()
func WithMockMailer(m mailer.Mailer) AppOption {
	return func(a *App) {
		a.mailer = m
	}
}

// WithLogger sets a custom logger
func WithLogger(logger logger.Logger) AppOption {
	return func(a *App) {
		a.logger = logger
	}
}

// NewApp creates a new application instance
func NewApp(cfg *config.Config, opts ...AppOption) AppInterface {
	// Create shutdown context
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())

	app := &App{
		config:          cfg,
		logger:          logger.NewLoggerWithLevel(cfg.LogLevel), // Use configured log level
		mux:             http.NewServeMux(),
		serverStarted:   make(chan struct{}),
		shutdownCtx:     shutdownCtx,
		shutdownCancel:  shutdownCancel,
		shutdownTimeout: 60 * time.Second, // Default 60 seconds shutdown timeout (5 seconds buffer for 55-second tasks)
	}

	// Apply options
	for _, opt := range opts {
		opt(app)
	}

	return app
}

// InitTracing initializes OpenCensus tracing
func (a *App) InitTracing() error {
	tracingConfig := &a.config.Tracing

	if err := tracing.InitTracing(tracingConfig); err != nil {
		return fmt.Errorf("failed to initialize tracing: %w", err)
	}

	if tracingConfig.Enabled {
		exporter := tracingConfig.TraceExporter
		if exporter == "" {
			exporter = "jaeger" // Default
		}

		metricsExporter := tracingConfig.MetricsExporter
		if metricsExporter == "" {
			metricsExporter = "prometheus" // Default
		}

		a.logger.WithField("trace_exporter", exporter).
			WithField("metrics_exporter", metricsExporter).
			WithField("sampling_rate", tracingConfig.SamplingProbability).
			Info("Tracing initialized successfully")
	}

	return nil
}

// InitDB initializes the database connection
func (a *App) InitDB() error {

	password := a.config.Database.Password
	maskedPassword := ""
	if len(password) > 0 {
		maskedPassword = fmt.Sprintf("%c...%c", password[0], password[len(password)-1])
	}
	a.logger.Info(fmt.Sprintf("Connecting to database %s:%d, user %s, sslmode %s, password: %s, dbname: %s", a.config.Database.Host, a.config.Database.Port, a.config.Database.User, a.config.Database.SSLMode, maskedPassword, a.config.Database.DBName))

	// Ensure system database exists
	if err := database.EnsureSystemDatabaseExists(database.GetPostgresDSN(&a.config.Database), a.config.Database.DBName); err != nil {
		a.logger.Error(err.Error())
		return fmt.Errorf("failed to ensure system database exists: %w", err)
	}

	a.logger.Info("System database check completed")

	// If tracing is enabled, wrap the postgres driver
	driverName := "postgres"
	if a.config.Tracing.Enabled {
		var err error
		driverName, err = ocsql.Register(driverName, ocsql.WithAllTraceOptions())
		if err != nil {
			return fmt.Errorf("failed to register opencensus sql driver: %w", err)
		}
		a.logger.Info("Database driver wrapped with OpenCensus tracing")
	}

	// Connect to system database
	db, err := sql.Open(driverName, database.GetSystemDSN(&a.config.Database))
	if err != nil {
		return fmt.Errorf("failed to connect to system database: %w", err)
	}

	// Test database connection
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to ping system database: %w", err)
	}

	// Initialize database schema if needed
	if err := database.InitializeDatabase(db, a.config.RootEmail); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to initialize database schema: %w", err)
	}

	// Run migrations separately
	migrationManager := migrations.NewManager(a.logger)
	ctx := context.Background()
	if err := migrationManager.RunMigrations(ctx, a.config, db); err != nil {
		// Check if this is a restart-required signal
		if errors.Is(err, migrations.ErrRestartRequired) {
			a.logger.Info("Migration completed successfully - server restart required to reload configuration")
			a.logger.Info("Closing database connection and exiting for restart...")

			// Close database connection before exit
			if closeErr := db.Close(); closeErr != nil {
				a.logger.WithField("error", closeErr).Warn("Error closing database during restart")
			}

			// Give logs time to flush
			time.Sleep(200 * time.Millisecond)

			// Exit to trigger process manager restart (Air, systemd, Docker, etc.)
			a.logger.Info("Exiting now - process manager should restart the server")
			os.Exit(0)
		}
		_ = db.Close()
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	a.db = db

	// Initialize connection manager singleton
	// This will configure the system DB pool settings appropriately
	if err := pkgDatabase.InitializeConnectionManager(a.config, db); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to initialize connection manager: %w", err)
	}

	a.logger.WithField("max_connections", a.config.Database.MaxConnections).
		WithField("max_connections_per_db", a.config.Database.MaxConnectionsPerDB).
		Info("Connection manager initialized")

	return nil
}

// InitMailer initializes the mailer service
// This method can be called multiple times to reinitialize the mailer with updated configuration
func (a *App) InitMailer() error {
	// Always initialize/reinitialize the mailer
	// This allows config changes (e.g., after setup wizard) to take effect

	if a.config.IsDevelopment() {
		// Use console mailer in development
		a.mailer = mailer.NewConsoleMailer()
		a.logger.Info("Using console mailer for development")
	} else {
		// Use SMTP mailer in production
		mailerConfig := &mailer.Config{
			SMTPHost:     a.config.SMTP.Host,
			SMTPPort:     a.config.SMTP.Port,
			SMTPUsername: a.config.SMTP.Username,
			SMTPPassword: a.config.SMTP.Password,
			FromEmail:    a.config.SMTP.FromEmail,
			FromName:     a.config.SMTP.FromName,
			APIEndpoint:  a.config.APIEndpoint,
			UseTLS:       a.config.SMTP.UseTLS,
			EHLOHostname: a.config.SMTP.EHLOHostname,
		}

		a.mailer = mailer.NewSMTPMailer(mailerConfig)
		a.logger.Info("Using SMTP mailer for production")
	}

	return nil
}

// InitRepositories initializes all repositories
func (a *App) InitRepositories() error {
	if a.db == nil {
		return fmt.Errorf("database must be initialized before repositories")
	}

	// Get connection manager
	connManager, err := pkgDatabase.GetConnectionManager()
	if err != nil {
		return fmt.Errorf("failed to get connection manager: %w", err)
	}

	a.userRepo = repository.NewUserRepository(a.db)
	a.taskRepo = repository.NewTaskRepository(a.db)
	a.authRepo = repository.NewSQLAuthRepository(a.db)
	a.settingRepo = repository.NewSQLSettingRepository(a.db)
	a.workspaceRepo = repository.NewWorkspaceRepository(a.db, &a.config.Database, a.config.Security.SecretKey, connManager)
	a.contactRepo = repository.NewContactRepository(a.workspaceRepo)
	a.listRepo = repository.NewListRepository(a.workspaceRepo)
	a.contactListRepo = repository.NewContactListRepository(a.workspaceRepo)
	a.templateRepo = repository.NewTemplateRepository(a.workspaceRepo)
	a.broadcastRepo = repository.NewBroadcastRepository(a.workspaceRepo)
	a.transactionalNotificationRepo = repository.NewTransactionalNotificationRepository(a.workspaceRepo)
	a.messageHistoryRepo = repository.NewMessageHistoryRepository(a.workspaceRepo)
	// === Veridian patch === Wrap messageHistoryRepo with quota increment
	// decorator. Le decorator incrémente veridian_plan.emails_sent_this_month
	// apres chaque Create reussi → paywall middleware peut enforcer le quota
	// mensuel. Sans cette decoration, le compteur reste a 0 et le paywall ne
	// bloque jamais sur le quota (bug détecté 2026-05-20).
	// veridianPlanRepo est créé plus bas dans la fonction (ligne ~450) — on
	// le crée maintenant en avance pour pouvoir wrapper.
	a.veridianPlanRepo = repository.NewVeridianPlanRepository(a.db)
	a.messageHistoryRepo = repository.NewVeridianMessageHistoryDecorator(a.messageHistoryRepo, a.veridianPlanRepo, a.logger)
	a.inboundWebhookEventRepo = repository.NewInboundWebhookEventRepository(a.workspaceRepo)
	a.telemetryRepo = repository.NewTelemetryRepository(a.workspaceRepo)
	a.analyticsRepo = repository.NewAnalyticsRepository(a.workspaceRepo, a.logger)
	a.contactTimelineRepo = repository.NewContactTimelineRepository(a.workspaceRepo)
	a.segmentRepo = repository.NewSegmentRepository(a.workspaceRepo)
	a.contactSegmentQueueRepo = repository.NewContactSegmentQueueRepository(a.workspaceRepo)
	a.blogCategoryRepo = repository.NewBlogCategoryRepository(a.workspaceRepo)
	a.blogPostRepo = repository.NewBlogPostRepository(a.workspaceRepo)
	a.blogThemeRepo = repository.NewBlogThemeRepository(a.workspaceRepo)
	a.customEventRepo = repository.NewCustomEventRepository(a.workspaceRepo)
	a.webhookSubscriptionRepo = repository.NewWebhookSubscriptionRepository(a.workspaceRepo)
	a.webhookDeliveryRepo = repository.NewWebhookDeliveryRepository(a.workspaceRepo)

	// Create trigger generator for automation repository
	queryBuilder := service.NewQueryBuilder()
	triggerGenerator := service.NewAutomationTriggerGenerator(queryBuilder)
	a.automationRepo = repository.NewAutomationRepository(a.workspaceRepo, triggerGenerator)

	// Initialize email queue repository
	a.emailQueueRepo = repository.NewEmailQueueRepository(a.workspaceRepo)

	// === Veridian patches ===
	// veridianPlanRepo est deja initialise plus haut (avant le decorator
	// messageHistoryRepo). On garde juste l'init du repo idempotency ici.
	a.veridianIdempotencyRepo = repository.NewVeridianIdempotencyRepository(a.db)

	// === Veridian patch — Lot K (2026-05-21) === Grace period repo pour
	// rotate-api-key (CONTRAT-HUB §5.15). Voir migration V41 + service
	// VeridianAPIKeyGraceCleanupService.
	a.veridianAPIKeyGraceRepo = repository.NewVeridianAPIKeyGraceRepository(a.db)

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

	// Initialize setting service
	a.settingService = service.NewSettingService(a.settingRepo)

	return nil
}

// InitServices initializes all application services
func (a *App) InitServices() error {
	// Initialize event bus first
	a.eventBus = domain.NewInMemoryEventBus()

	// Initialize auth service with JWT secret provider callback
	// Secret is loaded on-demand, so this never fails
	a.authService = service.NewAuthService(service.AuthServiceConfig{
		Repository:          a.authRepo,
		WorkspaceRepository: a.workspaceRepo,
		GetSecret: func() (secret []byte, err error) {
			// Return current JWT secret from config
			// This will be nil before setup, or loaded from env/database after setup
			if len(a.config.Security.JWTSecret) == 0 {
				return nil, fmt.Errorf("system setup not completed or SECRET_KEY not configured")
			}
			return a.config.Security.JWTSecret, nil
		},
		Logger: a.logger,
	})

	var err error

	// Initialize global rate limiter with namespace support
	a.rateLimiter = ratelimiter.NewRateLimiter()

	// Configure policies for different use cases
	a.rateLimiter.SetPolicy("signin", 5, 5*time.Minute)           // Strict auth
	a.rateLimiter.SetPolicy("verify", 5, 5*time.Minute)           // Strict auth
	a.rateLimiter.SetPolicy("smtp", 5, 1*time.Minute)             // SMTP bridge
	a.rateLimiter.SetPolicy("subscribe:email", 10, 1*time.Minute)    // Public subscribe by email
	a.rateLimiter.SetPolicy("subscribe:ip", 50, 1*time.Minute)      // Public subscribe by IP
	a.rateLimiter.SetPolicy("preferences:email", 20, 1*time.Minute)  // Public preferences by email
	a.rateLimiter.SetPolicy("preferences:ip", 100, 1*time.Minute)   // Public preferences by IP

	// Initialize user service
	userServiceConfig := service.UserServiceConfig{
		Repository:    a.userRepo,
		AuthService:   a.authService,
		EmailSender:   a.mailer,
		SessionExpiry: 30 * 24 * time.Hour, // 30 days
		IsProduction:  a.config.IsProduction(),
		Logger:        a.logger,
		Tracer:        tracing.GetTracer(),
		RateLimiter:   a.rateLimiter, // Pass global rate limiter
		SecretKey:     a.config.Security.SecretKey,
		RootEmail:     a.config.RootEmail,
	}

	a.userService, err = service.NewUserService(userServiceConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize user service: %w", err)
	}

	// Initialize setup service with environment config from config loader
	// Config tracks which values came from actual env vars (not database, not generated)
	rootEmail, apiEndpoint, smtpHost, smtpUsername, smtpPassword, smtpFromEmail, smtpFromName, smtpPort, smtpUseTLS, smtpBridgeEnabled, smtpBridgeDomain, smtpBridgeTLSCertBase64, smtpBridgeTLSKeyBase64, smtpBridgePort := a.config.GetEnvValues()
	envConfig := &service.EnvironmentConfig{
		RootEmail:              rootEmail,
		APIEndpoint:            apiEndpoint,
		SMTPHost:               smtpHost,
		SMTPPort:               smtpPort,
		SMTPUsername:           smtpUsername,
		SMTPPassword:           smtpPassword,
		SMTPFromEmail:          smtpFromEmail,
		SMTPFromName:           smtpFromName,
		SMTPUseTLS:             smtpUseTLS,
		SMTPBridgeEnabled:       smtpBridgeEnabled,
		SMTPBridgeDomain:        smtpBridgeDomain,
		SMTPBridgePort:          smtpBridgePort,
		SMTPBridgeTLSCertBase64: smtpBridgeTLSCertBase64,
		SMTPBridgeTLSKeyBase64:  smtpBridgeTLSKeyBase64,
		SMTPBridgeTLSMode:       a.config.EnvValues.SMTPBridgeTLSMode,
	}

	a.setupService = service.NewSetupService(
		a.settingService,
		a.userService,
		a.userRepo,
		a.logger,
		a.config.Security.SecretKey,
		nil, // No callback needed - server restarts after setup
		envConfig,
	)

	// Initialize template service
	a.templateService = service.NewTemplateService(
		a.templateRepo,
		a.workspaceRepo,
		a.authService,
		a.logger,
		a.config.APIEndpoint,
	)

	// Initialize template block service
	a.templateBlockService = service.NewTemplateBlockService(
		a.workspaceRepo,
		a.authService,
		a.logger,
	)

	// Initialize contact service
	a.contactService = service.NewContactService(
		a.contactRepo,
		a.workspaceRepo,
		a.authService,
		a.messageHistoryRepo,
		a.inboundWebhookEventRepo,
		a.contactListRepo,
		a.contactTimelineRepo,
		a.logger,
	)

	// Initialize contact list service
	a.contactListService = service.NewContactListService(
		a.contactListRepo,
		a.workspaceRepo,
		a.authService,
		a.contactRepo,
		a.listRepo,
		a.contactListRepo,
		a.logger,
	)

	// Initialize custom event service
	a.customEventService = service.NewCustomEventService(
		a.customEventRepo,
		a.contactRepo,
		a.authService,
		a.logger,
	)

	// Initialize http client
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Wrap HTTP client with tracing if enabled
	if a.config.Tracing.Enabled {
		httpClient = tracing.WrapHTTPClient(httpClient)
		a.logger.Info("HTTP client wrapped with OpenCensus tracing")
	}

	// Initialize email provider services
	a.postmarkService = service.NewPostmarkService(httpClient, a.authService, a.logger)
	a.mailgunService = service.NewMailgunService(httpClient, a.authService, a.logger, a.config.WebhookEndpoint)
	a.mailjetService = service.NewMailjetService(httpClient, a.authService, a.logger)
	a.sparkPostService = service.NewSparkPostService(httpClient, a.authService, a.logger)
	a.sesService = service.NewSESService(a.authService, a.logger)
	a.sendGridService = service.NewSendGridService(httpClient, a.authService, a.logger)

	// Initialize email service
	a.emailService = service.NewEmailService(
		a.logger,
		a.authService,
		a.config.Security.SecretKey,
		a.config.IsDemo(),
		a.workspaceRepo,
		a.templateRepo,
		a.templateService,
		a.messageHistoryRepo,
		httpClient,
		a.config.WebhookEndpoint,
		a.config.APIEndpoint,
	)

	// Initialize webhook registration service
	a.webhookRegistrationService = service.NewWebhookRegistrationService(
		a.workspaceRepo,
		a.authService,
		a.postmarkService,
		a.mailgunService,
		a.mailjetService,
		a.sparkPostService,
		a.sesService,
		a.sendGridService,
		a.logger,
		a.config.WebhookEndpoint,
	)

	// Initialize list service after webhook registration service
	a.listService = service.NewListService(
		a.listRepo,
		a.workspaceRepo,
		a.contactListRepo,
		a.contactRepo,
		a.messageHistoryRepo,
		a.authService,
		a.emailService,
		a.logger,
		a.config.APIEndpoint,
		a.blogCache,
	)

	// Initialize DNS verification service (before workspace service)
	a.dnsVerificationService = service.NewDNSVerificationService(
		a.logger,
		a.config.APIEndpoint, // Expected CNAME target
	)

	// Initialize task service
	// === Veridian patch ===
	// Use SchedulerEndpoint() so the self-call HTTP dispatch hits the local
	// container (INTERNAL_API_ENDPOINT) instead of looping via Cloudflare.
	// Falls back to APIEndpoint when INTERNAL_API_ENDPOINT is not set.
	a.taskService = service.NewTaskService(a.taskRepo, a.settingRepo, a.logger, a.authService, a.config.SchedulerEndpoint())

	// Configure autoExecuteImmediate based on TaskScheduler.Enabled
	// If task scheduler is disabled (e.g., in tests), also disable background task execution
	a.taskService.SetAutoExecuteImmediate(a.config.TaskScheduler.Enabled)

	// Initialize transactional notification service
	a.transactionalNotificationService = service.NewTransactionalNotificationService(
		a.transactionalNotificationRepo,
		a.messageHistoryRepo,
		a.templateService,
		a.contactService,
		a.emailService,
		a.authService,
		a.logger,
		a.workspaceRepo,
		a.config.APIEndpoint,
	)

	a.inboundWebhookEventService = service.NewInboundWebhookEventService(
		a.inboundWebhookEventRepo,
		a.authService,
		a.logger,
		a.workspaceRepo,
		a.messageHistoryRepo,
		a.contactRepo,
	)

	// Initialize Supabase service (before workspace service)
	a.supabaseService = service.NewSupabaseService(
		a.workspaceRepo,
		a.emailService,
		a.contactService,
		a.listRepo,
		a.contactListRepo,
		a.templateRepo,
		a.templateService,
		a.transactionalNotificationRepo,
		a.transactionalNotificationService,
		a.inboundWebhookEventRepo,
		a.logger,
	)

	// Initialize data feed fetcher for external data in broadcasts
	a.dataFeedFetcher = broadcast.NewDataFeedFetcher(a.logger)

	// Initialize broadcast service
	a.broadcastService = service.NewBroadcastService(
		a.logger,
		a.broadcastRepo,
		a.workspaceRepo,
		a.emailService,
		a.contactRepo,
		a.templateService,
		nil,        // No taskService yet
		a.taskRepo, // Task repository
		a.authService,
		a.eventBus,           // Pass the event bus
		a.messageHistoryRepo, // Message history repository
		a.emailQueueRepo,     // Email queue for mid-flight pause/resume/cancel
		a.listService,        // List service for web publication validation
		a.dataFeedFetcher,    // Data feed fetcher for global/recipient data
		a.config.APIEndpoint, // API endpoint for tracking URLs
	)

	// Create broadcast factory with refactored components
	broadcastConfig := broadcast.DefaultConfig()
	// Override default rate limit if set in config
	if a.config.Broadcast.DefaultRateLimit > 0 {
		broadcastConfig.DefaultRateLimit = a.config.Broadcast.DefaultRateLimit
	}
	broadcastFactory := broadcast.NewFactory(
		a.broadcastRepo,
		a.messageHistoryRepo,
		a.templateRepo,
		a.emailService,
		a.contactRepo,
		a.taskRepo,
		a.workspaceRepo,
		a.emailQueueRepo,
		a.dataFeedFetcher,
		a.logger,
		broadcastConfig,
		a.config.APIEndpoint,
		a.eventBus,
		true, // useQueueSender - use queue-based message sender for broadcasts
	)

	// Register the broadcast factory with the task service
	broadcastFactory.RegisterWithTaskService(a.taskService)

	// Register task service to listen for broadcast events
	a.taskService.SubscribeToBroadcastEvents(a.eventBus)

	// Set the task service on the broadcast service
	a.broadcastService.SetTaskService(a.taskService)

	// Initialize message history service
	a.messageHistoryService = service.NewMessageHistoryService(a.messageHistoryRepo, a.workspaceRepo, a.logger, a.authService)

	// Initialize notification center service
	a.notificationCenterService = service.NewNotificationCenterService(
		a.contactRepo,
		a.workspaceRepo,
		a.listRepo,
		a.logger,
	)

	// Initialize system notification service
	a.systemNotificationService = service.NewSystemNotificationService(
		a.workspaceRepo,
		a.broadcastRepo,
		a.mailer,
		a.logger,
	)

	// Register system notification service with event bus
	a.systemNotificationService.RegisterWithEventBus(a.eventBus)

	// Initialize segment service (before demo service since it depends on it)
	a.segmentService = service.NewSegmentService(
		a.segmentRepo,
		a.workspaceRepo,
		a.taskService,
		a.logger,
	)

	// Initialize blog service (before workspace service)
	a.blogService = service.NewBlogService(
		a.logger,
		a.blogCategoryRepo,
		a.blogPostRepo,
		a.blogThemeRepo,
		a.workspaceRepo,
		a.listRepo,
		a.templateRepo,
		a.authService,
		a.blogCache,
	)

	// Initialize workspace service (after all its dependencies)
	a.workspaceService = service.NewWorkspaceService(
		a.workspaceRepo,
		a.userRepo,
		a.taskRepo,
		a.logger,
		a.userService,
		a.authService,
		a.mailer,
		a.config,
		a.contactService,
		a.listService,
		a.contactListService,
		a.templateService,
		a.webhookRegistrationService,
		a.config.Security.SecretKey,
		a.supabaseService,
		a.dnsVerificationService,
		a.blogService,
	)

	// Initialize and register segment build processor
	segmentBuildProcessor := service.NewSegmentBuildProcessor(
		a.segmentRepo,
		a.contactRepo,
		a.taskRepo,
		a.workspaceRepo,
		a.logger,
	)
	a.taskService.RegisterProcessor(segmentBuildProcessor)

	// Initialize and register segment recompute task processor
	segmentRecomputeProcessor := service.NewSegmentRecomputeTaskProcessor(
		a.segmentRepo,
		a.taskRepo,
		a.taskService,
		a.logger,
	)
	a.taskService.RegisterProcessor(segmentRecomputeProcessor)

	// Initialize contact segment queue processor
	contactSegmentQueueProcessor := service.NewContactSegmentQueueProcessor(
		a.contactSegmentQueueRepo,
		a.segmentRepo,
		a.contactRepo,
		a.workspaceRepo,
		a.logger,
	)

	// Initialize and register contact segment queue task processor
	contactSegmentQueueTaskProcessor := service.NewContactSegmentQueueTaskProcessor(
		contactSegmentQueueProcessor,
		a.taskRepo,
		a.logger,
	)
	a.taskService.RegisterProcessor(contactSegmentQueueTaskProcessor)

	// Initialize integration sync processor for recurring integration sync tasks
	integrationSyncProcessor := service.NewIntegrationSyncProcessor(a.logger)
	// TODO: Register integration-specific handlers here as integrations are added
	// Example: integrationSyncProcessor.RegisterHandler("staminads", staminadsHandler)
	a.taskService.RegisterProcessor(integrationSyncProcessor)

	// Initialize webhook subscription service (before demo service so it can create subscriptions)
	a.webhookSubscriptionService = service.NewWebhookSubscriptionService(
		a.webhookSubscriptionRepo,
		a.webhookDeliveryRepo,
		a.authService,
		a.logger,
	)

	// Initialize demo service
	a.demoService = service.NewDemoService(
		a.logger,
		a.config,
		a.workspaceService,
		a.userService,
		a.contactService,
		a.listService,
		a.contactListService,
		a.templateService,
		a.emailService,
		a.broadcastService,
		a.taskService,
		a.transactionalNotificationService,
		a.inboundWebhookEventService,
		a.webhookRegistrationService,
		a.messageHistoryService,
		a.notificationCenterService,
		a.segmentService,
		a.workspaceRepo,
		a.taskRepo,
		a.messageHistoryRepo,
		a.inboundWebhookEventRepo,
		a.broadcastRepo,
		a.customEventRepo,
		a.webhookSubscriptionService,
	)

	// Initialize telemetry service
	telemetryConfig := service.TelemetryServiceConfig{
		Enabled:       a.config.Telemetry,
		APIEndpoint:   a.config.APIEndpoint,
		WorkspaceRepo: a.workspaceRepo,
		TelemetryRepo: a.telemetryRepo,
		Logger:        a.logger,
		HTTPClient:    httpClient, // Reuse the HTTP client created above
	}
	a.telemetryService = service.NewTelemetryService(telemetryConfig)

	// Initialize analytics service
	a.analyticsService = service.NewAnalyticsService(
		a.analyticsRepo,
		a.authService,
		a.logger,
	)

	// Initialize contact timeline service
	a.contactTimelineService = service.NewContactTimelineService(a.contactTimelineRepo)

	// Initialize task scheduler
	a.taskScheduler = service.NewTaskScheduler(
		a.taskService,
		a.logger,
		a.config.TaskScheduler.Interval,
		a.config.TaskScheduler.MaxTasks,
	)

	// Initialize webhook delivery worker
	a.webhookDeliveryWorker = service.NewWebhookDeliveryWorker(
		a.webhookSubscriptionRepo,
		a.webhookDeliveryRepo,
		a.workspaceRepo,
		a.logger,
		httpClient,
	)

	// Initialize email queue worker for processing marketing emails (broadcasts & automations)
	// Worker creates message_history entries via UPSERT after each send attempt
	a.emailQueueWorker = queue.NewEmailQueueWorker(
		a.emailQueueRepo,
		a.workspaceRepo,
		a.emailService,
		a.messageHistoryRepo,
		queue.DefaultWorkerConfig(),
		a.logger,
	)

	// Initialize automation service
	a.automationService = service.NewAutomationService(
		a.automationRepo,
		a.authService,
		a.logger,
	)

	// Initialize Firecrawl service
	firecrawlService := service.NewFirecrawlService(a.logger)

	// Initialize server-side tool registry
	toolRegistry := service.NewServerSideToolRegistry(firecrawlService, a.logger)

	// Initialize LLM service with tool registry
	a.llmService = service.NewLLMService(service.LLMServiceConfig{
		AuthService:   a.authService,
		WorkspaceRepo: a.workspaceRepo,
		Logger:        a.logger,
		ToolRegistry:  toolRegistry,
	})

	// Initialize automation executor and scheduler
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

	automationExecutor := service.NewAutomationExecutor(
		a.automationRepo,
		a.contactRepo,
		a.workspaceRepo,
		a.contactListRepo,
		a.listRepo,
		a.templateRepo,
		a.emailQueueRepo,
		a.messageHistoryRepo,
		a.contactTimelineRepo,
		a.logger,
		a.config.APIEndpoint,
	)
	// Branche le checker stop-on-reply (Lot 3) sur le gate d'exit cold (Lot 9).
	automationExecutor.SetColdReplyChecker(a.veridianReplyService)
	a.automationScheduler = service.NewAutomationScheduler(
		automationExecutor,
		a.logger,
		a.config.AutomationScheduler.Interval,
		a.config.AutomationScheduler.BatchSize,
	)

	// Initialize SMTP bridge handler service
	a.smtpBridgeHandlerService = service.NewSMTPBridgeHandlerService(
		a.authService,
		a.transactionalNotificationService,
		a.workspaceRepo,
		a.logger,
		a.config.Security.JWTSecret,
		a.rateLimiter, // Use global rate limiter
	)

	// Initialize SMTP bridge server if enabled
	if a.config.SMTPBridge.Enabled {
		// TLS is required for starttls and implicit modes; skipped for off.
		var tlsConfig *tls.Config
		if a.config.SMTPBridge.TLSMode != smtp_bridge.ModeOff {
			cfg, err := smtp_bridge.SetupTLS(smtp_bridge.TLSConfig{
				CertBase64: a.config.SMTPBridge.TLSCertBase64,
				KeyBase64:  a.config.SMTPBridge.TLSKeyBase64,
				Logger:     a.logger,
			})
			if err != nil {
				a.logger.WithField("error", err.Error()).Error("Failed to setup TLS for SMTP bridge")
				return fmt.Errorf("failed to setup TLS for SMTP bridge: %w", err)
			}
			tlsConfig = cfg
		}

		// Create SMTP backend with authentication and message handlers
		backend := smtp_bridge.NewBackend(
			a.smtpBridgeHandlerService.Authenticate,
			a.smtpBridgeHandlerService.HandleMessage,
			a.logger,
		)

		// Create SMTP server configuration
		smtpConfig := smtp_bridge.ServerConfig{
			Host:      a.config.SMTPBridge.Host,
			Port:      a.config.SMTPBridge.Port,
			Domain:    a.config.SMTPBridge.Domain,
			Mode:      a.config.SMTPBridge.TLSMode,
			TLSConfig: tlsConfig,
			Logger:    a.logger,
		}

		// Create the SMTP server
		smtpBridgeServer, err := smtp_bridge.NewServer(smtpConfig, backend)
		if err != nil {
			a.logger.WithField("error", err.Error()).Error("Failed to create SMTP bridge server")
			return fmt.Errorf("failed to create SMTP bridge server: %w", err)
		}

		a.smtpBridgeServer = smtpBridgeServer
		a.logger.WithFields(map[string]interface{}{
			"port":   a.config.SMTPBridge.Port,
			"domain": a.config.SMTPBridge.Domain,
			"mode":   a.config.SMTPBridge.TLSMode,
		}).Info("SMTP bridge server initialized successfully")
	}

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

	return nil
}

// InitHandlers initializes all HTTP handlers and routes
func (a *App) InitHandlers() error {
	// Create a new ServeMux to avoid route conflicts on restart
	a.mux = http.NewServeMux()

	// Create a callback for getting the JWT secret on-demand
	// This ensures handlers always use the current JWT secret from config
	getJWTSecret := func() ([]byte, error) {
		if len(a.config.Security.JWTSecret) == 0 {
			return nil, fmt.Errorf("JWT secret not configured")
		}
		return a.config.Security.JWTSecret, nil
	}

	// Initialize handlers (pass callback instead of static JWT secret)
	userHandler := httpHandler.NewUserHandler(
		a.userService,
		a.workspaceService,
		a.config,
		getJWTSecret,
		a.logger)
	rootHandler := httpHandler.NewRootHandler(
		"console/dist",
		"notification_center/dist",
		a.logger,
		a.config.APIEndpoint,
		a.config.Version,
		a.config.RootEmail,
		&a.isInstalled,
		a.config.SMTPBridge.Enabled,
		a.config.SMTPBridge.Domain,
		a.config.SMTPBridge.Port,
		a.config.SMTPBridge.TLSMode,
		a.workspaceRepo,
		a.blogService,
		a.blogCache,
	)
	setupHandler := httpHandler.NewSetupHandler(
		a.setupService,
		a.settingService,
		a.logger,
		a, // Pass app for shutdown capability
	)
	settingsHandler := httpHandler.NewSettingsHandler(
		a.setupService,
		a.settingService,
		a.userService,
		getJWTSecret,
		a.logger,
		a.config.Security.SecretKey,
		a.config.RootEmail,
		a, // AppShutdowner
	)
	workspaceHandler := httpHandler.NewWorkspaceHandler(
		a.workspaceService,
		a.authService,
		getJWTSecret,
		a.logger,
		a.config.Security.SecretKey,
	)
	contactHandler := httpHandler.NewContactHandler(a.contactService, getJWTSecret, a.logger)
	listHandler := httpHandler.NewListHandler(a.listService, getJWTSecret, a.logger)
	contactListHandler := httpHandler.NewContactListHandler(a.contactListService, getJWTSecret, a.logger)
	templateHandler := httpHandler.NewTemplateHandler(a.templateService, getJWTSecret, a.logger)
	templateBlockHandler := httpHandler.NewTemplateBlockHandler(a.templateBlockService, getJWTSecret, a.logger)
	emailHandler := httpHandler.NewEmailHandler(a.emailService, getJWTSecret, a.logger, a.config.Security.SecretKey)
	broadcastHandler := httpHandler.NewBroadcastHandler(a.broadcastService, a.templateService, getJWTSecret, a.logger, a.config.IsDemo())
	blogHandler := httpHandler.NewBlogHandler(a.blogService, getJWTSecret, a.logger, a.config.IsDemo())
	blogThemeHandler := httpHandler.NewBlogThemeHandler(a.blogService, getJWTSecret, a.logger)
	taskHandler := httpHandler.NewTaskHandler(
		a.taskService,
		getJWTSecret,
		a.logger,
		a.config.Security.SecretKey,
	)
	transactionalHandler := httpHandler.NewTransactionalNotificationHandler(a.transactionalNotificationService, getJWTSecret, a.logger, a.config.IsDemo())
	inboundWebhookEventHandler := httpHandler.NewInboundWebhookEventHandler(a.inboundWebhookEventService, getJWTSecret, a.logger)
	webhookRegistrationHandler := httpHandler.NewWebhookRegistrationHandler(a.webhookRegistrationService, getJWTSecret, a.logger)
	supabaseWebhookHandler := httpHandler.NewSupabaseWebhookHandler(a.supabaseService, a.logger)
	messageHistoryHandler := httpHandler.NewMessageHistoryHandler(
		a.messageHistoryService,
		a.authService,
		getJWTSecret,
		a.logger,
	)
	notificationCenterHandler := httpHandler.NewNotificationCenterHandler(
		a.notificationCenterService,
		a.listService,
		a.logger,
		a.rateLimiter, // Pass global rate limiter
	)
	analyticsHandler := httpHandler.NewAnalyticsHandler(
		a.analyticsService,
		getJWTSecret,
		a.logger,
	)
	contactTimelineHandler := httpHandler.NewContactTimelineHandler(
		a.contactTimelineService,
		a.authService,
		getJWTSecret,
		a.logger,
	)
	segmentHandler := httpHandler.NewSegmentHandler(
		a.segmentService,
		getJWTSecret,
		a.logger,
	)
	customEventHandler := httpHandler.NewCustomEventHandler(
		a.customEventService,
		getJWTSecret,
		a.logger,
	)
	webhookSubscriptionHandler := httpHandler.NewWebhookSubscriptionHandler(
		a.webhookSubscriptionService,
		a.webhookDeliveryWorker,
		getJWTSecret,
		a.logger,
	)
	automationHandler := httpHandler.NewAutomationHandler(
		a.automationService,
		getJWTSecret,
		a.logger,
	)
	llmHandler := httpHandler.NewLLMHandler(
		a.llmService,
		getJWTSecret,
		a.logger,
	)
	if !a.config.IsProduction() {
		demoHandler := httpHandler.NewDemoHandler(a.demoService, a.logger)
		demoHandler.RegisterRoutes(a.mux)
	}

	// Register routes
	setupHandler.RegisterRoutes(a.mux) // Setup handler first (should be accessible without auth)
	settingsHandler.RegisterRoutes(a.mux)
	userHandler.RegisterRoutes(a.mux)
	workspaceHandler.RegisterRoutes(a.mux)
	rootHandler.RegisterRoutes(a.mux)
	contactHandler.RegisterRoutes(a.mux)
	listHandler.RegisterRoutes(a.mux)
	contactListHandler.RegisterRoutes(a.mux)
	templateHandler.RegisterRoutes(a.mux)
	templateBlockHandler.RegisterRoutes(a.mux)
	emailHandler.RegisterRoutes(a.mux)
	broadcastHandler.RegisterRoutes(a.mux)
	blogHandler.RegisterRoutes(a.mux)
	blogThemeHandler.RegisterRoutes(a.mux)
	taskHandler.RegisterRoutes(a.mux)
	transactionalHandler.RegisterRoutes(a.mux)
	inboundWebhookEventHandler.RegisterRoutes(a.mux)
	webhookRegistrationHandler.RegisterRoutes(a.mux)
	supabaseWebhookHandler.RegisterRoutes(a.mux)
	messageHistoryHandler.RegisterRoutes(a.mux)
	notificationCenterHandler.RegisterRoutes(a.mux)
	analyticsHandler.RegisterRoutes(a.mux)
	contactTimelineHandler.RegisterRoutes(a.mux)
	segmentHandler.RegisterRoutes(a.mux)
	customEventHandler.RegisterRoutes(a.mux)
	webhookSubscriptionHandler.RegisterRoutes(a.mux)
	automationHandler.RegisterRoutes(a.mux)
	llmHandler.RegisterRoutes(a.mux)

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

// Start starts the HTTP server
func (a *App) Start() error {
	// Create server with wrapped handler for CORS and tracing
	var handler http.Handler = a.mux

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

	// Apply graceful shutdown middleware first (outermost)
	handler = a.gracefulShutdownMiddleware(handler)
	a.logger.Info("Graceful shutdown middleware enabled")

	// === Veridian — rate-limit GLOBAL de l'API (OWASP API4:2023) ===
	// Defense-in-depth : limite tout /api/ par IP + identité authentifiée
	// best-effort. Placé ICI dans la chaîne (donc EXÉCUTÉ avant les filtres
	// paywall/frozen/soft-deleted qui font des lookups DB, mais APRÈS le CORS
	// + preflight gérés par VeridianSecurityHeadersMiddleware plus externe) :
	// un flood est rejeté en 429 sans toucher la DB. Réutilise le RateLimiter
	// PARTAGÉ (a.rateLimiter), pas de 2e goroutine de cleanup. Config ENV
	// VERIDIAN_API_RATE_LIMIT_* (défaut activé, 300/min IP + 600/min identité).
	// Exempte health/version + flux HMAC Hub (signature + anti-replay + rafales
	// cron reconcile). Cf. internal/http/middleware/veridian_rate_limit.go.
	veridianRLConfig := middleware.LoadVeridianAPIRateLimitConfig()
	handler = middleware.VeridianAPIRateLimitMiddleware(a.rateLimiter, veridianRLConfig, a.logger)(handler)
	a.logger.WithField("rate_limit", veridianRLConfig.String()).
		Info("Veridian global API rate limit middleware configured")

	// Apply tracing middleware if enabled
	if a.config.Tracing.Enabled {
		handler = middleware.TracingMiddleware(handler)
		a.logger.Info("OpenCensus tracing middleware enabled")
	}

	// === Veridian patch 2026-05-20 — pentest doomsday CRITICAL ===
	// Remplace CORSMiddleware upstream (qui renvoyait ACAO=* + ACAC=true,
	// combo interdit par spec CORS et exploitable cross-origin) par notre
	// VeridianSecurityHeadersMiddleware qui :
	//   1. Fait CORS allowlist explicite (notifuse.app/staging.veridian.site
	//      + localhost dev), Origin hors allowlist → pas d'ACAO renvoyée
	//   2. Pose HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy
	//      sur toutes les réponses
	//   3. CSP volontairement absente — calibrage manuel à faire (cf. doc
	//      en tête du middleware)
	// Cf. ~/.veridian-obs/pentest/runs/20260520-190723-doomsday/report.md
	handler = middleware.VeridianSecurityHeadersMiddleware(handler)

	addr := fmt.Sprintf("%s:%d", a.config.Server.Host, a.config.Server.Port)
	a.logger.WithField("address", addr).
		WithField("api_endpoint", a.config.APIEndpoint).
		WithField("port", a.config.Server.Port).
		Info(fmt.Sprintf("Server starting on %s with API endpoint: %s", addr, a.config.APIEndpoint))

	// Create a fresh notification channel and update the server
	a.serverMu.Lock()
	// Close the existing channel if it exists
	if a.serverStarted != nil {
		close(a.serverStarted)
	}
	a.serverStarted = make(chan struct{})

	// Create the server
	a.server = &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Get a reference to the channel before unlocking
	serverStarted := a.serverStarted
	a.serverMu.Unlock()

	// Signal that the server has been created and is about to start
	close(serverStarted)

	// Start internal task scheduler if enabled (with 30 second delay)
	if a.config.TaskScheduler.Enabled && a.taskScheduler != nil {
		go func() {
			// Wait 30 seconds before starting to avoid hitting DB on server start
			a.logger.Info("Task scheduler will start in 30 seconds...")

			ctx := a.GetShutdownContext()

			// Use a timer that respects the shutdown context
			select {
			case <-time.After(30 * time.Second):
				// Check if we're shutting down before starting
				if ctx.Err() != nil {
					a.logger.Info("Server shutting down, task scheduler will not start")
					return
				}
				a.logger.Info("Starting task scheduler now")
				a.taskScheduler.Start(ctx)
			case <-ctx.Done():
				a.logger.Info("Server shutdown initiated during task scheduler delay, scheduler will not start")
				return
			}
		}()
	}

	// Start daily telemetry scheduler
	if a.telemetryService != nil {
		ctx := context.Background()
		a.telemetryService.StartDailyScheduler(ctx)
	}

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

	// Start SMTP bridge server if enabled
	if a.smtpBridgeServer != nil {
		go func() {
			a.logger.Info("Starting SMTP bridge server...")
			if err := a.smtpBridgeServer.Start(); err != nil {
				a.logger.WithField("error", err.Error()).Error("SMTP bridge server error")
			}
		}()
	}

	// Start webhook delivery worker (with 30 second delay like task scheduler)
	// Disabled in demo mode to prevent sending webhooks to external endpoints
	if a.webhookDeliveryWorker != nil && !a.config.IsDemo() {
		go func() {
			a.logger.Info("Webhook delivery worker will start in 30 seconds...")

			ctx := a.GetShutdownContext()

			// Use a timer that respects the shutdown context
			select {
			case <-time.After(30 * time.Second):
				// Check if we're shutting down before starting
				if ctx.Err() != nil {
					a.logger.Info("Server shutting down, webhook delivery worker will not start")
					return
				}
				a.logger.Info("Starting webhook delivery worker now")
				a.webhookDeliveryWorker.Start(ctx)
			case <-ctx.Done():
				a.logger.Info("Server shutdown initiated during webhook worker delay, worker will not start")
				return
			}
		}()
	}

	// Start email queue worker (with 30 second delay)
	// Disabled in demo mode to prevent sending marketing emails
	if a.emailQueueWorker != nil && !a.config.IsDemo() {
		go func() {
			a.logger.Info("Email queue worker will start in 30 seconds...")

			ctx := a.GetShutdownContext()

			// Use a timer that respects the shutdown context
			select {
			case <-time.After(30 * time.Second):
				// Check if we're shutting down before starting
				if ctx.Err() != nil {
					a.logger.Info("Server shutting down, email queue worker will not start")
					return
				}
				a.logger.Info("Starting email queue worker now")
				if err := a.emailQueueWorker.Start(ctx); err != nil {
					a.logger.WithField("error", err.Error()).Error("Failed to start email queue worker")
				}
			case <-ctx.Done():
				a.logger.Info("Server shutdown initiated during email queue worker delay, worker will not start")
				return
			}
		}()
	}

	// Start automation scheduler (with configurable delay)
	// Disabled in demo mode to prevent executing automations
	if a.automationScheduler != nil && !a.config.IsDemo() {
		go func() {
			ctx := a.GetShutdownContext()
			delay := a.config.AutomationScheduler.Delay

			if delay > 0 {
				a.logger.WithField("delay", delay).Info("Automation scheduler will start after delay...")
				select {
				case <-time.After(delay):
					// continue
				case <-ctx.Done():
					a.logger.Info("Server shutdown during automation scheduler delay")
					return
				}
			}

			if ctx.Err() != nil {
				a.logger.Info("Server shutting down, automation scheduler will not start")
				return
			}
			a.logger.Info("Starting automation scheduler now")
			a.automationScheduler.Start(ctx)
		}()
	}

	// Start the server based on SSL configuration
	if a.config.Server.SSL.Enabled {
		a.logger.WithField("cert_file", a.config.Server.SSL.CertFile).Info("SSL enabled")
		return a.server.ListenAndServeTLS(a.config.Server.SSL.CertFile, a.config.Server.SSL.KeyFile)
	}

	return a.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (a *App) Shutdown(ctx context.Context) error {
	a.logger.Info("Starting graceful shutdown...")

	// Signal shutdown to all components
	a.shutdownCancel()

	// Stop blog cache cleanup goroutine
	if a.blogCache != nil {
		a.logger.Info("Stopping blog cache...")
		a.blogCache.Stop()
	}

	// Stop task scheduler first (before stopping server)
	if a.taskScheduler != nil {
		a.taskScheduler.Stop()
	}

	// Stop automation scheduler
	if a.automationScheduler != nil {
		a.logger.Info("Stopping automation scheduler...")
		a.automationScheduler.Stop()
	}

	// Stop email queue worker
	if a.emailQueueWorker != nil {
		a.logger.Info("Stopping email queue worker...")
		a.emailQueueWorker.Stop()
	}

	// Stop global rate limiter
	if a.rateLimiter != nil {
		a.rateLimiter.Stop()
	}

	// Shutdown SMTP bridge server if running
	if a.smtpBridgeServer != nil {
		a.logger.Info("Shutting down SMTP bridge server...")
		smtpShutdownCtx, smtpShutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer smtpShutdownCancel()

		if err := a.smtpBridgeServer.Shutdown(smtpShutdownCtx); err != nil {
			a.logger.WithField("error", err.Error()).Error("Error shutting down SMTP bridge server")
		} else {
			a.logger.Info("SMTP bridge server shut down successfully")
		}
	}

	// Get server reference
	a.serverMu.RLock()
	server := a.server
	a.serverMu.RUnlock()

	if server == nil {
		a.logger.Info("No server to shutdown")
		return a.cleanupResources(ctx)
	}

	// Log current active requests
	activeCount := a.getActiveRequestCount()
	a.logger.WithField("active_requests", activeCount).Info("Active requests at shutdown start")

	// Create a timeout context for shutdown operations
	shutdownTimeout := a.shutdownTimeout
	if deadline, ok := ctx.Deadline(); ok {
		// Use the provided context deadline if it's sooner than our default timeout
		if remaining := time.Until(deadline); remaining < shutdownTimeout {
			shutdownTimeout = remaining - time.Second // Leave 1 second buffer
			if shutdownTimeout < 0 {
				shutdownTimeout = 0
			}
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	// Start HTTP server shutdown in a goroutine
	serverShutdownDone := make(chan error, 1)
	go func() {
		a.logger.WithField("timeout", shutdownTimeout).Info("Starting HTTP server shutdown")
		serverShutdownDone <- server.Shutdown(shutdownCtx)
	}()

	// Wait for active requests to complete in another goroutine
	requestsDone := make(chan struct{}, 1)
	go func() {
		defer close(requestsDone)

		// Wait for all active requests to complete
		a.logger.Info("Waiting for active requests to complete...")
		done := make(chan struct{})

		go func() {
			a.requestWg.Wait()
			close(done)
		}()

		// Monitor progress
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				a.logger.Info("All requests completed")
				return
			case <-ticker.C:
				activeCount := a.getActiveRequestCount()
				a.logger.WithField("active_requests", activeCount).Info("Still waiting for requests to complete...")
			case <-shutdownCtx.Done():
				activeCount := a.getActiveRequestCount()
				a.logger.WithField("active_requests", activeCount).Warn("Shutdown timeout reached, forcing shutdown")
				return
			}
		}
	}()

	// Wait for both server shutdown and requests to complete
	var shutdownErr error

	select {
	case err := <-serverShutdownDone:
		shutdownErr = err
		a.logger.Info("HTTP server shutdown completed")
	case <-shutdownCtx.Done():
		a.logger.Warn("Shutdown timeout reached")
		shutdownErr = fmt.Errorf("shutdown timeout exceeded")
	}

	// Wait a bit more for requests to finish if server shutdown completed quickly
	if shutdownErr == nil {
		select {
		case <-requestsDone:
			// All requests completed
		case <-time.After(2 * time.Second):
			// Give up after 2 more seconds
			activeCount := a.getActiveRequestCount()
			if activeCount > 0 {
				a.logger.WithField("active_requests", activeCount).Warn("Some requests still active, proceeding with shutdown")
			}
		}
	}

	// Cleanup resources
	if cleanupErr := a.cleanupResources(ctx); cleanupErr != nil {
		a.logger.WithField("error", cleanupErr).Error("Error during resource cleanup")
		if shutdownErr == nil {
			shutdownErr = cleanupErr
		}
	}

	if shutdownErr != nil {
		a.logger.WithField("error", shutdownErr).Error("Graceful shutdown completed with errors")
	} else {
		a.logger.Info("Graceful shutdown completed successfully")
	}

	return shutdownErr
}

// cleanupResources handles cleanup of database and other resources
func (a *App) cleanupResources(_ context.Context) error {
	a.logger.Info("Cleaning up resources...")

	// Close connection manager before closing database
	if connManager, err := pkgDatabase.GetConnectionManager(); err == nil {
		a.logger.Info("Closing connection manager")
		if err := connManager.Close(); err != nil {
			a.logger.WithField("error", err).Error("Error closing connection manager")
		} else {
			a.logger.Info("Connection manager closed successfully")
		}
	}

	// Close database connection if it exists
	if a.db != nil {
		// If tracing is enabled, record final stats
		if a.config.Tracing.Enabled {
			if err := ocsql.RecordStats(a.db, 5*time.Second); err != nil {
				a.logger.WithField("error", err).Error("Failed to record final database stats for tracing")
			}
		}

		a.logger.Info("Closing database connection")
		if err := a.db.Close(); err != nil {
			a.logger.WithField("error", err).Error("Error closing database connection")
			return err
		}
	}

	// Stop telemetry service if it exists
	if a.telemetryService != nil {
		a.logger.Info("Stopping telemetry service")
		// The telemetry service should respect context cancellation
	}

	a.logger.Info("Resource cleanup completed")
	return nil
}

// IsServerCreated safely checks if the server has been created
func (a *App) IsServerCreated() bool {
	a.serverMu.RLock()
	defer a.serverMu.RUnlock()
	return a.server != nil
}

// WaitForServerStart waits for the server to be created and initialized
// Returns true if the server started successfully, false if context expired
func (a *App) WaitForServerStart(ctx context.Context) bool {
	// Get the current channel under lock
	a.serverMu.RLock()
	started := a.serverStarted
	a.serverMu.RUnlock()

	// If the channel is nil, that's a logic error - just wait on the context
	if started == nil {
		a.logger.Error("serverStarted channel is nil - server initialization error")
		<-ctx.Done()
		return false
	}

	// Wait for signal or timeout
	select {
	case <-started:
		return a.IsServerCreated() // Double-check server was created
	case <-ctx.Done():
		return false
	}
}

// Initialize sets up all components of the application
func (a *App) Initialize() error {
	a.logger.WithField("version", a.config.Version).Info("Starting Notifuse application")

	if err := a.InitTracing(); err != nil {
		return err
	}

	if err := a.InitDB(); err != nil {
		return err
	}

	// Check if setup wizard is required (after migrations have run)
	var installedValue string
	err := a.db.QueryRow("SELECT value FROM settings WHERE key = 'is_installed'").Scan(&installedValue)
	a.isInstalled = err == nil && installedValue == "true"

	if !a.isInstalled {
		a.logger.Info("Setup wizard required - installation not complete")
	} else {
		a.logger.Info("System installation verified")
	}

	// Initialize dedicated blog cache
	a.blogCache = cache.NewInMemoryCache(domain.BlogCacheTTL)
	a.logger.Info("Blog cache initialized")

	if err := a.InitMailer(); err != nil {
		return err
	}

	if err := a.InitRepositories(); err != nil {
		return err
	}

	// Initialize services (with temporary keys if not installed)
	if err := a.InitServices(); err != nil {
		return err
	}

	if err := a.InitHandlers(); err != nil {
		return err
	}

	a.logger.Info("Application successfully initialized")

	// Send startup telemetry metrics
	if a.telemetryService != nil {
		go func() {
			ctx := context.Background()
			if err := a.telemetryService.SendMetricsForAllWorkspaces(ctx); err != nil {
				a.logger.WithField("error", err).Error("Failed to send startup telemetry metrics")
			}
		}()
	}

	return nil
}

// GetConfig returns the app's configuration
func (a *App) GetConfig() *config.Config {
	return a.config
}

// GetLogger returns the app's logger
func (a *App) GetLogger() logger.Logger {
	return a.logger
}

// GetMux returns the app's HTTP multiplexer
func (a *App) GetMux() *http.ServeMux {
	return a.mux
}

// GetDB returns the app's database connection
func (a *App) GetDB() *sql.DB {
	return a.db
}

// GetMailer returns the app's mailer
func (a *App) GetMailer() mailer.Mailer {
	return a.mailer
}

// Repository getters for testing
func (a *App) GetUserRepository() domain.UserRepository {
	return a.userRepo
}

func (a *App) GetWorkspaceRepository() domain.WorkspaceRepository {
	return a.workspaceRepo
}

func (a *App) GetContactRepository() domain.ContactRepository {
	return a.contactRepo
}

func (a *App) GetListRepository() domain.ListRepository {
	return a.listRepo
}

func (a *App) GetTemplateRepository() domain.TemplateRepository {
	return a.templateRepo
}

func (a *App) GetBroadcastRepository() domain.BroadcastRepository {
	return a.broadcastRepo
}

func (a *App) GetMessageHistoryRepository() domain.MessageHistoryRepository {
	return a.messageHistoryRepo
}

func (a *App) GetContactListRepository() domain.ContactListRepository {
	return a.contactListRepo
}

func (a *App) GetTransactionalNotificationRepository() domain.TransactionalNotificationRepository {
	return a.transactionalNotificationRepo
}

func (a *App) GetTelemetryRepository() domain.TelemetryRepository {
	return a.telemetryRepo
}

func (a *App) GetEmailQueueRepository() domain.EmailQueueRepository {
	return a.emailQueueRepo
}

func (a *App) GetTaskRepository() domain.TaskRepository {
	return a.taskRepo
}

func (a *App) GetEmailQueueWorker() *queue.EmailQueueWorker {
	return a.emailQueueWorker
}

func (a *App) GetAuthService() interface{} {
	return a.authService
}

func (a *App) GetTransactionalNotificationService() domain.TransactionalNotificationService {
	return a.transactionalNotificationService
}

// GetAutomationScheduler returns the automation scheduler instance
func (a *App) GetAutomationScheduler() *service.AutomationScheduler {
	return a.automationScheduler
}

// GetTaskScheduler returns the task scheduler instance.
// Used by tests that drive the live scheduler directly, bypassing app.Start()'s
// delayed-start goroutine.
func (a *App) GetTaskScheduler() *service.TaskScheduler {
	return a.taskScheduler
}

// SetHandler allows setting a custom HTTP handler
func (a *App) SetHandler(handler http.Handler) {
	a.mux = handler.(*http.ServeMux)
}

// incrementActiveRequests atomically increments the active request counter
func (a *App) incrementActiveRequests() {
	atomic.AddInt64(&a.activeRequests, 1)
	a.requestWg.Add(1)
}

// decrementActiveRequests atomically decrements the active request counter
func (a *App) decrementActiveRequests() {
	atomic.AddInt64(&a.activeRequests, -1)
	a.requestWg.Done()
}

// getActiveRequestCount returns the current number of active requests
func (a *App) getActiveRequestCount() int64 {
	return atomic.LoadInt64(&a.activeRequests)
}

// GetActiveRequestCount returns the current number of active requests (public interface method)
func (a *App) GetActiveRequestCount() int64 {
	return a.getActiveRequestCount()
}

// SetShutdownTimeout sets the timeout for graceful shutdown
func (a *App) SetShutdownTimeout(timeout time.Duration) {
	a.shutdownTimeout = timeout
	a.logger.WithField("shutdown_timeout", timeout).Info("Shutdown timeout configured")
}

// GetShutdownContext returns the shutdown context for components that need to watch for shutdown
func (a *App) GetShutdownContext() context.Context {
	return a.shutdownCtx
}

// isShuttingDown returns true if the application is in shutdown mode
func (a *App) isShuttingDown() bool {
	select {
	case <-a.shutdownCtx.Done():
		return true
	default:
		return false
	}
}

// gracefulShutdownMiddleware wraps HTTP handlers to track active requests
func (a *App) gracefulShutdownMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if we're shutting down
		if a.isShuttingDown() {
			// Return 503 Service Unavailable if we're shutting down
			http.Error(w, "Server is shutting down", http.StatusServiceUnavailable)
			return
		}

		// Track this request
		a.incrementActiveRequests()
		defer a.decrementActiveRequests()

		// Call the next handler
		next.ServeHTTP(w, r)
	})
}

// Ensure App implements AppInterface
var _ AppInterface = (*App)(nil)
