package mailbox

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/mailbox/dedupsql"
	"github.com/lib/pq"
)

func dedupsqlStatements() []string { return dedupsql.Statements }

// Déduplication de la boîte unifiée (L1c, 01/11/2026). Cas réel : le filtre Sieve d'un relais
// copie chaque vraie réponse vers la boîte de Hugo (communication@animalsd.fr), que Notifuse lit
// aussi. La copie porte le même Message-ID que l'original. Le contrat testé ici :
//   - une seule ligne visible par message, quel que soit l'ordre d'arrivée ;
//   - c'est l'ORIGINAL (boîte du relais) qui reste visible ;
//   - la copie n'est jamais supprimée (traçabilité) ;
//   - deux vrais messages distincts ne fusionnent jamais.

// box2 : la boîte Infomaniak de Hugo, destinataire des copies.
var box2 = BoxInfo{IntegrationID: "imap2", Address: "communication@animalsd.fr", ProfileID: "prof2", Folder: "INBOX"}

func ref2(uid uint32) MailboxRef {
	return MailboxRef{IntegrationID: "imap2", Folder: "INBOX", UIDValidity: 9, UID: uid}
}

// mailOpts décrit un mail reçu de prospect, avec les en-têtes qui distinguent original et copie.
type mailOpts struct {
	msgID     string // sans chevrons ; vide : pas de Message-ID
	from      string
	subject   string
	date      string
	body      string
	extraHdrs []string // lignes d'en-tête ajoutées en tête (Delivered-To, X-Sieve-Redirected-From...)
	to        string
}

func (o mailOpts) raw() []byte {
	if o.from == "" {
		o.from = "Juliette <contact@animessence.fr>"
	}
	if o.subject == "" {
		o.subject = "Re: Alimentation pro chiens et chats : on en parle ?"
	}
	if o.date == "" {
		o.date = "Sat, 31 Oct 2026 19:07:35 +0100"
	}
	if o.body == "" {
		o.body = "Bonjour, oui, envoyez-moi vos tarifs."
	}
	if o.to == "" {
		o.to = "hugo.bourdat@contact.animal-services-distribution.fr"
	}
	var b strings.Builder
	for _, h := range o.extraHdrs {
		b.WriteString(h + "\n")
	}
	b.WriteString("From: " + o.from + "\nTo: " + o.to + "\nSubject: " + o.subject + "\nDate: " + o.date + "\n")
	if o.msgID != "" {
		b.WriteString("Message-ID: <" + o.msgID + ">\n")
	}
	b.WriteString("MIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\n\n" + o.body + "\n")
	return crlf(b.String())
}

// original : tel que livré dans la boîte du relais.
func original(msgID string) mailOpts {
	return mailOpts{msgID: msgID, extraHdrs: []string{"Delivered-To: hugo.bourdat@contact.animal-services-distribution.fr"}}
}

// siveCopy : la copie du filtre Sieve (en-tête posé par la redirection).
func siveCopy(msgID string) mailOpts {
	return mailOpts{msgID: msgID, extraHdrs: []string{
		"X-Sieve-Redirected-From: hugo.bourdat@contact.animal-services-distribution.fr",
		"Delivered-To: communication@animalsd.fr",
		"Delivered-To: hugo.bourdat@contact.animal-services-distribution.fr",
	}}
}

// aliasCopy : l'ancienne copie par alias Postfix (avant le 29/10), sans en-tête Sieve ; seule
// la livraison au nom de la boîte de Hugo, qui n'est pas destinataire du mail, la trahit.
func aliasCopy(msgID string) mailOpts {
	return mailOpts{msgID: msgID, extraHdrs: []string{"Delivered-To: communication@animalsd.fr"}}
}

func inbound(t *testing.T, s *Store) []Summary {
	t.Helper()
	got, _, err := s.List(context.Background(), "ws1", Filter{Direction: DirectionIn, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustIngest(t *testing.T, g *Ingestor, box BoxInfo, r MailboxRef, raw []byte, at time.Time) *Message {
	t.Helper()
	m, inserted, err := g.IngestRaw(context.Background(), "ws1", box, r, raw, at, false)
	if err != nil || !inserted {
		t.Fatalf("ingestion: %v inserted=%v", err, inserted)
	}
	return m
}

func rowCount(t *testing.T, s *Store, where string) int {
	t.Helper()
	db, _ := s.db(context.Background(), "ws1")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM veridian_mail_message WHERE ` + where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Cas du jour : l'original est déjà là, la copie Sieve arrive 2 s plus tard.
func TestDedup_SieveCopyAfterOriginal(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	now := time.Now()
	orig := mustIngest(t, g, box1, ref(1), original("CACXf-reponse1@mail.gmail.com").raw(), now)
	mustIngest(t, g, box2, ref2(1), siveCopy("CACXf-reponse1@mail.gmail.com").raw(), now.Add(2*time.Second))

	got := inbound(t, s)
	if len(got) != 1 || got[0].ID != orig.ID {
		t.Fatalf("une seule ligne visible, l'original (boîte du relais) : %d lignes %+v", len(got), got)
	}
	if got[0].ProfileID != "prof1" {
		t.Fatalf("l'original vient de la boîte du relais: %s", got[0].ProfileID)
	}
}

// L'ordre d'arrivée n'est pas garanti : la copie peut précéder l'original.
func TestDedup_SieveCopyBeforeOriginal(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	now := time.Now()
	cp := mustIngest(t, g, box2, ref2(1), siveCopy("CACXf-reponse2@mail.gmail.com").raw(), now)
	orig := mustIngest(t, g, box1, ref(1), original("CACXf-reponse2@mail.gmail.com").raw(), now.Add(2*time.Second))

	got := inbound(t, s)
	if len(got) != 1 || got[0].ID != orig.ID {
		t.Fatalf("la copie arrivée la première doit céder la place à l'original: %d lignes %+v", len(got), got)
	}
	// la copie n'est pas perdue, elle est liée à l'original
	c, err := s.Get(context.Background(), "ws1", cp.ID)
	if err != nil {
		t.Fatalf("la copie doit rester lisible par son id: %v", err)
	}
	if c.DuplicateOf != orig.ID {
		t.Fatalf("la copie doit pointer l'original: %q (attendu %q)", c.DuplicateOf, orig.ID)
	}
}

// La copie n'est jamais supprimée (traçabilité), et un filtre explicite la rend.
func TestDedup_CopyIsKeptAndLinked(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	now := time.Now()
	orig := mustIngest(t, g, box1, ref(1), original("trace1@mail.gmail.com").raw(), now)
	cp := mustIngest(t, g, box2, ref2(1), siveCopy("trace1@mail.gmail.com").raw(), now.Add(time.Second))

	if n := rowCount(t, s, "TRUE"); n != 2 {
		t.Fatalf("aucune ligne supprimée: %d", n)
	}
	if cp.DuplicateOf != orig.ID {
		t.Fatalf("la copie ingérée rend son original: %q", cp.DuplicateOf)
	}
	if orig.DuplicateOf != "" {
		t.Fatalf("l'original n'est la copie de personne: %q", orig.DuplicateOf)
	}
	all, _, err := s.List(context.Background(), "ws1", Filter{Direction: DirectionIn, IncludeDuplicates: true})
	if err != nil || len(all) != 2 {
		t.Fatalf("avec IncludeDuplicates les deux lignes: %v %d", err, len(all))
	}
}

// Ancienne copie par alias (avant le Sieve) : pas d'en-tête Sieve, dans les deux ordres.
func TestDedup_AliasCopyWithoutSieveHeader_BothOrders(t *testing.T) {
	for _, copyFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("copie_d_abord=%v", copyFirst), func(t *testing.T) {
			s, _ := testStore(t)
			g := testIngestor(s)
			now := time.Now()
			var orig *Message
			if copyFirst {
				mustIngest(t, g, box2, ref2(1), aliasCopy("alias1@mail.gmail.com").raw(), now)
				orig = mustIngest(t, g, box1, ref(1), original("alias1@mail.gmail.com").raw(), now.Add(time.Second))
			} else {
				orig = mustIngest(t, g, box1, ref(1), original("alias1@mail.gmail.com").raw(), now)
				mustIngest(t, g, box2, ref2(1), aliasCopy("alias1@mail.gmail.com").raw(), now.Add(time.Second))
			}
			got := inbound(t, s)
			if len(got) != 1 || got[0].ID != orig.ID {
				t.Fatalf("l'original, livré à son destinataire, reste visible: %d lignes %+v", len(got), got)
			}
		})
	}
}

// Un rebond reçu en double (ancienne copie par alias) ne compte qu'une fois, le contact est conservé.
func TestDedup_BounceCopiedByAlias(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	addSent(t, db, j0uuid, "mort@gmail.com")
	bnc := bounce550(j0uuid + "@animalservicedistribution.fr")
	now := time.Now()
	orig := mustIngest(t, g, box1, ref(1), append([]byte("Delivered-To: hugo.bourdat@animalservicedistribution.fr\r\n"), bnc...), now)
	mustIngest(t, g, box2, ref2(1), append([]byte("Delivered-To: communication@animalsd.fr\r\n"), bnc...), now.Add(time.Second))

	got, _, _ := s.List(context.Background(), "ws1", Filter{Kinds: []Kind{KindBounceHard}})
	if len(got) != 1 || got[0].ID != orig.ID {
		t.Fatalf("un seul rebond visible: %+v", got)
	}
}

// Les rebonds, accusés et réponses automatiques DISTINCTS (Message-ID différents) ne sont pas cassés.
func TestDedup_DistinctBouncesAndAutoRepliesUntouched(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	db.Exec(`INSERT INTO contacts (email) VALUES ('contact@societe.fr')`)
	now := time.Now()
	b1 := string(bounce550("aaa@x.fr"))
	b2 := strings.Replace(b1, "bnc1@", "bnc9@", 1)
	mustIngest(t, g, box1, ref(1), []byte(b1), now)
	mustIngest(t, g, box1, ref(2), []byte(b2), now.Add(time.Minute))
	a1 := string(autoReply("<q@x.fr>"))
	a2 := strings.Replace(a1, "auto1@", "auto2@", 1)
	mustIngest(t, g, box1, ref(3), []byte(a1), now.Add(2*time.Minute))
	mustIngest(t, g, box1, ref(4), []byte(a2), now.Add(3*time.Minute))

	if got := inbound(t, s); len(got) != 4 {
		t.Fatalf("quatre messages distincts, quatre lignes: %d", len(got))
	}
}

// Deux vrais messages distincts de même objet et de même expéditeur ne fusionnent JAMAIS.
func TestDedup_TwoRealMessagesSameSubjectDoNotMerge(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	now := time.Now()
	a := original("meme-objet-a@mail.gmail.com")
	b := original("meme-objet-b@mail.gmail.com")
	b.date = "Sat, 31 Oct 2026 19:09:00 +0100"
	b.body = "Autre message : finalement oui, appelez-moi."
	mustIngest(t, g, box1, ref(1), a.raw(), now)
	mustIngest(t, g, box1, ref(2), b.raw(), now.Add(90*time.Second))
	// et le même contact, mêmes objet et corps début, dans l'autre boîte
	c := siveCopy("meme-objet-a@mail.gmail.com")
	mustIngest(t, g, box2, ref2(1), c.raw(), now.Add(time.Second))

	if got := inbound(t, s); len(got) != 2 {
		t.Fatalf("deux messages distincts (la copie de A est masquée): %d", len(got))
	}
}

// Sans Message-ID : repli sur contact + fil + date d'envoi + empreinte du corps.
func TestDedup_NoMessageIDFallback(t *testing.T) {
	for _, copyFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("copie_d_abord=%v", copyFirst), func(t *testing.T) {
			s, _ := testStore(t)
			g := testIngestor(s)
			now := time.Now()
			o := original("")
			c := aliasCopy("")
			var orig *Message
			if copyFirst {
				mustIngest(t, g, box2, ref2(1), c.raw(), now)
				orig = mustIngest(t, g, box1, ref(1), o.raw(), now.Add(time.Second))
			} else {
				orig = mustIngest(t, g, box1, ref(1), o.raw(), now)
				mustIngest(t, g, box2, ref2(1), c.raw(), now.Add(time.Second))
			}
			got := inbound(t, s)
			if len(got) != 1 || got[0].ID != orig.ID {
				t.Fatalf("sans Message-ID, la copie est reconnue par son empreinte: %d lignes", len(got))
			}
		})
	}
}

// Sans Message-ID, deux messages qui ne diffèrent que par la date ou le corps restent distincts.
func TestDedup_NoMessageIDDistinctMessagesStayDistinct(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	now := time.Now()
	a := original("")
	b := original("")
	b.date = "Sat, 31 Oct 2026 19:30:00 +0100" // autre heure, même corps
	c := original("")
	c.body = "Un tout autre texte, même heure que A." // même heure que A, autre corps
	mustIngest(t, g, box1, ref(1), a.raw(), now)
	mustIngest(t, g, box1, ref(2), b.raw(), now.Add(time.Minute))
	mustIngest(t, g, box1, ref(3), c.raw(), now.Add(2*time.Minute))
	if got := inbound(t, s); len(got) != 3 {
		t.Fatalf("trois messages distincts sans Message-ID: %d", len(got))
	}
}

// Les compteurs « à traiter », le fil et les fils d'un contact ne voient pas la copie.
func TestDedup_CountersThreadsAndContactViewsIgnoreCopy(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	now := time.Now()
	orig := mustIngest(t, g, box1, ref(1), original("compteurs1@mail.gmail.com").raw(), now)
	mustIngest(t, g, box2, ref2(1), siveCopy("compteurs1@mail.gmail.com").raw(), now.Add(time.Second))

	counts, err := s.Counts(ctx, "ws1")
	if err != nil {
		t.Fatal(err)
	}
	// reply_human n'est posé que pour un contact connu : on compte les entrants et les « autres »
	if c := counts["prof1"]; c.CountIn != 1 {
		t.Fatalf("la boîte du relais a reçu 1 message: %+v", c)
	}
	if c := counts["prof2"]; c.CountIn != 0 {
		t.Fatalf("la copie ne compte pas dans la boîte de Hugo: %+v", c)
	}
	msgs, err := s.ThreadMessages(ctx, "ws1", orig.ThreadID)
	if err != nil || len(msgs) != 1 || msgs[0].ID != orig.ID {
		t.Fatalf("le fil montre le message une fois: %v %d", err, len(msgs))
	}
	ths, err := s.ThreadsOfContact(ctx, "ws1", "contact@animessence.fr", 10)
	if err != nil || len(ths) != 1 || ths[0].Count != 1 {
		t.Fatalf("les fils du contact comptent une fois: %v %+v", err, ths)
	}
	byContact, _, _ := s.List(ctx, "ws1", Filter{ContactEmail: "contact@animessence.fr"})
	if len(byContact) != 1 {
		t.Fatalf("liste filtrée par contact: %d", len(byContact))
	}
}

// Traiter un message traite le groupe ; un message traité côté copie reste traité quand l'original arrive.
func TestDedup_HandledStateFollowsTheGroup(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	now := time.Now()
	cp := mustIngest(t, g, box2, ref2(1), siveCopy("traite1@mail.gmail.com").raw(), now)
	yes := true
	if err := s.Update(ctx, "ws1", cp.ID, &yes, "hugo", nil); err != nil {
		t.Fatal(err)
	}
	orig := mustIngest(t, g, box1, ref(1), original("traite1@mail.gmail.com").raw(), now.Add(time.Second))
	got, err := s.Get(ctx, "ws1", orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HandledAt == nil || got.HandledBy != "hugo" {
		t.Fatalf("l'original hérite du traitement fait sur la copie: %+v", got.HandledAt)
	}
	no := false
	open, _, _ := s.List(ctx, "ws1", Filter{Handled: &no})
	if len(open) != 0 {
		t.Fatalf("rien à traiter: %d", len(open))
	}
	// agir sur la copie par son id agit aussi sur l'original
	if err := s.Update(ctx, "ws1", cp.ID, &no, "", nil); err != nil {
		t.Fatal(err)
	}
	open, _, _ = s.List(ctx, "ws1", Filter{Handled: &no})
	if len(open) != 1 || open[0].ID != orig.ID {
		t.Fatalf("remettre la copie à traiter remet l'original à traiter: %+v", open)
	}
}

// Le webhook message.received et le flux d'événements ne signalent pas la copie Sieve.
func TestDedup_WebhookAndEventsIgnoreSieveCopy(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	now := time.Now()
	db.Exec(`INSERT INTO webhook_subscriptions (id, enabled, settings) VALUES ('s1', true, '{"event_types":["message.received"]}')`)
	// copie d'abord (ordre inverse) puis original : un seul événement, celui de l'original
	mustIngest(t, g, box2, ref2(1), siveCopy("evt1@mail.gmail.com").raw(), now)
	orig := mustIngest(t, g, box1, ref(1), original("evt1@mail.gmail.com").raw(), now.Add(time.Second))
	var n int
	var mid string
	db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(payload->>'message_id'),'') FROM webhook_deliveries`).Scan(&n, &mid)
	if n != 1 || mid != orig.ID {
		t.Fatalf("une seule livraison, pour l'original: %d %q", n, mid)
	}
}

// Une copie Sieve dont l'original n'est pas (encore) rangé reste visible dans la liste (rien n'est
// perdu) mais n'est jamais un événement : l'original arrive par la boîte du relais.
func TestDedup_SieveCopyAloneVisibleButNotAnEvent(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	now := time.Now()
	db.Exec(`INSERT INTO webhook_subscriptions (id, enabled, settings) VALUES ('s1', true, '{"event_types":["message.received"]}')`)
	first := mustIngest(t, g, box1, ref(1), original("evt-premier@mail.gmail.com").raw(), now)
	cp := mustIngest(t, g, box2, ref2(1), siveCopy("evt-seule@mail.gmail.com").raw(), now.Add(time.Second))

	vis := inbound(t, s)
	if len(vis) != 2 {
		t.Fatalf("la copie sans original reste visible: %d", len(vis))
	}
	ev, _, err := s.List(ctx, "ws1", Filter{AfterSeq: first.Seq, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ev {
		if e.ID == cp.ID {
			t.Fatalf("la copie Sieve ne figure pas dans le flux d'événements: %+v", ev)
		}
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries WHERE payload->>'message_id' = $1`, cp.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("aucun webhook pour la copie Sieve: %d", n)
	}
}

// Un Message-ID inexploitable (sans @) ne sert jamais de clé : deux mails distincts qui le partagent restent distincts.
func TestDedup_UnusableMessageIDNeverMerges(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	now := time.Now()
	a := original("1")
	b := original("1")
	b.date = "Sat, 31 Oct 2026 20:00:00 +0100"
	b.body = "Un autre mail, un autre jour."
	mustIngest(t, g, box1, ref(1), a.raw(), now)
	mustIngest(t, g, box2, ref2(1), b.raw(), now.Add(time.Second))
	if got := inbound(t, s); len(got) != 2 {
		t.Fatalf("Message-ID <1> n'est pas une clé: %d lignes visibles", len(got))
	}
}

// Relire le même UID, ou rejouer toute la boîte, ne change rien.
func TestDedup_IdempotentOnReplay(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	now := time.Now()
	oRaw := original("rejeu1@mail.gmail.com").raw()
	cRaw := siveCopy("rejeu1@mail.gmail.com").raw()
	mustIngest(t, g, box1, ref(1), oRaw, now)
	mustIngest(t, g, box2, ref2(1), cRaw, now.Add(time.Second))
	for i := 0; i < 3; i++ {
		g.IngestRaw(context.Background(), "ws1", box1, ref(1), oRaw, now, false)
		g.IngestRaw(context.Background(), "ws1", box2, ref2(1), cRaw, now, false)
	}
	if got := inbound(t, s); len(got) != 1 {
		t.Fatalf("après rejeu: %d lignes visibles", len(got))
	}
	if n := rowCount(t, s, "TRUE"); n != 2 {
		t.Fatalf("après rejeu: %d lignes en base", n)
	}
}

// Les mails sortants ne sont jamais touchés, même avec un Message-ID qui ressemble.
func TestDedup_OutboundUntouched(t *testing.T) {
	s, _ := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, err := g.RecordSent(ctx, SentInput{WorkspaceID: "ws1", MessageHistoryID: fmt.Sprintf("ws1_out%d", i), ContactEmail: "m@x.fr",
			FromAddress: "h@d.fr", Subject: "Même objet", SentAt: time.Now(), BodySource: BodyCaptured})
		if err != nil {
			t.Fatal(err)
		}
	}
	out, _, _ := s.List(ctx, "ws1", Filter{Direction: DirectionOut})
	if len(out) != 2 {
		t.Fatalf("deux envois, deux lignes: %d", len(out))
	}
	if n := rowCount(t, s, "duplicate_of IS NOT NULL"); n != 0 {
		t.Fatalf("aucun sortant marqué: %d", n)
	}
}

// insertLegacy range une ligne comme le faisait la version d'avant V68 (aucune colonne de déduplication).
func insertLegacy(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, id, dir, kind, msgID, contact, subject string, headers string, to []string, recv time.Time, handled bool) {
	t.Helper()
	var hAt any
	if handled {
		hAt = recv
	}
	_, err := db.Exec(`INSERT INTO veridian_mail_message (id, workspace_id, direction, kind, profile_id, message_id, thread_id, contact_email,
		subject, to_addrs, received_at, occurred_at, headers, handled_at, handled_by)
		VALUES ($1,'ws1',$2,$3,'p',NULLIF($4,''),'t1',$5,$6,$7,$8,$8,$9::jsonb,$10,CASE WHEN $10::timestamptz IS NULL THEN NULL ELSE 'hugo' END)`,
		id, dir, kind, msgID, contact, subject, pq.Array(to), recv, headers, hAt)
	if err != nil {
		t.Fatal(err)
	}
}

// Rattrapage de la migration V68 sur des lignes existantes : doublons marqués, rien de perdu,
// sortants et messages distincts intacts, traitement de la copie reporté sur l'original.
func TestDedupMigration_BackfillMarksExistingDuplicates(t *testing.T) {
	s, db := testStoreWith(t, SchemaStatements) // schéma d'avant V68
	t0 := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	rcpt := []string{"hugo.bourdat@contact.animal-services-distribution.fr"}
	sieve := `{"X-Sieve-Redirected-From":["hugo.bourdat@contact.animal-services-distribution.fr"]}`
	insertLegacy(t, db, "o1", "in", "reply_human", "<a@x.fr>", "c@x.fr", "Re: Offre", `{"Delivered-To":["hugo.bourdat@contact.animal-services-distribution.fr"]}`, rcpt, t0, false)
	insertLegacy(t, db, "c1", "in", "reply_human", "<a@x.fr>", "c@x.fr", "Re: Offre", sieve, rcpt, t0.Add(time.Second), true) // copie traitée
	// alias sans en-tête Sieve, la copie arrivée la première
	insertLegacy(t, db, "c2", "in", "bounce_hard", "<B@X.fr>", "d@x.fr", "Undelivered", `{"Delivered-To":["communication@animalsd.fr"]}`, rcpt, t0, false)
	insertLegacy(t, db, "o2", "in", "bounce_hard", "<b@x.fr>", "d@x.fr", "Undelivered", `{"Delivered-To":["hugo.bourdat@contact.animal-services-distribution.fr"]}`, rcpt, t0.Add(time.Second), false)
	// deux messages distincts de même objet
	insertLegacy(t, db, "d1", "in", "other", "<n1@x.fr>", "e@x.fr", "Notification", `{}`, rcpt, t0, false)
	insertLegacy(t, db, "d2", "in", "other", "<n2@x.fr>", "e@x.fr", "Notification", `{}`, rcpt, t0.Add(time.Hour), false)
	// sans Message-ID : jamais regroupés par la migration
	insertLegacy(t, db, "n1", "in", "other", "", "f@x.fr", "Sans id", `{}`, rcpt, t0, false)
	insertLegacy(t, db, "n2", "in", "other", "", "f@x.fr", "Sans id", `{}`, rcpt, t0, false)
	// sortants au même Message-ID : intouchés
	insertLegacy(t, db, "s1", "out", "sent", "<same@x.fr>", "g@x.fr", "Envoi", `{}`, rcpt, t0, false)
	insertLegacy(t, db, "s2", "out", "sent", "<same@x.fr>", "g@x.fr", "Envoi", `{}`, rcpt, t0.Add(time.Second), false)

	before := rowCount(t, s, "TRUE")
	for _, q := range dedupsqlStatements() {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("migration: %v\n%s", err, q)
		}
	}
	// idempotente : une seconde application ne change rien
	for _, q := range dedupsqlStatements() {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seconde application: %v", err)
		}
	}
	if after := rowCount(t, s, "TRUE"); after != before {
		t.Fatalf("aucune ligne perdue ou ajoutée: %d -> %d", before, after)
	}
	dup := map[string]string{}
	rows, err := db.Query(`SELECT id, COALESCE(duplicate_of,'') FROM veridian_mail_message`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, d string
		rows.Scan(&id, &d)
		dup[id] = d
	}
	rows.Close()
	want := map[string]string{"o1": "", "c1": "o1", "o2": "", "c2": "o2", "d1": "", "d2": "", "n1": "", "n2": "", "s1": "", "s2": ""}
	for id, w := range want {
		if dup[id] != w {
			t.Errorf("duplicate_of de %s = %q, attendu %q", id, dup[id], w)
		}
	}
	var handledBy string
	var handledAt sql.NullTime
	db.QueryRow(`SELECT handled_at, COALESCE(handled_by,'') FROM veridian_mail_message WHERE id='o1'`).Scan(&handledAt, &handledBy)
	if !handledAt.Valid || handledBy != "hugo" {
		t.Errorf("le traitement fait sur la copie passe à l'original: %v %q", handledAt, handledBy)
	}
	vis, _, _ := s.List(context.Background(), "ws1", Filter{Direction: DirectionIn, Limit: 200})
	if len(vis) != 6 { // o1, o2, d1, d2, n1, n2
		t.Errorf("lignes entrantes visibles: %d (attendu 6)", len(vis))
	}
}

// SettleDuplicates rattrape les mails rangés sans marquage (version antérieure, panne de résolution).
func TestDedup_SettleCatchesUnmarkedRows(t *testing.T) {
	s, db := testStore(t)
	t0 := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	rcpt := []string{"hugo.bourdat@contact.animal-services-distribution.fr"}
	insertLegacy(t, db, "o1", "in", "reply_human", "<a@x.fr>", "c@x.fr", "Re: Offre", `{"Delivered-To":["hugo.bourdat@contact.animal-services-distribution.fr"]}`, rcpt, t0, false)
	insertLegacy(t, db, "c1", "in", "reply_human", "<a@x.fr>", "c@x.fr", "Re: Offre", `{"X-Sieve-Redirected-From":["x"]}`, rcpt, t0.Add(time.Second), false)
	if vis := inbound(t, s); len(vis) != 2 {
		t.Fatalf("avant rattrapage: %d visibles", len(vis))
	}
	n, err := s.SettleDuplicates(context.Background(), "ws1")
	if err != nil || n == 0 {
		t.Fatalf("rattrapage: %v %d", err, n)
	}
	if vis := inbound(t, s); len(vis) != 1 || vis[0].ID != "o1" {
		t.Fatalf("après rattrapage: %+v", vis)
	}
	if n, _ := s.SettleDuplicates(context.Background(), "ws1"); n != 0 {
		t.Fatalf("un second rattrapage ne change rien: %d", n)
	}
}

// Empreinte de repli : stable pour le même mail, différente dès que le contact, la date ou le corps change.
func TestFallbackDedupKey(t *testing.T) {
	d := time.Date(2026, 10, 31, 18, 7, 35, 0, time.UTC)
	base := func() *Message {
		return &Message{ContactEmail: "A@x.fr", ThreadID: "t1", SentAt: &d, BodyText: "Bonjour,\n oui   envoyez vos tarifs"}
	}
	k := FallbackDedupKey(base())
	if !strings.HasPrefix(k, "fp:") {
		t.Fatalf("clé: %q", k)
	}
	same := base()
	same.ContactEmail, same.BodyText = "a@x.fr", "bonjour, oui envoyez vos tarifs"
	if FallbackDedupKey(same) != k {
		t.Fatal("casse et espaces ne changent pas l'empreinte")
	}
	d2 := d.Add(time.Second)
	for name, m := range map[string]*Message{
		"autre date":    {ContactEmail: "a@x.fr", ThreadID: "t1", SentAt: &d2, BodyText: base().BodyText},
		"autre corps":   {ContactEmail: "a@x.fr", ThreadID: "t1", SentAt: &d, BodyText: "autre texte"},
		"autre contact": {ContactEmail: "b@x.fr", ThreadID: "t1", SentAt: &d, BodyText: base().BodyText},
		"autre fil":     {ContactEmail: "a@x.fr", ThreadID: "t2", SentAt: &d, BodyText: base().BodyText},
	} {
		if FallbackDedupKey(m) == k {
			t.Errorf("%s : l'empreinte doit changer", name)
		}
	}
	for name, m := range map[string]*Message{
		"sans date":    {ContactEmail: "a@x.fr", ThreadID: "t1", BodyText: "x"},
		"sans contact": {ThreadID: "t1", SentAt: &d, BodyText: "x"},
		"sans corps":   {ContactEmail: "a@x.fr", ThreadID: "t1", SentAt: &d},
	} {
		if FallbackDedupKey(m) != "" {
			t.Errorf("%s : pas assez d'éléments, pas de clé", name)
		}
	}
}

// recordingNotifier note chaque message annoncé à Notifier.
type recordingNotifier struct{ ids []string }

func (r *recordingNotifier) MessageReceived(_ context.Context, _ string, m *Message) {
	r.ids = append(r.ids, m.ID)
}

// Le notifier (réveil du flux, branché par un futur câblage : nil en production aujourd'hui) n'est
// prévenu que de l'original, quel que soit l'ordre d'arrivée de la copie Sieve.
func TestDedup_NotifierOnlyToldAboutTheOriginal(t *testing.T) {
	for _, copyFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("copie_d_abord=%v", copyFirst), func(t *testing.T) {
			s, _ := testStore(t)
			n := &recordingNotifier{}
			g := NewIngestor(s, testIngestor(s).log, n)
			now := time.Now()
			var orig *Message
			if copyFirst {
				mustIngest(t, g, box2, ref2(1), siveCopy("notif1@mail.gmail.com").raw(), now)
				orig = mustIngest(t, g, box1, ref(1), original("notif1@mail.gmail.com").raw(), now.Add(time.Second))
			} else {
				orig = mustIngest(t, g, box1, ref(1), original("notif1@mail.gmail.com").raw(), now)
				mustIngest(t, g, box2, ref2(1), siveCopy("notif1@mail.gmail.com").raw(), now.Add(time.Second))
			}
			if len(n.ids) != 1 || n.ids[0] != orig.ID {
				t.Fatalf("une seule annonce, pour l'original: %v (original %s)", n.ids, orig.ID)
			}
		})
	}
}
