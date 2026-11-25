package boxapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/google/uuid"
)

// Profil de configuration Apple (.mobileconfig) : serveurs, ports, TLS et identifiant, SANS mot de passe. L'appareil le demande une fois et s'en sert
// pour recevoir et pour envoyer : STARTTLS au mauvais endroit et mot de passe vide d'un côté ne peuvent plus arriver.

// profileMIME : le type qu'iOS et macOS ouvrent comme un profil.
const profileMIME = "application/x-apple-aspen-config"

// linkLife : durée du lien (QR compris). Il ne porte qu'un identifiant signé, jamais de secret.
const linkLife = 15 * time.Minute

var errLink = errors.New("lien de profil refusé ou expiré")

// linkPayload : échéance (4 octets), identifiant (16), espace. Compact : le lien entier tient dans un QR lisible (213 octets, version 10).
func linkPayload(ws string, id uuid.UUID, exp time.Time) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(exp.Unix())) //nolint:gosec // valable jusqu'en 2106
	return append(append(b, id[:]...), ws...)
}

// linkMAC : 128 bits, assez pour un lien de 15 minutes qui ne donne que des réglages.
func linkMAC(key, payload []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("veridian-box-profile:"))
	m.Write(payload)
	return m.Sum(nil)[:16]
}

// SignLink rend le jeton d'un lien de profil.
func SignLink(key []byte, ws, id string, exp time.Time) (string, error) {
	u, err := uuid.Parse(id)
	if err != nil || ws == "" || len(key) == 0 {
		return "", errLink
	}
	p := linkPayload(ws, u, exp)
	return base64.RawURLEncoding.EncodeToString(append(p, linkMAC(key, p)...)), nil
}

// ReadLink vérifie un jeton.
func ReadLink(key []byte, token string, now time.Time) (ws, id string, err error) {
	if len(key) == 0 || len(token) > 512 {
		return "", "", errLink
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) < 4+16+1+16 {
		return "", "", errLink
	}
	p, mac := b[:len(b)-16], b[len(b)-16:]
	if !hmac.Equal(mac, linkMAC(key, p)) || now.Unix() >= int64(binary.BigEndian.Uint32(p)) {
		return "", "", errLink
	}
	u, err := uuid.FromBytes(p[4:20])
	if err != nil {
		return "", "", errLink
	}
	return string(p[20:]), u.String(), nil
}

// MobileConfig rend le profil d'un accès. Identifiants stables : réinstaller remplace, sans doublon.
func MobileConfig(a *boxaccess.Access, c Connection, org string) ([]byte, error) {
	if c.IMAP == nil || c.SMTP == nil {
		return nil, errors.New("réception et envoi doivent être servis")
	}
	base, err := uuid.Parse(a.ID)
	if err != nil {
		return nil, fmt.Errorf("accès %q : %w", a.ID, err)
	}
	name := a.Address
	if a.Label != "" {
		name = a.Address + " (" + a.Label + ")"
	}
	var b bytes.Buffer
	str := func(k, v string) {
		b.WriteString("<key>" + k + "</key><string>")
		_ = xml.EscapeText(&b, []byte(v))
		b.WriteString("</string>\n")
	}
	num := func(k string, v int) { fmt.Fprintf(&b, "<key>%s</key><integer>%d</integer>\n", k, v) }
	yes := func(k string, v bool) { fmt.Fprintf(&b, "<key>%s</key><%t/>\n", k, v) }
	b.WriteString(xml.Header + `<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n<plist version=\"1.0\"><dict>\n")
	str("PayloadType", "Configuration")
	num("PayloadVersion", 1)
	str("PayloadIdentifier", "site.veridian.mail."+a.ID)
	str("PayloadUUID", strings.ToUpper(uuid.NewSHA1(base, []byte("profil")).String()))
	str("PayloadDisplayName", name)
	str("PayloadOrganization", org)
	str("PayloadDescription", "Courrier "+a.Address)
	yes("PayloadRemovalDisallowed", false)
	b.WriteString("<key>PayloadContent</key><array><dict>\n")
	str("PayloadType", "com.apple.mail.managed")
	num("PayloadVersion", 1)
	str("PayloadIdentifier", "site.veridian.mail."+a.ID+".compte")
	str("PayloadUUID", strings.ToUpper(uuid.NewSHA1(base, []byte("compte")).String()))
	str("PayloadDisplayName", name)
	str("EmailAccountType", "EmailTypeIMAP")
	str("EmailAccountDescription", name)
	str("EmailAddress", a.Address)
	str("IncomingMailServerAuthentication", "EmailAuthPassword")
	str("IncomingMailServerHostName", c.IMAP.Host)
	num("IncomingMailServerPortNumber", c.IMAP.Port)
	yes("IncomingMailServerUseSSL", true)
	str("IncomingMailServerUsername", a.Address)
	str("OutgoingMailServerAuthentication", "EmailAuthPassword")
	str("OutgoingMailServerHostName", c.SMTP.Host)
	num("OutgoingMailServerPortNumber", c.SMTP.Port)
	yes("OutgoingMailServerUseSSL", true)
	str("OutgoingMailServerUsername", a.Address)
	yes("OutgoingPasswordSameAsIncomingPassword", true)
	b.WriteString("</dict></array>\n</dict></plist>\n")
	return b.Bytes(), nil
}
