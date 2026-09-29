package migrations

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/config"
	"github.com/Notifuse/notifuse/internal/domain"
)

// V58Migration makes message_history.sent_at nullable and stops the
// webhook_message_history_trigger from firing a false email.sent event on
// INSERT when sent_at is not set.
//
// 🔴 Correctif du 2026-09-29 (incident workspace robertbrunon, automation
// ecomscale) : sent_at était NOT NULL et posé sans condition par TROIS voies
// d'envoi (queue/worker.go upsertMessageHistory, broadcast/message_sender.go
// SendToRecipient, email_service.go SendEmailForTemplate) même quand le send
// échouait (gate de plafond/chauffe/classe, pré-filtre, échec SMTP réel). Le
// 29/09, 36 lignes message_history ont porté sent_at ce jour-là pour
// l'automation ecomscale alors que les relais mail-relay-nord et
// mail-relay-ovh-dev n'ont réellement transmis que 3 messages : la plupart des
// 36 étaient des rejets `excluded_provider_class:*` avec sent_at == failed_at.
// Deux effets mesurés : (1) toute stat agrégée par
// `SUM(CASE WHEN sent_at IS NOT NULL ...)` (message_history_postgre.go
// GetBroadcastStats, veridian_engagement_by_class_postgres.go, domain/analytics.go)
// comptait des échecs comme envois ; (2) webhook_message_history_trigger
// déclenchait `email.sent` sur CHAQUE INSERT, y compris un échec permanent
// (jamais retenté, donc jamais corrigé par un UPDATE ultérieur) — faux
// événement livré à toute intégration webhook abonnée.
//
// Les COUNT de plafond journalier (CountSentSinceForContact/Class/SenderDomain,
// veridianReserveDailyQuota) filtraient déjà `failed_at IS NULL` en plus de
// `sent_at >= since` : ils n'étaient PAS faussés par ce bug (une ligne
// sent_at+failed_at simultanés ne matche aucune fenêtre `since` réaliste une
// fois le jour passé, et est de toute façon exclue par failed_at IS NULL). Le
// bug était un problème d'AUDIT/STATS/WEBHOOK, pas d'oversend — mais restait la
// cause exacte du symptôme rapporté ("36 envoyés, 3 réels").
//
// Expand-only : un binaire plus ancien qui lit sent_at continue de fonctionner
// (les lignes historiques gardent leur valeur ; seules les nouvelles lignes en
// échec deviennent NULL au lieu d'une fausse date).
type V58Migration struct{}

func (m *V58Migration) GetMajorVersion() float64  { return 58.0 }
func (m *V58Migration) HasSystemUpdate() bool     { return false }
func (m *V58Migration) HasWorkspaceUpdate() bool  { return true }
func (m *V58Migration) ShouldRestartServer() bool { return false }

func (m *V58Migration) UpdateSystem(_ context.Context, _ *config.Config, _ DBExecutor) error {
	return nil
}

func (m *V58Migration) UpdateWorkspace(ctx context.Context, _ *config.Config, _ *domain.Workspace, db DBExecutor) error {
	if _, err := db.ExecContext(ctx, `ALTER TABLE message_history ALTER COLUMN sent_at DROP NOT NULL`); err != nil {
		return fmt.Errorf("make message_history.sent_at nullable: %w", err)
	}

	// Repair historical rows that already carry the bug: sent_at posed
	// alongside failed_at (a permanent policy rejection or SMTP failure that
	// was never actually accepted by SMTP). Never touch a row without
	// failed_at — a genuinely successful send must keep its sent_at.
	if _, err := db.ExecContext(ctx, `UPDATE message_history SET sent_at = NULL WHERE sent_at IS NOT NULL AND failed_at IS NOT NULL`); err != nil {
		return fmt.Errorf("repair message_history rows with sent_at posed on a failure: %w", err)
	}

	// Re-create the webhook trigger function so an INSERT with sent_at IS NULL
	// (permanent rejection, never retried) never fires a false email.sent
	// event. Full body kept in sync with database/init.go (dual-maintenance
	// pattern already used by this fork, cf. v56.go veridian_profile_id).
	if _, err := db.ExecContext(ctx, `CREATE OR REPLACE FUNCTION webhook_message_history_trigger()
		RETURNS TRIGGER AS $$
		DECLARE
			sub RECORD;
			event_kind VARCHAR(50);
			event_timestamp TIMESTAMPTZ;
			payload JSONB;
		BEGIN
			-- Detect which email event occurred
			-- Veridian fork (correctif 2026-09-29, v57) : sent_at est désormais nullable
			-- et n'est posé que par un envoi réellement accepté. Un INSERT dont sent_at
			-- est NULL (rejet de gate / échec SMTP permanent, jamais retenté) ne doit pas
			-- déclencher email.sent : ce serait un faux positif webhook pour un mail qui
			-- n'a jamais quitté l'infra.
			IF TG_OP = 'INSERT' THEN
				IF NEW.sent_at IS NULL THEN
					RETURN NEW;
				END IF;
				event_kind := 'email.sent';
				event_timestamp := NEW.sent_at;
			ELSIF TG_OP = 'UPDATE' THEN
				IF NEW.delivered_at IS NOT NULL AND OLD.delivered_at IS NULL THEN
					event_kind := 'email.delivered';
					event_timestamp := NEW.delivered_at;
				ELSIF NEW.opened_at IS NOT NULL AND OLD.opened_at IS NULL THEN
					event_kind := 'email.opened';
					event_timestamp := NEW.opened_at;
				ELSIF NEW.clicked_at IS NOT NULL AND OLD.clicked_at IS NULL THEN
					event_kind := 'email.clicked';
					event_timestamp := NEW.clicked_at;
				ELSIF NEW.bounced_at IS NOT NULL AND OLD.bounced_at IS NULL THEN
					event_kind := 'email.bounced';
					event_timestamp := NEW.bounced_at;
				ELSIF NEW.complained_at IS NOT NULL AND OLD.complained_at IS NULL THEN
					event_kind := 'email.complained';
					event_timestamp := NEW.complained_at;
				ELSIF NEW.unsubscribed_at IS NOT NULL AND OLD.unsubscribed_at IS NULL THEN
					event_kind := 'email.unsubscribed';
					event_timestamp := NEW.unsubscribed_at;
				ELSE
					RETURN NEW;
				END IF;
			ELSE
				RETURN NEW;
			END IF;

			-- Build rich payload with full message context
			payload := jsonb_build_object(
				'email', NEW.contact_email,
				'message_id', NEW.id,
				'template_id', NEW.template_id,
				'broadcast_id', NEW.broadcast_id,
				'list_id', NEW.list_id,
				'channel', NEW.channel,
				'event_timestamp', event_timestamp
			);

			-- Insert webhook deliveries for matching subscriptions
			FOR sub IN
				SELECT id FROM webhook_subscriptions
				WHERE enabled = true AND event_kind = ANY(ARRAY(SELECT jsonb_array_elements_text(settings->'event_types')))
			LOOP
				INSERT INTO webhook_deliveries (id, subscription_id, event_type, payload, status, attempts, max_attempts, next_attempt_at)
				VALUES (gen_random_uuid()::text, sub.id, event_kind, payload, 'pending', 0, 10, NOW());
			END LOOP;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		return fmt.Errorf("update webhook_message_history_trigger for nullable sent_at: %w", err)
	}

	return nil
}

func init() { Register(&V58Migration{}) }
