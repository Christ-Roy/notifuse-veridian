package domain

import (
	"regexp"
	"strings"
)

// Veridian fork — empreinte de Message-ID (2026-09-28).
//
// Constat Robert (28/09, envoi réel coldtunnel/agence-veridian.fr) : le Message-ID
// RFC822 envoyé était `coldtunnel_fc75fbbd-...@agence-veridian.fr` — le nom du
// workspace en clair dans un en-tête, un tell de logiciel d'envoi de masse qu'aucun
// Thunderbird/Apple Mail ne produit. Cette valeur vient de message_history.id,
// construit ailleurs (queue_message_sender.go, message_sender.go,
// automation_node_executor.go) comme `<workspace_id>_<uuid>`. Le préfixe workspace
// N'EST PAS requis pour l'unicité en base (chaque workspace a sa propre base
// Postgres isolée, notifuse_ws_<id>) : il ne sert qu'à des besoins d'affichage/debug
// historiques. On NE TOUCHE PAS message_history.id (colonne déjà écrite en prod,
// beaucoup d'appelants) ; on découple seulement ce qui part sur le fil :
//   - à l'envoi (veridianMessageIDForSend) : on n'expose QUE l'UUID nu comme
//     local-part du header Message-ID.
//   - à la réception d'une réponse (FindContactEmailByMessageID) : on reconstruit
//     le <workspace_id>_<uuid> stocké avant le lookup exact-match.
// Testé : TestVeridianBareMessageUUID / TestVeridianReconstructStoredMessageID
// couvrent le format nominal, un id déjà nu, un id déjà préfixé (legacy) et un id
// qui ne ressemble pas à un UUID (fallback non-régression, jamais d'échec d'envoi
// ou de perte silencieuse d'un match de réponse pour cette raison).

// veridianUUIDPattern reconnaît un UUID canonique RFC4122 tel que produit par
// google/uuid (uuid.New().String()) : 36 caractères, hex minuscule, tirets en
// 8-4-4-4-12. C'est exactement la forme utilisée partout dans ce fork pour minter
// un message_history.id.
var veridianUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// VeridianBareMessageUUID retire un préfixe `<workspace_id>_` d'un id en forme
// message_history.id, pour ne garder que l'UUID final. Utilisé au moment de poser
// le header Message-ID RFC822 sortant : un client mail normal ne montre jamais de
// nom de tenant/workspace dans son Message-ID.
//
// Défensif : si les 36 derniers caractères ne sont pas un UUID bien formé, la
// valeur est retournée TELLE QUELLE (non-régression — jamais d'échec d'envoi pour
// cette raison, au pire l'ancien comportement légèrement plus verbeux persiste).
func VeridianBareMessageUUID(fullID string) string {
	const uuidLen = 36
	if len(fullID) < uuidLen {
		return fullID
	}
	candidate := fullID[len(fullID)-uuidLen:]
	if !veridianUUIDPattern.MatchString(candidate) {
		return fullID
	}
	return candidate
}

// VeridianReconstructStoredMessageID reconstruit la forme stockée en base
// (`<workspace_id>_<uuid>`) à partir d'un id potentiellement nu tel que cité par un
// prospect dans In-Reply-To/References (cf VeridianBareMessageUUID côté envoi).
//
// Trois cas, dans l'ordre :
//  1. déjà préfixé par CE workspace (`workspaceID + "_"`) → retourné tel quel
//     (compatibilité avec les lignes écrites AVANT ce changement, ou un appelant
//     qui passerait déjà la forme complète) ;
//  2. UUID nu bien formé → reconstruit en `workspaceID + "_" + messageID` ;
//  3. autre chose (vide, forme inattendue) → retourné tel quel ; le lookup exact-
//     match qui suit ne trouvera simplement rien (best-effort, jamais d'erreur).
func VeridianReconstructStoredMessageID(workspaceID, messageID string) string {
	if messageID == "" || workspaceID == "" {
		return messageID
	}
	if strings.HasPrefix(messageID, workspaceID+"_") {
		return messageID
	}
	if !veridianUUIDPattern.MatchString(messageID) {
		return messageID
	}
	return workspaceID + "_" + messageID
}
