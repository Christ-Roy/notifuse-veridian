// Package domauth : domaines d'envoi authentifiés d'un espace (VER-166, fiche 97). Un client fait partir ses mails de SON domaine :
// une clé DKIM RSA-2048 par espace et par domaine (clé privée chiffrée par SECRET_KEY, jamais rendue), les enregistrements DNS exacts à
// publier (DKIM, SPF par notre include, DMARC recommandé), une vérification par lecture DNS réelle, et la signature d=<domaine> de tout
// mail sortant dont le From est sur un domaine VÉRIFIÉ de l'espace, juste avant la soumission SMTP (service.SetVeridianOutgoingSigner).
//
// Vérifié = l'enregistrement DKIM publié porte exactement notre clé publique : c'est la seule condition pour signer sans produire de
// dkim=fail. SPF et DMARC sont relevés et rendus avec leur verdict ; « complete » dit que les trois sont en place.
package domauth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// Statuts d'un domaine.
const (
	StatusPending  = "pending"  // l'enregistrement DKIM n'est pas (encore) lu avec notre clé : aucune signature
	StatusVerified = "verified" // DKIM publié et conforme : les mails From sur ce domaine partent signés d=<domaine>
)

// Natures d'enregistrement.
const (
	KindDKIM  = "dkim"
	KindSPF   = "spf"
	KindDMARC = "dmarc"
)

// KeyBits : taille de la clé RSA (RFC 8301 : 1024 minimum, 2048 recommandé, au-delà les TXT deviennent difficiles à publier).
const KeyBits = 2048

// MaxPerWorkspace : borne du nombre de domaines d'un espace.
const MaxPerWorkspace = 20

// DefaultSPFInclude : l'enregistrement SPF que nous maintenons et qui liste les adresses de nos relais d'envoi (iac/dns/veridian-site.yaml).
const DefaultSPFInclude = "_spf-envoi.veridian.site"

// Erreurs.
var (
	ErrNotFound    = errors.New("domaine d'envoi introuvable")
	ErrExists      = errors.New("ce domaine est déjà déclaré dans cet espace")
	ErrTooMany     = fmt.Errorf("%d domaines d'envoi au plus par espace", MaxPerWorkspace)
	ErrInvalidName = errors.New("nom de domaine invalide")
)

// Record : un enregistrement DNS à publier, tel quel.
type Record struct {
	Kind     string `json:"kind"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Required bool   `json:"required"`
	Note     string `json:"note,omitempty"`
}

// Check : verdict d'un enregistrement après lecture DNS.
type Check struct {
	Kind string `json:"kind"`
	OK   bool   `json:"ok"`
	// Verdict : pass, missing, mismatch, multiple, temperror (DNS injoignable : rien n'est conclu).
	Verdict string   `json:"verdict"`
	Found   []string `json:"found,omitempty"`
	Detail  string   `json:"detail"`
}

// Domain : un domaine d'envoi d'un espace. La clé privée n'est jamais dans cette structure.
type Domain struct {
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspace_id"`
	Domain        string     `json:"domain"`
	Selector      string     `json:"selector"`
	PublicKey     string     `json:"-"`
	Status        string     `json:"status"`
	Checks        []Check    `json:"checks"`
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
	// LostAt, LostReason : le domaine ÉTAIT vérifié et ne l'est plus (clé DKIM retirée : missing, ou changée : mismatch), constaté à cette
	// date par une vérification (bouton, route ou revérification à l'usage). Remis à zéro quand il redevient vérifié.
	LostAt     *time.Time `json:"lost_at,omitempty"`
	LostReason string     `json:"lost_reason,omitempty"`
}

// Complete : DKIM, SPF et DMARC relevés conformes au dernier contrôle.
func (d Domain) Complete() bool {
	seen := map[string]bool{}
	for _, c := range d.Checks {
		seen[c.Kind] = c.OK
	}
	return seen[KindDKIM] && seen[KindSPF] && seen[KindDMARC]
}

// NormalizeDomain rend le nom en minuscules ASCII (IDN converti), ou ErrInvalidName. Un suffixe public (fr, co.uk) est refusé.
func NormalizeDomain(raw string) (string, error) {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if d == "" || strings.ContainsAny(d, " @/:\\\t\r\n") {
		return "", ErrInvalidName
	}
	ascii, err := idna.Lookup.ToASCII(d)
	if err != nil {
		return "", ErrInvalidName
	}
	if len(ascii) > 253 {
		return "", ErrInvalidName
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", ErrInvalidName
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", ErrInvalidName
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", ErrInvalidName
			}
		}
	}
	if etld1, err := publicsuffix.EffectiveTLDPlusOne(ascii); err != nil || etld1 == "" {
		return "", ErrInvalidName
	}
	return ascii, nil
}

// NewSelector : sélecteur neuf, daté (une rotation de clé en crée un autre sans casser l'ancien).
func NewSelector(now time.Time) (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "vd" + now.UTC().Format("200601") + hex.EncodeToString(b), nil
}

// GenerateKey : clé RSA-2048, rendue en PEM PKCS#8 (à chiffrer) et clé publique base64 DER (le p= du TXT).
func GenerateKey() (privPEM string, pubB64 string, err error) {
	k, err := rsa.GenerateKey(rand.Reader, KeyBits)
	if err != nil {
		return "", "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return "", "", err
	}
	pub, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), base64.StdEncoding.EncodeToString(pub), nil
}

// ParsePrivateKey relit une clé PEM PKCS#8 RSA.
func ParsePrivateKey(privPEM string) (*rsa.PrivateKey, error) {
	b, _ := pem.Decode([]byte(privPEM))
	if b == nil {
		return nil, errors.New("clé DKIM illisible")
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, errors.New("clé DKIM illisible")
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("clé DKIM non RSA")
	}
	return rk, nil
}

// DKIMName : nom du TXT DKIM.
func DKIMName(d Domain) string { return d.Selector + "._domainkey." + d.Domain }

// DKIMValue : valeur du TXT DKIM.
func DKIMValue(d Domain) string { return "v=DKIM1; k=rsa; p=" + d.PublicKey }

// SPFValue : le SPF à publier quand le domaine n'en a pas.
func SPFValue(include string) string { return "v=spf1 include:" + include + " ~all" }

// DMARCValue : la politique recommandée pour commencer (surveillance, alignement souple).
const DMARCValue = "v=DMARC1; p=none; adkim=r; aspf=r"

// Records : les enregistrements exacts à publier pour ce domaine.
func Records(d Domain, include string) []Record {
	return []Record{
		{Kind: KindDKIM, Type: "TXT", Name: DKIMName(d), Value: DKIMValue(d), Required: true,
			Note: "Signature de vos mails. À publier tel quel ; certains hébergeurs DNS veulent le nom sans le domaine (" + d.Selector + "._domainkey)."},
		{Kind: KindSPF, Type: "TXT", Name: d.Domain, Value: SPFValue(include), Required: true,
			Note: "Autorise nos relais à envoyer pour vous. Un domaine n'a qu'un seul SPF : s'il en existe déjà un, ajoutez-y « include:" + include + " » avant le « all »."},
		{Kind: KindDMARC, Type: "TXT", Name: "_dmarc." + d.Domain, Value: DMARCValue, Required: false,
			Note: "Recommandé. Commencez en p=none, passez à p=quarantine quand les rapports sont propres. S'il en existe déjà un, gardez-le."},
	}
}
