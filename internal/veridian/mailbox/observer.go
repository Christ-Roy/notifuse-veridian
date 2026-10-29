package mailbox

import (
	"context"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// SentCapture implémente queue.SentObserver : à chaque envoi accepté, range le rendu exact
// du mail dans la boîte unifiée. Travaille à part pour ne jamais ralentir l'envoi.
type SentCapture struct {
	ingest *Ingestor
	log    logger.Logger
}

// NewSentCapture construit l'observateur d'envoi.
func NewSentCapture(ingest *Ingestor, log logger.Logger) *SentCapture {
	return &SentCapture{ingest: ingest, log: log}
}

// OnSent enregistre le mail parti (appelé par le worker, rend aussitôt la main).
func (c *SentCapture) OnSent(ws *domain.Workspace, entry *domain.EmailQueueEntry) {
	in := SentInputFromEntry(ws.ID, entry)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.log.WithFields(map[string]interface{}{"workspace_id": ws.ID, "panic": r}).Error("Mailbox: sent capture panicked, recovered")
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := c.ingest.RecordSent(ctx, in); err != nil {
			c.log.WithFields(map[string]interface{}{"workspace_id": ws.ID, "message_id": entry.MessageID, "error": err.Error()}).
				Warn("Mailbox: sent capture failed (history import will fill the gap)")
		}
	}()
}

// SentInputFromEntry traduit une entrée de file envoyée en mail sortant (rendu exact).
func SentInputFromEntry(workspaceID string, e *domain.EmailQueueEntry) SentInput {
	p := e.Payload
	in := SentInput{
		WorkspaceID:      workspaceID,
		MessageHistoryID: e.MessageID,
		ContactEmail:     e.ContactEmail,
		FromAddress:      p.FromAddress,
		FromName:         p.FromName,
		Subject:          p.Subject,
		HTML:             p.HTMLContent,
		Text:             p.TextContent,
		InReplyTo:        p.EmailOptions.InReplyTo,
		References:       normalizeRefs(p.EmailOptions.References),
		ProfileID:        e.IntegrationID,
		NodeID:           e.NodeID,
		SentAt:           time.Now().UTC(),
		BodySource:       BodyCaptured,
		Cc:               lowerAll(p.EmailOptions.CC),
	}
	switch e.SourceType {
	case domain.EmailQueueSourceAutomation:
		in.AutomationID = e.SourceID
	case domain.EmailQueueSourceBroadcast:
		in.BroadcastID = e.SourceID
	}
	for i, a := range p.EmailOptions.Attachments {
		in.AttachmentNames = append(in.AttachmentNames, Attachment{Index: i, Name: a.Filename, ContentType: a.ContentType, Available: false})
	}
	if p.EmailOptions.Subject != nil && strings.TrimSpace(*p.EmailOptions.Subject) != "" && in.Subject == "" {
		in.Subject = *p.EmailOptions.Subject
	}
	return in
}

func normalizeRefs(refs []string) []string {
	var out []string
	for _, r := range refs {
		if id := normalizeMsgID(r); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func lowerAll(v []string) []string {
	var out []string
	for _, s := range v {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}
