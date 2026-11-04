// Package relaysettings lit la configuration du relais SMTP de soumission et du certificat géré (fiche 71).
// La source de vérité est la table des réglages (chiffrée par SECRET_KEY pour le jeton DNS). Une variable
// d'environnement posée prime pour les deux interrupteurs ; pour l'adresse ACME, l'annuaire et le jeton,
// la variable ne sert que de repli quand le réglage est vide (amorçage, premier démarrage).
package relaysettings

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/crypto"
)

// Clés des réglages système.
const (
	KeyRelayEnabled    = "veridian_smtp_relay_enabled"
	KeyCertManaged     = "veridian_relay_cert_managed"
	KeyACMEEmail       = "veridian_relay_acme_email"
	KeyACMEDirectory   = "veridian_relay_acme_directory"
	KeyCFToken         = "encrypted_veridian_relay_cf_token"
	KeyCertNotAfter    = "veridian_relay_cert_not_after"
	KeyCertSerial      = "veridian_relay_cert_serial"
	KeyCertLastRenewed = "veridian_relay_cert_last_renewed"
	KeyCertLastError   = "veridian_relay_cert_last_error"
)

// Variables d'environnement (surcharge d'amorçage).
const (
	EnvRelayEnabled  = "VERIDIAN_SMTP_RELAY_ENABLED"
	EnvCertManaged   = "VERIDIAN_RELAY_CERT_MANAGED"
	EnvACMEEmail     = "VERIDIAN_RELAY_ACME_EMAIL"
	EnvACMEDirectory = "VERIDIAN_RELAY_ACME_DIRECTORY"
	EnvCFToken       = "CLOUDFLARE_DNS_API_TOKEN"
)

// Annuaires ACME Let's Encrypt. Le nom (production|test) est le seul réglage accepté : pas d'URL libre.
const (
	DirectoryProduction = "production"
	DirectoryTest       = "test"
	urlProduction       = "https://acme-v02.api.letsencrypt.org/directory"
	urlTest             = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// Lookup lit une variable d'environnement (os.LookupEnv en production, injectable en test).
type Lookup func(key string) (string, bool)

// NormalizeDirectory accepte "production" (défaut si vide) ou "test". Toute autre valeur est refusée.
func NormalizeDirectory(v string) (string, error) {
	switch strings.TrimSpace(v) {
	case "", DirectoryProduction:
		return DirectoryProduction, nil
	case DirectoryTest:
		return DirectoryTest, nil
	default:
		return "", fmt.Errorf("veridian_relay_acme_directory : valeur refusée (production ou test)")
	}
}

// DirectoryURL rend l'URL de l'annuaire pour un nom normalisé.
func DirectoryURL(name string) (string, error) {
	n, err := NormalizeDirectory(name)
	if err != nil {
		return "", err
	}
	if n == DirectoryTest {
		return urlTest, nil
	}
	return urlProduction, nil
}

// Cert : configuration effective du certificat géré. CFToken est en clair en mémoire seulement :
// jamais journalisé, jamais renvoyé par l'API.
type Cert struct {
	Managed      bool
	Email        string
	Directory    string // production | test
	DirectoryURL string
	CFToken      string
}

// ReadEnabled rend l'interrupteur du relais : la variable VERIDIAN_SMTP_RELAY_ENABLED si elle est posée
// (non vide), sinon le réglage. Lu à chaque message : effet immédiat, sans redémarrage.
func ReadEnabled(ctx context.Context, repo domain.SettingRepository, lookup Lookup) bool {
	if v, ok := envValue(lookup, EnvRelayEnabled); ok {
		return parseBool(v)
	}
	v, _ := setting(ctx, repo, KeyRelayEnabled)
	return v == "true"
}

// ReadCert rend la configuration du certificat géré. Le jeton chiffré est déchiffré avec secretKey ;
// s'il est absent, la variable CLOUDFLARE_DNS_API_TOKEN sert de repli.
func ReadCert(ctx context.Context, repo domain.SettingRepository, secretKey string, lookup Lookup) (Cert, error) {
	var c Cert
	if v, ok := envValue(lookup, EnvCertManaged); ok {
		c.Managed = parseBool(v)
	} else {
		v, _ := setting(ctx, repo, KeyCertManaged)
		c.Managed = v == "true"
	}

	c.Email = firstNonEmpty(settingValue(ctx, repo, KeyACMEEmail), envString(lookup, EnvACMEEmail))

	dir, err := NormalizeDirectory(firstNonEmpty(settingValue(ctx, repo, KeyACMEDirectory), envString(lookup, EnvACMEDirectory)))
	if err != nil {
		return Cert{}, err
	}
	c.Directory = dir
	if c.DirectoryURL, err = DirectoryURL(dir); err != nil {
		return Cert{}, err
	}

	if enc := settingValue(ctx, repo, KeyCFToken); enc != "" {
		plain, err := crypto.DecryptFromHexString(enc, secretKey)
		if err != nil {
			// Message sans valeur : ni le jeton ni le chiffré ne sortent.
			return Cert{}, fmt.Errorf("jeton DNS du relais illisible (SECRET_KEY ou réglage encrypted_veridian_relay_cf_token)")
		}
		c.CFToken = plain
	} else {
		c.CFToken = envString(lookup, EnvCFToken)
	}
	return c, nil
}

// envValue rend la valeur d'une variable posée et non vide.
func envValue(lookup Lookup, key string) (string, bool) {
	if lookup == nil {
		return "", false
	}
	v, ok := lookup(key)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

func envString(lookup Lookup, key string) string {
	v, _ := envValue(lookup, key)
	return v
}

// setting rend la valeur d'un réglage et si la clé existe.
func setting(ctx context.Context, repo domain.SettingRepository, key string) (string, bool) {
	if repo == nil {
		return "", false
	}
	s, err := repo.Get(ctx, key)
	if err != nil || s == nil {
		return "", false
	}
	return strings.TrimSpace(s.Value), true
}

func settingValue(ctx context.Context, repo domain.SettingRepository, key string) string {
	v, _ := setting(ctx, repo, key)
	return v
}

func parseBool(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
