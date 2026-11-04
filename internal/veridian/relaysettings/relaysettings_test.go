package relaysettings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/crypto"
)

const testSecret = "secret-key-for-tests-0123456789abcdef"

type fakeRepo struct{ values map[string]string }

func (f *fakeRepo) Get(_ context.Context, key string) (*domain.Setting, error) {
	v, ok := f.values[key]
	if !ok {
		return nil, errors.New("absent")
	}
	return &domain.Setting{Key: key, Value: v}, nil
}
func (f *fakeRepo) Set(_ context.Context, key, value string) error {
	f.values[key] = value
	return nil
}
func (f *fakeRepo) Delete(_ context.Context, key string) error { delete(f.values, key); return nil }
func (f *fakeRepo) List(context.Context) ([]*domain.Setting, error) {
	return nil, nil
}
func (f *fakeRepo) SetLastCronRun(context.Context) error { return nil }
func (f *fakeRepo) GetLastCronRun(context.Context) (*time.Time, error) {
	return nil, nil
}

func env(m map[string]string) Lookup {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestNormalizeDirectory(t *testing.T) {
	for in, want := range map[string]string{"": DirectoryProduction, " production ": DirectoryProduction, "test": DirectoryTest} {
		got, err := NormalizeDirectory(in)
		if err != nil || got != want {
			t.Fatalf("NormalizeDirectory(%q) = %q, %v ; attendu %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"https://evil.example/dir", "staging", "PRODUCTION "} {
		if _, err := NormalizeDirectory(bad); err == nil {
			t.Fatalf("NormalizeDirectory(%q) doit être refusé (pas d'URL libre)", bad)
		}
	}
}

func TestDirectoryURL_TestIsStaging(t *testing.T) {
	u, err := DirectoryURL(DirectoryTest)
	if err != nil || u != "https://acme-staging-v02.api.letsencrypt.org/directory" {
		t.Fatalf("annuaire de test attendu : %q %v", u, err)
	}
	u, _ = DirectoryURL(DirectoryProduction)
	if u != "https://acme-v02.api.letsencrypt.org/directory" {
		t.Fatalf("annuaire de production attendu : %q", u)
	}
}

// L'env prime sur le réglage pour l'interrupteur ; une variable vide ne compte pas.
func TestReadEnabled_EnvOverridesSetting(t *testing.T) {
	repo := &fakeRepo{values: map[string]string{KeyRelayEnabled: "false"}}
	if !ReadEnabled(context.Background(), repo, env(map[string]string{EnvRelayEnabled: "true"})) {
		t.Fatal("variable à true : relais actif attendu, même si le réglage est à false")
	}
	repo.values[KeyRelayEnabled] = "true"
	if ReadEnabled(context.Background(), repo, env(map[string]string{EnvRelayEnabled: "false"})) {
		t.Fatal("variable à false : relais coupé attendu, même si le réglage est à true")
	}
	if !ReadEnabled(context.Background(), repo, env(map[string]string{EnvRelayEnabled: ""})) {
		t.Fatal("variable vide : le réglage s'applique")
	}
}

// Le réglage est lu à chaque appel : une bascule prend effet sans redémarrage.
func TestReadEnabled_ReadsSettingEachCall(t *testing.T) {
	repo := &fakeRepo{values: map[string]string{}}
	if ReadEnabled(context.Background(), repo, env(nil)) {
		t.Fatal("défaut : relais désactivé")
	}
	repo.values[KeyRelayEnabled] = "true"
	if !ReadEnabled(context.Background(), repo, env(nil)) {
		t.Fatal("réglage à true : relais actif sans redémarrage attendu")
	}
}

func TestReadCert_SettingsWinOverEnvFallback(t *testing.T) {
	repo := &fakeRepo{values: map[string]string{
		KeyACMEEmail:     "ops@example.test",
		KeyACMEDirectory: DirectoryTest,
	}}
	lookup := env(map[string]string{EnvACMEEmail: "autre@example.test", EnvACMEDirectory: "production"})
	c, err := ReadCert(context.Background(), repo, testSecret, lookup)
	if err != nil {
		t.Fatalf("ReadCert: %v", err)
	}
	if c.Email != "ops@example.test" || c.Directory != DirectoryTest {
		t.Fatalf("le réglage doit primer sur l'environnement : %+v", c)
	}
}

// Repli sur l'environnement quand le réglage est vide (amorçage).
func TestReadCert_EnvFallbackWhenSettingEmpty(t *testing.T) {
	repo := &fakeRepo{values: map[string]string{KeyACMEEmail: ""}}
	lookup := env(map[string]string{EnvACMEEmail: "ops@example.test", EnvCFToken: "jeton-env"})
	c, err := ReadCert(context.Background(), repo, testSecret, lookup)
	if err != nil {
		t.Fatalf("ReadCert: %v", err)
	}
	if c.Email != "ops@example.test" || c.CFToken != "jeton-env" || c.Directory != DirectoryProduction {
		t.Fatalf("repli sur l'environnement attendu : %+v", c)
	}
}

// Le jeton stocké est chiffré par SECRET_KEY et déchiffré à la lecture ; le réglage prime sur l'environnement.
func TestReadCert_TokenDecryptedFromSettings(t *testing.T) {
	enc, err := crypto.EncryptString("jeton-de-test-chiffre", testSecret)
	if err != nil {
		t.Fatal(err)
	}
	repo := &fakeRepo{values: map[string]string{KeyCFToken: enc, KeyCertManaged: "true"}}
	c, err := ReadCert(context.Background(), repo, testSecret, env(map[string]string{EnvCFToken: "ne-doit-pas-servir"}))
	if err != nil {
		t.Fatalf("ReadCert: %v", err)
	}
	if c.CFToken != "jeton-de-test-chiffre" || !c.Managed {
		t.Fatalf("jeton du réglage attendu (et géré) : %+v", c)
	}
	if enc == "jeton-de-test-chiffre" {
		t.Fatal("le stockage doit être chiffré")
	}
}

// Mauvaise SECRET_KEY : erreur sans valeur (ni jeton ni chiffré dans le message).
func TestReadCert_WrongSecretHidesValue(t *testing.T) {
	enc, _ := crypto.EncryptString("jeton-de-test-chiffre", testSecret)
	repo := &fakeRepo{values: map[string]string{KeyCFToken: enc}}
	_, err := ReadCert(context.Background(), repo, "autre-cle-0123456789abcdef0123456789", env(nil))
	if err == nil {
		t.Fatal("mauvaise SECRET_KEY : erreur attendue")
	}
	if got := err.Error(); strings.Contains(got, "jeton-de-test-chiffre") || strings.Contains(got, enc) {
		t.Fatalf("le message d'erreur ne doit contenir ni le jeton ni le chiffré : %q", got)
	}
}

func TestReadCert_ManagedEnvOverride(t *testing.T) {
	repo := &fakeRepo{values: map[string]string{KeyCertManaged: "false"}}
	c, _ := ReadCert(context.Background(), repo, testSecret, env(map[string]string{EnvCertManaged: "true"}))
	if !c.Managed {
		t.Fatal("VERIDIAN_RELAY_CERT_MANAGED=true doit primer sur le réglage à false")
	}
}

func TestReadCert_RefusesUnknownDirectory(t *testing.T) {
	repo := &fakeRepo{values: map[string]string{KeyACMEDirectory: "https://evil.example/dir"}}
	if _, err := ReadCert(context.Background(), repo, testSecret, env(nil)); err == nil {
		t.Fatal("annuaire hors liste : erreur attendue")
	}
}
