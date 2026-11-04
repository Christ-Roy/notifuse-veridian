package relaycert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

// LegoIssuer émet via la bibliothèque lego v4 (MIT, github.com/go-acme/lego), DNS-01 Cloudflare.
// Le jeton DNS vient des réglages (déchiffré à chaque décision) ; il n'est jamais journalisé.
type LegoIssuer struct {
	// DirectoryURL : autorité ACME (production Let's Encrypt, ou l'autorité de test pour les épreuves).
	DirectoryURL string
	// CFToken : jeton Cloudflare (DNS-01, zone du domaine).
	CFToken string
	// Nameservers : résolveurs publics pour vérifier la propagation du TXT (le résolveur local peut être en retard).
	Nameservers []string
	// OnCleanupError reçoit une erreur de nettoyage du TXT (sans jeton). Nil = ignorée.
	OnCleanupError func(error)
}

type legoUser struct {
	email string
	reg   *registration.Resource
	key   crypto.PrivateKey
}

func (u *legoUser) GetEmail() string                        { return u.email }
func (u *legoUser) GetRegistration() *registration.Resource { return u.reg }
func (u *legoUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// Obtain émet un certificat pour domain. accountKeyPEM réutilise le compte ACME s'il existe.
func (l LegoIssuer) Obtain(_ context.Context, domain, email string, accountKeyPEM []byte) (*Bundle, error) {
	if l.DirectoryURL == "" {
		return nil, errors.New("relaycert: autorité ACME absente")
	}
	key, accountPEM, err := accountKey(accountKeyPEM)
	if err != nil {
		return nil, err
	}
	user := &legoUser{email: email, key: key}
	cfg := lego.NewConfig(user)
	cfg.CADirURL = l.DirectoryURL
	cfg.Certificate.KeyType = ecdsaP256Type()

	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("relaycert: client ACME: %w", err)
	}
	provider, err := NewCloudflareDNS(l.CFToken)
	if err != nil {
		return nil, err
	}
	// Nettoyage du TXT sur TOUS les chemins : succès, erreur, et panic (defer s'exécute pendant un panic).
	defer func() {
		if cerr := provider.CleanUpAll(); cerr != nil && l.OnCleanupError != nil {
			l.OnCleanupError(cerr)
		}
	}()
	ns := l.Nameservers
	if len(ns) == 0 {
		ns = []string{"1.1.1.1:53", "8.8.8.8:53"}
	}
	if err := client.Challenge.SetDNS01Provider(provider, dns01.AddRecursiveNameservers(ns)); err != nil {
		return nil, fmt.Errorf("relaycert: challenge DNS-01: %w", err)
	}
	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return nil, fmt.Errorf("relaycert: enregistrement du compte ACME: %w", err)
	}
	user.reg = reg

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: []string{domain}, Bundle: true})
	if err != nil {
		return nil, fmt.Errorf("relaycert: émission DNS-01 pour %s: %w", domain, err)
	}
	return &Bundle{CertPEM: res.Certificate, KeyPEM: res.PrivateKey, AccountKeyPEM: accountPEM}, nil
}

// ecdsaP256Type : type de clé du certificat émis (ECDSA P-256, constante lego).
func ecdsaP256Type() certcrypto.KeyType { return certcrypto.EC256 }

// accountKey relit la clé de compte ECDSA P-256 fournie, ou en génère une nouvelle.
func accountKey(pemBytes []byte) (crypto.PrivateKey, []byte, error) {
	var k *ecdsa.PrivateKey
	if len(pemBytes) > 0 {
		blk, _ := pem.Decode(pemBytes)
		if blk == nil {
			return nil, nil, errors.New("relaycert: clé de compte ACME illisible")
		}
		parsed, err := x509.ParseECPrivateKey(blk.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("relaycert: clé de compte ACME invalide: %w", err)
		}
		k = parsed
	} else {
		generated, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, nil, fmt.Errorf("relaycert: génération de la clé de compte: %w", err)
		}
		k = generated
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, nil, err
	}
	return k, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}
