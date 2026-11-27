package mailbox

import (
	"context"
	"testing"
	"time"
)

// L'issue d'un mail écrit à la main : rangé dans la boîte = parti ; failed_at consigné = échec, avec sa raison ;
// rien encore = en cours. Un envoi qui échoue puis réussit à la tentative suivante se lit « parti ».
func TestStore_SendOutcome(t *testing.T) {
	s, db := testStore(t)
	ctx := context.Background()
	ing := testIngestor(s)

	if got, err := s.SendOutcome(ctx, "ws", "ws_inconnu"); err != nil || got.State != SendStatePending {
		t.Fatalf("aucune trace : en cours d'envoi attendu, %+v %v", got, err)
	}

	if _, err := db.Exec(`ALTER TABLE message_history ADD COLUMN IF NOT EXISTS status_info TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO message_history (id, contact_email, failed_at, status_info) VALUES ('ws_echec','p@client.fr', NOW(), ' 550 5.1.1 boîte inconnue ')`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SendOutcome(ctx, "ws", "ws_echec"); err != nil || got.State != SendStateFailed || got.Reason != "550 5.1.1 boîte inconnue" {
		t.Fatalf("échec consigné attendu avec sa raison, %+v %v", got, err)
	}

	if _, err := db.Exec(`INSERT INTO message_history (id, contact_email) VALUES ('ws_attente','p@client.fr')`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SendOutcome(ctx, "ws", "ws_attente"); err != nil || got.State != SendStatePending {
		t.Fatalf("ligne d'historique sans issue : en cours, %+v %v", got, err)
	}

	// Parti après un premier échec : la boîte fait foi.
	if _, err := ing.RecordSent(ctx, SentInput{WorkspaceID: "ws", MessageHistoryID: "ws_echec", ContactEmail: "p@client.fr",
		FromAddress: "a@boite.fr", Subject: "S", Text: "t", SentAt: time.Now(), BodySource: BodyCaptured}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SendOutcome(ctx, "ws", "ws_echec"); err != nil || got.State != SendStateSent {
		t.Fatalf("rangé dans la boîte : parti, %+v %v", got, err)
	}
}
