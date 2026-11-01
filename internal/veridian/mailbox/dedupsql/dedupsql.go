// Package dedupsql porte le SQL de la déduplication de la boîte unifiée (migration V68 et
// exécution courante). Paquet feuille : la migration l'importe sans tirer le paquet mailbox
// (qui importe les migrations dans ses tests), et il n'existe QU'UNE définition du classement
// des copies, partagée par le rattrapage de la migration et par le code qui range chaque mail.
//
// Contrat. Un mail entrant a une clé de regroupement (dedup_key) :
//   - « mid: » + Message-ID RFC en minuscules, quand il est exploitable (contient @, sans espace) ;
//   - sinon « fp: » + empreinte (contact, fil, date d'envoi, corps), posée par le code Go ;
//   - sinon NULL : jamais regroupé (pas assez d'éléments pour affirmer que deux mails sont le même).
//
// Dans un groupe, UN SEUL mail reste visible : l'original. La copie pointe l'original
// (duplicate_of) et n'est jamais supprimée. Le choix de l'original ne dépend ni de l'ordre
// d'arrivée ni de l'ordre des identifiants : il est recalculé sur tout le groupe à chaque arrivée.
//
// Classement (le premier est l'original) :
//  1. pas d'en-tête X-Sieve-Redirected-From (posé par la redirection Sieve du relais) ;
//  2. livré à l'un de ses destinataires To/Cc (Delivered-To) : une copie par alias ou par Bcc
//     est livrée à une adresse qui n'est pas dans les destinataires du mail ;
//  3. reçu le plus tôt ;
//  4. numéro d'ordre le plus petit.
package dedupsql

// MidKeyExpr : clé « mid: » en SQL, NULL si le Message-ID n'est pas exploitable.
// Seule définition de cette clé : le code Go ne la recalcule jamais.
const MidKeyExpr = `CASE WHEN message_id LIKE '%@%' AND message_id NOT LIKE '% %' THEN 'mid:' || lower(btrim(message_id)) END`

// SieveHeader : en-tête posé par la redirection Sieve du relais sur la copie.
const SieveHeader = "X-Sieve-Redirected-From"

// ResolveSQL marque, dans chaque groupe retenu par filter (condition SQL sur la ligne, par
// exemple `dedup_key = $1` ou `TRUE`), l'original (duplicate_of NULL) et ses copies (duplicate_of
// = id de l'original). Idempotent : ne modifie que les lignes dont le marquage change.
func ResolveSQL(filter string) string {
	return `WITH cand AS (
		SELECT id, dedup_key, received_at, seq,
			CASE WHEN jsonb_exists(headers, '` + SieveHeader + `') THEN 1 ELSE 0 END AS sieve,
			CASE WHEN EXISTS (
				SELECT 1 FROM jsonb_array_elements_text(
					CASE WHEN jsonb_typeof(headers->'Delivered-To') = 'array' THEN headers->'Delivered-To' ELSE '[]'::jsonb END) AS d
				WHERE lower(btrim(d)) IN (SELECT lower(x) FROM unnest(to_addrs || cc_addrs) AS x)
			) THEN 0 ELSE 1 END AS not_addressed
		FROM veridian_mail_message
		WHERE direction = 'in' AND dedup_key IS NOT NULL AND (` + filter + `)
	), ranked AS (
		SELECT id, ROW_NUMBER() OVER w AS rn, FIRST_VALUE(id) OVER w AS canon
		FROM cand
		WINDOW w AS (PARTITION BY dedup_key ORDER BY sieve, not_addressed, received_at NULLS LAST, seq)
	)
	UPDATE veridian_mail_message m
	SET duplicate_of = CASE WHEN r.rn = 1 THEN NULL ELSE r.canon END, updated_at = NOW()
	FROM ranked r
	WHERE m.id = r.id AND m.duplicate_of IS DISTINCT FROM (CASE WHEN r.rn = 1 THEN NULL ELSE r.canon END)`
}

// PropagateHandledSQL aligne l'état « traité » d'un groupe : si un de ses mails est traité, tous
// le sont (le plus ancien traitement fait foi). Une copie traitée avant l'arrivée de l'original ne
// ressort donc pas « à traiter ».
func PropagateHandledSQL(filter string) string {
	return `UPDATE veridian_mail_message m SET handled_at = h.handled_at, handled_by = h.handled_by, updated_at = NOW()
	FROM (
		SELECT DISTINCT ON (dedup_key) dedup_key, handled_at, handled_by FROM veridian_mail_message
		WHERE direction = 'in' AND dedup_key IS NOT NULL AND handled_at IS NOT NULL AND (` + filter + `)
		ORDER BY dedup_key, handled_at ASC, seq ASC
	) h
	WHERE m.dedup_key = h.dedup_key AND m.direction = 'in' AND m.handled_at IS NULL`
}

// AssignKeysSQL pose la clé « mid: » des entrants qui n'en ont pas (rattrapage de la migration,
// et mails rangés par une version antérieure pendant un déploiement glissant).
const AssignKeysSQL = `UPDATE veridian_mail_message SET dedup_key = ` + MidKeyExpr + `
	WHERE direction = 'in' AND dedup_key IS NULL AND (` + MidKeyExpr + `) IS NOT NULL`

// UnsettledFilter retient les groupes qui ont plus d'un mail visible (marquage à refaire).
const UnsettledFilter = `dedup_key IN (SELECT dedup_key FROM veridian_mail_message
		WHERE direction = 'in' AND dedup_key IS NOT NULL GROUP BY dedup_key
		HAVING COUNT(*) FILTER (WHERE duplicate_of IS NULL) > 1)`

// WebhookTriggerFunction : le webhook message.received ne signale pas une copie Sieve (l'original
// arrive toujours par la boîte du relais). Remplace la fonction de V63, sinon identique.
const WebhookTriggerFunction = `CREATE OR REPLACE FUNCTION webhook_mail_message_trigger()
	RETURNS TRIGGER AS $$
	DECLARE
		sub RECORD;
		payload JSONB;
	BEGIN
		IF NEW.direction <> 'in' OR jsonb_exists(NEW.headers, '` + SieveHeader + `') THEN
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
	$$ LANGUAGE plpgsql`

// Statements est la migration V68 (additive, idempotente) : colonnes, index, webhook, puis
// rattrapage des doublons existants. Aucune ligne n'est supprimée ni les sortants touchés.
var Statements = []string{
	`ALTER TABLE veridian_mail_message ADD COLUMN IF NOT EXISTS duplicate_of TEXT`,
	`ALTER TABLE veridian_mail_message ADD COLUMN IF NOT EXISTS dedup_key TEXT`,
	`CREATE INDEX IF NOT EXISTS idx_vmm_dedup_key ON veridian_mail_message (dedup_key) WHERE dedup_key IS NOT NULL`,
	`CREATE INDEX IF NOT EXISTS idx_vmm_duplicate_of ON veridian_mail_message (duplicate_of) WHERE duplicate_of IS NOT NULL`,
	WebhookTriggerFunction,
	AssignKeysSQL,
	ResolveSQL(`TRUE`),
	PropagateHandledSQL(`TRUE`),
}
