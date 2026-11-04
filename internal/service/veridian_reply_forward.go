package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/Notifuse/notifuse/pkg/veridian_ndr"
	"github.com/google/uuid"
)

// Veridian fork (fiche 78, 04/11/2026) : REDIRECTION DES REPONSES de prospects par profil.
//
// Quand une reponse HUMAINE est rattachee a un envoi d'un profil qui porte
// veridian_reply_forward_to, le forwarder envoie UNE copie aux adresses configurees, par le
// SMTP du meme profil (expediteur = identite d'envoi du profil), avec Reply-To = le prospect
// pour qu'un clic sur "repondre" lui parle directement, et l'en-tete X-Veridian-Forwarded.
//
// Principes (cf. docs/claude/78-redirection-reponses-prospects.md) :
//   - HORS CHEMIN CRITIQUE : le service de detection appelle Dispatch, qui rend la main tout
//     de suite ; le travail (lecture DB, envoi SMTP) tourne dans une goroutine bornee. Un echec
//     ne remonte jamais : ni la detection, ni la sortie de sequence, ni l'acquittement IMAP
//     n'en dependent.
//   - IDEMPOTENT : une reponse = une ligne du journal (cle primaire = empreinte de la reponse),
//     reservee AVANT l'envoi. Un poller qui relit le meme message ne declenche aucune 2e copie.
//     Contrepartie assumee : au plus une fois (un envoi qui echoue est journalise "failed",
//     pas rejoue tout seul).
//   - ANTI-BOUCLE : jamais pour un rebond/DSN, un auto-repondeur ou un defi ; jamais vers
//     l'adresse d'envoi du profil ni vers le prospect ; jamais si le message entrant porte deja
//     X-Veridian-Forwarded ; plafond de VeridianReplyForwardDailyCap copies par profil et par jour.
//   - AUCUNE donnee personnelle dans les journaux applicatifs (ni adresse, ni objet, ni corps).

const (
	replyForwardTimeout     = 90 * time.Second
	replyForwardMaxBody     = 64 << 10
	replyForwardMaxSubject  = 200
	replyForwardMaxInFlight = 8
	replyForwardRetryDelay  = 5 * time.Second
)

// veridianReplyForwardMailer : l'envoi par le transport du profil (EmailService.SendEmail).
type veridianReplyForwardMailer interface {
	SendEmail(ctx context.Context, request domain.SendEmailProviderRequest, isMarketing bool) error
}

// veridianReplyForwardWorkspaces : lecture d'un workspace (secrets dechiffres).
type veridianReplyForwardWorkspaces interface {
	GetByID(ctx context.Context, id string) (*domain.Workspace, error)
}

// VeridianReplyForwardOutcome dit ce qui s'est passe (tests et journal).
type VeridianReplyForwardOutcome string

const (
	ReplyForwardSent           VeridianReplyForwardOutcome = "sent"
	ReplyForwardFailed         VeridianReplyForwardOutcome = "failed"
	ReplyForwardSkipNotHuman   VeridianReplyForwardOutcome = "skip_not_human"
	ReplyForwardSkipLoop       VeridianReplyForwardOutcome = "skip_loop"
	ReplyForwardSkipNoConfig   VeridianReplyForwardOutcome = "skip_no_config"
	ReplyForwardSkipNoProfile  VeridianReplyForwardOutcome = "skip_no_profile"
	ReplyForwardSkipDuplicate  VeridianReplyForwardOutcome = "skip_duplicate"
	ReplyForwardSkipCapped     VeridianReplyForwardOutcome = "skip_capped"
	ReplyForwardSkipNoRecipent VeridianReplyForwardOutcome = "skip_no_recipient"
	ReplyForwardError          VeridianReplyForwardOutcome = "error"
)

// VeridianReplyForwarder envoie les copies de reponses.
type VeridianReplyForwarder struct {
	repo       domain.VeridianReplyForwardRepository
	workspaces veridianReplyForwardWorkspaces
	mailer     veridianReplyForwardMailer
	logger     logger.Logger

	dailyCap   int
	retryDelay time.Duration
	now        func() time.Time
	// run lance le travail ; par defaut une goroutine (les tests l'executent en ligne).
	run func(func())
	sem chan struct{}
}

// NewVeridianReplyForwarder construit le forwarder.
func NewVeridianReplyForwarder(repo domain.VeridianReplyForwardRepository, workspaces veridianReplyForwardWorkspaces, mailer veridianReplyForwardMailer, log logger.Logger) *VeridianReplyForwarder {
	return &VeridianReplyForwarder{
		repo:       repo,
		workspaces: workspaces,
		mailer:     mailer,
		logger:     log,
		dailyCap:   domain.VeridianReplyForwardDailyCap,
		retryDelay: replyForwardRetryDelay,
		now:        func() time.Time { return time.Now().UTC() },
		run:        func(fn func()) { go fn() },
		sem:        make(chan struct{}, replyForwardMaxInFlight),
	}
}

// Dispatch est l'unique point d'entree du service de detection. Il ne bloque pas, ne
// retourne rien et ne panique jamais vers l'appelant.
func (f *VeridianReplyForwarder) Dispatch(workspaceID string, msg *domain.VeridianIMAPMessage, det domain.VeridianReplyDetection) {
	if f == nil || msg == nil {
		return
	}
	select {
	case f.sem <- struct{}{}:
	default:
		f.logger.WithFields(map[string]interface{}{"workspace_id": workspaceID}).Warn("VeridianReplyForward: too many copies in flight, skipped")
		return
	}
	f.run(func() {
		defer func() { <-f.sem }()
		defer func() {
			if r := recover(); r != nil {
				f.logger.WithFields(map[string]interface{}{"workspace_id": workspaceID}).Error("VeridianReplyForward: panic recovered")
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), replyForwardTimeout)
		defer cancel()
		f.Forward(ctx, workspaceID, msg, det)
	})
}

// Forward fait le travail, de facon synchrone (testable). Rend l'issue ; n'echoue jamais.
func (f *VeridianReplyForwarder) Forward(ctx context.Context, workspaceID string, msg *domain.VeridianIMAPMessage, det domain.VeridianReplyDetection) VeridianReplyForwardOutcome {
	out, profileID := f.forward(ctx, workspaceID, msg, det)
	fields := map[string]interface{}{"workspace_id": workspaceID, "outcome": string(out)}
	if profileID != "" {
		fields["profile_id"] = profileID
	}
	switch out {
	case ReplyForwardSent:
		f.logger.WithFields(fields).Info("VeridianReplyForward: copy sent")
	case ReplyForwardFailed, ReplyForwardError, ReplyForwardSkipCapped, ReplyForwardSkipLoop:
		f.logger.WithFields(fields).Warn("VeridianReplyForward: no copy sent")
	default:
		f.logger.WithFields(fields).Debug("VeridianReplyForward: skipped")
	}
	return out
}

func (f *VeridianReplyForwarder) forward(ctx context.Context, workspaceID string, msg *domain.VeridianIMAPMessage, det domain.VeridianReplyDetection) (VeridianReplyForwardOutcome, string) {
	if msg == nil || !det.IsReply {
		return ReplyForwardSkipNotHuman, ""
	}

	// 1. Garde-fous de nature (rejouables ici, independamment de l'appelant) : humain seulement.
	if det.ReplyType != "" && det.ReplyType != domain.VeridianReplyTypeHuman {
		return ReplyForwardSkipNotHuman, ""
	}
	if domain.VeridianFromLooksLikeDaemon(msg.From) ||
		veridian_ndr.Parse(msg.RawBody, msg.From, msg.Subject).IsNDR ||
		domain.VeridianClassifyReplyType(msg) != domain.VeridianReplyTypeHuman {
		return ReplyForwardSkipNotHuman, ""
	}
	// Une copie deja passee par nous ne repart jamais.
	if domain.VeridianParseRawHeaders(msg.RawBody).Get(domain.VeridianReplyForwardHeader) != "" {
		return ReplyForwardSkipLoop, ""
	}

	// 2. A quel profil appartient l'envoi auquel il repond ?
	ref, found, err := f.repo.ResolveSentMessage(ctx, workspaceID, det.MatchedMessageID, det.ContactEmail)
	if err != nil {
		return ReplyForwardError, ""
	}
	if !found || ref.ProfileID == "" {
		return ReplyForwardSkipNoProfile, ""
	}
	ws, err := f.workspaces.GetByID(ctx, workspaceID)
	if err != nil || ws == nil {
		return ReplyForwardError, ref.ProfileID
	}
	integ := ws.GetIntegrationByID(ref.ProfileID)
	if integ == nil || integ.Type != domain.IntegrationTypeEmail {
		return ReplyForwardSkipNoProfile, ref.ProfileID
	}
	provider := integ.EmailProvider
	recipients := provider.VeridianEffectiveReplyForwardTo()
	if len(recipients) == 0 {
		return ReplyForwardSkipNoConfig, ref.ProfileID
	}
	if provider.Kind != domain.EmailProviderKindSMTP {
		// Le texte brut n'est supporte que par le transport SMTP (profils SMTP et Gmail).
		return ReplyForwardSkipNoConfig, ref.ProfileID
	}

	// 3. Lecture du message et anti-boucle sur les adresses.
	var parsed *mailbox.Parsed
	if p, perr := mailbox.ParseRaw(msg.RawBody); perr == nil {
		parsed = p
	} else {
		parsed = &mailbox.Parsed{}
	}
	fromAddr := domain.VeridianNormalizeEmail(msg.From)
	if fromAddr == "" {
		fromAddr = domain.VeridianNormalizeEmail(parsed.From)
	}
	if fromAddr == "" || provider.VeridianIsOwnSenderAddress(fromAddr) || strings.EqualFold(fromAddr, ref.SenderEmail) {
		return ReplyForwardSkipLoop, ref.ProfileID
	}
	for _, r := range recipients {
		if r == fromAddr {
			// Le destinataire de la copie est celui qui ecrit : sa propre reponse a une copie.
			return ReplyForwardSkipLoop, ref.ProfileID
		}
	}
	replyTo := fromAddr
	if rt := replyFwdBareAddr(parsed.ReplyTo); rt != "" && !provider.VeridianIsOwnSenderAddress(rt) && !replyFwdContains(recipients, rt) {
		replyTo = rt
	}
	prospects := map[string]bool{fromAddr: true, replyTo: true, domain.VeridianNormalizeEmail(det.ContactEmail): true}
	var finalRecipients []string
	for _, r := range recipients {
		if !prospects[r] {
			finalRecipients = append(finalRecipients, r)
		}
	}
	if len(finalRecipients) == 0 {
		return ReplyForwardSkipNoRecipent, ref.ProfileID
	}

	// 4. Reservation idempotente + plafond.
	key := replyForwardKey(workspaceID, ref.ProfileID, msg)
	now := f.now()
	inserted, duplicate, capped, err := f.repo.Claim(ctx, workspaceID, domain.VeridianReplyForwardRecord{
		ReplyKey: key, ProfileID: ref.ProfileID, Recipients: len(finalRecipients), Status: domain.VeridianReplyForwardStatusPending,
	}, f.dailyCap, now)
	if err != nil {
		return ReplyForwardError, ref.ProfileID
	}
	if duplicate {
		return ReplyForwardSkipDuplicate, ref.ProfileID
	}
	if capped || !inserted {
		return ReplyForwardSkipCapped, ref.ProfileID
	}

	// 5. Composition et envoi (une relance apres une courte pause, puis on abandonne).
	sender := pickReplyForwardSender(provider, ref.SenderEmail)
	if sender == nil {
		f.finish(ctx, workspaceID, key, domain.VeridianReplyForwardStatusFailed, "no_sender")
		return ReplyForwardFailed, ref.ProfileID
	}
	prospectName := replyFwdCleanText(parsed.FromName, 80)
	if prospectName == "" {
		prospectName = fromAddr
	}
	origSubject := replyFwdCleanText(mailbox.StripReplyPrefix(replyFwdFirstNonEmpty(parsed.Subject, msg.Subject)), replyForwardMaxSubject)
	if origSubject == "" {
		origSubject = "(sans objet)"
	}
	subject := replyFwdTruncate(fmt.Sprintf("Réponse de %s : %s", prospectName, origSubject), replyForwardMaxSubject)
	text := composeReplyForwardBody(prospectName, fromAddr, origSubject, sender.Email, replyForwardBodyText(parsed, msg))
	fromName := replyFwdCleanText(sender.Name, 80)
	if fromName == "" {
		fromName = sender.Email
	}
	req := domain.SendEmailProviderRequest{
		WorkspaceID:   workspaceID,
		IntegrationID: integ.ID,
		MessageID:     uuid.NewString(),
		FromAddress:   sender.Email,
		FromName:      fromName,
		To:            finalRecipients[0],
		Subject:       subject,
		Content:       "<pre>" + html.EscapeString(text) + "</pre>",
		TextContent:   text,
		PlainTextOnly: true,
		Provider:      &provider,
		EmailOptions: domain.EmailOptions{
			ReplyTo: replyTo,
			CC:      finalRecipients[1:],
		},
		VeridianExtraHeaders: map[string]string{domain.VeridianReplyForwardHeader: "1"},
	}
	var sendErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(f.retryDelay):
			}
		}
		if sendErr = f.mailer.SendEmail(ctx, req, false); sendErr == nil {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	if sendErr != nil {
		f.finish(ctx, workspaceID, key, domain.VeridianReplyForwardStatusFailed, classifyReplyForwardError(sendErr))
		return ReplyForwardFailed, ref.ProfileID
	}
	f.finish(ctx, workspaceID, key, domain.VeridianReplyForwardStatusSent, "")
	return ReplyForwardSent, ref.ProfileID
}

// finish pose le resultat ; le contexte d'envoi peut etre expire, d'ou un contexte frais.
func (f *VeridianReplyForwarder) finish(_ context.Context, workspaceID, key, status, errClass string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.repo.Finish(ctx, workspaceID, key, status, errClass, f.now()); err != nil {
		f.logger.WithFields(map[string]interface{}{"workspace_id": workspaceID, "status": status}).Warn("VeridianReplyForward: journal update failed")
	}
}

// replyForwardKey : empreinte stable de la reponse (Message-ID du mail entrant, sinon
// coordonnees IMAP). Hachee : la cle ne contient rien de lisible.
func replyForwardKey(workspaceID, profileID string, msg *domain.VeridianIMAPMessage) string {
	id := strings.TrimSpace(msg.MessageID)
	if id == "" {
		id = fmt.Sprintf("imap:%s:%s:%d:%d", msg.IntegrationID, msg.Folder, msg.UIDValidity, msg.UID)
	}
	sum := sha256.Sum256([]byte(workspaceID + "|" + profileID + "|" + id))
	return hex.EncodeToString(sum[:])
}

func pickReplyForwardSender(p domain.EmailProvider, sentFrom string) *domain.EmailSender {
	var def *domain.EmailSender
	for i := range p.Senders {
		s := &p.Senders[i]
		if sentFrom != "" && strings.EqualFold(strings.TrimSpace(s.Email), sentFrom) {
			return s
		}
		if def == nil || s.IsDefault {
			def = s
		}
	}
	return def
}

func composeReplyForwardBody(name, addr, subject, sentFrom, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Réponse reçue de %s (%s) à votre message « %s », envoyé depuis %s.\n", name, addr, subject, sentFrom)
	b.WriteString("Pour lui répondre, répondez simplement à ce mail : votre réponse partira chez lui.\n")
	b.WriteString("\n----------------------------------------\n\n")
	if strings.TrimSpace(body) == "" {
		b.WriteString("(le corps de la réponse n'a pas pu être lu)\n")
	} else {
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// replyForwardBodyText : texte de la reponse, citation conservee, borne et nettoye.
func replyForwardBodyText(p *mailbox.Parsed, msg *domain.VeridianIMAPMessage) string {
	t := strings.TrimSpace(p.Text)
	if t == "" && strings.TrimSpace(p.HTML) != "" {
		t = strings.TrimSpace(veridianHTMLToText(p.HTML))
	}
	t = strings.ReplaceAll(strings.ReplaceAll(t, "\r\n", "\n"), "\r", "\n")
	t = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			return r
		}
		return -1
	}, t)
	if len(t) > replyForwardMaxBody {
		t = strings.ToValidUTF8(t[:replyForwardMaxBody], "") + "\n[...] (réponse tronquée)"
	}
	return t
}

func replyFwdCleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f || r == '<' || r == '>' || r == '"':
			return -1
		}
		return r
	}, s)
	return replyFwdTruncate(strings.Join(strings.Fields(s), " "), max)
}

func replyFwdTruncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max])
}

func replyFwdBareAddr(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	a, err := mail.ParseAddress(s)
	if err != nil || strings.ContainsAny(a.Address, "\r\n") {
		return ""
	}
	return strings.ToLower(a.Address)
}

func replyFwdContains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func replyFwdFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// classifyReplyForwardError : classe grossiere, sans donnee personnelle (une erreur SMTP
// brute peut contenir une adresse).
func classifyReplyForwardError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "auth") || strings.Contains(m, "535") || strings.Contains(m, "534"):
		return "auth"
	case strings.Contains(m, "timeout") || strings.Contains(m, "deadline"):
		return "timeout"
	case strings.Contains(m, "invalid request") || strings.Contains(m, "veridian guard"):
		return "refused_local"
	}
	return "send_error"
}
