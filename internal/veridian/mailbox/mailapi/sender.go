package mailapi

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/google/uuid"
)

// Limites d'un envoi par API (un seul destinataire principal, comme un client mail).
const (
	maxSubject  = 300
	maxHTMLSize = 512 << 10
	maxTextSize = 256 << 10
	maxCc       = 5
)

// EnqueueRepo est la file d'envoi de Notifuse.
type EnqueueRepo interface {
	Enqueue(ctx context.Context, workspaceID string, entries []*domain.EmailQueueEntry) error
}

// QueueSender met le mail dans la MÊME file que les séquences : le worker lui applique les mêmes
// portes (fusible de réputation, plafonds du profil, de la classe de destinataire et de
// l'adresse émettrice, chauffe, fenêtre d'envoi, cadence) puis l'envoie par le relais du profil.
// Aucune voie parallèle : un mail envoyé par l'API compte dans les plafonds du jour.
type QueueSender struct {
	Queue EnqueueRepo
	WS    WorkspaceGetter
	Store *mailbox.Store
	Now   func() time.Time
}

// SendMail valide, vérifie les droits d'écriture et met en file (messages.send, messages.reply).
func (s *Service) SendMail(ctx context.Context, req SendRequest) (*SendResult, error) {
	if s.Sender == nil {
		return nil, ErrNotImplemented
	}
	if req.Workspace == "" {
		return nil, invalid("workspace_id requis")
	}
	ctx, actor, err := s.Auth.Authorize(ctx, req.Workspace, domain.PermissionResourceMessageHistory, domain.PermissionTypeWrite)
	if err != nil {
		return nil, err
	}
	ctx, _, err = s.Auth.Authorize(ctx, req.Workspace, domain.PermissionResourceTransactional, domain.PermissionTypeWrite)
	if err != nil {
		return nil, err
	}
	return s.Sender.Send(ctx, req, actor)
}

// Send implémente Sender.
func (q *QueueSender) Send(ctx context.Context, req SendRequest, actor Actor) (*SendResult, error) {
	now := time.Now
	if q.Now != nil {
		now = q.Now
	}
	w, err := q.WS.GetByID(ctx, req.Workspace)
	if err != nil {
		return nil, fmt.Errorf("lecture du workspace: %w", err)
	}

	var parent *mailbox.Message
	if req.ReplyToMessage != "" {
		parent, err = q.Store.Get(ctx, req.Workspace, req.ReplyToMessage)
		if err != nil {
			return nil, err
		}
		if parent.Direction != mailbox.DirectionIn {
			return nil, invalid("on répond à un message reçu, pas à un message envoyé")
		}
	}

	// Destinataire.
	to := strings.TrimSpace(req.To)
	if to == "" && parent != nil {
		to = replyAddress(parent)
	}
	addr, err := mail.ParseAddress(to)
	if err != nil || addr.Address == "" || strings.ContainsAny(addr.Address, "\r\n") {
		return nil, invalid("destinataire invalide")
	}
	toAddr := strings.ToLower(addr.Address)
	var cc []string
	if len(req.Cc) > maxCc {
		return nil, invalid("%d copies au plus", maxCc)
	}
	for _, c := range req.Cc {
		a, err := mail.ParseAddress(strings.TrimSpace(c))
		if err != nil || strings.ContainsAny(a.Address, "\r\n") {
			return nil, invalid("copie invalide %q", c)
		}
		cc = append(cc, strings.ToLower(a.Address))
	}

	// Contenu.
	subject := strings.TrimSpace(req.Subject)
	if subject == "" && parent != nil {
		subject = "Re: " + strings.TrimSpace(mailbox.StripReplyPrefix(parent.Subject))
	}
	if subject == "" || len(subject) > maxSubject || strings.ContainsAny(subject, "\r\n") {
		return nil, invalid("objet requis (%d caractères au plus, sur une ligne)", maxSubject)
	}
	if strings.TrimSpace(req.Text) == "" && strings.TrimSpace(req.HTML) == "" {
		return nil, invalid("text ou html requis")
	}
	if len(req.HTML) > maxHTMLSize || len(req.Text) > maxTextSize {
		return nil, invalid("contenu trop gros")
	}

	// Profil d'envoi.
	profileID := req.ProfileID
	if profileID == "" && parent != nil {
		profileID = parent.ProfileID
	}
	if profileID == "" {
		return nil, invalid("profile_id requis (voir mail.mailboxes.list)")
	}
	prof := w.GetIntegrationByID(profileID)
	if prof == nil || prof.Type != domain.IntegrationTypeEmail {
		return nil, invalid("profil d'envoi %q introuvable", profileID)
	}
	if prof.EmailProvider.VeridianPaused {
		return nil, &RefusedError{Reason: "le profil d'envoi est en pause", Gate: "profile_paused"}
	}
	var sender *domain.EmailSender
	for i := range prof.EmailProvider.Senders {
		if prof.EmailProvider.Senders[i].IsDefault || sender == nil {
			sender = &prof.EmailProvider.Senders[i]
		}
	}
	if sender == nil {
		return nil, invalid("le profil %q n'a pas d'adresse d'envoi", profileID)
	}

	// Suppression : jamais d'envoi à un contact qui s'est désinscrit, a plaint ou dont l'adresse est morte.
	if blocked, why, err := q.Store.Suppressed(ctx, req.Workspace, toAddr); err != nil {
		return nil, err
	} else if blocked {
		return nil, &RefusedError{Reason: "le contact ne doit plus recevoir de courrier (" + why + ")", Gate: "suppression"}
	}

	id := uuid.NewString()
	storedID := req.Workspace + "_" + id
	opts := domain.EmailOptions{CC: cc}
	var irtThread string
	if parent != nil {
		opts.InReplyTo = parent.MessageID
		refs := append([]string{}, parent.References...)
		if parent.MessageID != "" {
			refs = append(refs, parent.MessageID)
		}
		opts.References = refs
		irtThread = parent.ThreadID
	}
	html := req.HTML
	plainOnly := false
	if strings.TrimSpace(html) == "" {
		html = mailbox.TextToHTML(req.Text)
		plainOnly = true
	}
	prio := domain.EmailQueuePriorityMarketing
	if parent != nil {
		prio = domain.EmailQueuePriorityTransactional // une réponse à un humain ne fait pas la queue derrière les relances
	}
	entry := &domain.EmailQueueEntry{
		ID: uuid.NewString(), Status: domain.EmailQueueStatusPending, Priority: prio,
		SourceType: domain.EmailQueueSourceMail, SourceID: truncID(actor.ID),
		IntegrationID: prof.ID, ProviderKind: prof.EmailProvider.Kind,
		ContactEmail: toAddr, MessageID: storedID, TemplateID: "mail-api",
		Payload: domain.EmailQueuePayload{
			FromAddress: sender.Email, FromName: sender.Name, Subject: subject, HTMLContent: html,
			TextContent: req.Text, PlainTextOnly: plainOnly, RateLimitPerMinute: prof.EmailProvider.RateLimitPerMinute,
			EmailOptions: opts,
		},
		MaxAttempts: 3, CreatedAt: now().UTC(), UpdatedAt: now().UTC(),
	}
	if err := q.Queue.Enqueue(ctx, req.Workspace, []*domain.EmailQueueEntry{entry}); err != nil {
		return nil, fmt.Errorf("mise en file: %w", err)
	}
	thread := irtThread
	if thread == "" {
		thread = mailbox.ThreadIDFromRoot(id)
	}
	return &SendResult{
		MessageID: storedID, QueueEntry: entry.ID, Status: "queued", ThreadID: thread,
		Note: "en file : le mail part quand les plafonds du profil le permettent (mêmes portes que les séquences) ; il apparaît dans le fil dès qu'il est parti",
	}, nil
}

// replyAddress : où répondre (Reply-To du message reçu, sinon l'expéditeur, sinon le contact).
func replyAddress(m *mailbox.Message) string {
	if v := m.Headers["Reply-To"]; len(v) > 0 && strings.TrimSpace(v[0]) != "" {
		if a, err := mail.ParseAddress(v[0]); err == nil {
			return a.Address
		}
	}
	if m.Kind == mailbox.KindReplyHuman || m.Kind == mailbox.KindReplyAuto || m.Kind == mailbox.KindOther {
		if m.From != "" {
			return m.From
		}
	}
	return m.ContactEmail
}

func truncID(s string) string {
	if s == "" {
		return "mail-api"
	}
	if len(s) > 36 {
		return s[:36]
	}
	return s
}

var _ = errors.New
