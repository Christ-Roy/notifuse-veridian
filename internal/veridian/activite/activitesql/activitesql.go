// Package activitesql porte le schéma de l'activité Mail vers le Hub (V86, CONTRAT-HUB §5.25). Paquet feuille : la migration
// l'importe sans tirer le module (dont les tests importent les migrations).
//
// Deux déclencheurs remplissent une file bornée (veridian_activite_outbox), lue par le worker du module :
//   - message_history (envois) : message.sent, delivered, opened, clicked, bounced, unsubscribed, chacun UNE fois par message ;
//   - veridian_mail_message (reçus) : message.received pour une réponse (humaine, automatique) ou un rejet.
//
// Ils n'écrivent RIEN si l'espace n'a pas l'abonnement du Hub (veridianhubevenements, actif) : un espace qui n'est pas relié au
// Hub ne paie qu'une lecture d'index par envoi. Ils ne bloquent jamais l'écriture qui les déclenche (erreur avalée, journalisée).
// La file ne porte que des références (identifiant, événement, date, hôte d'un clic) : objet, adresses et fil sont lus à l'envoi,
// jamais le corps.
package activitesql

import (
	"strings"

	"github.com/Notifuse/notifuse/internal/veridian/mailbox/dedupsql"
)

// SubscriptionID : l'abonnement que le Hub pose sur l'espace (internal/veridian/organisation) ; son secret signe les lots.
const SubscriptionID = "veridianhubevenements"

// Sources d'une ligne de la file.
const (
	SourceHistory = "history" // ref_id = message_history.id
	SourceMail    = "mail"    // ref_id = veridian_mail_message.id
)

// Table : la file. Unicité (source, ref_id, event) : une seule ouverture et un seul clic par message, dédoublonnés à l'entrée.
const Table = `CREATE TABLE IF NOT EXISTS veridian_activite_outbox (
	id           BIGSERIAL PRIMARY KEY,
	source       TEXT NOT NULL,
	ref_id       TEXT NOT NULL,
	event        TEXT NOT NULL,
	occurred_at  TIMESTAMPTZ NOT NULL,
	host         TEXT NOT NULL DEFAULT '',
	created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	attempts     INTEGER NOT NULL DEFAULT 0,
	done_at      TIMESTAMPTZ,
	outcome      TEXT NOT NULL DEFAULT '',
	lot_id       TEXT
)`

var subscribed = `EXISTS (SELECT 1 FROM webhook_subscriptions WHERE id = '` + SubscriptionID + `' AND enabled = true)`

// HistoryTrigger : un événement par transition NULL -> date d'une colonne de message_history (l'upstream n'en garde qu'un par
// mise à jour : ici chacun compte, un clic qui pose aussi l'ouverture en fait deux).
var HistoryTrigger = `CREATE OR REPLACE FUNCTION veridian_activite_history_trigger()
	RETURNS TRIGGER AS $$
	BEGIN
		-- Hors du bloc EXCEPTION (un sous-transaction par ligne) : un espace sans abonnement ne paie que cette lecture.
		IF NEW.channel IS DISTINCT FROM 'email' OR NOT ` + subscribed + ` THEN
			RETURN NEW;
		END IF;
		BEGIN
			IF NEW.sent_at IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.sent_at IS NULL) THEN
				INSERT INTO veridian_activite_outbox (source, ref_id, event, occurred_at) VALUES ('history', NEW.id, 'message.sent', NEW.sent_at) ON CONFLICT DO NOTHING;
			END IF;
			IF TG_OP = 'UPDATE' THEN
				IF NEW.delivered_at IS NOT NULL AND OLD.delivered_at IS NULL THEN
					INSERT INTO veridian_activite_outbox (source, ref_id, event, occurred_at) VALUES ('history', NEW.id, 'message.delivered', NEW.delivered_at) ON CONFLICT DO NOTHING;
				END IF;
				IF NEW.opened_at IS NOT NULL AND OLD.opened_at IS NULL THEN
					INSERT INTO veridian_activite_outbox (source, ref_id, event, occurred_at) VALUES ('history', NEW.id, 'message.opened', NEW.opened_at) ON CONFLICT DO NOTHING;
				END IF;
				IF NEW.clicked_at IS NOT NULL AND OLD.clicked_at IS NULL THEN
					INSERT INTO veridian_activite_outbox (source, ref_id, event, occurred_at) VALUES ('history', NEW.id, 'message.clicked', NEW.clicked_at) ON CONFLICT DO NOTHING;
				END IF;
				IF NEW.bounced_at IS NOT NULL AND OLD.bounced_at IS NULL THEN
					INSERT INTO veridian_activite_outbox (source, ref_id, event, occurred_at) VALUES ('history', NEW.id, 'message.bounced', NEW.bounced_at) ON CONFLICT DO NOTHING;
				END IF;
				IF NEW.unsubscribed_at IS NOT NULL AND OLD.unsubscribed_at IS NULL THEN
					INSERT INTO veridian_activite_outbox (source, ref_id, event, occurred_at) VALUES ('history', NEW.id, 'message.unsubscribed', NEW.unsubscribed_at) ON CONFLICT DO NOTHING;
				END IF;
			END IF;
		EXCEPTION WHEN OTHERS THEN
			RAISE WARNING 'activite mail ignoree: %', SQLERRM;
		END;
		RETURN NEW;
	END;
	$$ LANGUAGE plpgsql`

// MailTrigger : un reçu qui compte (réponse humaine ou automatique, rejet), une fois par mail : ni copie Sieve, ni second rangement
// du même Message-ID par une autre boîte (même règle que le webhook message.received).
var MailTrigger = `CREATE OR REPLACE FUNCTION veridian_activite_mail_trigger()
	RETURNS TRIGGER AS $$
	DECLARE
		mid TEXT;
	BEGIN
		IF NEW.direction <> 'in' OR NEW.kind NOT IN ('reply_human', 'reply_auto', 'bounce_hard', 'bounce_soft')
			OR jsonb_exists(NEW.headers, '` + dedupsql.SieveHeader + `') OR NOT ` + subscribed + ` THEN
			RETURN NEW;
		END IF;
		BEGIN
			mid := ` + strings.ReplaceAll(dedupsql.MidKeyExpr, "message_id", "NEW.message_id") + `;
			IF mid IS NOT NULL AND EXISTS (
				SELECT 1 FROM veridian_mail_message o
				WHERE o.dedup_key = mid AND o.direction = 'in' AND o.id <> NEW.id AND NOT jsonb_exists(o.headers, '` + dedupsql.SieveHeader + `')
			) THEN
				RETURN NEW;
			END IF;
			INSERT INTO veridian_activite_outbox (source, ref_id, event, occurred_at) VALUES ('mail', NEW.id, 'message.received', NEW.occurred_at) ON CONFLICT DO NOTHING;
		EXCEPTION WHEN OTHERS THEN
			RAISE WARNING 'activite mail ignoree: %', SQLERRM;
		END;
		RETURN NEW;
	END;
	$$ LANGUAGE plpgsql`

// Statements : la migration V86 (additive, idempotente).
var Statements = []string{
	Table,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_vao_ref_event ON veridian_activite_outbox (source, ref_id, event)`,
	`CREATE INDEX IF NOT EXISTS idx_vao_pending ON veridian_activite_outbox (id) WHERE done_at IS NULL`,
	`CREATE INDEX IF NOT EXISTS idx_vao_done ON veridian_activite_outbox (done_at) WHERE done_at IS NOT NULL`,
	HistoryTrigger,
	`CREATE OR REPLACE TRIGGER veridian_activite_history AFTER INSERT OR UPDATE OF sent_at, delivered_at, opened_at, clicked_at, bounced_at, unsubscribed_at
		ON message_history FOR EACH ROW WHEN (NEW.channel = 'email') EXECUTE FUNCTION veridian_activite_history_trigger()`,
	MailTrigger,
	`CREATE OR REPLACE TRIGGER veridian_activite_mail AFTER INSERT ON veridian_mail_message FOR EACH ROW WHEN (NEW.direction = 'in')
		EXECUTE FUNCTION veridian_activite_mail_trigger()`,
}
