package mailbox

import (
	"context"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

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

// personalHosts : messageries grand public ou de bureau, ou une boite est d'abord celle d'une PERSONNE.
var personalHosts = []string{"imap.gmail.com", "imap.googlemail.com", "outlook.office365.com", "imap-mail.outlook.com", "imap.mail.yahoo.com",
	"imap.mail.me.com", "imap.orange.fr", "imap.free.fr", "imap.laposte.net", "imap.sfr.fr", "imap4.bouygtel.fr", "imap.bbox.fr", "imap.aol.com", "imap.gmx.com", "imap.gmx.net"}

// IsPersonalBox dit si la boite lue est celle d'une personne, courrier prive compris : messagerie grand public, dossier de
// reception entier. Un libelle dedie (« GBC/Reponses ») est un tri deja fait : il se garde en entier.
func IsPersonalBox(s *domain.IMAPSettings) bool {
	if s == nil {
		return false
	}
	folder := strings.ToLower(s.GetFolder())
	if folder != "inbox" && !strings.HasPrefix(folder, "[gmail]") && !strings.HasPrefix(folder, "[google mail]") {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(s.Host))
	for _, h := range personalHosts {
		if host == h {
			return true
		}
	}
	return false
}

// EffectiveScope : le reglage du profil, sinon « replies » pour une boite personnelle (Robert, 24/11/2026 : « lire l'IMAP de Gmail
// mais que avec les reponses aux prospects pour ne pas melanger les mails perso »). Mesure : 499 mails sans lien ranges en 7 jours
// pour un client dont la boite Gmail entiere etait lue. « all » garde tout, dit explicitement.
func EffectiveScope(setting string, s *domain.IMAPSettings) string {
	if setting == "" && IsPersonalBox(s) {
		return domain.VeridianReturnIMAPScopeReplies
	}
	return setting
}

// PurgeOutOfScopeSQL supprime, pour UNE boite de retour, les messages entrants que la portee « replies » n'aurait pas
// rangés : tout ce qui n'est pas une reponse ou un rejet relie a un de nos envois par Message-ID. Garde-fous : un message
// deja traite par un humain reste, et un message dont une copie dit « duplicate_of » reste (l'original ne disparait pas
// sous ses copies). Les mails restent dans la boite IMAP (jamais supprimes) : seule la copie de l'outil est retiree.
const PurgeOutOfScopeSQL = `DELETE FROM veridian_mail_message m
WHERE m.direction = 'in' AND m.mailbox_integration_id = $1 AND m.handled_at IS NULL
  AND NOT (m.kind <> 'other' AND COALESCE(m.link->>'match_type', '') = 'message_id')
  AND NOT EXISTS (SELECT 1 FROM veridian_mail_message d WHERE d.duplicate_of = m.id)`

// PurgeOutOfScope applique PurgeOutOfScopeSQL a la boite integrationID du workspace ws. Idempotent.
func (s *Store) PurgeOutOfScope(ctx context.Context, ws, integrationID string) (int64, error) {
	db, err := s.db(ctx, ws)
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, PurgeOutOfScopeSQL, integrationID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
