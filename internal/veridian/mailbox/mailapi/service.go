// Package mailapi expose la boîte unifiée en HTTP (JSON, inspiré de JMAP) : boîtes, messages,
// fils, message brut, pièces jointes, mise à jour, flux d'événements, envoi et réponse.
// Aucune route n'expose d'IMAP ni de SMTP : l'agent reste dans l'API.
package mailapi

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
)

// ErrInvalid : requête invalide (400).
type ErrInvalid struct{ Msg string }

func (e *ErrInvalid) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ErrInvalid{Msg: fmt.Sprintf(format, a...)} }

// Actor : qui agit (clé d'API ou utilisateur), pour « traité par ».
type Actor struct {
	ID   string
	Kind string // "agent" (clé d'API) ou "humain"
}

// Label rend l'identité écrite dans handled_by.
func (a Actor) Label() string {
	if a.ID == "" {
		return a.Kind
	}
	return a.Kind + ":" + a.ID
}

// Authorizer vérifie l'appartenance au workspace et la permission.
type Authorizer interface {
	Authorize(ctx context.Context, workspaceID string, resource domain.PermissionResource, perm domain.PermissionType) (context.Context, Actor, error)
}

// DomainAuthorizer adapte domain.AuthService.
type DomainAuthorizer struct{ Auth domain.AuthService }

// Authorize implémente Authorizer.
func (d DomainAuthorizer) Authorize(ctx context.Context, ws string, res domain.PermissionResource, perm domain.PermissionType) (context.Context, Actor, error) {
	ctx, user, uw, err := d.Auth.AuthenticateUserForWorkspace(ctx, ws)
	if err != nil {
		return ctx, Actor{}, fmt.Errorf("authentification: %w", err)
	}
	if uw == nil || !uw.HasPermission(res, perm) {
		return ctx, Actor{}, domain.NewPermissionError(res, perm, fmt.Sprintf("droit %s:%s requis", res, perm))
	}
	a := Actor{Kind: "humain"}
	if user != nil {
		a.ID = user.ID
		if user.Type == domain.UserTypeAPIKey {
			a.Kind = "agent"
		}
	}
	return ctx, a, nil
}

// WorkspaceGetter lit un workspace (intégrations, profils).
type WorkspaceGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Workspace, error)
}

// Service : logique des routes.
type Service struct {
	Store  *mailbox.Store
	Auth   Authorizer
	WS     WorkspaceGetter
	Sender Sender // nil : envoi non disponible (501)
	Now    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) read(ctx context.Context, ws string) (context.Context, Actor, error) {
	if ws == "" {
		return ctx, Actor{}, invalid("workspace_id requis")
	}
	return s.Auth.Authorize(ctx, ws, domain.PermissionResourceMessageHistory, domain.PermissionTypeRead)
}

func (s *Service) write(ctx context.Context, ws string) (context.Context, Actor, error) {
	if ws == "" {
		return ctx, Actor{}, invalid("workspace_id requis")
	}
	return s.Auth.Authorize(ctx, ws, domain.PermissionResourceMessageHistory, domain.PermissionTypeWrite)
}

// Mailboxes : une boîte par profil d'envoi et par adresse.
func (s *Service) Mailboxes(ctx context.Context, ws string) ([]mailbox.Mailbox, error) {
	ctx, _, err := s.read(ctx, ws)
	if err != nil {
		return nil, err
	}
	w, err := s.WS.GetByID(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("lecture du workspace: %w", err)
	}
	counts, err := s.Store.Counts(ctx, ws)
	if err != nil {
		return nil, err
	}
	syncs, err := s.Store.ListSync(ctx, ws)
	if err != nil {
		return nil, err
	}
	var out []mailbox.Mailbox
	seen := map[string]bool{}
	for i := range w.Integrations {
		in := &w.Integrations[i]
		if in.Type != domain.IntegrationTypeEmail {
			continue
		}
		mb := mailbox.Mailbox{ID: in.ID, ProfileID: in.ID, ProfileName: in.Name}
		for _, snd := range in.EmailProvider.Senders {
			if snd.IsDefault || mb.Address == "" {
				mb.Address = strings.ToLower(snd.Email)
			}
		}
		if imapID := in.EmailProvider.VeridianReturnIMAPIntegrationID; imapID != "" {
			mb.IntegrationID = imapID
			seen[imapID] = true
			for j := range w.Integrations {
				if w.Integrations[j].ID == imapID && w.Integrations[j].IMAPSettings != nil {
					mb.Folder = w.Integrations[j].IMAPSettings.GetFolder()
				}
			}
			if st, ok := syncs[imapID]; ok {
				st := st
				mb.Sync = &st
			}
		}
		c := counts[mb.ProfileID]
		if mb.IntegrationID != "" {
			ci := counts[mb.IntegrationID]
			if ci.CountIn > c.CountIn {
				c.CountIn = ci.CountIn
			}
			if ci.Unhandled > c.Unhandled {
				c.Unhandled = ci.Unhandled
			}
			if c.Last == nil || (ci.Last != nil && ci.Last.After(*c.Last)) {
				c.Last = ci.Last
			}
		}
		mb.CountIn, mb.CountOut, mb.CountUnread, mb.LastMessage = c.CountIn, c.CountOut, c.Unhandled, c.Last
		out = append(out, mb)
	}
	// Boîtes IMAP sans profil lié (retours globaux du workspace).
	for i := range w.Integrations {
		in := &w.Integrations[i]
		if in.Type != domain.IntegrationTypeIMAP || seen[in.ID] || in.IMAPSettings == nil {
			continue
		}
		mb := mailbox.Mailbox{ID: in.ID, IntegrationID: in.ID, Address: strings.ToLower(in.IMAPSettings.Username), ProfileName: in.Name, Folder: in.IMAPSettings.GetFolder()}
		c := counts[in.ID]
		mb.CountIn, mb.CountOut, mb.CountUnread, mb.LastMessage = c.CountIn, c.CountOut, c.Unhandled, c.Last
		if st, ok := syncs[in.ID]; ok {
			st := st
			mb.Sync = &st
		}
		out = append(out, mb)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ProfileName < out[j].ProfileName })
	return out, nil
}

// ListParams : paramètres de messages.list.
type ListParams struct {
	Workspace  string
	Mailbox    string
	Contact    string
	Thread     string
	Kinds      []mailbox.Kind
	Direction  mailbox.Direction
	Handled    *bool
	Since      *time.Time
	Until      *time.Time
	Q          string
	BodySearch bool
	Limit      int
	Cursor     string
	AfterSeq   int64
}

// List : messages.list.
func (s *Service) List(ctx context.Context, p ListParams) ([]mailbox.Summary, string, error) {
	ctx, _, err := s.read(ctx, p.Workspace)
	if err != nil {
		return nil, "", err
	}
	for _, k := range p.Kinds {
		if !mailbox.ValidKind(k) {
			return nil, "", invalid("nature inconnue %q (sent, reply_human, reply_auto, bounce_hard, bounce_soft, dsn, other)", k)
		}
	}
	if p.Direction != "" && p.Direction != mailbox.DirectionIn && p.Direction != mailbox.DirectionOut {
		return nil, "", invalid("direction inconnue %q (in, out)", p.Direction)
	}
	if len(p.Q) > 200 {
		return nil, "", invalid("recherche trop longue")
	}
	return s.Store.List(ctx, p.Workspace, mailbox.Filter{
		MailboxID: p.Mailbox, ContactEmail: p.Contact, ThreadID: p.Thread, Kinds: p.Kinds, Direction: p.Direction,
		Handled: p.Handled, Since: p.Since, Until: p.Until, Q: p.Q, BodySearch: p.BodySearch, Limit: p.Limit,
		Cursor: p.Cursor, AfterSeq: p.AfterSeq,
	})
}

// Get : messages.get, message entier (sans le message brut).
func (s *Service) Get(ctx context.Context, ws, id string) (*mailbox.Message, error) {
	ctx, _, err := s.read(ctx, ws)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, invalid("id requis")
	}
	m, err := s.Store.Get(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	m.RawEML = nil
	return m, nil
}

// Raw : messages.raw, RFC 822. Un mail reçu rend le message tel que reçu ; un mail envoyé est
// reconstitué depuis le rendu conservé (l'en-tête X-Veridian-Reconstructed le dit).
func (s *Service) Raw(ctx context.Context, ws, id string) ([]byte, *mailbox.Message, error) {
	ctx, _, err := s.read(ctx, ws)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.Store.Get(ctx, ws, id)
	if err != nil {
		return nil, nil, err
	}
	if len(m.RawEML) > 0 {
		raw := m.RawEML
		m.RawEML = nil
		return raw, m, nil
	}
	return mailbox.Reconstruct(m), m, nil
}

// Attachment : contenu d'une pièce jointe, à la demande.
func (s *Service) Attachment(ctx context.Context, ws, id string, index int) (mailbox.Attachment, []byte, error) {
	ctx, _, err := s.read(ctx, ws)
	if err != nil {
		return mailbox.Attachment{}, nil, err
	}
	m, err := s.Store.Get(ctx, ws, id)
	if err != nil {
		return mailbox.Attachment{}, nil, err
	}
	if len(m.RawEML) == 0 {
		return mailbox.Attachment{}, nil, mailbox.ErrNotFound
	}
	return mailbox.ReadAttachment(m.RawEML, index)
}

// UpdateParams : messages.update.
type UpdateParams struct {
	Workspace string
	ID        string
	Handled   *bool
	Labels    *[]string
}

// Update : traité / libellés. Un libellé fait au plus 40 caractères, 20 libellés au plus.
func (s *Service) Update(ctx context.Context, p UpdateParams) (*mailbox.Message, error) {
	ctx, actor, err := s.write(ctx, p.Workspace)
	if err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, invalid("id requis")
	}
	if p.Handled == nil && p.Labels == nil {
		return nil, invalid("rien à modifier : handled et/ou labels")
	}
	if p.Labels != nil {
		if len(*p.Labels) > 20 {
			return nil, invalid("20 libellés au plus")
		}
		clean := make([]string, 0, len(*p.Labels))
		for _, l := range *p.Labels {
			l = strings.TrimSpace(l)
			if l == "" || len(l) > 40 || strings.ContainsAny(l, "\r\n\x00") {
				return nil, invalid("libellé invalide %q (1 à 40 caractères)", l)
			}
			clean = append(clean, l)
		}
		p.Labels = &clean
	}
	if err := s.Store.Update(ctx, p.Workspace, p.ID, p.Handled, actor.Label(), p.Labels); err != nil {
		return nil, err
	}
	return s.Get(ctx, p.Workspace, p.ID)
}

// Thread : threads.get par fil, ou par contact (tous ses fils).
type ThreadResult struct {
	Thread  *mailbox.Thread  `json:"thread,omitempty"`
	Threads []mailbox.Thread `json:"threads,omitempty"`
	Contact string           `json:"contact_email,omitempty"`
}

// GetThread rend un fil complet, ou les fils d'un contact.
func (s *Service) GetThread(ctx context.Context, ws, threadID, contact string) (*ThreadResult, error) {
	ctx, _, err := s.read(ctx, ws)
	if err != nil {
		return nil, err
	}
	switch {
	case threadID != "":
		t, err := s.buildThread(ctx, ws, threadID)
		if err != nil {
			return nil, err
		}
		return &ThreadResult{Thread: t}, nil
	case contact != "":
		ths, err := s.Store.ThreadsOfContact(ctx, ws, contact, 20)
		if err != nil {
			return nil, err
		}
		res := &ThreadResult{Contact: strings.ToLower(contact)}
		for _, th := range ths {
			full, err := s.buildThread(ctx, ws, th.ThreadID)
			if err != nil {
				return nil, err
			}
			res.Threads = append(res.Threads, *full)
		}
		// le plus ancien d'abord : on lit la conversation dans l'ordre
		sort.SliceStable(res.Threads, func(i, j int) bool { return res.Threads[i].FirstAt.Before(res.Threads[j].FirstAt) })
		return res, nil
	}
	return nil, invalid("thread_id ou contact_email requis")
}

// ListThreads : résumés des fils d'un contact.
func (s *Service) ListThreads(ctx context.Context, ws, contact string, limit int) ([]mailbox.Thread, error) {
	ctx, _, err := s.read(ctx, ws)
	if err != nil {
		return nil, err
	}
	if contact == "" {
		return nil, invalid("contact_email requis")
	}
	return s.Store.ThreadsOfContact(ctx, ws, contact, limit)
}

func (s *Service) buildThread(ctx context.Context, ws, threadID string) (*mailbox.Thread, error) {
	msgs, err := s.Store.ThreadMessages(ctx, ws, threadID)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, mailbox.ErrNotFound
	}
	for i := range msgs {
		msgs[i].RawEML = nil
	}
	return mailbox.NewThread(threadID, msgs), nil
}

// Events : événements depuis un numéro d'ordre (flux). Rend les messages reçus par défaut.
func (s *Service) Events(ctx context.Context, ws string, afterSeq int64, direction mailbox.Direction, kinds []mailbox.Kind, limit int) ([]mailbox.Summary, error) {
	out, _, err := s.Store.List(ctx, ws, mailbox.Filter{AfterSeq: afterSeq, Direction: direction, Kinds: kinds, Limit: limit})
	return out, err
}

// AuthorizeStream vérifie le droit de lecture une fois, à l'ouverture d'un flux.
func (s *Service) AuthorizeStream(ctx context.Context, ws string) (context.Context, error) {
	ctx, _, err := s.read(ctx, ws)
	return ctx, err
}

// MaxSeq rend le dernier numéro d'ordre (point de départ d'un flux « seulement le neuf »).
func (s *Service) MaxSeq(ctx context.Context, ws string) (int64, error) {
	return s.Store.MaxSeq(ctx, ws)
}

// Sender : envoi et réponse par un profil, mêmes plafonds que les séquences.
type Sender interface {
	Send(ctx context.Context, req SendRequest, actor Actor) (*SendResult, error)
}

// SendRequest : messages.send et messages.reply.
type SendRequest struct {
	Workspace string   `json:"workspace_id"`
	ProfileID string   `json:"profile_id"`
	To        string   `json:"to"`
	Cc        []string `json:"cc,omitempty"`
	Subject   string   `json:"subject"`
	Text      string   `json:"text,omitempty"`
	HTML      string   `json:"html,omitempty"`
	// ReplyTo : id d'un message de la boîte unifiée auquel on répond (messages.reply).
	ReplyToMessage string `json:"in_reply_to_message,omitempty"`
}

// SendResult : accepté en file (l'envoi réel suit les mêmes portes que les séquences).
type SendResult struct {
	MessageID  string `json:"message_id"`
	QueueEntry string `json:"queue_entry_id,omitempty"`
	Status     string `json:"status"`
	ThreadID   string `json:"thread_id,omitempty"`
	Note       string `json:"note,omitempty"`
}

// ErrNotImplemented : fonction non branchée.
var ErrNotImplemented = errors.New("envoi non disponible sur ce serveur")
