// Package accountgate ferme la connexion d'un compte supprimé côté Hub (users.hub_deleted_at, V71) : aucune session ne s'ouvre, aucune
// session ne vaut. Avant le 25/11/2026 la suppression coupait les sessions du moment, mais `user.signin` + `user.verify` (code par e-mail) et
// un lien de connexion déjà émis en rouvraient une. Les dépôts d'origine ne sont pas modifiés : ils sont enveloppés au câblage.
package accountgate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/google/uuid"
)

// SinceVar : date (RFC 3339) à partir de laquelle une suppression au Hub coupe le compte. Les comptes supprimés avant restent ouverts, comptés et
// dits (GET /api/hub/users, journal du démarrage), le temps d'une décision. La reculer les ferme, sans rien coder.
const SinceVar = "VERIDIAN_ACCOUNT_GATE_SINCE"

// ReadSince lit la date. Illisible : erreur, l'application ne démarre pas. Absente : erreur en production
// reliée au Hub ; ailleurs, date nulle, donc TOUT compte
// supprimé est coupé. La garde n'est jamais éteinte.
func ReadSince(value string, production bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if production {
			return time.Time{}, fmt.Errorf("%s absente : date à partir de laquelle un compte supprimé au Hub est coupé (RFC 3339)", SinceVar)
		}
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s illisible (RFC 3339 attendu) : %w", SinceVar, err)
	}
	return t.UTC(), nil
}

var since atomic.Int64 // secondes ; 0 : tout compte supprimé

// SetSince pose la date, une fois, au démarrage.
func SetSince(t time.Time) {
	if t.IsZero() {
		since.Store(0)
		return
	}
	since.Store(t.Unix())
}

// Since rend la date en vigueur. Zéro (1970) : tout compte supprimé est coupé, c'est aussi l'état avant SetSince.
func Since() time.Time { return time.Unix(since.Load(), 0).UTC() }

// CountOpen compte les comptes supprimés avant la date qui ont encore une place dans un espace ou une session : ceux que la garde ne coupe pas.
func CountOpen(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u WHERE u.type = 'user' AND u.hub_deleted_at < $1
		AND (EXISTS (SELECT 1 FROM user_workspaces w WHERE w.user_id = u.id) OR EXISTS (SELECT 1 FROM user_sessions s WHERE s.user_id = u.id))`, Since()).Scan(&n)
	return n, err
}

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
	err = db.QueryRowContext(ctx, `SELECT COALESCE(hub_deleted_at >= $2, FALSE) FROM users WHERE id = $1`, id.String(), Since()).Scan(&d)
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
