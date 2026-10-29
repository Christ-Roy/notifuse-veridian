package mailbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/google/uuid"
)

// Notifier est prévenu de chaque nouveau message reçu (webhook message.received, réveil du flux).
type Notifier interface {
	MessageReceived(ctx context.Context, workspaceID string, m *Message)
}

// BoxInfo décrit la boîte IMAP d'où vient un mail.
type BoxInfo struct {
	IntegrationID string
	Address       string
	ProfileID     string
	ProfileName   string
	Folder        string
}

// Ingestor range les mails dans la boîte unifiée.
type Ingestor struct {
	store    *Store
	log      logger.Logger
	notifier Notifier
	now      func() time.Time
}

// NewIngestor construit l'ingestion. notifier peut être nil.
func NewIngestor(store *Store, log logger.Logger, notifier Notifier) *Ingestor {
	return &Ingestor{store: store, log: log, notifier: notifier, now: time.Now}
}

// IngestRaw lit, classe, relie et enregistre un mail reçu. Idempotent. Retourne le message
// et inserted=false s'il était déjà là. Les journaux ne portent jamais de contenu : seulement
// des identifiants, la nature et la taille.
func (g *Ingestor) IngestRaw(ctx context.Context, ws string, box BoxInfo, ref MailboxRef, raw []byte, internalDate time.Time, truncated bool) (*Message, bool, error) {
	p, err := ParseRaw(raw)
	if err != nil {
		p = &Parsed{Subject: "(message illisible)", Headers: map[string][]string{}, Truncated: true}
	}
	if truncated {
		p.Truncated = true
	}

	// Premier passage : rebond ? (son identifiant d'origine sert à retrouver l'envoi)
	verdict := Classify(p, raw, true)

	cands := CandidateIDs(p.InReplyTo, p.References)
	if verdict.DSN != nil && verdict.DSN.OriginalMessageID != "" {
		if id := normalizeMsgID(verdict.DSN.OriginalMessageID); id != "" {
			cands = append(cands, id)
		}
	}
	storedForms := storedFormsOf(ws, cands)
	hits, err := g.store.FindByIDs(ctx, ws, cands, storedForms)
	if err != nil {
		return nil, false, fmt.Errorf("recherche des références: %w", err)
	}
	sent, err := g.store.FindSent(ctx, ws, storedForms)
	if err != nil {
		return nil, false, fmt.Errorf("recherche de l'envoi cité: %w", err)
	}

	link := &Link{}
	contact := ""
	profile := box.ProfileID
	linked := false
	switch {
	case len(hits) > 0 && hits[0].Direction == DirectionOut:
		h := hits[0]
		linked = true
		sid := h.SentRef
		if h.Link != nil && h.Link.SentMessageID != "" {
			sid = h.Link.SentMessageID
		}
		link = &Link{SentMessageID: sid, MatchType: "message_id"}
		if h.Link != nil {
			link.AutomationID, link.BroadcastID, link.NodeID = h.Link.AutomationID, h.Link.BroadcastID, h.Link.NodeID
		}
		contact = h.ContactEmail
		if profile == "" {
			profile = h.ProfileID
		}
	case len(sent) > 0:
		s := sent[0]
		linked = true
		link = &Link{SentMessageID: s.ID, AutomationID: s.AutomationID, BroadcastID: s.BroadcastID, MatchType: "message_id"}
		contact = domain.VeridianNormalizeEmail(s.ContactEmail)
		if profile == "" {
			profile = s.ProfileID
		}
	case len(hits) > 0:
		// Cite un fil connu (un message reçu plus tôt) sans que l'envoi d'origine soit retrouvé.
		linked = true
		link = &Link{MatchType: "message_id"}
		if hits[0].Link != nil {
			l := *hits[0].Link
			l.MatchType = "message_id"
			link = &l
		}
		contact = hits[0].ContactEmail
	}
	if contact == "" {
		contact = domain.VeridianNormalizeEmail(p.From)
	}
	if !linked && verdict.DSN == nil && contact != "" {
		if ok, _ := g.store.ContactExists(ctx, ws, contact); ok {
			linked = true
			link = &Link{MatchType: "sender"}
		}
	}
	if !linked {
		link = nil
	}
	if verdict.DSN == nil && !linked {
		verdict = Verdict{Kind: KindOther}
	} else if verdict.DSN == nil {
		verdict = Classify(p, raw, true)
	}
	if verdict.DSN != nil && verdict.Recipient != "" && link == nil {
		// Rebond dont l'envoi d'origine n'est pas retrouvé : le contact est l'adresse en échec.
		contact = verdict.Recipient
	}

	received := internalDate
	if received.IsZero() {
		received = p.Date
	}
	if received.IsZero() {
		received = g.now()
	}
	received = received.UTC()

	// Fil : cité en base, sinon racine des références, sinon objet + contact, sinon le message lui-même.
	threadID := ""
	if len(hits) > 0 {
		threadID = hits[0].ThreadID
	}
	if threadID == "" && (len(p.References) > 0 || p.InReplyTo != "") {
		threadID = DeterministicThreadID("", p.InReplyTo, p.References, contact, p.Subject)
	}
	if threadID == "" && len(sent) > 0 {
		threadID = ThreadIDFromRoot(domain.VeridianBareMessageUUID(sent[0].ID))
	}
	if threadID == "" {
		if tid, _ := g.store.FindThreadBySubject(ctx, ws, contact, p.Subject, received); tid != "" {
			threadID = tid
		}
	}
	if threadID == "" {
		threadID = DeterministicThreadID(p.MessageID, "", nil, contact, p.Subject)
	}

	text := p.Text
	if strings.TrimSpace(text) == "" {
		text = HTMLToText(p.HTML)
	}
	m := &Message{
		ID:           uuid.NewString(),
		Direction:    DirectionIn,
		Kind:         verdict.Kind,
		WorkspaceID:  ws,
		ProfileID:    profile,
		Mailbox:      &ref,
		MessageID:    p.MessageID,
		InReplyTo:    p.InReplyTo,
		References:   p.References,
		ThreadID:     threadID,
		ContactEmail: contact,
		From:         p.From,
		FromName:     p.FromName,
		To:           p.To,
		Cc:           p.Cc,
		Subject:      p.Subject,
		ReceivedAt:   &received,
		OccurredAt:   received,
		Headers:      p.Headers,
		BodyText:     text,
		BodySource:   BodyReceived,
		SizeBytes:    int64(len(raw)),
		Truncated:    p.Truncated,
		Attachments:  p.Attachments,
		DSN:          verdict.DSN,
		Link:         link,
	}
	if !p.Date.IsZero() {
		d := p.Date.UTC()
		m.SentAt = &d
	}
	m.BodyHTMLSanitized = SanitizeHTML(p.HTML, p.CIDs)
	for i := range m.Attachments {
		m.Attachments[i].Available = m.Attachments[i].Available && len(raw) <= MaxRawBytes
	}
	if len(raw) <= MaxRawBytes {
		m.RawEML = raw
	}

	inserted, err := g.store.InsertInbound(ctx, m)
	if err != nil {
		return nil, false, err
	}
	if !inserted {
		return m, false, nil
	}
	if m.MessageID != "" {
		if _, err := g.store.Rethread(ctx, ws, m.ThreadID, m.MessageID, ""); err != nil {
			g.log.WithFields(map[string]interface{}{"workspace_id": ws, "error": err.Error()}).Warn("Mailbox: rethread failed")
		}
	}
	g.log.WithFields(map[string]interface{}{
		"workspace_id": ws, "integration_id": ref.IntegrationID, "uid": ref.UID, "kind": string(m.Kind),
		"bytes": m.SizeBytes, "attachments": len(m.Attachments),
	}).Info("Mailbox: message ingested")
	if g.notifier != nil {
		g.notifier.MessageReceived(ctx, ws, m)
	}
	return m, true, nil
}

// storedFormsOf donne, pour chaque identifiant cité, les formes sous lesquelles
// message_history.id peut être stocké : la partie locale nue et `<workspace>_<uuid>`.
func storedFormsOf(ws string, cands []string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, c := range cands {
		lp := LocalPart(c)
		add(lp)
		add(domain.VeridianReconstructStoredMessageID(ws, lp))
	}
	return out
}

// SentInput : un mail sortant à enregistrer (capture à l'envoi, ou reprise de l'historique).
type SentInput struct {
	WorkspaceID      string
	MessageHistoryID string
	ContactEmail     string
	FromAddress      string
	FromName         string
	To               []string
	Cc               []string
	Subject          string
	HTML             string
	Text             string
	InReplyTo        string
	References       []string
	ProfileID        string
	AutomationID     string
	NodeID           string
	BroadcastID      string
	SentAt           time.Time
	AttachmentNames  []Attachment
	BodySource       string
	// ThreadBySubject : reprise de l'historique, sans en-têtes de fil connus : on rattache au fil
	// existant du même contact et du même objet normalisé.
	ThreadBySubject bool
}

// RecordSent enregistre un mail sortant accepté par le relais. Idempotent sur MessageHistoryID.
func (g *Ingestor) RecordSent(ctx context.Context, in SentInput) (bool, error) {
	if in.MessageHistoryID == "" || in.WorkspaceID == "" {
		return false, fmt.Errorf("identifiant d'envoi requis")
	}
	rfc := domain.VeridianRFCMessageID(in.MessageHistoryID, in.FromAddress)
	messageID := ""
	if rfc != "" {
		messageID = "<" + rfc + ">"
	} else {
		messageID = "<" + domain.VeridianBareMessageUUID(in.MessageHistoryID) + ">"
	}
	contact := strings.ToLower(strings.TrimSpace(in.ContactEmail))
	to := in.To
	if len(to) == 0 && contact != "" {
		to = []string{contact}
	}
	text := in.Text
	if strings.TrimSpace(text) == "" {
		text = HTMLToText(in.HTML)
	}
	refs := in.References
	irt := normalizeMsgID(in.InReplyTo)
	sentAt := in.SentAt.UTC()
	hdr := map[string][]string{
		"Message-Id": {messageID},
		"From":       {formatAddr(in.FromName, in.FromAddress)},
		"To":         {strings.Join(to, ", ")},
		"Subject":    {in.Subject},
		"Date":       {sentAt.Format(time.RFC1123Z)},
	}
	if irt != "" {
		hdr["In-Reply-To"] = []string{irt}
	}
	if len(refs) > 0 {
		hdr["References"] = []string{strings.Join(refs, " ")}
	}
	threadID := DeterministicThreadID(messageID, irt, refs, contact, in.Subject)
	if in.ThreadBySubject && irt == "" && len(refs) == 0 {
		if tid, _ := g.store.FindThreadBySubject(ctx, in.WorkspaceID, contact, in.Subject, sentAt); tid != "" {
			threadID = tid
		}
	}
	m := &Message{
		ID:           uuid.NewString(),
		Direction:    DirectionOut,
		Kind:         KindSent,
		WorkspaceID:  in.WorkspaceID,
		ProfileID:    in.ProfileID,
		MessageID:    messageID,
		InReplyTo:    irt,
		References:   refs,
		ThreadID:     threadID,
		ContactEmail: contact,
		From:         strings.ToLower(in.FromAddress),
		FromName:     in.FromName,
		To:           to,
		Cc:           in.Cc,
		Subject:      in.Subject,
		SentAt:       &sentAt,
		OccurredAt:   sentAt,
		Headers:      hdr,
		BodyText:     text,
		BodySource:   in.BodySource,
		Attachments:  in.AttachmentNames,
		SentRef:      in.MessageHistoryID,
		Link: &Link{SentMessageID: in.MessageHistoryID, AutomationID: in.AutomationID, NodeID: in.NodeID,
			BroadcastID: in.BroadcastID},
	}
	if in.HTML != "" {
		m.BodyHTMLSanitized = SanitizeHTML(in.HTML, nil)
	}
	m.SizeBytes = int64(len(in.HTML) + len(in.Text))
	inserted, err := g.store.UpsertSent(ctx, m)
	if err != nil {
		return false, err
	}
	if inserted && messageID != "" {
		if _, err := g.store.Rethread(ctx, in.WorkspaceID, m.ThreadID, messageID, in.MessageHistoryID); err != nil {
			g.log.WithFields(map[string]interface{}{"workspace_id": in.WorkspaceID, "error": err.Error()}).Warn("Mailbox: rethread failed")
		}
	}
	return inserted, nil
}

func formatAddr(name, addr string) string {
	if name == "" {
		return addr
	}
	return fmt.Sprintf("%q <%s>", name, addr)
}
