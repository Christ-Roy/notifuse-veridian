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
// Get : lookup par (key, tenantID). Retourne sql.ErrNoRows si absent.
// tenantID vide = scope "pas de tenant identifiable" (NULL cote colonne).
//
// === Veridian patch 2026-10-04 (audit backend) ===
// Avant ce correctif, le lookup etait WHERE key = $1 SEUL, globalement :
// deux appelants (tenants differents) choisissant par coincidence la meme
// valeur de cle se rejouaient mutuellement leurs reponses cachees -- un
// rejeu cross-tenant. Pas de migration/index necessaire : key reste la PK,
// Postgres localise la ligne UNIQUE par cle avant meme d'evaluer le filtre
// tenant_id (0 ou 1 ligne), donc aucun index composite n'apporte quoi que
// ce soit. Meme raisonnement que V41 (veridian_api_key_grace) : une table
// petite/transiente (TTL 24h) n'a pas besoin d'un index que CREATE INDEX
// CONCURRENTLY ne peut de toute facon pas poser depuis cette migration
// (executeMigration wrappe UpdateSystem en BEGIN/COMMIT, CONCURRENTLY est
// interdit en transaction -- cf check-migration-safety.sh §12).
//
// Save : INSERT. Conflit sur PK (race) → erreur (le caller doit catch
// avec errors.Is et fallback en GET).
//
// DeleteExpired : cron, retourne le nombre de lignes supprimees.
type VeridianIdempotencyRepository interface {
	Get(ctx context.Context, key string, tenantID string) (*VeridianIdempotencyEntry, error)
	Save(ctx context.Context, entry *VeridianIdempotencyEntry) error
	DeleteExpired(ctx context.Context) (int64, error)
}
