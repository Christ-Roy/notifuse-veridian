package mailbox

import (
	"context"
	"strings"
	"testing"
	"time"
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
