package mailbox

// SchemaStatements est le schéma V63 (additif, idempotent), un ordre SQL par entrée.
// La migration (internal/migrations/veridian/v63_boite_unifiee.go) en porte la même copie
// littérale ; schema_test.go vérifie qu'elles ne divergent pas.
//
// Colonnes en clair : adresses, objet, dates, références (indexables, comme
// message_history.contact_email). Chiffrées (hex AES-GCM, clé du workspace, comme
// message_data) : body_text, body_html_sanitized, raw_eml.
var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS veridian_mail_message (
		id                     TEXT PRIMARY KEY,
		seq                    BIGSERIAL NOT NULL,
		workspace_id           TEXT NOT NULL DEFAULT '',
		direction              TEXT NOT NULL CHECK (direction IN ('out', 'in')),
		kind                   TEXT NOT NULL CHECK (kind IN ('sent', 'reply_human', 'reply_auto', 'bounce_hard', 'bounce_soft', 'dsn', 'other')),
		profile_id             TEXT,
		mailbox_integration_id TEXT,
		mailbox_folder         TEXT,
		mailbox_uid_validity   BIGINT,
		mailbox_uid            BIGINT,
		message_id             TEXT,
		in_reply_to            TEXT,
		references_ids         TEXT[] NOT NULL DEFAULT '{}',
		thread_id              TEXT NOT NULL,
		contact_email          TEXT NOT NULL DEFAULT '',
		from_addr              TEXT NOT NULL DEFAULT '',
		from_name              TEXT NOT NULL DEFAULT '',
		to_addrs               TEXT[] NOT NULL DEFAULT '{}',
		cc_addrs               TEXT[] NOT NULL DEFAULT '{}',
		subject                TEXT NOT NULL DEFAULT '',
		sent_at                TIMESTAMPTZ,
		received_at            TIMESTAMPTZ,
		occurred_at            TIMESTAMPTZ NOT NULL,
		headers                JSONB NOT NULL DEFAULT '{}',
		body_text              TEXT,
		body_html_sanitized    TEXT,
		raw_eml                TEXT,
		body_source            TEXT NOT NULL DEFAULT '',
		size_bytes             BIGINT NOT NULL DEFAULT 0,
		truncated              BOOLEAN NOT NULL DEFAULT FALSE,
		attachments            JSONB NOT NULL DEFAULT '[]',
		dsn                    JSONB,
		link                   JSONB,
		sent_ref               TEXT,
		handled_at             TIMESTAMPTZ,
		handled_by             TEXT,
		labels                 TEXT[] NOT NULL DEFAULT '{}',
		created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_vmm_seq ON veridian_mail_message (seq)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_vmm_mailbox_uid ON veridian_mail_message (mailbox_integration_id, mailbox_folder, mailbox_uid_validity, mailbox_uid) WHERE mailbox_uid IS NOT NULL`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_vmm_sent_ref ON veridian_mail_message (sent_ref) WHERE sent_ref IS NOT NULL`,
	`CREATE INDEX IF NOT EXISTS idx_vmm_contact_received ON veridian_mail_message (contact_email, received_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_vmm_contact_occurred ON veridian_mail_message (contact_email, occurred_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_vmm_thread ON veridian_mail_message (thread_id, occurred_at)`,
	`CREATE INDEX IF NOT EXISTS idx_vmm_message_id ON veridian_mail_message (message_id)`,
	`CREATE INDEX IF NOT EXISTS idx_vmm_kind_received ON veridian_mail_message (kind, received_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_vmm_profile_occurred ON veridian_mail_message (profile_id, occurred_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_vmm_in_reply_to ON veridian_mail_message (in_reply_to) WHERE in_reply_to IS NOT NULL`,
	`CREATE INDEX IF NOT EXISTS idx_vmm_references ON veridian_mail_message USING GIN (references_ids)`,
	`CREATE OR REPLACE FUNCTION webhook_mail_message_trigger()
	RETURNS TRIGGER AS $$
	DECLARE
		sub RECORD;
		payload JSONB;
	BEGIN
		IF NEW.direction <> 'in' THEN
			RETURN NEW;
		END IF;
		BEGIN
			payload := jsonb_build_object(
				'message_id', NEW.id,
				'kind', NEW.kind,
				'direction', NEW.direction,
				'profile_id', NEW.profile_id,
				'email', NEW.contact_email,
				'thread_id', NEW.thread_id,
				'subject', NEW.subject,
				'from', NEW.from_addr,
				'attachment_count', jsonb_array_length(NEW.attachments),
				'sent_message_id', NEW.link->>'sent_message_id',
				'event_timestamp', NEW.occurred_at
			);
			FOR sub IN
				SELECT id FROM webhook_subscriptions
				WHERE enabled = true AND 'message.received' = ANY(ARRAY(SELECT jsonb_array_elements_text(settings->'event_types')))
			LOOP
				INSERT INTO webhook_deliveries (id, subscription_id, event_type, payload, status, attempts, max_attempts, next_attempt_at)
				VALUES (gen_random_uuid()::text, sub.id, 'message.received', payload, 'pending', 0, 10, NOW());
			END LOOP;
		EXCEPTION WHEN OTHERS THEN
			RAISE WARNING 'webhook message.received ignoré: %', SQLERRM;
		END;
		RETURN NEW;
	END;
	$$ LANGUAGE plpgsql`,
	`CREATE OR REPLACE TRIGGER webhook_mail_message AFTER INSERT ON veridian_mail_message FOR EACH ROW EXECUTE FUNCTION webhook_mail_message_trigger()`,
	`CREATE TABLE IF NOT EXISTS veridian_mail_sync_state (
		integration_id  TEXT NOT NULL,
		folder          TEXT NOT NULL,
		uid_validity    BIGINT NOT NULL DEFAULT 0,
		last_uid        BIGINT NOT NULL DEFAULT 0,
		cursor          TEXT NOT NULL DEFAULT '',
		last_run_at     TIMESTAMPTZ,
		last_ok_at      TIMESTAMPTZ,
		last_error      TEXT NOT NULL DEFAULT '',
		total_ingested  BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (integration_id, folder)
	)`,
}
