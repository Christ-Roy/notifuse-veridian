package mailbox

import (
	"context"
	"testing"
	"time"
)

type recordingBounceSink struct{ got []Bounce }

func (r *recordingBounceSink) BounceReceived(_ context.Context, _ string, b Bounce) error {
	r.got = append(r.got, b)
	return nil
}

// VER-220 : chaque rapport de non-remise rangé par la boîte unifiée est remis UNE fois à la
// chaîne des rebonds, avec l'envoi qu'il cite ; rien d'autre ne l'est.
func TestIngest_BouncesFeedTheBounceChainOnce(t *testing.T) {
	s, db := testStore(t)
	g := testIngestor(s)
	sink := &recordingBounceSink{}
	g.SetBounceSink(sink)
	ctx := context.Background()
	addSent(t, db, j0uuid, "mort@gmail.com")

	if _, _, err := g.IngestRaw(ctx, "ws1", box1, ref(2), bounce550(j0uuid+"@animalservicedistribution.fr"), time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if len(sink.got) != 1 {
		t.Fatalf("le rebond dur doit être remis à la chaîne une fois: %+v", sink.got)
	}
	b := sink.got[0]
	if b.SentMessageID != "ws1_"+j0uuid || b.Recipient != "mort@gmail.com" || b.Code != "5.1.1" || b.ProfileID != "prof1" {
		t.Fatalf("rebond remis sans l'envoi, l'adresse, le code ou le profil: %+v", b)
	}

	// relu (même UID) : déjà rangé, pas réappliqué
	if _, _, err := g.IngestRaw(ctx, "ws1", box1, ref(2), bounce550(j0uuid+"@animalservicedistribution.fr"), time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if len(sink.got) != 1 {
		t.Fatalf("un rapport relu ne s'applique pas deux fois: %d", len(sink.got))
	}

	// rebond temporaire : remis aussi (la chaîne le compte vers le seuil)
	if _, _, err := g.IngestRaw(ctx, "ws1", box1, ref(3), bounceSoft("inconnu123@x.fr"), time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if len(sink.got) != 2 || sink.got[1].Severity != "soft" {
		t.Fatalf("le rebond temporaire doit être remis: %+v", sink.got)
	}

	// réponse humaine : jamais remise
	if _, _, err := g.IngestRaw(ctx, "ws1", box1, ref(4), replyHuman("<"+j0uuid+"@animalservicedistribution.fr>", "<"+j0uuid+"@animalservicedistribution.fr>"), time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if len(sink.got) != 2 {
		t.Fatalf("une réponse n'est pas un rebond: %+v", sink.got)
	}

	// boîte « replies », rebond du courrier d'un salarié (envoi inconnu) : ni rangé ni appliqué
	boite := box1
	boite.Scope = "replies"
	if _, _, err := g.IngestRaw(ctx, "ws1", boite, ref(5), bounce550("inconnu999@x.fr"), time.Now(), false); err != nil {
		t.Fatal(err)
	}
	if len(sink.got) != 2 {
		t.Fatalf("un rebond qui n'est pas de nos envois ne s'applique pas: %+v", sink.got)
	}
}
