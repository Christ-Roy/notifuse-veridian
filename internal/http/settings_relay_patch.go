package http

// relayValues : valeurs courantes des réglages relais et certificat géré (fiche 71).
type relayValues struct {
	SMTPRelayEnabled bool
	CertManaged      bool
	ACMEEmail        string
	ACMEDirectory    string
}

// relayPatch : champs relais optionnels d'une mise à jour. Absent (nil) = valeur INCHANGÉE.
// Un appel partiel (CLI, script, ancien formulaire) ne coupe donc jamais le relais ni le certificat géré.
type relayPatch struct {
	SMTPRelayEnabled *bool   `json:"veridian_smtp_relay_enabled"`
	CertManaged      *bool   `json:"veridian_relay_cert_managed"`
	ACMEEmail        *string `json:"veridian_relay_acme_email"`
	ACMEDirectory    *string `json:"veridian_relay_acme_directory"`
}

// applyRelayPatch rend les valeurs à écrire : chaque champ absent garde sa valeur courante.
func applyRelayPatch(cur relayValues, p relayPatch) relayValues {
	out := cur
	if p.SMTPRelayEnabled != nil {
		out.SMTPRelayEnabled = *p.SMTPRelayEnabled
	}
	if p.CertManaged != nil {
		out.CertManaged = *p.CertManaged
	}
	if p.ACMEEmail != nil {
		out.ACMEEmail = *p.ACMEEmail
	}
	if p.ACMEDirectory != nil {
		out.ACMEDirectory = *p.ACMEDirectory
	}
	return out
}
