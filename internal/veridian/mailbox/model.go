// Package mailbox est la « boîte unifiée » Veridian (lot L1, 29/10/2026) : chaque mail
// envoyé et reçu par un workspace est gardé en entier (en-têtes, texte, HTML assaini,
// pièces jointes, accusé de non-remise décodé), rangé dans son fil de conversation, et
// exposé par API comme dans un client IMAP.
//
// Principe de panne : Notifuse LIT les boîtes IMAP des relais avec un repère (dernier UID
// traité par boîte), il n'est pas dans le chemin du courrier. S'il tombe, rien n'est perdu :
// au redémarrage le rattrapage reprend à l'UID suivant.
//
// Les corps (texte, HTML, message brut) sont chiffrés au repos avec la clé du workspace,
// comme message_history.message_data. Aucun contenu de mail n'entre jamais dans un log.
//
// Contrat de données : docs/veridian/BOITE-UNIFIEE.md (et plateforme/DESIGN-ET-ROADMAP.md 3.2).
package mailbox

import (
	"time"
)

// Direction : sens du mail vu de Notifuse.
type Direction string

const (
	DirectionOut Direction = "out"
	DirectionIn  Direction = "in"
)

// Kind : nature du mail.
type Kind string

const (
	// KindSent : mail sortant accepté par le relais.
	KindSent Kind = "sent"
	// KindReplyHuman : un humain a répondu.
	KindReplyHuman Kind = "reply_human"
	// KindReplyAuto : auto-répondeur, absence, défi ou filtre anti-spam.
	KindReplyAuto Kind = "reply_auto"
	// KindBounceHard : rebond définitif (DSN 5.x.x).
	KindBounceHard Kind = "bounce_hard"
	// KindBounceSoft : rebond temporaire (DSN 4.x.x ou code absent).
	KindBounceSoft Kind = "bounce_soft"
	// KindDSN : rapport de livraison qui n'est pas un rebond (retardé, relayé, délivré).
	KindDSN Kind = "dsn"
	// KindOther : tout le reste (mail sans lien avec un envoi).
	KindOther Kind = "other"
)

// ValidKind dit si k est une valeur connue (validation des filtres d'API).
func ValidKind(k Kind) bool {
	switch k {
	case KindSent, KindReplyHuman, KindReplyAuto, KindBounceHard, KindBounceSoft, KindDSN, KindOther:
		return true
	}
	return false
}

// BodySource : d'où vient le corps d'un mail sortant.
const (
	// BodyCaptured : rendu exact capturé au moment de l'envoi par le worker.
	BodyCaptured = "captured"
	// BodyRerendered : reconstitué depuis le modèle et ses données (historique antérieur à L1).
	BodyRerendered = "rerendered"
	// BodyUnavailable : le corps n'est pas disponible (historique sans modèle exploitable).
	BodyUnavailable = "unavailable"
	// BodyReceived : mail reçu, corps lu dans le message brut.
	BodyReceived = "received"
)

// MailboxRef identifie un mail dans une boîte IMAP (rattrapage par UID).
type MailboxRef struct {
	IntegrationID string `json:"integration_id"`
	Folder        string `json:"folder"`
	UIDValidity   uint32 `json:"uid_validity"`
	UID           uint32 `json:"uid"`
}

// Key est la clé de déduplication d'un mail dans sa boîte.
func (r MailboxRef) Key() string {
	return r.IntegrationID + "|" + r.Folder + "|" + utoa(r.UIDValidity) + "|" + utoa(r.UID)
}

// Attachment décrit une pièce jointe (le contenu se lit à la demande depuis le message brut).
type Attachment struct {
	// Index : rang de la pièce dans le message (stable, sert à la relire).
	Index       int    `json:"index"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	// SHA256 : empreinte hexadécimale du contenu.
	SHA256 string `json:"sha256,omitempty"`
	// Inline : image ou ressource incorporée au corps HTML (cid:).
	Inline    bool   `json:"inline,omitempty"`
	ContentID string `json:"content_id,omitempty"`
	// Available : le contenu peut être relu (message brut conservé).
	Available bool `json:"available"`
}

// DSN est le rapport de non-remise décodé.
type DSN struct {
	// Code : code de statut enrichi (RFC 3463), par exemple 5.1.1.
	Code string `json:"code,omitempty"`
	// Severity : hard, soft ou unknown.
	Severity string `json:"severity,omitempty"`
	// Action : failed, delayed, delivered, relayed, expanded.
	Action string `json:"action,omitempty"`
	// Diagnostic : réponse du serveur distant, tronquée (jamais de contenu du mail d'origine).
	Diagnostic string `json:"diagnostic,omitempty"`
	// Recipient : l'adresse en échec.
	Recipient string `json:"recipient,omitempty"`
	// Provider : fournisseur ou passerelle qui a répondu (Gmail, Orange, OVH, Microsoft...).
	Provider string `json:"provider,omitempty"`
	// ReasonFR : la raison en français courant.
	ReasonFR string `json:"reason_fr"`
	// AdviceFR : ce que ça implique pour la prospection.
	AdviceFR string `json:"advice_fr,omitempty"`
	// Category : adresse_inexistante, boite_pleine, refus_politique, greylisting, ...
	Category string `json:"category,omitempty"`
	// OriginalMessageID : Message-ID du mail d'origine, s'il est cité.
	OriginalMessageID string `json:"original_message_id,omitempty"`
}

// Link relie un mail à ce qui l'a produit côté Notifuse.
type Link struct {
	SentMessageID string `json:"sent_message_id,omitempty"`
	AutomationID  string `json:"automation_id,omitempty"`
	NodeID        string `json:"node_id,omitempty"`
	BroadcastID   string `json:"broadcast_id,omitempty"`
	// MatchType : message_id (référence), sender (expéditeur connu), subject (objet).
	MatchType string `json:"match_type,omitempty"`
}

// Message est un mail entrant ou sortant, corps en clair (en mémoire seulement).
type Message struct {
	ID          string    `json:"id"`
	Seq         int64     `json:"seq"`
	Direction   Direction `json:"direction"`
	Kind        Kind      `json:"kind"`
	WorkspaceID string    `json:"workspace_id"`
	ProfileID   string    `json:"profile_id,omitempty"`
	// Mailbox : boîte source et UID IMAP (entrants). nil pour les sortants.
	Mailbox *MailboxRef `json:"mailbox_ref,omitempty"`

	MessageID  string   `json:"message_id,omitempty"`
	InReplyTo  string   `json:"in_reply_to,omitempty"`
	References []string `json:"references,omitempty"`
	ThreadID   string   `json:"thread_id"`

	ContactEmail string   `json:"contact_email"`
	From         string   `json:"from"`
	FromName     string   `json:"from_name,omitempty"`
	To           []string `json:"to"`
	Cc           []string `json:"cc,omitempty"`
	Subject      string   `json:"subject"`

	SentAt     *time.Time `json:"sent_at,omitempty"`
	ReceivedAt *time.Time `json:"received_at,omitempty"`
	// OccurredAt : received_at pour un entrant, sent_at pour un sortant (tri chronologique).
	OccurredAt time.Time `json:"occurred_at"`

	Headers map[string][]string `json:"headers,omitempty"`

	BodyText          string `json:"body_text,omitempty"`
	BodyHTMLSanitized string `json:"body_html_sanitized,omitempty"`
	BodySource        string `json:"body_source,omitempty"`
	// RawEML : message RFC 822 brut (entrants), nil si trop gros ou sortant.
	RawEML    []byte `json:"-"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	// Truncated : le message brut a été lu tronqué (taille plafonnée).
	Truncated bool `json:"truncated,omitempty"`

	Attachments []Attachment `json:"attachments,omitempty"`
	DSN         *DSN         `json:"dsn,omitempty"`
	Link        *Link        `json:"link,omitempty"`

	// SentRef : id message_history du mail sortant (clé d'idempotence de la capture).
	SentRef string `json:"-"`

	HandledAt *time.Time `json:"handled_at,omitempty"`
	HandledBy string     `json:"handled_by,omitempty"`
	Labels    []string   `json:"labels,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Summary est la ligne de liste (sans corps entier, avec un aperçu).
type Summary struct {
	ID           string     `json:"id"`
	Seq          int64      `json:"seq"`
	Direction    Direction  `json:"direction"`
	Kind         Kind       `json:"kind"`
	ProfileID    string     `json:"profile_id,omitempty"`
	ThreadID     string     `json:"thread_id"`
	ContactEmail string     `json:"contact_email"`
	From         string     `json:"from"`
	To           []string   `json:"to"`
	Subject      string     `json:"subject"`
	Preview      string     `json:"preview,omitempty"`
	OccurredAt   time.Time  `json:"occurred_at"`
	SentAt       *time.Time `json:"sent_at,omitempty"`
	ReceivedAt   *time.Time `json:"received_at,omitempty"`
	HasAttach    bool       `json:"has_attachments"`
	AttachCount  int        `json:"attachment_count"`
	DSNReasonFR  string     `json:"dsn_reason_fr,omitempty"`
	HandledAt    *time.Time `json:"handled_at,omitempty"`
	HandledBy    string     `json:"handled_by,omitempty"`
	Labels       []string   `json:"labels,omitempty"`
}

// Mailbox est une boîte : une par profil d'envoi et par adresse.
type Mailbox struct {
	// ID : identifiant stable (id de l'intégration IMAP, sinon id du profil).
	ID            string `json:"id"`
	Address       string `json:"address"`
	ProfileID     string `json:"profile_id,omitempty"`
	ProfileName   string `json:"profile_name,omitempty"`
	IntegrationID string `json:"integration_id,omitempty"`
	Folder        string `json:"folder,omitempty"`
	// Compteurs lus en base.
	CountIn     int        `json:"count_in"`
	CountOut    int        `json:"count_out"`
	CountUnread int        `json:"count_unhandled"`
	LastMessage *time.Time `json:"last_message_at,omitempty"`
	// État de la synchronisation IMAP.
	Sync *SyncState `json:"sync,omitempty"`
}

// SyncState : repère de lecture d'une boîte IMAP.
type SyncState struct {
	UIDValidity   uint32     `json:"uid_validity"`
	LastUID       uint32     `json:"last_uid"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	LastOKAt      *time.Time `json:"last_ok_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	TotalIngested int64      `json:"total_ingested"`
}

// Thread est un fil de conversation, messages dans l'ordre chronologique.
type Thread struct {
	ThreadID     string      `json:"thread_id"`
	Subject      string      `json:"subject"`
	ContactEmail string      `json:"contact_email"`
	FirstAt      time.Time   `json:"first_at"`
	LastAt       time.Time   `json:"last_at"`
	Count        int         `json:"message_count"`
	Messages     []Message   `json:"messages,omitempty"`
	Summaries    []Summary   `json:"summaries,omitempty"`
	LastKind     Kind        `json:"last_kind,omitempty"`
	Milestones   []Milestone `json:"milestones,omitempty"`
}

// Milestone : étape lisible d'un fil (J0, relance 1, réponse, rebond...).
type Milestone struct {
	MessageID string    `json:"id"`
	Label     string    `json:"label"`
	Kind      Kind      `json:"kind"`
	At        time.Time `json:"at"`
}

func utoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
