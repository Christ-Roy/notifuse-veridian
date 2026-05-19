package domain

import (
	"context"
	"time"
)

//go:generate mockgen -destination mocks/mock_veridian_idempotency_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianIdempotencyRepository

// VeridianIdempotencyEntry represente une ligne de la table
// veridian_idempotency_keys (CONTRAT-HUB sec. 5.11).
//
// Le middleware veridian_idempotency.go utilise cette table pour rejouer
// une reponse cachee plutot que de re-executer le handler quand le client
// (Hub) renvoie la meme cle dans les 24h. Defense contre le double-deploy
// d'un effet de bord (provisioning double, charge Stripe double, etc.).
type VeridianIdempotencyEntry struct {
	Key            string    `json:"key"`              // UUID v4 cote client
	Endpoint       string    `json:"endpoint"`         // ex: "/api/tenants/provision"
	TenantID       string    `json:"tenant_id,omitempty"`
	RequestHash    string    `json:"request_hash"`     // SHA-256 hex du body normalise
	ResponseStatus int       `json:"response_status"`  // pour replay
	ResponseBody   []byte    `json:"response_body"`    // JSONB raw pour replay
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// VeridianIdempotencyRepository gere la table veridian_idempotency_keys.
//
// Get : lookup par PK (key). Retourne sql.ErrNoRows si absent.
// Save : INSERT. Conflit sur PK (race) → erreur (le caller doit catch
//
//	avec errors.Is et fallback en GET).
//
// DeleteExpired : cron, retourne le nombre de lignes supprimees.
type VeridianIdempotencyRepository interface {
	Get(ctx context.Context, key string) (*VeridianIdempotencyEntry, error)
	Save(ctx context.Context, entry *VeridianIdempotencyEntry) error
	DeleteExpired(ctx context.Context) (int64, error)
}
