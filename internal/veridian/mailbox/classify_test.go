package mailbox

import "testing"

func parse(t *testing.T, raw []byte) *Parsed {
	t.Helper()
	p, err := ParseRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		raw    []byte
		linked bool
		want   Kind
	}{
		{"réponse humaine", replyHuman("<a@x.fr>", "<a@x.fr>"), true, KindReplyHuman},
		{"réponse sans lien", replyHuman("<a@x.fr>", "<a@x.fr>"), false, KindOther},
		{"auto-répondeur", autoReply("<a@x.fr>"), true, KindReplyAuto},
		{"rebond dur", bounce550("a@x.fr"), false, KindBounceHard},
		{"rebond souple", bounceSoft("a@x.fr"), false, KindBounceSoft},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := parse(t, c.raw)
			v := Classify(p, c.raw, c.linked)
			if v.Kind != c.want {
				t.Fatalf("nature %q, attendu %q", v.Kind, c.want)
			}
		})
	}
}

func TestClassify_BounceDecoded(t *testing.T) {
	raw := bounce550("orig123@animalservicedistribution.fr")
	v := Classify(parse(t, raw), raw, false)
	if v.DSN == nil || v.DSN.Code != "5.1.1" || v.Recipient != "mort@gmail.com" {
		t.Fatalf("%+v %+v", v, v.DSN)
	}
	if v.DSN.Category != CatAdresseInexistante || v.DSN.Provider != "Gmail" || v.DSN.Action != "failed" {
		t.Fatalf("%+v", v.DSN)
	}
	if v.DSN.OriginalMessageID == "" {
		t.Fatalf("message d'origine non cité: %+v", v.DSN)
	}
	soft := bounceSoft("o@x.fr")
	vs := Classify(parse(t, soft), soft, false)
	if vs.DSN == nil || vs.DSN.Category != CatBoitePleine || vs.Recipient != "plein@orange.fr" {
		t.Fatalf("%+v", vs.DSN)
	}
}

func TestThreadIDs(t *testing.T) {
	root := "<j0uuid@animalservicedistribution.fr>"
	j0 := DeterministicThreadID(root, "", nil, "marie@x.fr", "Présentation ASD")
	relance := DeterministicThreadID("<r1@animalservicedistribution.fr>", root, []string{root}, "marie@x.fr", "Re: Présentation ASD")
	reply := DeterministicThreadID("<rep@x.fr>", "<r1@animalservicedistribution.fr>", []string{root, "<r1@animalservicedistribution.fr>"}, "marie@x.fr", "Re: Re: Présentation ASD")
	if j0 != relance || relance != reply {
		t.Fatalf("la chaîne J0, relance, réponse doit partager un fil: %s %s %s", j0, relance, reply)
	}
	other := DeterministicThreadID("<autre@x.fr>", "", nil, "marie@x.fr", "Présentation ASD")
	if other == j0 {
		t.Fatal("deux envois sans lien ne partagent pas de fil")
	}
	if ThreadIDFromSubject("Marie@X.fr", "RE: TR: Présentation  ASD") != ThreadIDFromSubject("marie@x.fr", "présentation asd") {
		t.Fatal("l'objet normalisé ignore préfixes, casse et espaces")
	}
	if NormalizeSubject("Fwd: Re : Hello") != "hello" {
		t.Fatalf("%q", NormalizeSubject("Fwd: Re : Hello"))
	}
}

func TestLocalPartAndCandidates(t *testing.T) {
	if LocalPart("<abc-123@host.fr>") != "abc-123" || LocalPart("abc") != "abc" {
		t.Fatal("LocalPart")
	}
	got := CandidateIDs("<c@x>", []string{"<a@x> <b@x>", "<c@x>"})
	if len(got) != 3 || got[0] != "<c@x>" || got[1] != "<b@x>" {
		t.Fatalf("%v", got)
	}
}
