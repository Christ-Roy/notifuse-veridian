package mailbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

func wsTwoProfiles() *domain.Workspace {
	mk := func(id string, emails ...string) domain.Integration {
		in := domain.Integration{ID: id, Type: domain.IntegrationTypeEmail}
		for _, e := range emails {
			in.EmailProvider.Senders = append(in.EmailProvider.Senders, domain.EmailSender{Email: e})
		}
		return in
	}
	return &domain.Workspace{ID: "ws", Integrations: domain.Integrations{
		mk("pJC", "Jean-Claude@boite.fr"), mk("pNord", "r@boite.fr"), mk("pDup", "r@boite.fr", "seul@boite.fr"),
		{ID: "imap", Type: domain.IntegrationTypeIMAP},
	}}
}

func TestOwnersResolve(t *testing.T) {
	o := OwnersOf(wsTwoProfiles())
	for name, tc := range map[string]struct {
		addrs  []string
		prefer []string
		want   string
	}{
		"adresse connue, casse ignorée":     {[]string{"JEAN-CLAUDE@boite.fr"}, []string{"pNord"}, "pJC"},
		"adresse inconnue":                  {[]string{"x@y.fr"}, []string{"pNord"}, ""},
		"partagée, départagée par la boîte": {[]string{"r@boite.fr"}, []string{"", "pDup"}, "pDup"},
		"partagée, rien ne départage":       {[]string{"r@boite.fr"}, nil, ""},
		"deux profils, boîte lue":           {[]string{"jean-claude@boite.fr", "seul@boite.fr"}, []string{"pDup"}, "pDup"},
	} {
		if got := o.Resolve(tc.addrs, tc.prefer...); got != tc.want {
			t.Errorf("%s : %q, attendu %q", name, got, tc.want)
		}
	}
	if len(OwnersOf(nil)) != 0 {
		t.Error("workspace absent")
	}
}

// Le cas greenbeamcraft : boîte Gmail commune, réponse qui cite un envoi parti d'un AUTRE profil.
func TestIngest_ProfileFollowsRecipientAddress(t *testing.T) {
	st, _ := testStore(t)
	ing := NewIngestor(st, logger.NewLogger(), nil)
	ctx := context.Background()
	if _, err := ing.RecordSent(ctx, SentInput{WorkspaceID: "ws", MessageHistoryID: "envoi-nord", ContactEmail: "p@client.fr", FromAddress: "r@boite.fr", Subject: "Offre", Text: "x", ProfileID: "pNord", SentAt: time.Now(), BodySource: BodyCaptured}); err != nil {
		t.Fatal(err)
	}
	box := BoxInfo{IntegrationID: "gmail", Owners: OwnersOf(wsTwoProfiles())}
	in := func(uid uint32, hdr string) *Message {
		raw := strings.ReplaceAll("From: p@client.fr\n"+hdr+"Subject: Re: Offre\nMessage-Id: <r"+string(rune('a'+uid))+"@client.fr>\n\nOui\n", "\n", "\r\n")
		m, ok, err := ing.IngestRaw(ctx, "ws", box, MailboxRef{IntegrationID: "gmail", Folder: "F", UIDValidity: 1, UID: uid}, []byte(raw), time.Now(), false)
		if err != nil || !ok {
			t.Fatalf("%v %v", ok, err)
		}
		return m
	}
	if m := in(1, "To: jean-claude@boite.fr\nIn-Reply-To: <envoi-nord@boite.fr>\n"); m.ProfileID != "pJC" {
		t.Fatalf("rangé sous %q au lieu du profil de l'adresse", m.ProfileID)
	}
	if m := in(2, "To: liste@ailleurs.fr\nDelivered-To: jean-claude@boite.fr\n"); m.ProfileID != "pJC" {
		t.Fatalf("redirection : rangé sous %q", m.ProfileID)
	}
	if m := in(3, "To: inconnu@ailleurs.fr\nIn-Reply-To: <envoi-nord@boite.fr>\n"); m.ProfileID != "pNord" {
		t.Fatalf("sans adresse connue, l'envoi cité décide : %q", m.ProfileID)
	}
}

func TestReassignOwners_FixesStoredMailOnce(t *testing.T) {
	st, db := testStore(t)
	ctx := context.Background()
	add := func(id, profile, to string) {
		var p any
		if profile != "" {
			p = profile
		}
		if _, err := db.Exec(`INSERT INTO veridian_mail_message (id, workspace_id, direction, kind, profile_id, thread_id, to_addrs, occurred_at) VALUES ($1,'ws','in','reply_human',$2,'t',ARRAY[$3],NOW())`, id, p, to); err != nil {
			t.Fatal(err)
		}
	}
	add("sans-profil", "", "jean-claude@boite.fr")
	add("autre-profil", "pNord", "Jean-Claude@boite.fr")
	add("deja-bon", "pJC", "jean-claude@boite.fr")
	add("partagee", "pNord", "r@boite.fr")
	add("ailleurs", "pNord", "x@y.fr")
	o := OwnersOf(wsTwoProfiles())
	n, err := st.ReassignOwners(ctx, "ws", o)
	if err != nil || n != 2 {
		t.Fatalf("%d lignes, %v", n, err)
	}
	for id, want := range map[string]string{"sans-profil": "pJC", "autre-profil": "pJC", "deja-bon": "pJC", "partagee": "pNord", "ailleurs": "pNord"} {
		var p string
		if err := db.QueryRow(`SELECT COALESCE(profile_id,'') FROM veridian_mail_message WHERE id=$1`, id).Scan(&p); err != nil || p != want {
			t.Errorf("%s : %q (%v), attendu %q", id, p, err, want)
		}
	}
	if n, _ := st.ReassignOwners(ctx, "ws", o); n != 0 {
		t.Fatalf("second passage : %d lignes", n)
	}
}
