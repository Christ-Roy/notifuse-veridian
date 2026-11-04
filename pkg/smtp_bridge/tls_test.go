package smtp_bridge

import (
	"crypto/tls"
	"encoding/base64"
	"testing"

	"github.com/Notifuse/notifuse/pkg/logger"
)

func TestSetupTLS_NoConfig(t *testing.T) {
	log := logger.NewLogger()

	cfg := TLSConfig{
		Logger: log,
	}

	tlsConfig, err := SetupTLS(cfg)

	if err != nil {
		t.Errorf("Expected no error for empty config, got: %v", err)
	}

	if tlsConfig != nil {
		t.Error("Expected nil TLS config when no configuration provided")
	}
}

func TestSetupTLS_OnlyCertProvided(t *testing.T) {
	log := logger.NewLogger()

	cfg := TLSConfig{
		CertBase64: base64.StdEncoding.EncodeToString([]byte("cert")),
		Logger:     log,
	}

	tlsConfig, err := SetupTLS(cfg)

	if err != nil {
		t.Errorf("Expected no error when only cert provided: %v", err)
	}

	if tlsConfig != nil {
		t.Error("Expected nil TLS config when only cert provided")
	}
}

func TestSetupTLS_OnlyKeyProvided(t *testing.T) {
	log := logger.NewLogger()

	cfg := TLSConfig{
		KeyBase64: base64.StdEncoding.EncodeToString([]byte("key")),
		Logger:    log,
	}

	tlsConfig, err := SetupTLS(cfg)

	if err != nil {
		t.Errorf("Expected no error when only key provided: %v", err)
	}

	if tlsConfig != nil {
		t.Error("Expected nil TLS config when only key provided")
	}
}

func TestSetupTLS_InvalidBase64Cert(t *testing.T) {
	log := logger.NewLogger()

	cfg := TLSConfig{
		CertBase64: "invalid-base64!!!",
		KeyBase64:  base64.StdEncoding.EncodeToString([]byte("key")),
		Logger:     log,
	}

	_, err := SetupTLS(cfg)

	if err == nil {
		t.Error("Expected error for invalid base64 cert")
	}
}

func TestSetupTLS_InvalidBase64Key(t *testing.T) {
	log := logger.NewLogger()

	cfg := TLSConfig{
		CertBase64: base64.StdEncoding.EncodeToString([]byte("cert")),
		KeyBase64:  "invalid-base64!!!",
		Logger:     log,
	}

	_, err := SetupTLS(cfg)

	if err == nil {
		t.Error("Expected error for invalid base64 key")
	}
}

func TestSetupTLS_ValidBase64ButInvalidCert(t *testing.T) {
	log := logger.NewLogger()

	cfg := TLSConfig{
		CertBase64: base64.StdEncoding.EncodeToString([]byte("not a cert")),
		KeyBase64:  base64.StdEncoding.EncodeToString([]byte("not a key")),
		Logger:     log,
	}

	_, err := SetupTLS(cfg)

	if err == nil {
		t.Error("Expected error for invalid certificate data")
	}

	if err != nil && err.Error() != "failed to load TLS certificate from base64: tls: failed to find any PEM data in certificate input" {
		// Error message may vary, just check we got an error
		t.Logf("Got expected error: %v", err)
	}
}

// fakeCertSource simule le détenteur à chaud (relaycert.Holder) : il sert un certificat qu'on remplace.
type fakeCertSource struct{ served int }

func (f *fakeCertSource) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	f.served++
	return &tls.Certificate{}, nil
}

// TestSetupTLS_ManagedSource : sans certificat fixe, SetupTLS rend un tls.Config qui interroge la source à
// chaque handshake (rechargement à chaud). Un certificat fixe fourni prime sur la source.
func TestSetupTLS_ManagedSource(t *testing.T) {
	log := logger.NewLogger()
	src := &fakeCertSource{}
	cfg, err := SetupTLS(TLSConfig{Logger: log, Source: src})
	if err != nil || cfg == nil || cfg.GetCertificate == nil {
		t.Fatalf("source sans certificat fixe : tls.Config avec GetCertificate attendu, err=%v cfg=%v", err, cfg)
	}
	if _, err := cfg.GetCertificate(nil); err != nil || src.served != 1 {
		t.Fatalf("GetCertificate doit interroger la source (served=%d)", src.served)
	}
}
