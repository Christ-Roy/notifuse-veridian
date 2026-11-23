package mailbox

import (
	"bytes"
	"context"
	"testing"
	"time"

	veridianmig "github.com/Notifuse/notifuse/internal/migrations/veridian"
	"github.com/Notifuse/notifuse/pkg/logger"
)

func boxStore(t *testing.T) (*Store, func(uid uint32, profile, to, subject string) string) {
	t.Helper()
	st, db := testStore(t)
	for _, sp := range veridianmig.Default.List() {
		if sp.Number == 75 {
			for _, q := range sp.Workspace {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	ing := NewIngestor(st, logger.NewLogger(), nil)
	return st, func(uid uint32, profile, to, subject string) string {
		raw := "From: p@client.fr\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nMessage-Id: <" + subject + "@client.fr>\r\n\r\nBonjour\r\n"
		m, ok, err := ing.IngestRaw(context.Background(), "ws", BoxInfo{IntegrationID: "i", ProfileID: profile}, MailboxRef{IntegrationID: "i", Folder: "F", UIDValidity: 1, UID: uid}, []byte(raw), time.Now(), false)
		if err != nil || !ok {
			t.Fatalf("%v %v", ok, err)
		}
		if _, err := db.Exec(`UPDATE veridian_mail_message SET created_at = NOW() - INTERVAL '1 minute'`); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
}

func TestBoxIndex_FoldersFlagsAndScope(t *testing.T) {
	st, add := boxStore(t)
	ctx := context.Background()
	a := add(1, "pA", "a@boite.fr", "un")
	b := add(2, "pB", "b@boite.fr", "deux")
	ids := func(p string, f BoxFolder) (out []string) {
		l, err := st.BoxIndex(ctx, "ws", p, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range l {
			out = append(out, e.ID)
		}
		return
	}
	if got := ids("pA", BoxInbox); len(got) != 1 || got[0] != a {
		t.Fatalf("INBOX de pA : %v", got)
	}
	if m, err := st.BoxMessages(ctx, "ws", "pA", []string{a, b}); err != nil || len(m) != 1 || m[a] == nil {
		t.Fatalf("un profil lit le courrier d'un autre : %v %v", m, err)
	}
	if _, err := st.BoxIndex(ctx, "ws", "pA", "Inconnu"); err != ErrNotFound {
		t.Fatalf("dossier inconnu : %v", err)
	}
	// Drapeaux : par profil, et sans effet depuis un autre profil.
	if err := st.BoxSetFlags(ctx, "ws", "pB", a, []string{`\Seen`}, nil); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.BoxIndex(ctx, "ws", "pA", BoxInbox); len(l[0].Flags) != 0 || l[0].Size == 0 {
		t.Fatalf("%+v", l[0])
	}
	yes, no := true, false
	if err := st.BoxSetFlags(ctx, "ws", "pA", a, []string{`\Seen`}, &yes); err != nil {
		t.Fatal(err)
	}
	if len(ids("pA", BoxInbox)) != 0 || len(ids("pA", BoxTrash)) != 1 {
		t.Fatal("un message retiré va à la corbeille")
	}
	if err := st.BoxSetFlags(ctx, "ws", "pA", a, []string{`\Seen`}, nil); err != nil || len(ids("pA", BoxTrash)) != 1 {
		t.Fatal("changer un drapeau ne doit pas sortir de la corbeille")
	}
	if err := st.BoxSetFlags(ctx, "ws", "pA", a, nil, &no); err != nil || len(ids("pA", BoxInbox)) != 1 {
		t.Fatal("restauration")
	}
	if err := st.BoxPurge(ctx, "ws", "pA", a, nil); err != nil || len(ids("pA", BoxTrash)) != 0 || len(ids("pA", BoxInbox)) != 0 {
		t.Fatal("vidé de la corbeille")
	}
	if _, err := st.Get(ctx, "ws", a); err != nil {
		t.Fatalf("jamais supprimé de la base : %v", err)
	}
}

func TestBoxIndex_WaitsForFreshRows(t *testing.T) {
	st, db := testStore(t)
	for _, sp := range veridianmig.Default.List() {
		if sp.Number == 75 {
			for _, q := range sp.Workspace {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if _, err := db.Exec(`INSERT INTO veridian_mail_message (id, workspace_id, direction, kind, profile_id, thread_id, occurred_at) VALUES ('x','ws','in','other','pA','t',NOW())`); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.BoxIndex(context.Background(), "ws", "pA", BoxInbox); len(l) != 0 {
		t.Fatal("une ligne à peine écrite doit attendre")
	}
}

func TestBoxSentKnown(t *testing.T) {
	st, _ := boxStore(t)
	ctx := context.Background()
	ing := NewIngestor(st, logger.NewLogger(), nil)
	if _, err := ing.RecordSent(ctx, SentInput{WorkspaceID: "ws", MessageHistoryID: "abc", ContactEmail: "p@client.fr", FromAddress: "a@boite.fr", Subject: "S", Text: "t", ProfileID: "pA", SentAt: time.Now(), BodySource: BodyCaptured}); err != nil {
		t.Fatal(err)
	}
	if _, err := ing.RecordSent(ctx, SentInput{WorkspaceID: "ws", MessageHistoryID: "brut", ContactEmail: "p@client.fr", FromAddress: "a@boite.fr", Subject: "S2", Text: "t", ProfileID: "pA", SentAt: time.Now(), BodySource: BodyCaptured, Raw: []byte("Subject: S2\r\n\r\navec pièce\r\n")}); err != nil {
		t.Fatal(err)
	}
	var rawID string
	db, _ := st.db(ctx, "ws")
	if err := db.QueryRow(`SELECT id FROM veridian_mail_message WHERE sent_ref = 'brut'`).Scan(&rawID); err != nil {
		t.Fatal(err)
	}
	if m, err := st.BoxMessages(ctx, "ws", "pA", []string{rawID}); err != nil || string(BoxRaw(m[rawID])) != "Subject: S2\r\n\r\navec pièce\r\n" {
		t.Fatalf("l'envoi d'un client mail se relit tel quel : %v", err)
	}
	if ok, err := st.BoxSentKnown(ctx, "ws", "pA", []string{"<abc@boite.fr>"}); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if ok, _ := st.BoxSentKnown(ctx, "ws", "pB", []string{"<abc@boite.fr>"}); ok {
		t.Fatal("envoi d'un autre profil reconnu")
	}
}

func TestBoxRaw_StableAndCRLF(t *testing.T) {
	m := &Message{ID: "m1", MessageID: "<m1@x.fr>", From: "a@x.fr", To: []string{"b@y.fr"}, Subject: "Été", BodyText: "ligne 1\nligne 2", BodyHTMLSanitized: "<p>ligne</p>", OccurredAt: time.Unix(0, 0)}
	one, two := BoxRaw(m), BoxRaw(m)
	if !bytes.Equal(one, two) {
		t.Fatal("deux lectures doivent rendre les mêmes octets")
	}
	if bytes.Contains(bytes.ReplaceAll(one, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("fins de ligne nues")
	}
	if got := BoxRaw(&Message{RawEML: []byte("A: b\n\nc\r\n")}); string(got) != "A: b\r\n\r\nc\r\n" {
		t.Fatalf("%q", got)
	}
}
