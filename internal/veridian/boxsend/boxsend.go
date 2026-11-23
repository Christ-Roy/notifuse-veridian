// Package boxsend est le SMTP des boîtes louées (fiche 84) : un client mail ou une application envoie avec l'accès de sa
// boîte, Notifuse envoie par le profil de CETTE boîte, tout de suite (ni fenêtre ni chauffe : c'est un humain qui écrit),
// et range l'envoi dans la boîte unifiée. From imposé, destinataires en suppression refusés, plafond journalier par accès.
package boxsend

import (
	"bytes"
	"context"
	"errors"
	"net/mail"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/boxaccess"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/internal/veridian/smtprelay"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/smtp_bridge"
	"github.com/emersion/go-smtp"
	"github.com/google/uuid"
)

// Prefix distingue un accès de boîte d'une clé d'API dans l'identité rendue au bridge SMTP.
const Prefix = "box:"

// LimitLogin borne les vérifications de mot de passe. Le secret a 160 bits : la borne protège la base, pas le secret, et elle
// est globale pour qu'un tiers ne puisse pas verrouiller la boîte d'un client en se trompant exprès.
const (
	LimitLogin          = "box_smtp_login"
	LimitLoginPerMinute = 600
)

// Accesses : accès de boîte.
type Accesses interface {
	Verify(ctx context.Context, address, secret string) (*boxaccess.Access, error)
	Alive(ctx context.Context, id string) (*boxaccess.Access, error)
	Touch(ctx context.Context, id string) error
	ReserveSend(ctx context.Context, id string, n int) error
}

// Recorder range l'envoi dans la boîte unifiée.
type Recorder interface {
	RecordSent(ctx context.Context, in mailbox.SentInput) (bool, error)
}

// Deps : ports.
type Deps struct {
	Accesses    Accesses
	WS          boxaccess.WorkspaceGetter
	Suppression smtprelay.Suppression
	Mailer      smtprelay.Mailer
	History     smtprelay.History
	Limiter     smtprelay.Limiter
	Logger      logger.Logger
	Now         func() time.Time
}

// Sender : porte SMTP des boîtes.
type Sender struct {
	d      Deps
	dedupe *smtprelay.Dedupe
}

// New construit la porte.
func New(d Deps) *Sender {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Sender{d: d, dedupe: smtprelay.NewDedupe(24 * time.Hour)}
}

type recorderBox struct{ r Recorder }

var recorder atomic.Value

// SetRecorder branche la boîte unifiée (posé par son module au câblage).
func SetRecorder(r Recorder) { recorder.Store(recorderBox{r}) }

func currentRecorder() Recorder {
	if b, ok := recorder.Load().(recorderBox); ok {
		return b.r
	}
	return nil
}

// IsBox dit si l'identité rendue par Login est un accès de boîte.
func IsBox(userID string) bool { return strings.HasPrefix(userID, Prefix) }

// LooksLikeAPIKey : une clé d'API est un jeton à trois parties ; un mot de passe de boîte n'a pas de point.
func LooksLikeAPIKey(secret string) bool { return strings.Count(secret, ".") == 2 }

// Login vérifie un accès de boîte et rend son identité pour le bridge.
func (s *Sender) Login(ctx context.Context, username, password string) (string, error) {
	if !s.d.Limiter.Allow(LimitLogin, "global") {
		return "", smtp_bridge.ErrTemporaryAuth
	}
	a, err := s.d.Accesses.Verify(ctx, username, password)
	if errors.Is(err, boxaccess.ErrAuth) {
		return "", err
	}
	if err != nil {
		s.d.Logger.WithField("error", err.Error()).Error("Box SMTP: access check failed")
		return "", smtp_bridge.ErrTemporaryAuth
	}
	_ = s.d.Accesses.Touch(ctx, a.ID)
	s.d.Logger.WithFields(map[string]interface{}{"workspace_id": a.WorkspaceID, "access_id": a.ID}).Info("Box SMTP: login")
	return Prefix + a.ID, nil
}

func refuse(code, c1, c2, c3 int, msg string) error {
	return &smtp.SMTPError{Code: code, EnhancedCode: smtp.EnhancedCode{c1, c2, c3}, Message: msg}
}

var errBusy = refuse(451, 4, 3, 0, "service indisponible, réessayez")

// box relit l'accès et son profil : révocation, adresse retirée, profil en pause.
func (s *Sender) box(ctx context.Context, userID string) (*boxaccess.Access, *domain.Workspace, *domain.Integration, error) {
	a, err := s.d.Accesses.Alive(ctx, strings.TrimPrefix(userID, Prefix))
	if errors.Is(err, boxaccess.ErrAuth) {
		return nil, nil, nil, refuse(535, 5, 7, 8, "accès révoqué")
	}
	if err != nil {
		s.d.Logger.WithField("error", err.Error()).Error("Box SMTP: access read failed")
		return nil, nil, nil, errBusy
	}
	ws, err := s.d.WS.GetByID(ctx, a.WorkspaceID)
	if err != nil {
		s.d.Logger.WithField("error", err.Error()).Error("Box SMTP: workspace read failed")
		return nil, nil, nil, errBusy
	}
	p := boxaccess.ProfileOf(ws, a.ProfileID, a.Address)
	switch {
	case p == nil:
		return nil, nil, nil, refuse(535, 5, 7, 8, "boîte retirée")
	case !a.CanSend:
		return nil, nil, nil, refuse(550, 5, 7, 1, "cet accès est en lecture seule")
	case p.EmailProvider.VeridianPaused:
		return nil, nil, nil, refuse(550, 5, 7, 1, "profil d'envoi en pause")
	}
	return a, ws, p, nil
}

// CheckRecipient refuse au RCPT TO une adresse en suppression (rebond définitif, plainte).
func (s *Sender) CheckRecipient(ctx context.Context, userID, to string) error {
	a, _, _, err := s.box(ctx, userID)
	if err != nil {
		return err
	}
	st, err := s.d.Suppression.ContactListStatuses(ctx, a.WorkspaceID, to)
	if err != nil {
		s.d.Logger.WithField("error", err.Error()).Error("Box SMTP: suppression read failed")
		return errBusy
	}
	if smtprelay.IsSuppressed(st) {
		return refuse(550, 5, 7, 1, "destinataire refusé : adresse en suppression")
	}
	return nil
}

// Submit envoie un message. Chaque refus porte son code : aucune perte silencieuse.
func (s *Sender) Submit(ctx context.Context, userID, envelopeFrom string, rcpts []string, data []byte) error {
	a, ws, p, err := s.box(ctx, userID)
	if err != nil {
		return err
	}
	log := s.d.Logger.WithFields(map[string]interface{}{"workspace_id": a.WorkspaceID, "access_id": a.ID, "profile_id": a.ProfileID})
	rcpts = uniq(rcpts)
	if len(rcpts) == 0 {
		return refuse(554, 5, 5, 0, "aucun destinataire valide")
	}
	if err := smtprelay.CheckSize(len(data)); err != nil {
		return err
	}
	if err := smtprelay.CheckRecipients(len(rcpts)); err != nil {
		return err
	}
	sub, err := smtprelay.ParseSubmission(data)
	if err != nil {
		return err
	}
	if err := smtprelay.CheckFrom(sub.HeaderFrom, envelopeFrom, []domain.EmailSender{{Email: a.Address}}); err != nil {
		log.Warn("Box SMTP: sender is not the box address")
		return err
	}
	now := s.d.Now()
	key := ""
	if sub.MessageID != "" {
		key = a.ID + "|" + sub.MessageID
		if s.dedupe.Seen(key, now) {
			return nil // reprise du client après une coupure : déjà envoyé
		}
	}
	if err := s.d.Accesses.ReserveSend(ctx, a.ID, len(rcpts)); err != nil {
		if errors.Is(err, boxaccess.ErrCap) {
			return refuse(452, 4, 7, 1, "plafond d'envoi du jour atteint pour cet accès")
		}
		log.WithField("error", err.Error()).Error("Box SMTP: daily cap failed")
		return errBusy
	}

	to, cc, bcc := split(data, rcpts)
	wire := uuid.NewString()
	if sub.MessageID != "" {
		wire = smtprelay.WireLocalPart(sub.MessageID)
	}
	req := smtprelay.BuildRequest(a.WorkspaceID, a.ProfileID, &p.EmailProvider, sub, to[0], wire)
	req.EmailOptions.CC = append(append([]string{}, to[1:]...), cc...)
	req.EmailOptions.BCC = bcc
	if err := s.d.Mailer.SendEmail(ctx, req, false); err != nil {
		log.WithField("error", err.Error()).Error("Box SMTP: profile refused the message")
		return refuse(451, 4, 4, 1, "envoi temporairement impossible, réessayez")
	}
	if key != "" {
		s.dedupe.Remember(key, now)
	}
	s.record(ctx, a, ws, sub, to, cc, wire, now, log)
	log.WithFields(map[string]interface{}{"recipients": len(rcpts), "bytes": len(data)}).Info("Box SMTP: message accepted")
	return nil
}

// record écrit l'historique et range l'envoi. Le mail est parti : un échec se journalise, il ne change pas la réponse.
func (s *Sender) record(ctx context.Context, a *boxaccess.Access, ws *domain.Workspace, sub *smtprelay.Submission, to, cc []string, wire string, now time.Time, log logger.Logger) {
	at := now.UTC()
	h := &domain.MessageHistory{ID: wire, ContactEmail: to[0], TemplateID: smtprelay.RelayTemplateID, Channel: "email",
		MessageData: domain.MessageData{Data: map[string]interface{}{}}, SentAt: &at, CreatedAt: at, UpdatedAt: at, VeridianMessageType: "transactional"}
	if sub.MessageID != "" {
		id := sub.MessageID
		h.ExternalID = &id
	}
	if err := s.d.History.Create(ctx, ws.ID, ws.Settings.SecretKey, h); err != nil {
		log.WithField("error", err.Error()).Warn("Box SMTP: history not written (mail sent)")
	}
	r := currentRecorder()
	if r == nil {
		log.Warn("Box SMTP: unified mailbox not wired, sent mail not stored")
		return
	}
	in := mailbox.SentInput{WorkspaceID: a.WorkspaceID, MessageHistoryID: wire, ContactEmail: to[0], FromAddress: a.Address, FromName: sub.FromName,
		To: to, Cc: cc, Subject: sub.Subject, HTML: sub.HTML, Text: sub.Text, InReplyTo: sub.InReplyTo, References: sub.References,
		ProfileID: a.ProfileID, SentAt: at, BodySource: mailbox.BodyCaptured}
	for i, at := range sub.Attachments {
		in.AttachmentNames = append(in.AttachmentNames, mailbox.Attachment{Index: i, Name: at.Filename, ContentType: at.ContentType})
	}
	if _, err := r.RecordSent(ctx, in); err != nil {
		log.WithField("error", err.Error()).Warn("Box SMTP: sent mail not stored in the unified mailbox")
	}
}

func uniq(l []string) []string {
	seen, out := map[string]bool{}, []string{}
	for _, a := range l {
		a = boxaccess.NormalizeAddress(a)
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

// split répartit les destinataires d'ENVELOPPE (les seuls servis) selon les en-têtes To et Cc ; les autres sont en copie cachée.
// Au moins un destinataire est toujours en To (un message tout en copie cachée part au premier).
func split(data []byte, rcpts []string) (to, cc, bcc []string) {
	in := map[string]string{}
	if m, err := mail.ReadMessage(bytes.NewReader(data)); err == nil {
		for _, h := range []string{"Cc", "To"} {
			l, err := m.Header.AddressList(h)
			if err != nil {
				continue
			}
			for _, a := range l {
				in[boxaccess.NormalizeAddress(a.Address)] = h
			}
		}
	}
	for _, r := range rcpts {
		switch in[r] {
		case "To":
			to = append(to, r)
		case "Cc":
			cc = append(cc, r)
		default:
			bcc = append(bcc, r)
		}
	}
	if len(to) == 0 && len(cc) > 0 {
		to, cc = cc[:1], cc[1:]
	}
	if len(to) == 0 {
		to, bcc = bcc[:1], bcc[1:]
	}
	return to, cc, bcc
}
