package mailbox

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/pkg/crypto"
	"github.com/Notifuse/notifuse/pkg/logger"
	_ "github.com/lib/pq"
)

var schemaSeq int64

// testStore ouvre un schéma Postgres neuf (L1_TEST_PG_DSN), avec les tables de Notifuse dont on dépend.
func testStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("L1_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("L1_TEST_PG_DSN absent : test Postgres non exécuté")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("t_%d_%d", time.Now().UnixNano(), atomic.AddInt64(&schemaSeq, 1))
	if _, err := admin.Exec("CREATE SCHEMA " + name); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("SET search_path TO " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec("DROP SCHEMA " + name + " CASCADE")
		db.Close()
	})
	for _, q := range []string{
		`CREATE TABLE contacts (email VARCHAR(255) PRIMARY KEY)`,
		`CREATE TABLE webhook_subscriptions (id VARCHAR(36) PRIMARY KEY, enabled BOOLEAN NOT NULL DEFAULT true, settings JSONB NOT NULL DEFAULT '{}')`,
		`CREATE TABLE webhook_deliveries (id VARCHAR(36) PRIMARY KEY, subscription_id VARCHAR(36) NOT NULL, event_type VARCHAR(50) NOT NULL, payload JSONB NOT NULL,
			status VARCHAR(20) NOT NULL, attempts INT NOT NULL, max_attempts INT NOT NULL, next_attempt_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE message_history (
			id VARCHAR(255) PRIMARY KEY, contact_email VARCHAR(255) NOT NULL, automation_id VARCHAR(36), broadcast_id VARCHAR(255),
			template_id VARCHAR(32) NOT NULL DEFAULT 'tpl', template_version INTEGER NOT NULL DEFAULT 1, channel VARCHAR(20) NOT NULL DEFAULT 'email',
			message_data JSONB NOT NULL DEFAULT '{}', channel_options JSONB, sent_at TIMESTAMPTZ, failed_at TIMESTAMPTZ,
			veridian_profile_id VARCHAR(255), veridian_sender_email VARCHAR(255))`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range SchemaStatements {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("schéma: %v\n%s", err, q)
		}
	}
	return NewStore(
		func(context.Context, string) (*sql.DB, error) { return db, nil },
		func(context.Context, string) (string, error) { return "cle-de-test", nil },
	), db
}

func testIngestor(s *Store) *Ingestor {
	return NewIngestor(s, logger.NewLoggerWithLevel("disabled"), nil)
}

var box1 = BoxInfo{IntegrationID: "imap1", Address: "hugo.bourdat@animalservicedistribution.fr", ProfileID: "prof1", Folder: "INBOX"}

func ref(uid uint32) MailboxRef {
	return MailboxRef{IntegrationID: "imap1", Folder: "INBOX", UIDValidity: 7, UID: uid}
}

const j0uuid = "11111111-1111-4111-8111-111111111111"

func addSent(t *testing.T, db *sql.DB, uuid, contact string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO message_history (id, contact_email, automation_id, veridian_profile_id, veridian_sender_email, sent_at)
		VALUES ($1,$2,'auto1','prof1','hugo.bourdat@animalservicedistribution.fr', NOW() - interval '2 days')`, "ws1_"+uuid, contact); err != nil {
		t.Fatal(err)
	}
}

func TestIngest_ReplyLinkedToSent(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")

	raw := replyHuman("<"+j0uuid+"@animalservicedistribution.fr>", "<"+j0uuid+"@animalservicedistribution.fr>")
	m, inserted, err := g.IngestRaw(ctx, "ws1", box1, ref(1), raw, time.Now(), false)
	if err != nil || !inserted {
		t.Fatalf("%v %v", err, inserted)
	}
	if m.Kind != KindReplyHuman || m.Direction != DirectionIn {
		t.Fatalf("nature: %+v", m.Kind)
	}
	if m.Link == nil || m.Link.SentMessageID != "ws1_"+j0uuid || m.Link.AutomationID != "auto1" || m.Link.MatchType != "message_id" {
		t.Fatalf("lien: %+v", m.Link)
	}
	if m.ContactEmail != "marie@exemple-animaux.fr" || m.ProfileID != "prof1" {
		t.Fatalf("contact/profil: %q %q", m.ContactEmail, m.ProfileID)
	}
	if m.ThreadID != ThreadIDFromRoot("<"+j0uuid+"@x>") {
		t.Fatalf("fil: %s", m.ThreadID)
	}

	// idempotence : même UID
	_, again, err := g.IngestRaw(ctx, "ws1", box1, ref(1), raw, time.Now(), false)
	if err != nil || again {
		t.Fatalf("relecture du même UID: %v %v", err, again)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM veridian_mail_message`).Scan(&n)
	if n != 1 {
		t.Fatalf("doublon: %d lignes", n)
	}

	// lecture complète
	got, err := s.Get(ctx, "ws1", m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.BodyText, "m'intéresse") || !strings.Contains(got.BodyHTMLSanitized, "<b>oui</b>") || strings.Contains(got.BodyHTMLSanitized, "script") {
		t.Fatalf("corps: %q / %q", got.BodyText, got.BodyHTMLSanitized)
	}
	if string(got.RawEML) != string(raw) {
		t.Fatal("message brut relu différent")
	}
	if got.Mailbox == nil || got.Mailbox.UID != 1 || got.Headers["Subject"] == nil {
		t.Fatalf("repères: %+v", got.Mailbox)
	}
}

func TestIngest_BodiesEncryptedAtRest(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")
	raw := replyHuman("<"+j0uuid+"@x.fr>", "")
	m, _, err := g.IngestRaw(ctx, "ws1", box1, ref(1), raw, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	var bt, bh, re string
	if err := db.QueryRow(`SELECT body_text, body_html_sanitized, raw_eml FROM veridian_mail_message WHERE id=$1`, m.ID).Scan(&bt, &bh, &re); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"body_text": bt, "body_html": bh, "raw_eml": re} {
		if strings.Contains(v, "intéresse") || strings.Contains(v, "interesse") || strings.Contains(v, "Subject") || strings.Contains(v, "oui") {
			t.Fatalf("%s contient du clair", name)
		}
		if _, err := crypto.DecryptFromHexString(v, "cle-de-test"); err != nil {
			t.Fatalf("%s n'est pas chiffré avec la clé du workspace: %v", name, err)
		}
		if _, err := crypto.DecryptFromHexString(v, "autre-cle"); err == nil {
			t.Fatalf("%s se déchiffre avec une autre clé", name)
		}
	}
}

func TestIngest_BounceDecodedAndLinked(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	addSent(t, db, j0uuid, "mort@gmail.com")
	m, _, err := g.IngestRaw(ctx, "ws1", box1, ref(2), bounce550(j0uuid+"@animalservicedistribution.fr"), time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindBounceHard || m.DSN == nil || m.DSN.Code != "5.1.1" || m.DSN.Category != CatAdresseInexistante {
		t.Fatalf("%+v %+v", m.Kind, m.DSN)
	}
	if m.ContactEmail != "mort@gmail.com" {
		t.Fatalf("le contact d'un rebond est l'adresse en échec, pas MAILER-DAEMON: %q", m.ContactEmail)
	}
	if m.Link == nil || m.Link.SentMessageID != "ws1_"+j0uuid {
		t.Fatalf("rebond non relié à l'envoi: %+v", m.Link)
	}
	// rebond sans envoi retrouvé
	m2, _, err := g.IngestRaw(ctx, "ws1", box1, ref(3), bounceSoft("inconnu123@x.fr"), time.Now(), false)
	if err != nil || m2.Kind != KindBounceSoft || m2.ContactEmail != "plein@orange.fr" || m2.Link != nil {
		t.Fatalf("%v %+v", err, m2)
	}
}

func TestIngest_OtherAndKnownSender(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	// inconnu, sans référence : « autre »
	m, _, err := g.IngestRaw(ctx, "ws1", box1, ref(1), replyHuman("<zzz@x.fr>", "<zzz@x.fr>"), time.Now(), false)
	if err != nil || m.Kind != KindOther || m.Link != nil {
		t.Fatalf("%v %+v", err, m)
	}
	// expéditeur connu du workspace : réponse, rattachement « sender »
	db.Exec(`INSERT INTO contacts (email) VALUES ('marie@exemple-animaux.fr')`)
	raw := strings.Replace(string(replyHuman("<yyy@x.fr>", "<yyy@x.fr>")), "rep1@", "rep2@", 1)
	m2, _, err := g.IngestRaw(ctx, "ws1", box1, ref(2), []byte(raw), time.Now(), false)
	if err != nil || m2.Kind != KindReplyHuman || m2.Link == nil || m2.Link.MatchType != "sender" {
		t.Fatalf("%v %+v %+v", err, m2.Kind, m2.Link)
	}
	// auto-répondeur d'un contact connu
	db.Exec(`INSERT INTO contacts (email) VALUES ('contact@societe.fr')`)
	m3, _, err := g.IngestRaw(ctx, "ws1", box1, ref(3), autoReply("<q@x.fr>"), time.Now(), false)
	if err != nil || m3.Kind != KindReplyAuto {
		t.Fatalf("%v %+v", err, m3.Kind)
	}
}

func TestThreads_ChainAndOrderIndependence(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	const r1uuid = "22222222-2222-4222-8222-222222222222"
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")
	j0 := "<" + j0uuid + "@animalservicedistribution.fr>"
	r1 := "<" + r1uuid + "@animalservicedistribution.fr>"
	base := time.Now().Add(-48 * time.Hour)

	// la réponse arrive AVANT que J0 et la relance soient dans la boîte unifiée
	reply := replyHuman(r1, j0+" "+r1)
	rm, _, err := g.IngestRaw(ctx, "ws1", box1, ref(1), reply, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.RecordSent(ctx, SentInput{WorkspaceID: "ws1", MessageHistoryID: "ws1_" + j0uuid, ContactEmail: "marie@exemple-animaux.fr",
		FromAddress: "hugo.bourdat@animalservicedistribution.fr", Subject: "Présentation ASD", HTML: "<p>Bonjour</p>", SentAt: base, BodySource: BodyCaptured, ProfileID: "prof1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RecordSent(ctx, SentInput{WorkspaceID: "ws1", MessageHistoryID: "ws1_" + r1uuid, ContactEmail: "marie@exemple-animaux.fr",
		FromAddress: "hugo.bourdat@animalservicedistribution.fr", Subject: "Re: Présentation ASD", HTML: "<p>Relance</p>", SentAt: base.Add(24 * time.Hour),
		InReplyTo: j0, References: []string{j0}, BodySource: BodyCaptured, ProfileID: "prof1"}); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.ThreadMessages(ctx, "ws1", rm.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("le fil doit contenir J0, relance, réponse: %d messages", len(msgs))
	}
	if msgs[0].Kind != KindSent || msgs[1].Kind != KindSent || msgs[2].Kind != KindReplyHuman {
		t.Fatalf("ordre chronologique: %s %s %s", msgs[0].Kind, msgs[1].Kind, msgs[2].Kind)
	}
	if !strings.Contains(msgs[0].BodyText, "Bonjour") {
		t.Fatalf("texte dérivé du HTML: %q", msgs[0].BodyText)
	}
	ths, err := s.ThreadsOfContact(ctx, "ws1", "Marie@exemple-animaux.fr", 10)
	if err != nil || len(ths) != 1 || ths[0].Count != 3 || ths[0].Subject != "Présentation ASD" || ths[0].LastKind != KindReplyHuman {
		t.Fatalf("%v %+v", err, ths)
	}
}

func TestThreads_RethreadWhenParentArrivesLate(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	const r2uuid = "33333333-3333-4333-8333-333333333333"
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")
	j0 := "<" + j0uuid + "@animalservicedistribution.fr>"
	r2 := "<" + r2uuid + "@animalservicedistribution.fr>"
	// le client de Marie ne cite que la relance 2 (In-Reply-To seul)
	reply := replyHuman(r2, "")
	rm, _, _ := g.IngestRaw(ctx, "ws1", box1, ref(1), []byte(strings.Replace(string(reply), "References: \r\n", "", 1)), time.Now(), false)
	before := rm.ThreadID
	if _, err := g.RecordSent(ctx, SentInput{WorkspaceID: "ws1", MessageHistoryID: "ws1_" + r2uuid, ContactEmail: "marie@exemple-animaux.fr",
		FromAddress: "hugo.bourdat@animalservicedistribution.fr", Subject: "Re: Présentation ASD", HTML: "<p>R2</p>", SentAt: time.Now().Add(-time.Hour),
		InReplyTo: j0, References: []string{j0}, BodySource: BodyCaptured}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "ws1", rm.ID)
	if got.ThreadID == before {
		t.Fatalf("la réponse devait rejoindre le fil de J0 quand la relance est arrivée")
	}
	if got.ThreadID != ThreadIDFromRoot(j0) {
		t.Fatalf("fil %s, attendu celui de J0 %s", got.ThreadID, ThreadIDFromRoot(j0))
	}
}

func TestRecordSent_CaptureBeatsRerender(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	in := SentInput{WorkspaceID: "ws1", MessageHistoryID: "ws1_" + j0uuid, ContactEmail: "m@x.fr", FromAddress: "h@d.fr", Subject: "S", HTML: "<p>reconstitué</p>", SentAt: time.Now(), BodySource: BodyRerendered}
	if ok, err := g.RecordSent(ctx, in); err != nil || !ok {
		t.Fatalf("%v %v", err, ok)
	}
	in.HTML, in.BodySource = "<p>exact</p>", BodyCaptured
	if _, err := g.RecordSent(ctx, in); err != nil {
		t.Fatal(err)
	}
	var id string
	s.mustDB(t).QueryRow(`SELECT id FROM veridian_mail_message WHERE sent_ref='ws1_' || $1`, j0uuid).Scan(&id)
	m, _ := s.Get(ctx, "ws1", id)
	if m.BodySource != BodyCaptured || !strings.Contains(m.BodyHTMLSanitized, "exact") {
		t.Fatalf("la capture doit remplacer la reconstitution: %s %q", m.BodySource, m.BodyHTMLSanitized)
	}
	in.HTML, in.BodySource = "<p>autre reconstitution</p>", BodyRerendered
	g.RecordSent(ctx, in)
	m, _ = s.Get(ctx, "ws1", id)
	if !strings.Contains(m.BodyHTMLSanitized, "exact") {
		t.Fatalf("une reconstitution ne remplace jamais une capture: %q", m.BodyHTMLSanitized)
	}
	var n int
	s.mustDB(t).QueryRow(`SELECT COUNT(*) FROM veridian_mail_message`).Scan(&n)
	if n != 1 {
		t.Fatalf("doublon: %d", n)
	}
}

func (s *Store) mustDB(t *testing.T) *sql.DB {
	db, err := s.db(context.Background(), "ws1")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestList_FiltersCursorSearchUpdate(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")
	j0 := "<" + j0uuid + "@animalservicedistribution.fr>"
	for i := 1; i <= 5; i++ {
		raw := strings.Replace(string(replyHuman(j0, j0)), "rep1@", fmt.Sprintf("rep%d@", i+10), 1)
		if _, _, err := g.IngestRaw(ctx, "ws1", box1, ref(uint32(i)), []byte(raw), time.Now().Add(time.Duration(i)*time.Minute), false); err != nil {
			t.Fatal(err)
		}
	}
	g.IngestRaw(ctx, "ws1", box1, ref(9), bounce550(j0uuid+"@x.fr"), time.Now().Add(time.Hour), false)

	page1, next, err := s.List(ctx, "ws1", Filter{Kinds: []Kind{KindReplyHuman}, Limit: 2})
	if err != nil || len(page1) != 2 || next == "" {
		t.Fatalf("page 1: %v %d %q", err, len(page1), next)
	}
	page2, next2, err := s.List(ctx, "ws1", Filter{Kinds: []Kind{KindReplyHuman}, Limit: 2, Cursor: next})
	if err != nil || len(page2) != 2 || next2 == "" {
		t.Fatalf("page 2: %v %d", err, len(page2))
	}
	page3, next3, _ := s.List(ctx, "ws1", Filter{Kinds: []Kind{KindReplyHuman}, Limit: 2, Cursor: next2})
	if len(page3) != 1 || next3 != "" {
		t.Fatalf("page 3: %d %q", len(page3), next3)
	}
	if page1[0].OccurredAt.Before(page1[1].OccurredAt) || page1[1].ID == page2[0].ID {
		t.Fatal("ordre ou pagination incohérents")
	}
	if page1[0].Preview == "" || !strings.Contains(page1[0].Preview, "intéresse") || strings.Contains(page1[0].Preview, "nous distribuons") {
		t.Fatalf("aperçu (citation exclue): %q", page1[0].Preview)
	}
	all, _, _ := s.List(ctx, "ws1", Filter{ContactEmail: "marie@exemple-animaux.fr"})
	if len(all) != 6 { // 5 réponses + le rebond de l'envoi à ce contact
		t.Fatalf("filtre contact: %d", len(all))
	}
	bounces, _, _ := s.List(ctx, "ws1", Filter{Kinds: []Kind{KindBounceHard}})
	if len(bounces) != 1 || bounces[0].DSNReasonFR == "" {
		t.Fatalf("rebonds: %+v", bounces)
	}
	byBox, _, _ := s.List(ctx, "ws1", Filter{MailboxID: "imap1"})
	if len(byBox) != 6 {
		t.Fatalf("filtre boîte: %d", len(byBox))
	}
	subj, _, _ := s.List(ctx, "ws1", Filter{Q: "présentation"})
	if len(subj) != 5 {
		t.Fatalf("recherche objet: %d", len(subj))
	}
	body, _, _ := s.List(ctx, "ws1", Filter{Q: "pouvez-vous m'appeler", BodySearch: true})
	if len(body) != 5 {
		t.Fatalf("recherche dans les corps: %d", len(body))
	}
	if none, _, _ := s.List(ctx, "ws1", Filter{Q: "pouvez-vous m'appeler"}); len(none) != 0 {
		t.Fatalf("sans body_search, le corps chiffré ne se cherche pas: %d", len(none))
	}

	// traité / non traité
	id := page1[0].ID
	yes := true
	if err := s.Update(ctx, "ws1", id, &yes, "agent-test", &[]string{"a-rappeler"}); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Get(ctx, "ws1", id)
	if m.HandledAt == nil || m.HandledBy != "agent-test" || len(m.Labels) != 1 {
		t.Fatalf("%+v", m)
	}
	no := false
	open, _, _ := s.List(ctx, "ws1", Filter{Handled: &no, Kinds: []Kind{KindReplyHuman}})
	if len(open) != 4 {
		t.Fatalf("non traités: %d", len(open))
	}
	if err := s.Update(ctx, "ws1", "inexistant", &yes, "x", nil); err != ErrNotFound {
		t.Fatalf("%v", err)
	}
	counts, _ := s.Counts(ctx, "ws1")
	if c := counts["imap1"]; c.CountIn != 6 || c.Unhandled != 4 {
		t.Fatalf("%+v", c)
	}
	// reprise après l'événement n
	ev, _, _ := s.List(ctx, "ws1", Filter{AfterSeq: 3})
	if len(ev) != 3 || ev[0].Seq >= ev[1].Seq {
		t.Fatalf("flux depuis seq 3: %d", len(ev))
	}
}

func cryptoEnc(plain, key string) (string, error) { return crypto.EncryptString(plain, key) }

func TestWebhookTrigger_MessageReceived(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")
	db.Exec(`INSERT INTO webhook_subscriptions (id, enabled, settings) VALUES ('s1', true, '{"event_types":["message.received"]}'),
		('s2', true, '{"event_types":["email.sent"]}'), ('s3', false, '{"event_types":["message.received"]}')`)
	raw := replyHuman("<"+j0uuid+"@x.fr>", "<"+j0uuid+"@x.fr>")
	m, _, err := g.IngestRaw(ctx, "ws1", box1, ref(1), raw, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT subscription_id, event_type, payload->>'message_id', payload->>'kind', payload->>'email', payload->>'sent_message_id', payload::text FROM webhook_deliveries`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var sub, ev, mid, kind, email, sent, full string
		rows.Scan(&sub, &ev, &mid, &kind, &email, &sent, &full)
		n++
		if sub != "s1" || ev != "message.received" || mid != m.ID || kind != "reply_human" || email != "marie@exemple-animaux.fr" || sent != "ws1_"+j0uuid {
			t.Fatalf("livraison: %s %s %s %s %s %s", sub, ev, mid, kind, email, sent)
		}
		if strings.Contains(full, "intéresse") || strings.Contains(full, "appeler") {
			t.Fatalf("le webhook ne porte jamais le corps du mail: %s", full)
		}
	}
	if n != 1 {
		t.Fatalf("une livraison pour la seule souscription active et abonnée: %d", n)
	}
	// un mail sortant ne déclenche rien
	g.RecordSent(ctx, SentInput{WorkspaceID: "ws1", MessageHistoryID: "ws1_x", ContactEmail: "m@x.fr", FromAddress: "h@d.fr", Subject: "S", SentAt: time.Now(), BodySource: BodyCaptured})
	var total int
	db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&total)
	if total != 1 {
		t.Fatalf("mail sortant: %d livraisons", total)
	}
}

func TestWebhookTrigger_FailureNeverBlocksIngestion(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")
	db.Exec(`INSERT INTO webhook_subscriptions (id, enabled, settings) VALUES ('s1', true, '{"event_types":["message.received"]}')`)
	db.Exec(`DROP TABLE webhook_deliveries`)
	_, inserted, err := g.IngestRaw(ctx, "ws1", box1, ref(1), replyHuman("<"+j0uuid+"@x.fr>", ""), time.Now(), false)
	if err != nil || !inserted {
		t.Fatalf("une panne de webhook ne doit jamais empêcher de ranger le mail: %v %v", err, inserted)
	}
}
