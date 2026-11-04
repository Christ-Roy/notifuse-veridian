package smtp_bridge

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// TLSConfig holds configuration for TLS certificate management
type TLSConfig struct {
	CertBase64 string // Base64 encoded certificate (fixe : prime sur Source)
	KeyBase64  string // Base64 encoded key
	Logger     logger.Logger
	// Source fournit le certificat géré par l'application (renouvelé à chaud). Utilisée seulement
	// sans certificat fixe : la précédence reste au certificat fourni par fichier ou variable.
	Source CertificateSource
}

// CertificateSource rend le certificat courant à chaque handshake (relaycert.Holder).
type CertificateSource interface {
	GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
}

// SetupTLS configures TLS from base64-encoded certificates
// Returns a *tls.Config that can be used with the SMTP server
func SetupTLS(cfg TLSConfig) (*tls.Config, error) {
	// Check if certificates are provided
	if cfg.CertBase64 == "" || cfg.KeyBase64 == "" {
		if cfg.Source != nil {
			cfg.Logger.Info("SMTP bridge: TLS géré par l'application (certificat renouvelé à chaud)")
			return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: cfg.Source.GetCertificate}, nil
		}
		cfg.Logger.Warn("SMTP bridge: No TLS configuration provided - server will run without TLS (NOT recommended for production)")
		return nil, nil
	}

	cfg.Logger.Info("SMTP bridge: Configuring TLS from base64-encoded certificates")

	// Decode certificate
	certPEM, err := base64.StdEncoding.DecodeString(cfg.CertBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 certificate: %w", err)
	}

	// Decode key
	keyPEM, err := base64.StdEncoding.DecodeString(cfg.KeyBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 key: %w", err)
	}

	// Load certificate and key
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate from base64: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	cfg.Logger.Info("SMTP bridge: TLS configured successfully from base64 certificates")

	return tlsConfig, nil
}
