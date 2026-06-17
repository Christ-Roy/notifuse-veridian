package domain

import "testing"

func strPtr(s string) *NullableString { return &NullableString{String: s, IsNull: false} }

func contactWithClass(email, class string) *Contact {
	c := &Contact{Email: email}
	if class != "" {
		c.CustomString5 = strPtr(class)
	}
	return c
}

func TestVeridianResolveOpenPixel_NonRegressionHorsTunnel(t *testing.T) {
	// Aucun signal tunnel (pas de tag, pas de config) → nil = comportement
	// upstream (le pixel suit EnableTracking). NON-RÉGRESSION stricte.
	got := VeridianResolveOpenPixel(&Contact{Email: "jean@gmail.com"}, "jean@gmail.com", &Broadcast{}, nil, nil)
	if got != nil {
		t.Fatalf("hors tunnel : attendu nil (comportement upstream), got %v", *got)
	}
	// Broadcast nil, contact nil aussi
	if VeridianResolveOpenPixel(nil, "x@example.com", nil, nil, nil) != nil {
		t.Fatal("contact+broadcast nil : attendu nil")
	}
}

func TestVeridianResolveOpenPixel_BroadcastNilAvecTag(t *testing.T) {
	// broadcast nil ET workspace nil, mais tag contact présent → tunnel actif
	// via le tag, aucun nil-deref, résolution au défaut tunnel de la classe.
	got := VeridianResolveOpenPixel(contactWithClass("a@gmail.com", "google"), "a@gmail.com", nil, nil, nil)
	if got == nil || *got != false {
		t.Fatalf("broadcast+workspace nil, tag google → pixel OFF attendu, got %v", got)
	}
	got = VeridianResolveOpenPixel(contactWithClass("b@orange.fr", "freemail_fr"), "b@orange.fr", nil, nil, nil)
	if got == nil || *got != true {
		t.Fatalf("broadcast+workspace nil, tag freemail_fr → pixel ON attendu, got %v", got)
	}
}

func TestVeridianResolveOpenPixel_ClasseInconnueDefautFalse(t *testing.T) {
	// Tag canonique absent de la map de défaut serait impossible (les 5 classes
	// y sont) ; on vérifie la robustesse si un override broadcast cible une
	// classe que le destinataire n'a pas : on retombe sur le défaut de SA classe.
	b := &Broadcast{Metadata: MapOfAny{
		VeridianOpenPixelByClassMetadataKey: map[string]any{"microsoft": true},
	}}
	// Destinataire gmail : l'override microsoft ne le concerne pas → défaut
	// google = false.
	got := VeridianResolveOpenPixel(&Contact{Email: "x@gmail.com"}, "x@gmail.com", b, nil, nil)
	if got == nil || *got != false {
		t.Fatalf("override d'une AUTRE classe ne doit pas affecter google (défaut OFF), got %v", got)
	}
}

func TestVeridianResolveOpenPixel_DefautTunnelParClasse(t *testing.T) {
	// Tunnel actif via config rates broadcast (signal). Politique par défaut :
	// OFF google/microsoft, ON freemail_fr/yahoo_aol/corporate.
	tunnelBroadcast := &Broadcast{Metadata: MapOfAny{
		VeridianProviderClassRatesMetadataKey: map[string]any{"google": 1.0},
	}}
	cases := []struct {
		email string
		want  bool
	}{
		{"jean@gmail.com", false},          // google → OFF
		{"jean@outlook.fr", false},         // microsoft → OFF
		{"jean@yahoo.fr", true},            // yahoo_aol → ON
		{"jean@orange.fr", true},           // freemail_fr → ON
		{"jean@boucherie-durand.fr", true}, // corporate → ON
	}
	for _, c := range cases {
		got := VeridianResolveOpenPixel(&Contact{Email: c.email}, c.email, tunnelBroadcast, nil, nil)
		if got == nil {
			t.Fatalf("%s : tunnel actif attendu non-nil", c.email)
		}
		if *got != c.want {
			t.Errorf("%s : pixel attendu %v, got %v", c.email, c.want, *got)
		}
	}
}

func TestVeridianResolveOpenPixel_PixelOFFgoogle_ONfreemail(t *testing.T) {
	// Le cas central du ticket : pixel OFF sur google, ON sur freemail_fr,
	// signalé par le tag custom_string_5 du contact (option B).
	off := VeridianResolveOpenPixel(contactWithClass("a@gmail.com", "google"), "a@gmail.com", &Broadcast{}, nil, nil)
	if off == nil || *off != false {
		t.Fatalf("google taggué : pixel attendu OFF, got %v", off)
	}
	on := VeridianResolveOpenPixel(contactWithClass("b@orange.fr", "freemail_fr"), "b@orange.fr", &Broadcast{}, nil, nil)
	if on == nil || *on != true {
		t.Fatalf("freemail_fr taggué : pixel attendu ON, got %v", on)
	}
}

func TestVeridianResolveOpenPixel_TagPrimeSurClassificationEmail(t *testing.T) {
	// Email gmail.com mais taggué corporate → la classe du tag prime → ON.
	got := VeridianResolveOpenPixel(contactWithClass("vip@gmail.com", "corporate"), "vip@gmail.com", &Broadcast{}, nil, nil)
	if got == nil || *got != true {
		t.Fatalf("tag corporate doit primer (ON), got %v", got)
	}
}

func TestVeridianResolveOpenPixel_OverrideBroadcastPrimeSurDefaut(t *testing.T) {
	// Override broadcast : forcer le pixel ON sur google (révision data-driven).
	b := &Broadcast{Metadata: MapOfAny{
		VeridianOpenPixelByClassMetadataKey: map[string]any{"google": true},
	}}
	got := VeridianResolveOpenPixel(&Contact{Email: "x@gmail.com"}, "x@gmail.com", b, nil, nil)
	if got == nil || *got != true {
		t.Fatalf("override broadcast google=true attendu ON, got %v", got)
	}
}

func TestVeridianResolveOpenPixel_OverrideWorkspaceFallback(t *testing.T) {
	// Pas d'override broadcast mais override workspace settings : forcer OFF sur
	// freemail_fr. Le contexte tunnel est signalé par l'override workspace.
	ws := &Workspace{Settings: WorkspaceSettings{
		VeridianOpenPixelByClass: map[string]bool{"freemail_fr": false},
	}}
	got := VeridianResolveOpenPixel(&Contact{Email: "x@orange.fr"}, "x@orange.fr", &Broadcast{}, nil, ws)
	if got == nil || *got != false {
		t.Fatalf("override workspace freemail_fr=false attendu OFF, got %v", got)
	}
	// Broadcast override prime sur workspace
	b := &Broadcast{Metadata: MapOfAny{
		VeridianOpenPixelByClassMetadataKey: map[string]any{"freemail_fr": true},
	}}
	got = VeridianResolveOpenPixel(&Contact{Email: "x@orange.fr"}, "x@orange.fr", b, nil, ws)
	if got == nil || *got != true {
		t.Fatalf("broadcast doit primer sur workspace (ON), got %v", got)
	}
}

// TestVeridianResolveOpenPixel_InfraCascade verrouille le NIVEAU INFRA
// (EmailProvider) inséré au MILIEU de la cascade pixel (broadcast > INFRA >
// workspace > défaut), exactement comme les rates/caps R2. Couvre les 4 niveaux.
func TestVeridianResolveOpenPixel_InfraCascade(t *testing.T) {
	wsOnFreemail := &Workspace{Settings: WorkspaceSettings{
		VeridianOpenPixelByClass: map[string]bool{"freemail_fr": true},
	}}
	infraOffFreemail := &EmailProvider{
		VeridianOpenPixelByClass: map[string]bool{"freemail_fr": false},
	}
	bcastOnFreemail := &Broadcast{Metadata: MapOfAny{
		VeridianOpenPixelByClassMetadataKey: map[string]any{"freemail_fr": true},
	}}

	// 1. INFRA prime sur WORKSPACE : workspace dit ON, infra dit OFF → OFF.
	got := VeridianResolveOpenPixel(&Contact{Email: "x@orange.fr"}, "x@orange.fr", &Broadcast{}, infraOffFreemail, wsOnFreemail)
	if got == nil || *got != false {
		t.Fatalf("infra OFF doit primer sur workspace ON → OFF, got %v", got)
	}

	// 2. BROADCAST prime sur INFRA : infra dit OFF, broadcast dit ON → ON.
	got = VeridianResolveOpenPixel(&Contact{Email: "x@orange.fr"}, "x@orange.fr", bcastOnFreemail, infraOffFreemail, wsOnFreemail)
	if got == nil || *got != true {
		t.Fatalf("broadcast ON doit primer sur infra OFF → ON, got %v", got)
	}

	// 3. INFRA seule (pas de broadcast, pas de workspace) suffit à activer le
	//    tunnel ET à fixer la classe ; les classes non couvertes par l'infra
	//    tombent sur le défaut tunnel.
	got = VeridianResolveOpenPixel(&Contact{Email: "x@orange.fr"}, "x@orange.fr", &Broadcast{}, infraOffFreemail, nil)
	if got == nil || *got != false {
		t.Fatalf("infra seule freemail_fr=false attendu OFF, got %v", got)
	}
	// gmail non couvert par l'infra → défaut tunnel google = OFF.
	got = VeridianResolveOpenPixel(&Contact{Email: "x@gmail.com"}, "x@gmail.com", &Broadcast{}, infraOffFreemail, nil)
	if got == nil || *got != false {
		t.Fatalf("classe non couverte par l'infra → défaut tunnel (google OFF), got %v", got)
	}

	// 4. WORKSPACE reprend la main quand l'INFRA ne définit PAS la classe
	//    demandée (cascade par classe, pas tout-ou-rien). Infra ne couvre que
	//    google ; le destinataire freemail_fr tombe sur le workspace ON.
	infraGoogleOnly := &EmailProvider{VeridianOpenPixelByClass: map[string]bool{"google": true}}
	got = VeridianResolveOpenPixel(&Contact{Email: "x@orange.fr"}, "x@orange.fr", &Broadcast{}, infraGoogleOnly, wsOnFreemail)
	if got == nil || *got != true {
		t.Fatalf("infra ne couvre pas freemail_fr → workspace ON doit s'appliquer, got %v", got)
	}
}

// TestVeridianResolveOpenPixel_ProviderNilNonRegression : provider nil = niveau
// infra sauté = comportement pré-infra strictement inchangé (cascade
// broadcast > workspace > défaut d'origine).
func TestVeridianResolveOpenPixel_ProviderNilNonRegression(t *testing.T) {
	ws := &Workspace{Settings: WorkspaceSettings{
		VeridianOpenPixelByClass: map[string]bool{"freemail_fr": false},
	}}
	// provider nil + workspace OFF freemail_fr → OFF (comme avant l'ajout infra).
	got := VeridianResolveOpenPixel(&Contact{Email: "x@orange.fr"}, "x@orange.fr", &Broadcast{}, nil, ws)
	if got == nil || *got != false {
		t.Fatalf("provider nil + workspace OFF attendu OFF (non-régression), got %v", got)
	}
	// provider nil + aucun signal → nil (hors tunnel).
	if VeridianResolveOpenPixel(&Contact{Email: "x@gmail.com"}, "x@gmail.com", &Broadcast{}, nil, nil) != nil {
		t.Fatal("provider nil hors tunnel attendu nil (non-régression)")
	}
	// provider vide (config infra nil) ne crée PAS de contexte tunnel à lui seul.
	emptyInfra := &EmailProvider{}
	if VeridianResolveOpenPixel(&Contact{Email: "x@gmail.com"}, "x@gmail.com", &Broadcast{}, emptyInfra, nil) != nil {
		t.Fatal("infra sans config pixel hors tunnel attendu nil (non-régression)")
	}
}

func TestVeridianOpenPixelByClassFromMetadata(t *testing.T) {
	// nil / absent
	if VeridianOpenPixelByClassFromMetadata(nil) != nil {
		t.Fatal("metadata nil → nil")
	}
	if VeridianOpenPixelByClassFromMetadata(MapOfAny{"autre": 1}) != nil {
		t.Fatal("clé absente → nil")
	}
	// valide + classe non-canonique ignorée
	m := VeridianOpenPixelByClassFromMetadata(MapOfAny{
		VeridianOpenPixelByClassMetadataKey: map[string]any{
			"google": false, "freemail_fr": true, "INVALIDE": true,
		},
	})
	if len(m) != 2 || m["google"] != false || m["freemail_fr"] != true {
		t.Fatalf("parsing pixel map incorrect : %v", m)
	}
}

func TestVeridianToBool(t *testing.T) {
	cases := []struct {
		in    any
		val   bool
		valid bool
	}{
		{true, true, true},
		{false, false, true},
		{float64(1), true, true},
		{float64(0), false, true},
		{1, true, true},
		{"oui", false, false},
		{nil, false, false},
	}
	for _, c := range cases {
		v, ok := veridianToBool(c.in)
		if ok != c.valid || (ok && v != c.val) {
			t.Errorf("veridianToBool(%v) = (%v,%v), attendu (%v,%v)", c.in, v, ok, c.val, c.valid)
		}
	}
}

// TestVeridianDefaultOpenPixel_MXClasses verrouille la politique pixel par
// défaut pour les classes MX (Lot 4) : OFF sur les sensibles
// (security_gateway/apple_icloud) qui scrutent les pixels, ON sur les nébuleuses
// FR et hébergeurs propres (ovh/ionos/other_hoster/corporate_selfhost). Couvre
// la modif du défaut tunnel après l'ajout des classes MX.
func TestVeridianDefaultOpenPixel_MXClasses(t *testing.T) {
	// Contexte tunnel forcé via une config rates non vide (un signal suffit),
	// sans override pixel explicite → on tombe sur le défaut tunnel par classe.
	tunnelBroadcast := &Broadcast{
		ID:       "b",
		Metadata: MapOfAny{VeridianProviderClassRatesMetadataKey: map[string]any{"google": 1.0}},
	}

	cases := []struct {
		email string
		want  bool
	}{
		{"x@gmail.com", false}, // google : OFF (historique, non-régression)
		{"x@orange.fr", true},  // freemail_fr : ON (historique)
	}
	for _, c := range cases {
		got := VeridianResolveOpenPixel(nil, c.email, tunnelBroadcast, nil, nil)
		if got == nil {
			t.Fatalf("%s : contexte tunnel actif → pixel non nil attendu", c.email)
		}
		if *got != c.want {
			t.Errorf("%s : pixel = %v, attendu %v", c.email, *got, c.want)
		}
	}

	// Les classes MX sont posées via le tag contact (custom_string_5) pour
	// éviter tout lookup DNS dans ce test pur domaine.
	mxCases := []struct {
		class string
		want  bool
	}{
		{ProviderClassOVH, true},
		{ProviderClassIonos, true},
		{ProviderClassOtherHoster, true},
		{ProviderClassCorporateSelfhost, true},
		{ProviderClassSecurityGateway, false},
		{ProviderClassAppleICloud, false},
	}
	for _, c := range mxCases {
		contact := &Contact{Email: "x@custom.fr", CustomString5: &NullableString{String: c.class}}
		got := VeridianResolveOpenPixel(contact, "x@custom.fr", tunnelBroadcast, nil, nil)
		if got == nil {
			t.Fatalf("classe %s en contexte tunnel → pixel non nil attendu", c.class)
		}
		if *got != c.want {
			t.Errorf("classe %s : pixel = %v, attendu %v", c.class, *got, c.want)
		}
	}
}
