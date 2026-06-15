package broadcast

import (
	"context"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian — sélection du sender d'envoi avec ROTATION multi-SMTP (cold
// outbound), côté senders broadcast. Centralise la logique partagée entre le
// queue sender (buildQueueEntry) et le sender direct (SendToRecipient) :
//
//  1. Détecter si on est dans le tunnel cold (tag contact / config broadcast /
//     config workspace) — via domain.VeridianIsColdContext. Le workspace est
//     récupéré MÉMOÏSÉ depuis le pixel resolver déjà en place (zéro I/O
//     supplémentaire par recipient).
//  2. Hors cold OU rotator absent → retour direct GetSender (comportement
//     upstream strictement inchangé).
//  3. En cold → résoudre la classe du destinataire (tag contact sinon
//     classification par suffixe, fonction pure zéro I/O — la précision MX sert
//     au throttle/cap, pas à la répartition sender) puis round-robin par classe
//     via le rotator partagé (domain.EmailProvider.VeridianSelectSender).
//
// La précédence du SenderID explicite du template est gérée par
// VeridianSelectSender (un template qui force son expéditeur garde la main).

// veridianResolveSender choisit le sender pour cet envoi. rotator peut être nil
// (rotation désactivée → upstream). pixelResolver fournit le workspace mémoïsé
// pour la détection de contexte cold (peut être nil → détection sans workspace).
// Retourne le sender choisi, ou nil si l'infra n'a aucun sender utilisable
// (l'appelant gère l'erreur comme avec GetSender).
func veridianResolveSender(
	ctx context.Context,
	rotator *domain.VeridianSenderRotator,
	pixelResolver *veridianWorkspacePixelResolver,
	workspaceID string,
	integrationID string,
	emailProvider *domain.EmailProvider,
	templateSenderID string,
	contact *domain.Contact,
	email string,
	broadcast *domain.Broadcast,
) *domain.EmailSender {
	// Pas de rotation possible : retour upstream immédiat (zéro overhead).
	if rotator == nil || emailProvider.VeridianActiveSenderCount() <= 1 {
		return emailProvider.GetSender(templateSenderID)
	}

	// Workspace mémoïsé (pour la détection de contexte cold quand la config est
	// posée au seul niveau workspace). nil-safe.
	var workspace *domain.Workspace
	if pixelResolver != nil {
		workspace = pixelResolver.workspace(ctx, workspaceID)
	}

	// Hors tunnel cold : rotation désactivée, comportement upstream.
	if !domain.VeridianIsColdContext(contact, broadcast, workspace) {
		return emailProvider.GetSender(templateSenderID)
	}

	// Classe du destinataire pour clé de rotation : tag contact (option B) sinon
	// classification par suffixe (pure, zéro I/O). On ne fait PAS de lookup MX
	// ici : la répartition round-robin n'a pas besoin de la précision MX, qui est
	// réservée au throttle/cap sur le hot path worker.
	class := domain.VeridianContactProviderClass(contact)
	if class == "" {
		class = domain.ClassifyProviderClass(email)
	}

	return emailProvider.VeridianSelectSender(rotator, integrationID, templateSenderID, class)
}
