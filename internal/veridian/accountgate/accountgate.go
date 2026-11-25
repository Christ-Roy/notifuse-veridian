// Package accountgate ferme la connexion d'un compte supprimé côté Hub (users.hub_deleted_at, V71) : aucune session ne s'ouvre, aucune
// session ne vaut. Avant le 25/11/2026 la suppression coupait les sessions du moment, mais `user.signin` + `user.verify` (code par e-mail) et
// un lien de connexion déjà émis en rouvraient une. Les dépôts d'origine ne sont pas modifiés : ils sont enveloppés au câblage.
package accountgate

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/google/uuid"
)

func deleted(ctx context.Context, db *sql.DB, userID string) (bool, error) {
	// Geste du Hub au nom d'un propriétaire (session interne de quelques secondes, jamais une requête HTTP) : transférer l'espace d'un compte
	// supprimé doit rester possible.
	if sys, _ := ctx.Value(domain.SystemCallKey).(bool); sys {
		return false, nil
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return false, nil // pas un compte : le dépôt d'origine répond
	}
	var d bool
	err = db.QueryRowContext(ctx, `SELECT hub_deleted_at IS NOT NULL FROM users WHERE id = $1`, id.String()).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // compte absent : le dépôt d'origine répond
	}
	return d, err
}

// Auth enveloppe le dépôt d'authentification : chaque requête signée passe par là.
type Auth struct {
	domain.AuthRepository
	DB *sql.DB
}

// GetSessionByID : session d'un compte supprimé = session absente (401).
func (a *Auth) GetSessionByID(ctx context.Context, sessionID, userID string) (*time.Time, error) {
	gone, err := deleted(ctx, a.DB, userID)
	if err != nil {
		return nil, err
	}
	if gone {
		return nil, sql.ErrNoRows
	}
	return a.AuthRepository.GetSessionByID(ctx, sessionID, userID)
}

// Users enveloppe le dépôt des utilisateurs.
type Users struct {
	domain.UserRepository
	DB *sql.DB
}

// CreateSession : unique porte d'ouverture (code par e-mail, lien de connexion, session directe). Compte supprimé = compte absent.
func (u *Users) CreateSession(ctx context.Context, session *domain.Session) error {
	gone, err := deleted(ctx, u.DB, session.UserID)
	if err != nil {
		return err
	}
	if gone {
		return &domain.ErrUserNotFound{Message: "user does not exist"}
	}
	return u.UserRepository.CreateSession(ctx, session)
}

// GetSessionByID : session d'un compte supprimé = session absente.
func (u *Users) GetSessionByID(ctx context.Context, id string) (*domain.Session, error) {
	s, err := u.UserRepository.GetSessionByID(ctx, id)
	if err != nil {
		return nil, err
	}
	gone, err := deleted(ctx, u.DB, s.UserID)
	if err != nil {
		return nil, err
	}
	if gone {
		return nil, &domain.ErrSessionNotFound{Message: "session not found"}
	}
	return s, nil
}

// GetSessionsByUserID : aucune session, donc aucun code de connexion à comparer.
func (u *Users) GetSessionsByUserID(ctx context.Context, userID string) ([]*domain.Session, error) {
	gone, err := deleted(ctx, u.DB, userID)
	if err != nil {
		return nil, err
	}
	if gone {
		return nil, nil
	}
	return u.UserRepository.GetSessionsByUserID(ctx, userID)
}
