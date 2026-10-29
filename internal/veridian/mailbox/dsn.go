package mailbox

import (
	"regexp"
	"strings"

	"github.com/Notifuse/notifuse/pkg/veridian_ndr"
)

// Catégories de rebond (valeurs stables de DSN.Category).
const (
	CatAdresseInexistante = "adresse_inexistante"
	CatDomaineInexistant  = "domaine_inexistant"
	CatBoiteInactive      = "boite_inactive"
	CatBoitePleine        = "boite_pleine"
	CatMessageTropGros    = "message_trop_gros"
	CatRefusPolitique     = "refus_politique"
	CatReputation         = "reputation"
	CatAuthentification   = "authentification"
	CatGreylisting        = "greylisting"
	CatLimiteDebit        = "limite_debit"
	CatServeurInjoignable = "serveur_injoignable"
	CatRelaisRefuse       = "relais_refuse"
	CatRetard             = "retard"
	CatLivre              = "livre"
	CatInconnu            = "inconnu"
)

var (
	dsnActionRe    = regexp.MustCompile(`(?im)^action:\s*([a-z]+)`)
	dsnRemoteMTARe = regexp.MustCompile(`(?im)^remote-mta:\s*(?:dns;)?\s*(\S+)`)
	dsnReportMTARe = regexp.MustCompile(`(?im)^reporting-mta:\s*(?:dns;)?\s*(\S+)`)
	dsnSmtpCodeRe  = regexp.MustCompile(`\b([245]\d\d)[ -]`)
)

// providerHints : fragments d'hôte ou de diagnostic -> nom du fournisseur.
var providerHints = []struct{ frag, name string }{
	{"google", "Gmail"}, {"gmail", "Gmail"}, {"outlook", "Microsoft"}, {"hotmail", "Microsoft"}, {"microsoft", "Microsoft"},
	{"office365", "Microsoft"}, {"protection.outlook", "Microsoft"}, {"live.com", "Microsoft"},
	{"orange", "Orange"}, {"wanadoo", "Orange"}, {"free.fr", "Free"}, {"proxad", "Free"}, {"sfr", "SFR"}, {"neuf", "SFR"},
	{"laposte", "La Poste"}, {"bbox", "Bouygues"}, {"bouygues", "Bouygues"}, {"ovh", "OVH"}, {"gandi", "Gandi"},
	{"ionos", "IONOS"}, {"1and1", "IONOS"}, {"yahoo", "Yahoo"}, {"aol", "AOL"}, {"icloud", "Apple iCloud"}, {"apple", "Apple iCloud"},
	{"infomaniak", "Infomaniak"}, {"ik.me", "Infomaniak"}, {"proton", "Proton"}, {"zoho", "Zoho"}, {"o2switch", "o2switch"},
	{"scaleway", "Scaleway"}, {"hostinger", "Hostinger"}, {"mailinblack", "Mailinblack"}, {"xefi", "XEFI (anti-spam)"},
	{"mimecast", "Mimecast"}, {"proofpoint", "Proofpoint"}, {"barracuda", "Barracuda"}, {"vadesecure", "Vade"},
}

// DecodeDSN transforme le rapport de non-remise en raison française. ndr vient du parseur
// canonique (pkg/veridian_ndr) ; deliveryStatus est le texte de la partie message/delivery-status
// (peut être vide), diagFallback le texte du corps si aucun diagnostic structuré.
func DecodeDSN(ndr veridian_ndr.Result, deliveryStatus, bodyText string) *DSN {
	d := &DSN{
		Code:              ndr.DSNCode,
		Severity:          string(ndr.Severity),
		Diagnostic:        truncate(cleanLine(ndr.DiagnosticCode), 400),
		Recipient:         ndr.Recipient,
		OriginalMessageID: ndr.OriginalMessageID,
	}
	src := deliveryStatus
	if src == "" {
		src = bodyText
	}
	if m := dsnActionRe.FindStringSubmatch(src); m != nil {
		d.Action = strings.ToLower(m[1])
	}
	if d.Code == "" {
		if m := dsnSmtpCodeRe.FindStringSubmatch(d.Diagnostic); m != nil {
			// code SMTP basique : 5xx -> x.0.0, au moins la classe
			d.Code = string(m[1][0]) + ".0.0"
		}
	}
	if d.Severity == "" || d.Severity == string(veridian_ndr.SeverityUnknown) {
		switch {
		case strings.HasPrefix(d.Code, "5"):
			d.Severity = string(veridian_ndr.SeverityHard)
		case strings.HasPrefix(d.Code, "4"):
			d.Severity = string(veridian_ndr.SeveritySoft)
		}
	}
	d.Provider = detectProvider(strings.Join([]string{
		firstMatch(dsnRemoteMTARe, src), firstMatch(dsnReportMTARe, src), d.Diagnostic, d.Recipient,
	}, " "))
	d.Category, d.ReasonFR, d.AdviceFR = explain(d)
	if d.Category == CatInconnu && d.Action == "delayed" {
		d.Category = CatRetard
		d.ReasonFR = "Remise retardée : le serveur du destinataire n'a pas encore accepté le message, le relais réessaie."
		d.AdviceFR = "Rien à faire : le relais réessaie pendant plusieurs jours."
	}
	return d
}

func firstMatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func detectProvider(s string) string {
	l := strings.ToLower(s)
	for _, h := range providerHints {
		if strings.Contains(l, h.frag) {
			return h.name
		}
	}
	return ""
}

var (
	reUnknownUser = regexp.MustCompile(`(?i)(user unknown|unknown user|no such user|does not exist|doesn'?t exist|recipient (address )?(rejected|not found|unknown)|invalid (recipient|address|mailbox)|mailbox (not found|unavailable|does not exist|doesn'?t exist)|address (rejected|not found|unknown|does not exist)|no mailbox here|unknown (recipient|address)|n'existe pas|inconnu|account .*(disabled|does not exist)|recipientnotfound|nosuchuser)`)
	reDisabled    = regexp.MustCompile(`(?i)(disabled|deactivated|suspended|inactive|not accepting (mail|messages)|account (has been )?closed|désactiv|suspendu)`)
	reFull        = regexp.MustCompile(`(?i)(mailbox (is )?full|over ?quota|quota (exceeded|full)|insufficient (system )?storage|storage quota|boîte (pleine|saturée))`)
	reTooBig      = regexp.MustCompile(`(?i)(message (size|too (large|big))|exceeds .*(size|limit)|too large|taille maximale)`)
	reSpamPolicy  = regexp.MustCompile(`(?i)(spam|blocked|blacklist|blocklist|black list|denylist|listed (at|by|in)|spamhaus|barracuda|reputation|réputation|rejected due to|refused due to|not allowed|policy|poor reputation|dnsbl|sender is (blocked|blacklisted)|banned|abuse|unsolicited|junk|content rejected|refusé|poor sender)`)
	reAuth        = regexp.MustCompile(`(?i)(\bspf\b|\bdkim\b|\bdmarc\b|authentication (required|failed)|sender verify|reverse dns|ptr record|rdns|helo|not authorized to send)`)
	reGrey        = regexp.MustCompile(`(?i)(greylist|grey-list|graylist|try again (later|in)|temporarily (deferred|rejected|unavailable)|deferred|service unavailable, try|please retry)`)
	reRate        = regexp.MustCompile(`(?i)(rate limit|too many (connections|messages|recipients|emails)|throttl|exceeded .*(limit|rate)|sending limit|trop de)`)
	reRelay       = regexp.MustCompile(`(?i)(relay(ing)? (access )?denied|relaying not permitted|not permitted to relay)`)
	reDomain      = regexp.MustCompile(`(?i)(domain (not found|does not exist|name not found)|host (not found|or domain name not found)|no mx|nxdomain|name or service not known|unrouteable address|dns (error|failure)|bad destination system)`)
	reConnect     = regexp.MustCompile(`(?i)(connection (timed out|refused|reset)|timed out|no route to host|host is down|network is unreachable|could not connect|cannot connect)`)
)

// explain donne catégorie, raison et conseil, d'abord par code enrichi puis par diagnostic.
func explain(d *DSN) (cat, reason, advice string) {
	diag := d.Diagnostic
	code := d.Code
	hard := d.Severity == string(veridian_ndr.SeverityHard)
	soft := !hard
	who := ""
	if d.Provider != "" {
		who = " (" + d.Provider + ")"
	}

	// Réussites et autres rapports qui ne sont pas des échecs.
	switch d.Action {
	case "delivered", "relayed", "expanded":
		return CatLivre, "Accusé de livraison : le serveur du destinataire a accepté le message" + who + ".",
			"Rien à faire."
	}

	// Les diagnostics texte priment quand ils sont explicites (les codes 5.0.0 / 5.5.x sont vagues).
	switch {
	case reFull.MatchString(diag) || strings.HasPrefix(code, "5.2.2") || strings.HasPrefix(code, "4.2.2"):
		if soft {
			return CatBoitePleine, "Boîte du destinataire pleine pour le moment" + who + " : le message sera réessayé.",
				"Rien à faire tout de suite ; si cela se répète, l'adresse est abandonnée."
		}
		return CatBoitePleine, "Boîte du destinataire pleine" + who + " : le message est refusé.",
			"Contact injoignable pour l'instant : retirer de la séquence ou réessayer dans quelques semaines."
	case reTooBig.MatchString(diag) || strings.HasPrefix(code, "5.2.3") || strings.HasPrefix(code, "5.3.4"):
		return CatMessageTropGros, "Message trop volumineux pour le serveur du destinataire" + who + ".",
			"Alléger le mail (images, pièces jointes)."
	case reRelay.MatchString(diag):
		return CatRelaisRefuse, "Le serveur du destinataire refuse de relayer le message" + who + " (adresse ou domaine mal routé).",
			"Vérifier l'adresse du contact."
	case strings.HasPrefix(code, "5.1.2") || reDomain.MatchString(diag):
		return CatDomaineInexistant, "Le domaine de l'adresse n'existe pas ou ne reçoit pas de courrier" + who + ".",
			"Adresse morte : le contact est sorti des séquences, à retirer de la liste."
	case reUnknownUser.MatchString(diag) || strings.HasPrefix(code, "5.1.1") || strings.HasPrefix(code, "5.1.0") || strings.HasPrefix(code, "5.1.10") || strings.HasPrefix(code, "5.1.3"):
		return CatAdresseInexistante, "Adresse inexistante : le serveur du destinataire dit que cette boîte n'existe pas" + who + ".",
			"Adresse morte : le contact est sorti des séquences, à corriger ou retirer."
	case strings.HasPrefix(code, "5.2.1") || (hard && reDisabled.MatchString(diag)):
		return CatBoiteInactive, "La boîte du destinataire est désactivée ou n'accepte plus de courrier" + who + ".",
			"Adresse inutilisable : à retirer."
	case reAuth.MatchString(diag) || regexp.MustCompile(`^[45]\.7\.(2[0-9]|1[0-9]|8)`).MatchString(code):
		return CatAuthentification, "Refus lié à l'authentification de l'expéditeur" + who + " (SPF, DKIM, DMARC ou DNS inverse).",
			"Vérifier SPF, DKIM, DMARC et le DNS inverse du domaine d'envoi ; suspendre le profil tant que ce n'est pas corrigé."
	case reRate.MatchString(diag):
		return CatLimiteDebit, "Le serveur du destinataire limite le débit d'envoi" + who + " : trop de messages trop vite.",
			"Ralentir le profil sur ce fournisseur (plafonds par classe)."
	case reSpamPolicy.MatchString(diag) || strings.HasPrefix(code, "5.7"):
		if soft {
			return CatReputation, "Refus temporaire pour cause de politique ou de réputation" + who + ".",
				"Surveiller : plusieurs refus de ce type justifient de ralentir ce fournisseur."
		}
		return CatRefusPolitique, "Refus de politique" + who + " : le serveur du destinataire a rejeté le message comme indésirable ou à cause de la réputation d'envoi.",
			"Ce n'est pas l'adresse qui est morte : ralentir le fournisseur concerné, vérifier le contenu et la réputation du domaine d'envoi."
	case reGrey.MatchString(diag) || strings.HasPrefix(code, "4.7") || strings.HasPrefix(code, "4.2.0"):
		return CatGreylisting, "Greylisting ou refus temporaire" + who + " : le serveur demande de réessayer plus tard.",
			"Rien à faire : le relais réessaie tout seul (2 à 15 minutes en général)."
	case reConnect.MatchString(diag) || strings.HasPrefix(code, "4.4") || strings.HasPrefix(code, "5.4"):
		if hard {
			return CatServeurInjoignable, "Serveur du destinataire injoignable ou mal configuré" + who + " : échec définitif.",
				"Vérifier le domaine du contact."
		}
		return CatServeurInjoignable, "Serveur du destinataire injoignable pour le moment" + who + ".",
			"Rien à faire : le relais réessaie."
	}
	if hard {
		return CatInconnu, "Message refusé définitivement par le serveur du destinataire" + who + " (raison non reconnue, voir le diagnostic).",
			"Lire le diagnostic ; traiter l'adresse comme morte si le refus se répète."
	}
	return CatInconnu, "Échec temporaire de remise" + who + " (raison non reconnue, voir le diagnostic).",
		"Surveiller : le relais réessaie."
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
