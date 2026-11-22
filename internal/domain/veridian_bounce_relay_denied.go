package domain

import (
	"regexp"
	"strings"
)

// veridianIsRelayAccessDenied (VER-220, décision de la parente du 23/11/2026) : un refus définitif
// « Relay access denied » du serveur du destinataire est une ADRESSE MORTE, pas un refus de
// politique. Ce serveur n'héberge pas le domaine (MX périmé, domaine parti ailleurs) : aucun
// envoi n'y arrivera, quelle que soit notre réputation. Relevé en prod : une trentaine de
// contacts classés « refus de politique » (donc gardés dans leurs listes) pour cette raison.
// Seul un code 5.7.x compte (celui relevé en prod : « 554 5.7.1 … Relay access denied ») ; un
// 4.x.x (« try later ») reste temporaire, et un 5.4.1 garde la règle d'octobre (SoftCount sauf
// « recipient address rejected », TestClassifyBounce).
// Décision réversible : seul le statut du contact change.
func veridianIsRelayAccessDenied(subtype, diagnostic string) bool {
	if !strings.Contains(strings.ToLower(diagnostic), "relay access denied") {
		return false
	}
	return dsn57xRe.MatchString(subtype) || dsn57xRe.MatchString(diagnostic)
}

var dsn57xRe = regexp.MustCompile(`\b5\.7\.\d{1,3}\b`)
