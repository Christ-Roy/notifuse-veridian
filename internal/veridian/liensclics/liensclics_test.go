package liensclics

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)

func TestEnregistrer_CompteLesClicsEtLesBornes(t *testing.T) {
	var c Carte
	c = Enregistrer(c, "https://a.test/offre", t0.Add(time.Hour))
	c = Enregistrer(c, "https://a.test/offre", t0)
	c = Enregistrer(c, "https://a.test/offre", t0.Add(2*time.Hour))
	got := c["https://a.test/offre"]
	if got.Count != 3 {
		t.Fatalf("count = %d, veut 3", got.Count)
	}
	if !got.FirstAt.Equal(t0) || !got.LastAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("bornes = %v..%v, veut %v..%v", got.FirstAt, got.LastAt, t0, t0.Add(2*time.Hour))
	}
}

func TestEnregistrer_UrlVideIgnoree(t *testing.T) {
	c := Enregistrer(nil, "   ", t0)
	if len(c) != 0 {
		t.Fatalf("une URL vide ne doit rien compter, carte = %v", c)
	}
}

func TestEnregistrer_UtmDistinguentDeuxLiens(t *testing.T) {
	var c Carte
	c = Enregistrer(c, "https://a.test/?utm_content=haut", t0)
	c = Enregistrer(c, "https://a.test/?utm_content=bas", t0)
	if len(c) != 2 {
		t.Fatalf("deux UTM différents = deux liens, carte = %v", c)
	}
}

func TestDecoder_ValeursVidesEtInvalides(t *testing.T) {
	for _, brut := range []string{"", "null"} {
		c, err := Decoder([]byte(brut))
		if err != nil || c == nil || len(c) != 0 {
			t.Fatalf("Decoder(%q) = %v, %v ; veut une carte vide sans erreur", brut, c, err)
		}
	}
	if _, err := Decoder([]byte("{pas du json")); err == nil {
		t.Fatal("un JSON cassé doit remonter une erreur")
	}
}

func TestEncoderDecoder_AllerRetour(t *testing.T) {
	var c Carte
	c = Enregistrer(c, "https://a.test/x", t0)
	c = Enregistrer(c, "https://a.test/x", t0.Add(time.Minute))
	brut, err := Encoder(c)
	if err != nil {
		t.Fatal(err)
	}
	relue, err := Decoder(brut)
	if err != nil {
		t.Fatal(err)
	}
	if relue["https://a.test/x"].Count != 2 {
		t.Fatalf("aller-retour perdu : %v", relue)
	}
}

func TestRapporter_ClicsTotauxEtCliqueursUniques(t *testing.T) {
	a := Carte{"https://a.test/x": {Count: 3}, "https://a.test/y": {Count: 1}}
	b := Carte{"https://a.test/x": {Count: 1}}
	lignes := Rapporter([]Carte{a, b, {}})
	if len(lignes) != 2 {
		t.Fatalf("lignes = %v, veut 2", lignes)
	}
	if lignes[0] != (Ligne{URL: "https://a.test/x", Clics: 4, Cliqueurs: 2}) {
		t.Fatalf("première ligne = %+v, veut x : 4 clics, 2 cliqueurs", lignes[0])
	}
	if lignes[1] != (Ligne{URL: "https://a.test/y", Clics: 1, Cliqueurs: 1}) {
		t.Fatalf("seconde ligne = %+v", lignes[1])
	}
}

func TestRapporter_IgnoreLesCompteursNulsEtTrie(t *testing.T) {
	cartes := []Carte{
		{"https://z.test": {Count: 0}},
		{"https://b.test": {Count: 2}, "https://a.test": {Count: 2}},
	}
	lignes := Rapporter(cartes)
	if len(lignes) != 2 || lignes[0].URL != "https://a.test" || lignes[1].URL != "https://b.test" {
		t.Fatalf("ordre ou filtre faux : %+v", lignes)
	}
}
