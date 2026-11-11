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
	// VeridianReplyForwardDefaultDailyCap : plafond par defaut de copies par profil et par jour glissant
	// de 24 h (fusible anti-boucle d envoi), reglable par profil (veridian_reply_forward_daily_cap).
	// Au-dela, la copie n est PAS perdue : elle passe en `deferred` et part des qu une place se libere.
	VeridianReplyForwardDefaultDailyCap = 500
	// VeridianReplyForwardMaxDailyCap : maximum accepte a l ecriture.
	VeridianReplyForwardMaxDailyCap = 5000
	// VeridianReplyForwardLease : duree pendant laquelle une copie prise a bail est a l envoi ; passe ce delai
	// sans issue (processus mort), elle est reprise.
	VeridianReplyForwardLease = 5 * time.Minute
	// VeridianReplyForwardHeader trace une copie (et sert de garde anti-boucle en entree).
	VeridianReplyForwardHeader = "X-Veridian-Forwarded"
)

// Statuts du journal veridian_reply_forward.
const (
	VeridianReplyForwardStatusPending  = "pending"  // a l envoi maintenant (bail)
	VeridianReplyForwardStatusRetry    = "retry"    // echec transitoire, rejeu a echeance
	VeridianReplyForwardStatusDeferred = "deferred" // plafond atteint, en attente d une place
	VeridianReplyForwardStatusSent     = "sent"
	VeridianReplyForwardStatusFailed   = "failed" // echec definitif ou tentatives epuisees
)

// VeridianReplyForwardRetryDelays : attente avant chaque rejeu d un echec transitoire (1 min, 5 min,
// 30 min, 2 h, 6 h, 12 h) : une premiere tentative + 6 rejeux au plus, puis `failed`.
var VeridianReplyForwardRetryDelays = []time.Duration{
	time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 12 * time.Hour,
}

type veridianExtraHeadersKey struct{}

// WithVeridianExtraHeaders porte des en-tetes X-Veridian-* jusqu au transport SMTP (par le contexte :
// SendEmailProviderRequest vit dans email_provider.go, fichier gele). Le transport ne pose que les
// noms prefixes X-Veridian- et les valeurs sur une seule ligne.
func WithVeridianExtraHeaders(ctx context.Context, h map[string]string) context.Context {
	return context.WithValue(ctx, veridianExtraHeadersKey{}, h)
}

// VeridianExtraHeadersFromContext lit les en-tetes poses par WithVeridianExtraHeaders (nil si aucun).
func VeridianExtraHeadersFromContext(ctx context.Context) map[string]string {
	h, _ := ctx.Value(veridianExtraHeadersKey{}).(map[string]string)
	return h
}

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
	if e.VeridianReplyForwardDailyCap < 0 || e.VeridianReplyForwardDailyCap > VeridianReplyForwardMaxDailyCap {
		return NewValidationError(fmt.Sprintf("veridian_reply_forward_daily_cap: must be between 1 and %d (0 = default %d), got %d",
			VeridianReplyForwardMaxDailyCap, VeridianReplyForwardDefaultDailyCap, e.VeridianReplyForwardDailyCap))
	}
	if len(e.VeridianReplyForwardTo) == 0 {
		return nil
	}
	if err := e.validateVeridianReplyForwardTo(); err != nil {
		return NewValidationError(err.Error())
	}
	return nil
}

func (e *EmailProvider) validateVeridianReplyForwardTo() error {
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

// VeridianEffectiveReplyForwardDailyCap rend le plafond de copies par jour glissant du profil : sa valeur
// si elle est dans [1, maximum], sinon le defaut (500). Nil-safe.
func (e *EmailProvider) VeridianEffectiveReplyForwardDailyCap() int {
	if e == nil || e.VeridianReplyForwardDailyCap <= 0 || e.VeridianReplyForwardDailyCap > VeridianReplyForwardMaxDailyCap {
		return VeridianReplyForwardDefaultDailyCap
	}
	return e.VeridianReplyForwardDailyCap
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

// VeridianReplyForwardRecord est une ligne du journal veridian_reply_forward. Les adresses et le
// corps ne sont conserves (Payload) que le temps que la copie n'est pas partie : le rejeu en a
// besoin. Des que la ligne est `sent` ou `failed`, le contenu est efface.
type VeridianReplyForwardRecord struct {
	ReplyKey   string // PK : empreinte de la reponse (idempotence)
	ProfileID  string
	Recipients int
	Status     string
	ErrorClass string
	Payload    string // JSON de la copie a envoyer (efface une fois sent/failed)
}

// VeridianReplyForwardClaim est le resultat de la reservation d'une copie.
type VeridianReplyForwardClaim struct {
	// Duplicate : la reponse a deja ete traitee (le poller l'a relue), rien a faire.
	Duplicate bool
	// Leased : la copie a une place sous le plafond et est confiee a l'appelant (1re tentative).
	Leased bool
	// Deferred : plafond du profil atteint ; la copie est gardee (statut deferred) et partira
	// dans l'ordre des que le plafond le permet. Jamais perdue.
	Deferred bool
	Attempts int
}

// VeridianReplyForwardJob est une copie a envoyer (ou a rejouer), prise a bail.
type VeridianReplyForwardJob struct {
	ReplyKey  string
	ProfileID string
	Payload   string
	Attempts  int // numero de la tentative en cours (1 = premiere)
}

// VeridianReplyForwardStat est le compteur par profil et statut (controle).
type VeridianReplyForwardStat struct {
	ProfileID string
	Status    string
	Count     int
}

// VeridianReplyForwardProfileStats : ce que l'overview dit d'un profil (aucune adresse).
type VeridianReplyForwardProfileStats struct {
	ProfileID          string
	Sent24h            int
	Pending            int
	Retry              int
	Deferred           int
	Failed7d           int
	DominantErrorClass string
	OldestUnsentAt     *time.Time
}

// VeridianReplyForwardOverview est la visibilite exposee par emailProfiles.overview : combien
// de copies sont parties, attendent ou ont echoue, et depuis quand la plus ancienne attend.
type VeridianReplyForwardOverview struct {
	Enabled  bool `json:"enabled"`
	DailyCap int  `json:"daily_cap"`
	// StatsKnown=false : la lecture du journal a echoue, les compteurs ne sont PAS des zeros.
	StatsKnown             bool       `json:"stats_known"`
	Sent24h                int        `json:"sent_24h"`
	Pending                int        `json:"pending"`
	Retry                  int        `json:"retry"`
	Deferred               int        `json:"deferred"`
	Waiting                int        `json:"waiting"` // pending + retry + deferred
	Failed7d               int        `json:"failed_7d"`
	DominantErrorClass     string     `json:"dominant_error_class,omitempty"`
	OldestUnsentAt         *time.Time `json:"oldest_unsent_at"`
	OldestUnsentAgeSeconds int64      `json:"oldest_unsent_age_seconds"`
}

// VeridianBuildReplyForwardOverview assemble la vue d'un profil. stats nil avec known=true : aucune
// ligne dans le journal (zeros reels) ; known=false : mesure impossible.
func VeridianBuildReplyForwardOverview(provider *EmailProvider, stats *VeridianReplyForwardProfileStats, known bool, now time.Time) *VeridianReplyForwardOverview {
	enabled := len(provider.VeridianEffectiveReplyForwardTo()) > 0
	if !enabled && (stats == nil || !known) {
		return nil
	}
	o := &VeridianReplyForwardOverview{Enabled: enabled, DailyCap: provider.VeridianEffectiveReplyForwardDailyCap(), StatsKnown: known}
	if !known || stats == nil {
		return o
	}
	o.Sent24h, o.Pending, o.Retry, o.Deferred, o.Failed7d = stats.Sent24h, stats.Pending, stats.Retry, stats.Deferred, stats.Failed7d
	o.Waiting = stats.Pending + stats.Retry + stats.Deferred
	o.DominantErrorClass = stats.DominantErrorClass
	if stats.OldestUnsentAt != nil {
		t := *stats.OldestUnsentAt
		o.OldestUnsentAt = &t
		if age := now.Sub(t); age > 0 {
			o.OldestUnsentAgeSeconds = int64(age.Seconds())
		}
	}
	if !enabled && o.Waiting == 0 && o.Failed7d == 0 && o.Sent24h == 0 {
		return nil
	}
	return o
}

//go:generate mockgen -destination mocks/mock_veridian_reply_forward_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianReplyForwardRepository

// VeridianReplyForwardRepository : persistance du journal (table WORKSPACE, V69 + V74).
type VeridianReplyForwardRepository interface {
	// ResolveSentMessage rattache la reponse a un de NOS envois : par id de message cite
	// (matchedMessageID, forme nue ou prefixee) sinon, en repli, le dernier envoi accepte
	// vers contactEmail. found=false si rien (jamais une erreur).
	ResolveSentMessage(ctx context.Context, workspaceID, matchedMessageID, contactEmail string) (ref VeridianSentMessageRef, found bool, err error)
	// Claim pose la copie dans le journal (idempotent sur reply_key) puis lui donne une place
	// sous le plafond du profil dans l'ordre d'arrivee. Au-dela du plafond la copie reste
	// `deferred` : rien n'est jamais perdu.
	Claim(ctx context.Context, workspaceID string, rec VeridianReplyForwardRecord, dailyCap int, now time.Time) (VeridianReplyForwardClaim, error)
	// LeaseDue promeut les copies deferred qui ont maintenant une place (caps par profil, defaut
	// defaultCap), abandonne les lignes sans contenu a rejouer, puis prend a bail (compare-and-set)
	// jusqu'a limit copies dues : retry arrive a echeance, ou bail expire (processus mort).
	LeaseDue(ctx context.Context, workspaceID string, caps map[string]int, defaultCap int, now time.Time, limit int) ([]VeridianReplyForwardJob, error)
	// Finish pose l'issue definitive (sent / failed) et efface le contenu.
	Finish(ctx context.Context, workspaceID, replyKey, status, errorClass string, now time.Time) error
	// Reschedule remet la copie en `retry` a l'echeance next, avec la classe de la derniere erreur.
	Reschedule(ctx context.Context, workspaceID, replyKey, errorClass string, next time.Time) error
	// Stats compte les copies par profil et statut depuis since (controle).
	Stats(ctx context.Context, workspaceID string, since time.Time) ([]VeridianReplyForwardStat, error)
	// ProfileStats rend les compteurs d'overview par profil (envoyees sur 24 h, en attente, echecs 7 j).
	ProfileStats(ctx context.Context, workspaceID string, now time.Time) (map[string]VeridianReplyForwardProfileStats, error)
}

// VeridianSentMessageRef est ce que l on retient de l envoi auquel une reponse se rattache.
type VeridianSentMessageRef struct {
	ProfileID   string
	SenderEmail string
}
