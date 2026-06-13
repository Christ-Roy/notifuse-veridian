package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// ⚠️ COLONNE INERTE / DÉPRÉCIÉE DEPUIS LE PIVOT STAND-ALONE 2026-05-31. ⚠️
//
// Le pipeline "envoi via Hub Mail Gateway" (mail-provider-choice + proxy
// mail-accounts) a été RETIRÉ le 2026-05-31 : il créait une dépendance Hub
// sur l'envoi (aberration vs règle d'or stand-alone) et n'a JAMAIS été câblé
// à l'envoi réel. Conséquences pour cette colonne `mail_provider_choice` :
//   - Plus AUCUN code Go ne la lit ni ne l'écrit (lib `pkg/hub_mail_gateway`,
//     repo `veridian_mail_provider_postgres.go`, UI `veridian_mail_account_settings.tsx`,
//     wiring `app.go` : tout supprimé — cf. app.go:1344 + memory
//     project_mail_sending_standalone_decision). L'envoi passe uniquement par
//     le provider configuré par workspace (Settings > Integrations, natif
//     upstream : SMTP/SES/...).
//   - La colonne reste physiquement en prod (V48 déployée) à 'smtp_generic'
//     sur tous les workspaces : c'est un résidu additif INERTE, sans impact
//     fonctionnel (defaut constant, lu par personne).
//
// On NE supprime PAS physiquement la colonne ici : un retrait de colonne
// (instruction destructive DDL) = tier 💀 (cf. Constitution CI §12 Expand &
// Contract + CLAUDE.md racine §20), il casserait le rollback car le tag Docker
// N-1 doit pouvoir tourner sur le schéma courant. Le retrait physique, s'il est
// décidé, se fait via une migration V49 "contract" dédiée en 2 deploys. En
// attendant, la migration V48 reste en place telle quelle pour préserver la
// linéarité de l'historique et la replayabilité (idempotente).
//
// --- Doc historique d'origine (vague 6, 2026-05-25, archi abandonnée) ---
//
// V48Migration ajoute la colonne `workspaces.mail_provider_choice` TEXT NOT NULL
// DEFAULT 'smtp_generic' pour materialiser la preference de provider d'envoi
// mail par workspace (CONTRAT-HUB §3.4 ticket mail-send-as-user-via-hub-gateway,
// vague 6 2026-05-25).
//
// Pourquoi : pour permettre a un workspace d'envoyer ses transactionnels via
// le compte Gmail du user owner (route Hub Mail Gateway §3.3) plutot que le
// sender SMTP generique Veridian. La preference vit sur le workspace plutot
// que sur le user — un workspace = une boite expedit. Si plus tard on veut
// per-user (per-seat), on ajoute une colonne `users.mail_provider_choice`
// (additif, pas de break).
//
// Schema :
//
//   mail_provider_choice TEXT NOT NULL DEFAULT 'smtp_generic'
//     CHECK (mail_provider_choice IN ('smtp_generic', 'hub_gmail'))
//
// Valeurs autorisees :
//   - 'smtp_generic' : sender SMTP Veridian generique (comportement actuel,
//     defaut sur tous les workspaces existants — back-compat strict).
//   - 'hub_gmail' : passe par POST <hub>/api/mail/send-as-user (Gmail user owner).
//
// Le CHECK constraint enforce les valeurs valides cote DB (defense en
// profondeur en plus de la validation Go IsValidMailProviderChoice). Si on
// veut ajouter 'microsoft_via_hub' / 'imap_custom' plus tard, on fait un
// ALTER CONSTRAINT additif strict (cf. CLAUDE.md secrets matrix §extensibilite).
//
// Safety §12 (Expand & Contract) : ADD COLUMN NON NULLABLE avec DEFAULT
// constant = additif pur sur Postgres ≥ 11 (backfill metadata-only, pas de
// rewrite table, AccessExclusiveLock court < 1s). Le tag Docker precedent
// (V47) tourne sur ce schema sans probleme (il n'utilise pas la colonne).
//
// Idempotent : `ADD COLUMN IF NOT EXISTS` sur ALTER TABLE. Re-run sans effet.
// Le CHECK constraint en clause inline est cree avec la colonne — il n'a pas
// besoin de NOT VALID + VALIDATE separe car la colonne nait avec un DEFAULT
// valide.
//
// Pas de patch upstream : on ajoute UNE colonne au table upstream `workspaces`
// via migration DB (additif). Aucun fichier upstream `internal/domain/workspace.go`
// ou `internal/repository/workspace_postgres.go` n'est touche. Le repo Veridian
// dedie `veridian_mail_provider_postgres.go` lit/ecrit cette colonne via SQL
// brut (meme pattern que V46 `users.hub_user_id`).
type V48Migration struct{}

func (m *V48Migration) GetMajorVersion() float64 {
	return 48.0
}

func (m *V48Migration) HasSystemUpdate() bool {
	return true
}

func (m *V48Migration) HasWorkspaceUpdate() bool {
	return false
}

func (m *V48Migration) ShouldRestartServer() bool {
	return false
}

func (m *V48Migration) UpdateSystem(ctx context.Context, _ *config.Config, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE workspaces
		ADD COLUMN IF NOT EXISTS mail_provider_choice TEXT NOT NULL DEFAULT 'smtp_generic'
		CHECK (mail_provider_choice IN ('smtp_generic', 'hub_gmail'))
	`); err != nil {
		return fmt.Errorf("add workspaces.mail_provider_choice: %w", err)
	}
	return nil
}

func (m *V48Migration) UpdateWorkspace(_ context.Context, _ *config.Config, _ *domain.Workspace, _ DBExecutor) error {
	return nil
}

func init() {
	Register(&V48Migration{})
}
