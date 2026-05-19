package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V35Migration cree la table veridian_idempotency_keys pour le middleware
// Idempotency-Key (CONTRAT-HUB sec. 5.11).
//
// Le middleware veridian_idempotency.go intercepte les endpoints d'ecriture
// (provision, update-plan, suspend, resume, soft-delete, restore, purge,
// touch) et :
//
//   - Lit le header Idempotency-Key envoye par le client (Hub).
//   - Si la cle existe en DB et le request_hash matche → replay la reponse
//     cachee (status + body) sans re-executer le handler.
//   - Si la cle existe mais le request_hash differe → 422 (le client a
//     reutilise la meme cle pour une requete differente — bug client).
//   - Si la cle n'existe pas → execute le handler normalement et INSERT la
//     reponse en DB avec TTL 24h.
//
// Cleanup : un cron quotidien DELETE WHERE expires_at < NOW().
//
// Schema design :
//   - key VARCHAR(64) PRIMARY KEY : longueur UUID v4 (36 chars) + marge
//   - endpoint VARCHAR(128) : path complet pour distinguer p.ex. /provision
//     de /update-plan avec la meme cle
//   - tenant_id VARCHAR(64) NULL : extrait du payload pour traceability,
//     nullable car certains endpoints n'ont pas de tenant_id (ex: wipe-test)
//   - request_hash CHAR(64) : SHA-256 du body normalise (detection mismatch)
//   - response_status INT : pour replay
//   - response_body JSONB : pour replay
//   - created_at, expires_at : pour TTL cron
type V35Migration struct{}

func (m *V35Migration) GetMajorVersion() float64 {
	return 35.0
}

func (m *V35Migration) HasSystemUpdate() bool {
	return true
}

func (m *V35Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V35Migration) ShouldRestartServer() bool {
	return false
}

func (m *V35Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS veridian_idempotency_keys (
			key VARCHAR(64) PRIMARY KEY,
			endpoint VARCHAR(128) NOT NULL,
			tenant_id VARCHAR(64),
			request_hash CHAR(64) NOT NULL,
			response_status INT NOT NULL,
			response_body JSONB NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create veridian_idempotency_keys: %w", err)
	}
	return nil
}

func (m *V35Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V35Migration{})
}
