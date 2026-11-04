package service

import (
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Fiche 79 (04/11/2026) : l'utilisateur d'un workspace, avec sa propre cle API, doit pouvoir
// gerer ses PROFILS D'ENVOI (SMTP/Gmail + boite IMAP liee) comme le fait l'admin.
//
// Regle (la plus restrictive qui satisfasse le besoin) :
//   - le owner du workspace garde tous les droits, inchange ;
//   - un membre (cle API comprise) peut creer et modifier une integration de type email
//     (transport SMTP uniquement) ou imap SI ET SEULEMENT SI il detient l'ecriture sur la
//     ressource "workspace" (une cle mintee en lecture seule ne l'a pas) ;
//   - supabase, llm, firecrawl, les fournisseurs HTTP (SES, SparkPost, Mailgun...) et la
//     SUPPRESSION restent reserves au owner ;
//   - un non-owner ne peut jamais rediriger un identifiant deja enregistre vers une autre
//     destination sans le re-saisir : changer hote, port ou identifiant d'un transport SMTP
//     ou IMAP exige le secret dans la meme requete (sinon le serveur enverrait le mot de passe
//     chiffre deja stocke a l'hote choisi par l'appelant).
//
// L'isolation entre workspaces n'est pas ici : AuthenticateUserForWorkspace refuse tout
// workspace dont l'appelant n'est pas membre (cle du workspace A sur le workspace B => 403).

const veridianIntegrationOwnerMessage = "user is not an owner of the workspace"

// veridianIntegrationWriteAllowed dit si uw peut creer/modifier une integration du type donne.
// integrationType vide = type encore inconnu : on ne verifie que le droit general (le type est
// controle des qu'il est connu).
func veridianIntegrationWriteAllowed(uw *domain.UserWorkspace, integrationType domain.IntegrationType) error {
	if uw == nil {
		return &domain.ErrUnauthorized{Message: veridianIntegrationOwnerMessage}
	}
	if uw.Role == "owner" {
		return nil
	}
	if uw.Role != "member" || !uw.HasPermission(domain.PermissionResourceWorkspace, domain.PermissionTypeWrite) {
		return &domain.ErrUnauthorized{Message: veridianIntegrationOwnerMessage}
	}
	switch integrationType {
	case "", domain.IntegrationTypeEmail, domain.IntegrationTypeIMAP:
		return nil
	}
	return &domain.ErrUnauthorized{Message: "only the workspace owner can manage " + string(integrationType) + " integrations"}
}

// veridianAuthorizeIntegrationCreate : droit de creation selon le type demande, et pour un
// non-owner transport SMTP uniquement.
func veridianAuthorizeIntegrationCreate(uw *domain.UserWorkspace, req *domain.CreateIntegrationRequest) error {
	if err := veridianIntegrationWriteAllowed(uw, req.Type); err != nil {
		return err
	}
	if uw.Role != "owner" && req.Type == domain.IntegrationTypeEmail && req.Provider.Kind != domain.EmailProviderKindSMTP {
		return &domain.ErrUnauthorized{Message: "only the workspace owner can manage non-SMTP email providers"}
	}
	return nil
}

// veridianAuthorizeIntegrationUpdate : type de l'integration EXISTANTE (le type ne change jamais),
// fournisseur SMTP uniquement pour un non-owner, et refus de re-lier un secret stocke a une
// nouvelle destination. Evalue sur la REQUETE (avant la preservation des secrets chiffres).
func veridianAuthorizeIntegrationUpdate(uw *domain.UserWorkspace, existing *domain.Integration, req *domain.UpdateIntegrationRequest, secretKey string) error {
	if existing == nil {
		return &domain.ErrUnauthorized{Message: veridianIntegrationOwnerMessage}
	}
	if err := veridianIntegrationWriteAllowed(uw, existing.Type); err != nil {
		return err
	}
	if uw.Role == "owner" {
		return nil
	}
	switch existing.Type {
	case domain.IntegrationTypeEmail:
		if existing.EmailProvider.Kind != domain.EmailProviderKindSMTP || req.Provider.Kind != domain.EmailProviderKindSMTP {
			return &domain.ErrUnauthorized{Message: "only the workspace owner can manage non-SMTP email providers"}
		}
		return veridianGuardSMTPRebind(req.Provider.SMTP, existing.EmailProvider.SMTP, secretKey)
	case domain.IntegrationTypeIMAP:
		return veridianGuardIMAPRebind(req.IMAPSettings, existing.IMAPSettings, secretKey)
	}
	return nil
}

const veridianRebindMessage = "re-enter the password when the host, port or username changes (stored credentials are never forwarded to a new destination)"

func veridianStoredSMTPUsername(s *domain.SMTPSettings, secretKey string) string {
	if s.Username != "" || s.EncryptedUsername == "" {
		return s.Username
	}
	clone := *s
	if err := clone.DecryptUsername(secretKey); err != nil {
		return "<undecryptable>" // inconnu : traite comme different (echec ferme)
	}
	return clone.Username
}

// veridianGuardSMTPRebind : next = requete, current = base. Un identifiant vide dans la requete
// signifie "inchange" (le serveur conserve alors le chiffre), donc seul un identifiant FOURNI et
// different compte comme un deplacement.
func veridianGuardSMTPRebind(next, current *domain.SMTPSettings, secretKey string) error {
	if next == nil || current == nil {
		return nil
	}
	moved := next.Host != current.Host || next.Port != current.Port ||
		(next.Username != "" && next.Username != veridianStoredSMTPUsername(current, secretKey)) ||
		(next.AuthType != "" && next.AuthType != current.AuthType) ||
		next.OAuth2Provider != current.OAuth2Provider || next.OAuth2TenantID != current.OAuth2TenantID ||
		next.OAuth2ClientID != current.OAuth2ClientID
	reuse := (next.Password == "" && next.EncryptedPassword == "" && current.EncryptedPassword != "") ||
		(next.OAuth2ClientSecret == "" && next.EncryptedOAuth2ClientSecret == "" && current.EncryptedOAuth2ClientSecret != "") ||
		(next.OAuth2RefreshToken == "" && next.EncryptedOAuth2RefreshToken == "" && current.EncryptedOAuth2RefreshToken != "")
	if moved && reuse {
		return domain.NewValidationError(veridianRebindMessage)
	}
	return nil
}

func veridianGuardIMAPRebind(next, current *domain.IMAPSettings, _ string) error {
	if next == nil || current == nil {
		return nil
	}
	moved := !strings.EqualFold(next.Host, current.Host) || next.Port != current.Port ||
		(next.Username != "" && next.Username != current.Username) || next.TLSServerName != current.TLSServerName
	reuse := next.Password == "" && next.EncryptedPassword == "" && current.EncryptedPassword != ""
	if moved && reuse {
		return domain.NewValidationError(veridianRebindMessage)
	}
	return nil
}
