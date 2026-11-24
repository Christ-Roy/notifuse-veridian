package mailbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

func TestInScope_Regle(t *testing.T) {
	msgID := &Link{MatchType: "message_id"}
	sender := &Link{MatchType: "sender"}
	cas := []struct {
		nom   string
		scope string
		kind  Kind
		link  *Link
		want  bool
	}{
		{"portée vide : tout est rangé (autre)", "", KindOther, nil, true},
		{"portée vide : réponse par expéditeur", "", KindReplyHuman, sender, true},
		{"replies : courrier courant écarté", "replies", KindOther, nil, false},
		{"replies : réponse reliée par Message-ID gardée", "replies", KindReplyHuman, msgID, true},
		{"replies : auto-réponse reliée gardée", "replies", KindReplyAuto, msgID, true},
		{"replies : rebond relié gardé", "replies", KindBounceHard, msgID, true},
		{"replies : rapport de livraison relié gardé", "replies", KindDSN, msgID, true},
		{"replies : rebond sans envoi d'origine écarté (courrier d'un salarié)", "replies", KindBounceHard, nil, false},
		{"replies : réponse reliée seulement par l'expéditeur écartée", "replies", KindReplyHuman, sender, false},
	}
	for _, c := range cas {
		if got := InScope(c.scope, c.kind, c.link); got != c.want {
			t.Errorf("%s : InScope(%q, %s) = %v, attendu %v", c.nom, c.scope, c.kind, got, c.want)
		}
	}
}

// Boîte humaine à portée « replies » : seule la réponse à NOTRE envoi est rangée, le reste n'est ni stocké ni compté,
// et le message écarté ne bloque pas la lecture (inserted=false, pas d'erreur).
func TestIngest_PorteeReplies(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")
	boite := box1
	boite.Scope = "replies"

	// 1. réponse reliée à notre envoi : gardée
	m, inserted, err := g.IngestRaw(ctx, "ws1", boite, ref(1), replyHuman("<"+j0uuid+"@animalservicedistribution.fr>", "<"+j0uuid+"@animalservicedistribution.fr>"), time.Now(), false)
	if err != nil || !inserted || m == nil || m.Kind != KindReplyHuman || m.Link == nil || m.Link.MatchType != "message_id" {
		t.Fatalf("la réponse à un envoi doit être rangée: %v %v %+v", err, inserted, m)
	}

	// 2. courrier courant d'un inconnu : écarté
	raw := strings.Replace(string(replyHuman("<zzz@x.fr>", "<zzz@x.fr>")), "rep1@", "rep2@", 1)
	m2, inserted, err := g.IngestRaw(ctx, "ws1", boite, ref(2), []byte(raw), time.Now(), false)
	if err != nil || inserted || m2 != nil {
		t.Fatalf("un courrier sans lien avec un envoi ne doit pas être rangé: %v %v %+v", err, inserted, m2)
	}

	// 3. contact connu écrivant sans référence (GLS, fournisseur importé comme contact) : écarté
	db.Exec(`INSERT INTO contacts (email) VALUES ('marie@exemple-animaux.fr')`)
	raw = strings.Replace(string(replyHuman("<yyy@x.fr>", "<yyy@x.fr>")), "rep1@", "rep3@", 1)
	m3, inserted, err := g.IngestRaw(ctx, "ws1", boite, ref(3), []byte(raw), time.Now(), false)
	if err != nil || inserted || m3 != nil {
		t.Fatalf("un contact connu sans référence à un envoi ne doit pas être rangé: %v %v %+v", err, inserted, m3)
	}

	// 4. rebond du courrier d'un salarié (envoi d'origine inconnu) : écarté ; rebond de NOTRE envoi : gardé
	m4, inserted, err := g.IngestRaw(ctx, "ws1", boite, ref(4), bounce550("inconnu999@x.fr"), time.Now(), false)
	if err != nil || inserted || m4 != nil {
		t.Fatalf("un rebond sans envoi d'origine ne doit pas être rangé: %v %v %+v", err, inserted, m4)
	}
	addSent(t, db, "22222222-2222-4222-8222-222222222222", "inconnu999@x.fr")
	m5, inserted, err := g.IngestRaw(ctx, "ws1", boite, ref(5), bounce550("22222222-2222-4222-8222-222222222222@animalservicedistribution.fr"), time.Now(), false)
	if err != nil || !inserted || m5 == nil || m5.Kind != KindBounceHard {
		t.Fatalf("le rebond d'un de nos envois doit être rangé: %v %v %+v", err, inserted, m5)
	}

	// 5. même courrier courant dans une boîte sans portée : rangé comme avant (aucune régression)
	m6, inserted, err := g.IngestRaw(ctx, "ws1", box1, ref(6), []byte(strings.Replace(string(replyHuman("<zzz@x.fr>", "<zzz@x.fr>")), "rep1@", "rep4@", 1)), time.Now(), false)
	if err != nil || !inserted || m6 == nil {
		t.Fatalf("sans portée, tout reste rangé: %v %v %+v", err, inserted, m6)
	}

	var n int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM veridian_mail_message WHERE profile_id = 'prof1' AND direction = 'in'`).Scan(&n)
	if n != 3 { // réponse, rebond de notre envoi, courrier de la boîte sans portée
		t.Fatalf("attendu 3 messages entrants rangés, obtenu %d", n)
	}
}

// Boîte rangée AVANT la pose de la portée : la purge retire le courrier courant et ce qui n'est lié que par l'expéditeur,
// garde la réponse à notre envoi, le message déjà traité, et reste sans effet sur une autre boîte.
func TestPurgeOutOfScope(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	ctx := context.Background()
	addSent(t, db, j0uuid, "marie@exemple-animaux.fr")

	reply := replyHuman("<"+j0uuid+"@animalservicedistribution.fr>", "<"+j0uuid+"@animalservicedistribution.fr>")
	if _, ins, err := g.IngestRaw(ctx, "ws1", box1, ref(1), reply, time.Now(), false); err != nil || !ins {
		t.Fatalf("réponse: %v %v", err, ins)
	}
	autre := func(n, uid uint32) []byte {
		return []byte(strings.Replace(string(replyHuman("<zzz@x.fr>", "<zzz@x.fr>")), "rep1@", "rep"+string(rune('a'+n))+"@", 1))
	}
	for i, uid := range []uint32{2, 3} {
		if _, ins, err := g.IngestRaw(ctx, "ws1", box1, ref(uid), autre(uint32(i), uid), time.Now(), false); err != nil || !ins {
			t.Fatalf("autre %d: %v %v", uid, err, ins)
		}
	}
	// une autre boîte (imap2) garde son courrier
	box2 := box1
	box2.IntegrationID = "imap2"
	if _, ins, err := g.IngestRaw(ctx, "ws1", box2, MailboxRef{IntegrationID: "imap2", Folder: "INBOX", UIDValidity: 7, UID: 1}, autre(9, 1), time.Now(), false); err != nil || !ins {
		t.Fatalf("autre boîte: %v %v", err, ins)
	}
	// un courrier déjà traité par un humain reste
	db.Exec(`UPDATE veridian_mail_message SET handled_at = NOW(), handled_by = 'hugo' WHERE mailbox_uid = 3 AND mailbox_integration_id = 'imap1'`)

	n, err := s.PurgeOutOfScope(ctx, "ws1", "imap1")
	if err != nil || n != 1 {
		t.Fatalf("attendu 1 message retiré (le courrier courant non traité), obtenu %d (%v)", n, err)
	}
	n2, err := s.PurgeOutOfScope(ctx, "ws1", "imap1")
	if err != nil || n2 != 0 {
		t.Fatalf("la purge doit être idempotente: %d %v", n2, err)
	}
	var restant, autreBoite int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM veridian_mail_message WHERE mailbox_integration_id = 'imap1' AND direction = 'in'`).Scan(&restant)
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM veridian_mail_message WHERE mailbox_integration_id = 'imap2'`).Scan(&autreBoite)
	if restant != 2 || autreBoite != 1 { // réponse + traité ; imap2 intacte
		t.Fatalf("attendu imap1=2 (réponse + traité) et imap2=1, obtenu %d et %d", restant, autreBoite)
	}
}

func TestIsPersonalBoxAndEffectiveScope(t *testing.T) {
	for name, tc := range map[string]struct {
		s       *domain.IMAPSettings
		setting string
		want    string
	}{
		"Gmail, boite entiere":            {&domain.IMAPSettings{Host: "IMAP.gmail.com", Folder: "INBOX"}, "", "replies"},
		"Gmail, dossier par defaut":       {&domain.IMAPSettings{Host: "imap.gmail.com"}, "", "replies"},
		"Gmail, tous les messages":        {&domain.IMAPSettings{Host: "imap.gmail.com", Folder: "[Gmail]/Tous les messages"}, "", "replies"},
		"Gmail, libelle dedie":            {&domain.IMAPSettings{Host: "imap.gmail.com", Folder: "GBC/Reponses"}, "", ""},
		"Gmail, tout garde explicitement": {&domain.IMAPSettings{Host: "imap.gmail.com", Folder: "INBOX"}, "all", "all"},
		"relais interne":                  {&domain.IMAPSettings{Host: "100.88.202.29", Folder: "INBOX"}, "", ""},
		"relais interne, portee reglee":   {&domain.IMAPSettings{Host: "100.88.202.29", Folder: "INBOX"}, "replies", "replies"},
		"sans reglage IMAP":               {nil, "", ""},
	} {
		if got := EffectiveScope(tc.setting, tc.s); got != tc.want {
			t.Errorf("%s : %q, attendu %q", name, got, tc.want)
		}
	}
	if IsPersonalBox(&domain.IMAPSettings{Host: "mail.infomaniak.com", Folder: "INBOX"}) {
		t.Error("un hebergeur professionnel n'est pas une messagerie grand public")
	}
}

// La portee prise par defaut ecarte le courrier prive a venir, et ne retire JAMAIS ce qui est deja range.
func TestBoxFor_DefaultScopeFiltersWithoutPurging(t *testing.T) {
	ws := &domain.Workspace{ID: "ws", Integrations: domain.Integrations{{ID: "g", Type: domain.IntegrationTypeIMAP, IMAPSettings: &domain.IMAPSettings{Host: "imap.gmail.com", Folder: "INBOX", Username: "client@gmail.com"}}}}
	b := BoxFor(ws, &ws.Integrations[0])
	if b.Scope != domain.VeridianReturnIMAPScopeReplies || b.SetScope != "" {
		t.Fatalf("%+v", b)
	}
	st, db := testStore(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO veridian_mail_message (id, workspace_id, direction, kind, mailbox_integration_id, thread_id, occurred_at) VALUES ('deja','ws','in','other','g','t',NOW())`); err != nil {
		t.Fatal(err)
	}
	ing := NewIngestor(st, logger.NewLogger(), nil)
	put := func(uid uint32, hdr string) bool {
		_, ok, err := ing.IngestRaw(ctx, "ws", b, MailboxRef{IntegrationID: "g", Folder: "INBOX", UIDValidity: 1, UID: uid}, []byte("From: x@y.fr\r\nTo: client@gmail.com\r\nMessage-Id: <p"+string(rune('a'+uid))+"@y.fr>\r\n"+hdr+"Subject: S\r\n\r\nBonjour\r\n"), time.Now(), false)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if put(1, "") {
		t.Fatal("un mail prive ne doit pas etre range")
	}
	if _, err := ing.RecordSent(ctx, SentInput{WorkspaceID: "ws", MessageHistoryID: "envoi", ContactEmail: "x@y.fr", FromAddress: "client@gmail.com", Subject: "S", Text: "t", SentAt: time.Now(), BodySource: BodyCaptured}); err != nil {
		t.Fatal(err)
	}
	if !put(2, "In-Reply-To: <envoi@gmail.com>\r\n") {
		t.Fatal("la reponse a un de nos envois doit etre rangee")
	}
	s := NewSyncer(st, ing, nil, nil, logger.NewLogger())
	s.purgeOutOfScope(ctx, "ws", "g", b.SetScope)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM veridian_mail_message WHERE id = 'deja'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("courrier deja range retire sans reglage : %d %v", n, err)
	}
}
