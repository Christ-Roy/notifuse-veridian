package mailbox

import "github.com/Notifuse/notifuse/internal/domain"

// InScope dit si un mail entrant doit etre range dans la boite unifiee.
//
// Une boite de retour peut etre HUMAINE : commerciale@, communication@, logistique@ recoivent aussi le courrier
// courant (transporteur, fournisseurs, comptes en ligne) que ni Hugo ni Robert n'ont a lire dans l'outil de
// prospection, et qui n'a pas a etre copie dans sa base. Avec la portee « replies » (profil, champ
// veridian_return_imap_scope), seuls restent :
//   - les reponses d'un humain ou automatiques reliees a un de NOS envois par Message-ID (In-Reply-To, References,
//     ou identifiant cite dans un rapport de rebond) ;
//   - les rejets et rapports de livraison relies a un de nos envois.
//
// Un rejet dont l'envoi d'origine n'est pas le notre (rebond du courrier d'un salarie) est ecarte, comme un mail
// relie seulement par son expediteur (contact connu) : ce lien-la fait passer pour une reponse n'importe quel courrier
// d'un contact. Portee vide : tout est range, comme avant.
func InScope(scope string, kind Kind, link *Link) bool {
	if scope != domain.VeridianReturnIMAPScopeReplies {
		return true
	}
	if kind == KindOther {
		return false
	}
	return link != nil && link.MatchType == "message_id"
}
