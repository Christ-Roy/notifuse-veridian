package boxaccess

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAttempts_AddOfSince(t *testing.T) {
	a := NewAttempts()
	if time.Since(a.Since()) > time.Minute {
		t.Fatal("date de départ")
	}
	a.Add("imap", " JC@Boite.fr", false, "bad_credentials")
	time.Sleep(2 * time.Millisecond)
	a.Add("smtp", "jc@boite.fr", true, "")
	a.Add("imap", "autre@boite.fr", true, "")
	got := a.Of([]string{"jc@boite.fr", "JC@boite.fr"}, 10)
	if len(got) != 2 || got[0].Via != "smtp" || !got[0].OK || got[1].Reason != "bad_credentials" {
		t.Fatalf("%+v", got)
	}
	if n := len(a.Of([]string{"jc@boite.fr", "autre@boite.fr"}, 2)); n != 2 {
		t.Fatal(n)
	}
	if l := a.Of([]string{"inconnue@boite.fr"}, 10); l == nil || len(l) != 0 {
		t.Fatal("une liste vide, jamais nil : le JSON doit rendre []")
	}
	a.Add("imap", "", true, "")
	a.Add("imap", strings.Repeat("a", 300), true, "")
	if len(a.boxes) != 2 {
		t.Fatal("adresse vide ou démesurée notée")
	}
}

func TestAttempts_Bounded(t *testing.T) {
	a := NewAttempts()
	for i := 0; i < attemptsPerBox+5; i++ {
		a.Add("imap", "jc@boite.fr", i%2 == 0, "")
	}
	if n := len(a.Of([]string{"jc@boite.fr"}, 100)); n != attemptsPerBox {
		t.Fatal(n)
	}
	for i := 0; i < attemptsBoxes+10; i++ {
		a.Add("imap", fmt.Sprintf("x%d@essai.fr", i), false, "bad_credentials")
	}
	if len(a.boxes) != attemptsBoxes {
		t.Fatalf("%d adresses gardées", len(a.boxes))
	}
	if n := len(a.Of([]string{fmt.Sprintf("x%d@essai.fr", attemptsBoxes+9)}, 10)); n != 1 {
		t.Fatal("la plus récente reste")
	}
}
