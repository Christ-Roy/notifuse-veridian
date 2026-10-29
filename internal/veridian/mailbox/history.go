package mailbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/crypto"
	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
)

// Renderer reconstitue le rendu d'un envoi ancien depuis son modèle et ses données.
type Renderer interface {
	Render(ctx context.Context, workspaceID, templateID string, version int64, data map[string]interface{}) (subject, html, text string, err error)
}

const (
	historyIntegrationKey = "__history__"
	historyFolderKey      = "message_history"
	// metadataSubjectKey : objet réellement envoyé, consigné par le worker pour les relances (automations).
	metadataSubjectKey = "veridian_subject"
)

// SyncWorkspace reprend l'historique des envois de message_history (par lots, curseur durable).
// Appelé par le poller IMAP à chaque passage ; sans effet une fois l'historique rattrapé.
func (s *Syncer) SyncWorkspace(ctx context.Context, ws *domain.Workspace) {
	if ws == nil {
		return
	}
	key := ws.ID + "|history"
	if !s.acquire(key) {
		return
	}
	defer s.release(key)
	defer func() {
		if r := recover(); r != nil {
			s.log.WithFields(map[string]interface{}{"workspace_id": ws.ID, "panic": r}).Error("Mailbox: history sync panicked, recovered")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, runBudget)
	defer cancel()
	log := s.log.WithField("workspace_id", ws.ID)

	st, err := s.store.GetSync(ctx, ws.ID, historyIntegrationKey, historyFolderKey)
	if err != nil {
		log.WithField("error", err.Error()).Warn("Mailbox: history cursor read failed")
		return
	}
	afterAt, afterID := time.Time{}, ""
	if st != nil && st.Cursor != "" {
		if i := strings.IndexByte(st.Cursor, '|'); i > 0 {
			if t, e := time.Parse(time.RFC3339Nano, st.Cursor[:i]); e == nil {
				afterAt, afterID = t, st.Cursor[i+1:]
			}
		}
	}
	rows, err := s.store.HistoryBatch(ctx, ws.ID, afterAt, afterID, historyBatch)
	if err != nil {
		log.WithField("error", err.Error()).Warn("Mailbox: history batch read failed")
		return
	}
	if len(rows) == 0 {
		return
	}
	done := 0
	for _, r := range rows {
		if ctx.Err() != nil {
			break
		}
		in, err := s.historyInput(ctx, ws, r)
		if err != nil {
			log.WithFields(map[string]interface{}{"message_id": r.ID, "error": err.Error()}).Warn("Mailbox: history row skipped")
		} else if _, err := s.ingest.RecordSent(ctx, in); err != nil {
			log.WithFields(map[string]interface{}{"message_id": r.ID, "error": err.Error()}).Warn("Mailbox: history row failed, will retry")
			break
		}
		afterAt, afterID = r.SentAt, r.ID
		done++
	}
	if done > 0 {
		cursor := afterAt.UTC().Format(time.RFC3339Nano) + "|" + afterID
		if err := s.store.SaveSync(context.Background(), ws.ID, historyIntegrationKey, historyFolderKey, 0, 0, cursor, true, "", done); err != nil {
			log.WithField("error", err.Error()).Warn("Mailbox: history cursor save failed")
			return
		}
		log.WithField("count", done).Info("Mailbox: history rows imported")
	}
}

func (s *Syncer) historyInput(ctx context.Context, ws *domain.Workspace, r HistoryRow) (SentInput, error) {
	in := SentInput{
		WorkspaceID: ws.ID, MessageHistoryID: r.ID, ContactEmail: r.ContactEmail, ProfileID: r.ProfileID,
		AutomationID: r.AutomationID, BroadcastID: r.BroadcastID, SentAt: r.SentAt, FromAddress: r.SenderEmail,
		BodySource: BodyUnavailable, ThreadBySubject: true,
	}
	// Adresse d'envoi : celle de l'envoi, sinon l'expéditeur par défaut du profil.
	for i := range ws.Integrations {
		p := &ws.Integrations[i]
		if p.ID != r.ProfileID || p.Type != domain.IntegrationTypeEmail {
			continue
		}
		for _, snd := range p.EmailProvider.Senders {
			if (in.FromAddress == "" && snd.IsDefault) || strings.EqualFold(snd.Email, in.FromAddress) {
				if in.FromAddress == "" {
					in.FromAddress = snd.Email
				}
				in.FromName = snd.Name
			}
		}
	}
	var md domain.MessageData
	if err := json.Unmarshal(r.DataEncrypted, &md); err != nil {
		return in, fmt.Errorf("données du message illisibles: %w", err)
	}
	if v, ok := md.Metadata[metadataSubjectKey].(string); ok && strings.TrimSpace(v) != "" {
		in.Subject = v
	}
	var opts domain.ChannelOptions
	if len(r.ChannelOptions) > 0 && json.Unmarshal(r.ChannelOptions, &opts) == nil {
		if in.Subject == "" && opts.Subject != nil {
			in.Subject = *opts.Subject
		}
		in.Cc = opts.CC
		if opts.FromName != nil && *opts.FromName != "" {
			in.FromName = *opts.FromName
		}
	}
	data := md.Data
	if enc, ok := md.Data["_encrypted"].(string); ok {
		key, err := s.store.secret(ctx, ws.ID)
		if err != nil {
			return in, err
		}
		plain, err := crypto.DecryptFromHexString(enc, key)
		if err != nil {
			return in, fmt.Errorf("déchiffrement des données: %w", err)
		}
		data = map[string]interface{}{}
		if err := json.Unmarshal([]byte(plain), &data); err != nil {
			return in, fmt.Errorf("données déchiffrées illisibles: %w", err)
		}
	}
	if s.renderer != nil && r.TemplateID != "" {
		subj, htmlOut, text, err := s.renderer.Render(ctx, ws.ID, r.TemplateID, r.TemplateVersion, data)
		if err == nil && htmlOut != "" {
			in.HTML, in.Text, in.BodySource = htmlOut, text, BodyRerendered
			if in.Subject == "" {
				in.Subject = subj
			}
		}
	}
	if in.Subject == "" {
		in.Subject = "(objet non conservé)"
	}
	if in.FromAddress == "" {
		in.FromAddress = "inconnu@invalid"
	}
	return in, nil
}

// TemplateRenderer rend un envoi ancien avec le service de modèles.
type TemplateRenderer struct {
	Templates domain.TemplateService
}

// Render compile le modèle (version d'époque) avec les données d'époque, sans suivi ni pixel.
func (t TemplateRenderer) Render(ctx context.Context, ws, templateID string, version int64, data map[string]interface{}) (string, string, string, error) {
	tpl, err := t.Templates.GetTemplateByID(ctx, ws, templateID, version)
	if err != nil || tpl == nil || tpl.Email == nil {
		return "", "", "", fmt.Errorf("modèle introuvable")
	}
	req := domain.CompileTemplateRequest{
		WorkspaceID:      ws,
		VisualEditorTree: tpl.Email.VisualEditorTree,
		TemplateData:     notifuse_mjml.MapOfAny(data),
	}
	req.MjmlSource = tpl.Email.GetCodeModeMjmlSource()
	res, err := t.Templates.CompileTemplate(ctx, req)
	if err != nil {
		return "", "", "", err
	}
	if res == nil || !res.Success || res.HTML == nil {
		return "", "", "", fmt.Errorf("compilation échouée")
	}
	subject, _ := notifuse_mjml.ProcessLiquidTemplate(tpl.Email.Subject, data, "email_subject")
	return subject, *res.HTML, "", nil
}
