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

// QueueSender confie le mail écrit à la main au worker de Notifuse, qui l'envoie par le relais du profil.
//
// 27/11/2026 (Robert : « si on veut en envoyer un à la main, faut qu'il parte tout de suite ») : un mail de la boîte
// n'est plus un envoi de prospection. Le worker ne lui applique aucune porte commerciale (fenêtre, débits et plafonds par
// classe, chauffe, fusible, étalement, quota du jour : queue/veridian_pool_failover.go), il passe devant les séquences
// (priorité transactionnelle) et part au tick suivant (1 s). Restent : profil en pause et contact supprimé (refusés
// ici), transport en panne (circuit ouvert), adresse invalide. L'API attend l'issue réelle au plus Wait et répond
// « parti », « refusé » avec la raison, ou « en cours » au-delà.
type QueueSender struct {
	Queue EnqueueRepo
	WS    WorkspaceGetter
	Store *mailbox.Store
	Now   func() time.Time
	// Wait : attente de l'issue (0 = aucune, l'API répond « en cours ») ; Poll : pas de relecture (défaut 250 ms).
	Wait time.Duration
	Poll time.Duration
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
	// Un mail écrit à la main (premier mail comme réponse) ne fait jamais la queue derrière les séquences.
	prio := domain.EmailQueuePriorityTransactional
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
	res := &SendResult{
		MessageID: storedID, QueueEntry: entry.ID, Status: mailbox.SendStatePending, ThreadID: thread,
		Note: "envoi en cours : il apparaît dans le fil dès que le relais l'a accepté",
	}
	q.awaitOutcome(ctx, req.Workspace, storedID, res)
	return res, nil
}

// awaitOutcome relit l'issue de l'envoi jusqu'à Wait : parti (rangé dans la boîte) ou refusé (failed_at, avec la
// raison). Au-delà, ou si la lecture échoue, la réponse reste « en cours » : le mail n'est jamais perdu, il est en file.
func (q *QueueSender) awaitOutcome(ctx context.Context, ws, historyID string, res *SendResult) {
	if q.Wait <= 0 || q.Store == nil {
		return
	}
	poll := q.Poll
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	deadline := time.NewTimer(q.Wait)
	defer deadline.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
		}
		out, err := q.Store.SendOutcome(ctx, ws, historyID)
		if err != nil {
			return
		}
		switch out.State {
		case mailbox.SendStateSent:
			res.Status, res.Note = mailbox.SendStateSent, "envoyé"
			return
		case mailbox.SendStateFailed:
			res.Status, res.Error = mailbox.SendStateFailed, out.Reason
			res.Note = "le relais n'a pas accepté le mail"
			return
		}
	}
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
