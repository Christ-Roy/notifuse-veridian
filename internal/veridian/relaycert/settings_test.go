package relaycert

import (
	"context"
	"testing"

	"github.com/Notifuse/notifuse/internal/veridian/relaysettings"
	"github.com/Notifuse/notifuse/pkg/crypto"
)

const settingsTestSecret = "secret-key-for-tests-0123456789abcdef"

// SettingsParams relit les réglages à chaque appel : une bascule (géré, annuaire) prend effet sans redémarrage.
func TestSettingsParams_ReadsSettingsEachCall(t *testing.T) {
	enc, err := crypto.EncryptString("jeton-de-test", settingsTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeSettings{values: map[string]string{
		relaysettings.KeyCertManaged:   "true",
		relaysettings.KeyACMEEmail:     "ops@example.test",
		relaysettings.KeyACMEDirectory: relaysettings.DirectoryTest,
		relaysettings.KeyCFToken:       enc,
	}}
	params := SettingsParams(repo, settingsTestSecret, nil)

	got, err := params(context.Background())
	if err != nil {
		t.Fatalf("SettingsParams : %v", err)
	}
	if !got.Managed || got.Email != "ops@example.test" || got.Directory != relaysettings.DirectoryTest ||
		got.CFToken != "jeton-de-test" || got.DirectoryURL == "" {
		t.Fatalf("configuration lue inattendue : %+v", got)
	}

	repo.values[relaysettings.KeyCertManaged] = "false"
	if got, _ = params(context.Background()); got.Managed {
		t.Fatal("réglage passé à false : la décision suivante doit le voir sans redémarrage")
	}
}

// Un annuaire hors liste dans les réglages est refusé avant toute décision.
func TestSettingsParams_RefusesUnknownDirectory(t *testing.T) {
	repo := &fakeSettings{values: map[string]string{relaysettings.KeyACMEDirectory: "https://evil.example/dir"}}
	if _, err := SettingsParams(repo, settingsTestSecret, nil)(context.Background()); err == nil {
		t.Fatal("annuaire hors liste : erreur attendue")
	}
}

// LegoIssuerFactory : l'émetteur reçoit l'annuaire et le jeton de la configuration courante (pas de l'environnement).
func TestLegoIssuerFactory_UsesCurrentParams(t *testing.T) {
	issuer := LegoIssuerFactory(nil)(Params{DirectoryURL: "https://acme.example.test/dir", CFToken: "jeton-courant"})
	lego, ok := issuer.(LegoIssuer)
	if !ok {
		t.Fatalf("émetteur lego attendu, got %T", issuer)
	}
	if lego.DirectoryURL != "https://acme.example.test/dir" || lego.CFToken != "jeton-courant" {
		t.Fatalf("paramètres de l'émetteur inattendus : %+v", lego)
	}
}
