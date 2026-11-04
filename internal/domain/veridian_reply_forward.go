package domain

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// === Veridian patch : redirection des reponses de prospects par profil (04/11/2026) ===
//
// Besoin (Robert, 04/11) : le prospect repond a l adresse SMTP d envoi, mais la reponse
// peut etre routee vers l adresse que l on veut (typiquement le mail du client lui-meme).
// Reglage PAR PROFIL D ENVOI, dans le JSON du fournisseur (EmailProvider
// .VeridianReplyForwardTo), donc sans migration du blob integrations. Vide (defaut) = AUCUN
// changement de comportement. Le service qui l execute est
// internal/service/veridian_reply_forward.go ; la table de journal/idempotence est creee
// par la migration V63 (veridian_reply_forward).

const (
	// VeridianReplyForwardMaxRecipients borne la liste (un profil, pas une liste de diffusion).
	VeridianReplyForwardMaxRecipients = 5
	// VeridianReplyForwardDailyCap : plafond de copies par profil et par jour (anti-boucle
	// d envoi). Compte toutes les tentatives du jour glissant de 24 h, echecs compris.
	VeridianReplyForwardDailyCap = 50
	// VeridianReplyForwardHeader trace une copie (et sert de garde anti-boucle en entree).
	VeridianReplyForwardHeader = "X-Veridian-Forwarded"
)

// Statuts du journal veridian_reply_forward.
const (
	VeridianReplyForwardStatusPending = "pending"
	VeridianReplyForwardStatusSent    = "sent"
	VeridianReplyForwardStatusFailed  = "failed"
)

// VeridianNormalizeReplyForwardList normalise une liste d adresses : trim, minuscules,
// doublons retires (ordre conserve). Rend une erreur sur la premiere adresse invalide :
// seule une adresse nue (pas de nom affiche, pas de chevrons, pas de saut de ligne) est
// acceptee, pour que ce qui est stocke soit exactement ce qui recevra le courrier.
func VeridianNormalizeReplyForwardList(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		a := strings.ToLower(strings.TrimSpace(raw))
		if a == "" {
			continue
		}
		if len(a) > 254 || strings.ContainsAny(a, "\r\n\t <>,;\"") {
			return nil, fmt.Errorf("veridian_reply_forward_to: invalid email address %q", truncateForError(raw))
		}
		parsed, err := mail.ParseAddress(a)
		if err != nil || parsed.Address != a || parsed.Name != "" {
			return nil, fmt.Errorf("veridian_reply_forward_to: invalid email address %q", truncateForError(raw))
		}
		at := strings.LastIndexByte(a, 0x40)
		if at < 1 || !strings.Contains(a[at+1:], ".") {
			return nil, fmt.Errorf("veridian_reply_forward_to: invalid email address %q", truncateForError(raw))
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out, nil
}

func truncateForError(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	if len(s) > 60 {
		return s[:60]
	}
	return s
}

// ValidateVeridianReplyForwardTo valide (et normalise en place) la liste : au plus 5
// adresses valides, jamais l adresse d envoi du profil (boucle).
func (e *EmailProvider) ValidateVeridianReplyForwardTo() error {
	if len(e.VeridianReplyForwardTo) == 0 {
		return nil
	}
	// Plafond sur la liste BRUTE (avant dedoublonnage) : un refus net vaut mieux
	// qu une troncature silencieuse.
	if len(e.VeridianReplyForwardTo) > VeridianReplyForwardMaxRecipients {
		return fmt.Errorf("veridian_reply_forward_to: at most %d addresses (got %d)",
			VeridianReplyForwardMaxRecipients, len(e.VeridianReplyForwardTo))
	}
	list, err := VeridianNormalizeReplyForwardList(e.VeridianReplyForwardTo)
	if err != nil {
		return err
	}
	for _, a := range list {
		if e.VeridianIsOwnSenderAddress(a) {
			return fmt.Errorf("veridian_reply_forward_to: %q is a sending address of this profile (loop)", a)
		}
	}
	if len(list) == 0 {
		e.VeridianReplyForwardTo = nil
		return nil
	}
	e.VeridianReplyForwardTo = list
	return nil
}

// VeridianIsOwnSenderAddress dit si addr est une adresse d envoi du profil (SMTP
// username compris : sur un profil Gmail c est l adresse reelle de la boite).
func (e *EmailProvider) VeridianIsOwnSenderAddress(addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if addr == "" || e == nil {
		return false
	}
	for _, s := range e.Senders {
		if strings.ToLower(strings.TrimSpace(s.Email)) == addr {
			return true
		}
	}
	if e.SMTP != nil && strings.ToLower(strings.TrimSpace(e.SMTP.Username)) == addr {
		return true
	}
	return false
}

// VeridianEffectiveReplyForwardTo rend la liste utilisable a l execution (nil-safe) :
// normalisee, sans adresse invalide ni adresse d envoi du profil, bornee a 5. Vide si le
// profil n a rien configure : l appelant ne fait alors strictement rien.
func (e *EmailProvider) VeridianEffectiveReplyForwardTo() []string {
	if e == nil || len(e.VeridianReplyForwardTo) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range e.VeridianReplyForwardTo {
		list, err := VeridianNormalizeReplyForwardList([]string{raw})
		if err != nil || len(list) == 0 {
			continue
		}
		a := list[0]
		if seen[a] || e.VeridianIsOwnSenderAddress(a) {
			continue
		}
		seen[a] = true
		out = append(out, a)
		if len(out) == VeridianReplyForwardMaxRecipients {
			break
		}
	}
	return out
}

// VeridianReplyForwardRecord est une ligne du journal veridian_reply_forward : aucune
// adresse, aucun contenu, seulement de quoi dedoublonner, plafonner et compter.
type VeridianReplyForwardRecord struct {
	ReplyKey   string // PK : empreinte de la reponse (idempotence)
	ProfileID  string
	Recipients int
	Status     string
	ErrorClass string
}

// VeridianSentMessageRef est ce que l on retient de l envoi auquel une reponse se rattache.
type VeridianSentMessageRef struct {
	ProfileID   string
	SenderEmail string
}

// VeridianReplyForwardStat est le compteur par profil et statut (controle).
type VeridianReplyForwardStat struct {
	ProfileID string
	Status    string
	Count     int
}

//go:generate mockgen -destination mocks/mock_veridian_reply_forward_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianReplyForwardRepository

// VeridianReplyForwardRepository : persistance du journal (table WORKSPACE, V63).
type VeridianReplyForwardRepository interface {
	// ResolveSentMessage rattache la reponse a un de NOS envois : par id de message cite
	// (matchedMessageID, forme nue ou prefixee) sinon, en repli, le dernier envoi accepte
	// vers contactEmail. found=false si rien (jamais une erreur).
	ResolveSentMessage(ctx context.Context, workspaceID, matchedMessageID, contactEmail string) (ref VeridianSentMessageRef, found bool, err error)
	// Claim reserve atomiquement la copie : inserted=true si la ligne vient d etre posee ;
	// duplicate=true si la reponse a deja ete traitee ; capped=true si le profil a atteint
	// dailyCap tentatives sur les 24 dernieres heures (rien n est insere).
	Claim(ctx context.Context, workspaceID string, rec VeridianReplyForwardRecord, dailyCap int, now time.Time) (inserted, duplicate, capped bool, err error)
	// Finish pose le resultat (sent / failed) de la reservation.
	Finish(ctx context.Context, workspaceID, replyKey, status, errorClass string, now time.Time) error
	// Stats compte les copies par profil et statut depuis since (controle).
	Stats(ctx context.Context, workspaceID string, since time.Time) ([]VeridianReplyForwardStat, error)
}
