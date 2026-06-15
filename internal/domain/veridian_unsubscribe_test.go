package domain

import "testing"

// TestVeridianSuppressUnsubscribe vérifie le contrat du gate :
//   - contexte tunnel cold (un signal suffit) → true (on supprime l'unsubscribe)
//   - hors tunnel → false (NON-RÉGRESSION : les broadcasts marketing gardent
//     leur unsubscribe à l'identique)
//
// Le prédicat délègue à VeridianIsColdContext ; on couvre ici les portes
// d'entrée du contexte cold (tag contact, config broadcast, config workspace) et
// la non-régression.
func TestVeridianSuppressUnsubscribe(t *testing.T) {
	// Signal 1 : tag contact custom_string_5 (option B import Prospection).
	coldContact := &Contact{Email: "lead@gmail.com", CustomString5: &NullableString{String: ProviderClassGoogle}}

	// Signal 2 : config cold posée sur le broadcast (rates dans metadata).
	coldBroadcast := &Broadcast{Metadata: MapOfAny{
		VeridianProviderClassRatesMetadataKey: map[string]any{"google": 1.0},
	}}

	// Signal 3 : config cold au niveau workspace (settings).
	coldWorkspace := &Workspace{Settings: WorkspaceSettings{
		VeridianProviderClassRates: map[string]float64{"google": 1.0},
	}}

	cases := []struct {
		name      string
		contact   *Contact
		broadcast *Broadcast
		workspace *Workspace
		want      bool
	}{
		{
			name:      "tunnel via tag contact -> supprime unsubscribe",
			contact:   coldContact,
			broadcast: &Broadcast{},
			workspace: nil,
			want:      true,
		},
		{
			name:      "tunnel via config broadcast -> supprime unsubscribe",
			contact:   &Contact{Email: "lead@gmail.com"},
			broadcast: coldBroadcast,
			workspace: nil,
			want:      true,
		},
		{
			name:      "tunnel via config workspace -> supprime unsubscribe",
			contact:   &Contact{Email: "lead@gmail.com"},
			broadcast: &Broadcast{},
			workspace: coldWorkspace,
			want:      true,
		},
		{
			name:      "hors tunnel (aucun signal) -> garde unsubscribe (non-regression)",
			contact:   &Contact{Email: "lead@gmail.com"},
			broadcast: &Broadcast{},
			workspace: nil,
			want:      false,
		},
		{
			name:      "hors tunnel avec workspace sans config cold -> garde unsubscribe",
			contact:   &Contact{Email: "lead@gmail.com"},
			broadcast: &Broadcast{},
			workspace: &Workspace{Settings: WorkspaceSettings{}},
			want:      false,
		},
		{
			name:      "tout nil -> garde unsubscribe (non-regression, nil-safe)",
			contact:   nil,
			broadcast: nil,
			workspace: nil,
			want:      false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := VeridianSuppressUnsubscribe(c.contact, c.broadcast, c.workspace); got != c.want {
				t.Errorf("VeridianSuppressUnsubscribe = %v, attendu %v", got, c.want)
			}
		})
	}
}
