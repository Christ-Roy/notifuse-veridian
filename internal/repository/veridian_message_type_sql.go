package repository

import (
	"context"
	"fmt"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork, lot 4 (08/10/2026) : separation du commercial et du
// transactionnel dans les compteurs.
//
// veridianCommercialRowSQL se colle a la fin d'un WHERE : il ecarte les messages
// transactionnels (API d'envoi, relais SMTP, modeles de categorie transactionnelle
// envoyes par une sequence). Un mail transactionnel ne compte dans aucun plafond,
// aucune chauffe, aucun fusible de reputation commerciaux, et inversement. Les
// lignes anterieures au lot 4 n'ont pas de veridian_message_type (NULL) : seules
// celles qui portent un transactional_notification_id sont transactionnelles,
// toutes les autres restent commerciales, comme avant.
const veridianCommercialRowSQL = ` AND veridian_message_type IS DISTINCT FROM 'transactional' AND transactional_notification_id IS NULL`

// veridianSenderScopeSQL rend la condition qui designe "les envois de cet
// emetteur". Sans profil dans le contexte : le domaine emetteur seul (comme
// avant le lot 4). Avec un profil (le fusible de reputation le pose par
// domain.WithVeridianReputationProfile) : les envois de CE profil, plus les
// anciennes lignes sans attribution de profil (anterieures a V56) du meme
// domaine. Deux profils d'un meme domaine ne melangent plus leurs rejets.
// domainParam est le numero du placeholder deja porteur du domaine, nextParam le
// premier placeholder libre pour l'identifiant du profil.
func veridianSenderScopeSQL(ctx context.Context, domainParam, nextParam int) (string, []interface{}) {
	domainSQL := fmt.Sprintf("lower(split_part(veridian_sender_email, '@', 2)) = lower($%d)", domainParam)
	profile := domain.VeridianReputationProfileFromContext(ctx)
	if profile == "" {
		return domainSQL, nil
	}
	return fmt.Sprintf("(veridian_profile_id = $%d OR (COALESCE(veridian_profile_id, '') = '' AND %s))", nextParam, domainSQL), []interface{}{profile}
}
