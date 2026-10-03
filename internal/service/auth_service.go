package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrSessionExpired = errors.New("session expired")
	ErrUserNotFound   = errors.New("user not found")
)

// UserClaims for user session tokens
type UserClaims struct {
	UserID    string `json:"user_id"`
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Email     string `json:"email,omitempty"`
	jwt.RegisteredClaims
}

// InvitationClaims for workspace invitation tokens
type InvitationClaims struct {
	InvitationID string `json:"invitation_id"`
	WorkspaceID  string `json:"workspace_id"`
	Email        string `json:"email"`
	jwt.RegisteredClaims
}

type AuthService struct {
	repo          domain.AuthRepository
	workspaceRepo domain.WorkspaceRepository
	logger        logger.Logger
	getSecret     func() ([]byte, error) // Changed from getKeys

	// Cached secret
	cachedSecret []byte
	secretLoaded bool
}

type AuthServiceConfig struct {
	Repository          domain.AuthRepository
	WorkspaceRepository domain.WorkspaceRepository
	GetSecret           func() ([]byte, error) // Changed from GetKeys
	Logger              logger.Logger
}

func NewAuthService(cfg AuthServiceConfig) *AuthService {
	return &AuthService{
		repo:          cfg.Repository,
		workspaceRepo: cfg.WorkspaceRepository,
		logger:        cfg.Logger,
		getSecret:     cfg.GetSecret,
		secretLoaded:  false,
	}
}

// ensureSecret loads and caches JWT secret if not already loaded
func (s *AuthService) ensureSecret() error {
	if s.secretLoaded {
		return nil
	}

	secret, err := s.getSecret()
	if err != nil {
		return fmt.Errorf("JWT secret not available: %w", err)
	}

	if len(secret) == 0 {
		return fmt.Errorf("JWT secret cannot be empty")
	}

	// Warn if secret is less than recommended length
	if len(secret) < 32 && s.logger != nil {
		s.logger.WithField("length", len(secret)).Warn("JWT secret is less than 32 bytes - consider using a stronger secret for production")
	}

	s.cachedSecret = secret
	s.secretLoaded = true
	return nil
}

// InvalidateSecretCache clears the cached secret, forcing it to be reloaded on next use
func (s *AuthService) InvalidateSecretCache() {
	s.secretLoaded = false
}
func (s *AuthService) AuthenticateUserFromContext(ctx context.Context) (*domain.User, error) {

	userID, ok := ctx.Value(domain.UserIDKey).(string)
	if !ok || userID == "" {
		return nil, ErrUserNotFound
	}
	userType, ok := ctx.Value(domain.UserTypeKey).(string)
	if !ok || userType == "" {
		return nil, ErrUserNotFound
	}
	if userType == string(domain.UserTypeUser) {
		sessionID, ok := ctx.Value(domain.SessionIDKey).(string)
		if !ok || sessionID == "" {
			return nil, ErrUserNotFound
		}
		return s.VerifyUserSession(ctx, userID, sessionID)
	} else if userType == string(domain.UserTypeAPIKey) {
		return s.GetUserByID(ctx, userID)
	}
	return nil, ErrUserNotFound
}

// AuthenticateUserForWorkspace checks if the user exists and the session is valid for a specific workspace
func (s *AuthService) AuthenticateUserForWorkspace(ctx context.Context, workspaceID string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
	// Check if user is already set in context for this workspace
	if workspaceUser, ok := ctx.Value(domain.WorkspaceUserKey(workspaceID)).(*domain.User); ok && workspaceUser != nil {
		// Also check if we have the userWorkspace in context
		if userWorkspace, ok := ctx.Value(domain.UserWorkspaceKey).(*domain.UserWorkspace); ok && userWorkspace != nil {
			return ctx, workspaceUser, userWorkspace, nil
		}
	}

	// === Veridian patch — mesure prod 2026-10-03 (mission "API & agents") ===
	// Avant ce correctif, les trois erreurs ci-dessous remontaient telles
	// quelles (sql.ErrNoRows nu, ErrUserNotFound, erreur repo brute) jusqu'au
	// handler HTTP, qui ne les reconnaissait pas et retombait sur son 500
	// générique. Zéro fuite de données (l'accès était bien bloqué), mais un
	// agent/CLI scripté ne peut pas distinguer "clé révoquée" (401, il doit
	// réémettre un jeton) de "pas les droits sur ce workspace" (403, inutile
	// de réessayer) de "panne serveur réelle" (500, à remonter). On classe
	// l'erreur UNE FOIS ICI, à la source : tout appelant (service puis
	// handler HTTP, cf. internal/http/utils.go WriteAuthAwareError) en
	// profite sans avoir à connaître les détails du repo.
	user, err := s.AuthenticateUserFromContext(ctx)
	if err != nil {
		// Identité elle-même invalide : session expirée, clé API révoquée/
		// supprimée (GetUserByID -> ErrUserNotFound), ou claims absents.
		// -> 401. On ne reclasse QUE les erreurs attendues de ce chemin
		// (sentinelles ErrUserNotFound/ErrSessionExpired) — une vraie panne
		// DB pendant VerifyUserSession/GetUserByID reste une 500.
		if errors.Is(err, ErrUserNotFound) || errors.Is(err, ErrSessionExpired) {
			return ctx, nil, nil, &domain.ErrAuthenticationFailed{Message: err.Error()}
		}
		return ctx, nil, nil, err
	}

	// Le workspace n'existe pas. Ne jamais le distinguer de "pas membre" au
	// client (sinon une clé scopée sur un AUTRE workspace peut sonder quels
	// IDs existent) : même 403 "not authorized for this workspace" dans les
	// deux cas. On ne reclasse QUE l'erreur typée "not found" — une vraie
	// panne DB (connexion coupée, timeout) reste une 500, jamais masquée
	// derrière un faux 403.
	_, err = s.workspaceRepo.GetByID(ctx, workspaceID)
	if err != nil {
		var notFound *domain.ErrWorkspaceNotFound
		if errors.As(err, &notFound) {
			return ctx, nil, nil, &domain.ErrUnauthorized{Message: "not authorized for this workspace"}
		}
		return ctx, nil, nil, err
	}

	// Identité valide (clé API non révoquée, session active) mais PAS membre
	// de CE workspace : c'est le cas mesuré en prod "clé scopée sur un autre
	// workspace". -> 403, jamais 401 (l'authentification a réussi, c'est
	// l'autorisation qui échoue). Même garde-fou : seule l'erreur typée
	// "pas membre" est reclassée, une vraie panne DB reste une 500.
	userWorkspace, err := s.workspaceRepo.GetUserWorkspace(ctx, user.ID, workspaceID)
	if err != nil {
		var notMember *domain.ErrUserNotWorkspaceMember
		if errors.As(err, &notMember) {
			return ctx, nil, nil, &domain.ErrUnauthorized{Message: "not authorized for this workspace"}
		}
		return ctx, nil, nil, err
	}

	// Store user and user workspace in context for future calls - return the new context to the caller
	newCtx := context.WithValue(ctx, domain.WorkspaceUserKey(workspaceID), user)
	newCtx = context.WithValue(newCtx, domain.UserWorkspaceKey, userWorkspace)
	return newCtx, user, userWorkspace, nil
}

// VerifyUserSession checks if the user exists and the session is valid
func (s *AuthService) VerifyUserSession(ctx context.Context, userID, sessionID string) (*domain.User, error) {
	// First check if the session is valid and not expired
	expiresAt, err := s.repo.GetSessionByID(ctx, sessionID, userID)

	if err == sql.ErrNoRows {
		if s.logger != nil {
			s.logger.WithField("user_id", userID).WithField("session_id", sessionID).Error("Session not found")
		}
		return nil, ErrSessionExpired
	}
	if err != nil {
		if s.logger != nil {
			s.logger.WithField("user_id", userID).WithField("session_id", sessionID).WithField("error", err.Error()).Error("Failed to query session")
		}
		return nil, err
	}

	// Check if session is expired
	if time.Now().After(*expiresAt) {
		if s.logger != nil {
			s.logger.WithField("user_id", userID).WithField("session_id", sessionID).WithField("expires_at", expiresAt).Error("Session expired")
		}
		return nil, ErrSessionExpired
	}

	// Get user details
	user, err := s.repo.GetUserByID(ctx, userID)

	if err == sql.ErrNoRows {
		if s.logger != nil {
			s.logger.WithField("user_id", userID).Error("User not found")
		}
		return nil, ErrUserNotFound
	}
	if err != nil {
		if s.logger != nil {
			s.logger.WithField("user_id", userID).WithField("error", err.Error()).Error("Failed to query user")
		}
		return nil, err
	}

	return user, nil
}

// GenerateUserAuthToken generates an authentication token for a user
func (s *AuthService) GenerateUserAuthToken(user *domain.User, sessionID string, expiresAt time.Time) string {
	if err := s.ensureSecret(); err != nil {
		if s.logger != nil {
			s.logger.WithField("error", err.Error()).Error("Cannot generate auth token")
		}
		return ""
	}

	claims := UserClaims{
		UserID:    user.ID,
		Type:      string(domain.UserTypeUser),
		SessionID: sessionID,
		Email:     user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.cachedSecret)
	if err != nil && s.logger != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to sign token")
		return ""
	}

	return signed
}

// GenerateAPIAuthToken generates an authentication token for an API key
func (s *AuthService) GenerateAPIAuthToken(user *domain.User) string {
	if err := s.ensureSecret(); err != nil {
		if s.logger != nil {
			s.logger.WithField("error", err.Error()).Error("Cannot generate API token")
		}
		return ""
	}

	claims := UserClaims{
		UserID: user.ID,
		Email:  user.Email, // Include email for SMTP Bridge authentication
		Type:   string(domain.UserTypeAPIKey),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * 24 * 365 * 10)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.cachedSecret)
	if err != nil && s.logger != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to sign API token")
		return ""
	}

	return signed
}

// GenerateInvitationToken generates a JWT token for a workspace invitation
func (s *AuthService) GenerateInvitationToken(invitation *domain.WorkspaceInvitation) string {
	if err := s.ensureSecret(); err != nil {
		if s.logger != nil {
			s.logger.WithField("error", err.Error()).Error("Cannot generate invitation token")
		}
		return ""
	}

	claims := InvitationClaims{
		InvitationID: invitation.ID,
		WorkspaceID:  invitation.WorkspaceID,
		Email:        invitation.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(invitation.ExpiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.cachedSecret)
	if err != nil && s.logger != nil {
		s.logger.WithField("error", err.Error()).Error("Failed to sign invitation token")
		return ""
	}

	return signed
}

// ValidateInvitationToken validates a JWT invitation token and returns the invitation details
func (s *AuthService) ValidateInvitationToken(tokenString string) (invitationID, workspaceID, email string, err error) {
	if err := s.ensureSecret(); err != nil {
		return "", "", "", fmt.Errorf("secret not available: %w", err)
	}

	claims := &InvitationClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		// CRITICAL: Verify signing method to prevent algorithm confusion attacks
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.cachedSecret, nil
	})

	// CRITICAL: Check both error AND token.Valid
	if err != nil {
		return "", "", "", fmt.Errorf("invalid invitation token: %w", err)
	}
	if !token.Valid {
		return "", "", "", fmt.Errorf("invalid invitation token: token not valid")
	}

	return claims.InvitationID, claims.WorkspaceID, claims.Email, nil
}

// GetUserByID retrieves a user by their ID
func (s *AuthService) GetUserByID(ctx context.Context, userID string) (*domain.User, error) {
	// Delegate to the repository
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		if s.logger != nil {
			s.logger.WithField("error", err.Error()).WithField("user_id", userID).Error("Failed to get user by ID")
		}
		return nil, err
	}
	return user, nil
}
