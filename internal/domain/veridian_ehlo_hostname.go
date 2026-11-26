package domain

import (
	"fmt"
	"regexp"
)

// veridianEHLOHostnameRe : un nom d'hôte (lettres, chiffres, point, tiret) ou un littéral d'adresse entre crochets.
// 26/11 (chasse sécu, lot C) : ehlo_hostname partait tel quel dans « EHLO %s\r\n » ; une valeur portant CRLF injectait
// des commandes SMTP (MAIL FROM, RCPT TO) vers le serveur du profil.
var veridianEHLOHostnameRe = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?|\[[0-9A-Fa-f:.]{2,45}\])$`)

// ValidateVeridianEHLOHostname refuse un ehlo_hostname SMTP qui n'est pas un nom d'hôte. Vide : accepté (repli sur
// le domaine de l'expéditeur au moment de l'envoi).
func (e *EmailProvider) ValidateVeridianEHLOHostname() error {
	if e == nil || e.SMTP == nil || e.SMTP.EHLOHostname == "" {
		return nil
	}
	if !veridianEHLOHostnameRe.MatchString(e.SMTP.EHLOHostname) {
		return fmt.Errorf("ehlo_hostname must be a host name (letters, digits, dots, hyphens)")
	}
	return nil
}

// ValidateVeridianSettings : les contrôles Veridian d'un profil, branchés par UNE ligne dans Validate (fichier gelé).
func (e *EmailProvider) ValidateVeridianSettings() error {
	if err := e.ValidateVeridianHardBounceFreezeThreshold(); err != nil {
		return err
	}
	return e.ValidateVeridianEHLOHostname()
}
