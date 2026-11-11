package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"net/textproto"
	"regexp"
	"strconv"
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
//   - ROBUSTE (11/11/2026) : la copie composee est gardee dans le journal tant qu'elle n'est pas
//     partie. Un echec transitoire (SMTP indisponible, 4xx, timeout, reseau) est rejoue par la
//     tache native "veridian_reply_forward_retry" (1 min, 5 min, 30 min, 2 h, 6 h, 12 h) ; un echec
//     definitif (5xx, refus local) reste "failed" avec sa classe. Le plafond n'en perd JAMAIS une :
//     au-dela, la copie est "deferred" et part dans l'ordre des qu'une place se libere. Un processus
//     qui meurt en plein envoi : le bail de 5 min expire et la copie est reprise (au pire un doublon).
//   - ANTI-BOUCLE : jamais pour un rebond/DSN, un auto-repondeur ou un defi ; jamais vers
//     l'adresse d'envoi du profil ni vers le prospect ; jamais si le message entrant porte deja
//     X-Veridian-Forwarded ; plafond par profil (veridian_reply_forward_daily_cap, 500 par defaut) qui REPORTE au lieu de perdre.
//   - AUCUNE donnee personnelle dans les journaux applicatifs (ni adresse, ni objet, ni corps).

const (
	replyForwardTimeout     = 90 * time.Second
	replyForwardMaxBody     = 64 << 10
	replyForwardMaxSubject  = 200
	replyForwardMaxInFlight = 8
	// replyForwardQueueWait : attente d'une place d'envoi avant de confier la copie a la tache de rejeu.
	replyForwardQueueWait = 20 * time.Second
	// replyForwardBatch : copies traitees par passage de la tache.
	replyForwardBatch = 25
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
	ReplyForwardDeferred       VeridianReplyForwardOutcome = "deferred"
	ReplyForwardRetrying       VeridianReplyForwardOutcome = "retrying"
	ReplyForwardQueued         VeridianReplyForwardOutcome = "queued"
	ReplyForwardSkipNoRecipent VeridianReplyForwardOutcome = "skip_no_recipient"
	ReplyForwardError          VeridianReplyForwardOutcome = "error"
)

// VeridianReplyForwarder envoie les copies de reponses.
type VeridianReplyForwarder struct {
	repo       domain.VeridianReplyForwardRepository
	workspaces veridianReplyForwardWorkspaces
	mailer     veridianReplyForwardMailer
	logger     logger.Logger

	tasks     veridianReplyForwardTaskEnsurer
	queueWait time.Duration
	now       func() time.Time
	// run lance le travail ; par defaut une goroutine (les tests l'executent en ligne).
	run func(func())
	sem chan struct{}
}

// veridianReplyForwardTaskEnsurer garantit l'existence de la tache native de rejeu du workspace.
type veridianReplyForwardTaskEnsurer interface {
	EnsureReplyForwardTask(ctx context.Context, workspaceID string) error
}

// SetTaskEnsurer branche la creation de la tache de rejeu (nil-safe : sans elle, pas de rejeu automatique).
func (f *VeridianReplyForwarder) SetTaskEnsurer(e veridianReplyForwardTaskEnsurer) { f.tasks = e }

// NewVeridianReplyForwarder construit le forwarder.
func NewVeridianReplyForwarder(repo domain.VeridianReplyForwardRepository, workspaces veridianReplyForwardWorkspaces, mailer veridianReplyForwardMailer, log logger.Logger) *VeridianReplyForwarder {
	return &VeridianReplyForwarder{
		repo:       repo,
		workspaces: workspaces,
		mailer:     mailer,
		logger:     log,
		queueWait:  replyForwardQueueWait,
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
	f.run(func() {
		defer func() {
			if r := recover(); r != nil {
				f.logger.WithFields(map[string]interface{}{"workspace_id": workspaceID}).Error("VeridianReplyForward: panic recovered")
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), replyForwardTimeout)
		defer cancel()
		// Pas de place d'envoi : la copie n'est PAS abandonnee, elle est reservee dans le journal et la
		// tache de rejeu l'envoie (jamais de perte silencieuse sous rafale).
		sendNow := false
		select {
		case f.sem <- struct{}{}:
			sendNow = true
			defer func() { <-f.sem }()
		case <-time.After(f.queueWait):
			f.logger.WithFields(map[string]interface{}{"workspace_id": workspaceID}).Warn("VeridianReplyForward: no send slot, copy handed to the retry task")
		}
		f.run2(ctx, workspaceID, msg, det, sendNow)
	})
}

// Forward fait le travail, de facon synchrone (testable). Rend l'issue ; n'echoue jamais.
func (f *VeridianReplyForwarder) Forward(ctx context.Context, workspaceID string, msg *domain.VeridianIMAPMessage, det domain.VeridianReplyDetection) VeridianReplyForwardOutcome {
	return f.run2(ctx, workspaceID, msg, det, true)
}

// run2 : Forward avec le choix d'envoyer tout de suite (sendNow) ou de confier l'envoi a la tache de rejeu.
func (f *VeridianReplyForwarder) run2(ctx context.Context, workspaceID string, msg *domain.VeridianIMAPMessage, det domain.VeridianReplyDetection, sendNow bool) VeridianReplyForwardOutcome {
	out, profileID := f.forward(ctx, workspaceID, msg, det, sendNow)
	fields := map[string]interface{}{"workspace_id": workspaceID, "outcome": string(out)}
	if profileID != "" {
		fields["profile_id"] = profileID
	}
	switch out {
	case ReplyForwardSent:
		f.logger.WithFields(fields).Info("VeridianReplyForward: copy sent")
	case ReplyForwardFailed, ReplyForwardError, ReplyForwardSkipLoop, ReplyForwardDeferred, ReplyForwardRetrying:
		f.logger.WithFields(fields).Warn("VeridianReplyForward: no copy sent")
	default:
		f.logger.WithFields(fields).Debug("VeridianReplyForward: skipped")
	}
	return out
}

func (f *VeridianReplyForwarder) forward(ctx context.Context, workspaceID string, msg *domain.VeridianIMAPMessage, det domain.VeridianReplyDetection, sendNow bool) (VeridianReplyForwardOutcome, string) {
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

	// 4. Composition (pure) de la copie : c'est elle qui est gardee dans le journal pour le rejeu.
	sender := pickReplyForwardSender(provider, ref.SenderEmail)
	payload := replyForwardPayload{IntegrationID: integ.ID, MessageID: uuid.NewString(), To: finalRecipients[0], CC: finalRecipients[1:], ReplyTo: replyTo}
	if sender != nil {
		prospectName := replyFwdCleanText(parsed.FromName, 80)
		if prospectName == "" {
			prospectName = fromAddr
		}
		origSubject := replyFwdCleanText(mailbox.StripReplyPrefix(replyFwdFirstNonEmpty(parsed.Subject, msg.Subject)), replyForwardMaxSubject)
		if origSubject == "" {
			origSubject = "(sans objet)"
		}
		payload.Subject = replyFwdTruncate(fmt.Sprintf("Réponse de %s : %s", prospectName, origSubject), replyForwardMaxSubject)
		payload.Text = composeReplyForwardBody(prospectName, fromAddr, origSubject, sender.Email, replyForwardBodyText(parsed, msg))
		payload.FromAddress = sender.Email
		payload.FromName = replyFwdCleanText(sender.Name, 80)
		if payload.FromName == "" {
			payload.FromName = sender.Email
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ReplyForwardError, ref.ProfileID
	}

	// 5. Reservation idempotente + plafond (le plafond reporte, il ne perd jamais).
	key := replyForwardKey(workspaceID, ref.ProfileID, msg)
	claim, err := f.repo.Claim(ctx, workspaceID, domain.VeridianReplyForwardRecord{
		ReplyKey: key, ProfileID: ref.ProfileID, Recipients: len(finalRecipients),
		Status: domain.VeridianReplyForwardStatusDeferred, Payload: string(raw),
	}, provider.VeridianEffectiveReplyForwardDailyCap(), f.now())
	if err != nil {
		return ReplyForwardError, ref.ProfileID
	}
	if claim.Duplicate {
		return ReplyForwardSkipDuplicate, ref.ProfileID
	}
	f.ensureTask(workspaceID)
	if claim.Deferred {
		return ReplyForwardDeferred, ref.ProfileID
	}
	if !claim.Leased {
		return ReplyForwardError, ref.ProfileID
	}
	if !sendNow {
		if err := f.reschedule(workspaceID, key, "", f.now()); err != nil {
			return ReplyForwardError, ref.ProfileID
		}
		return ReplyForwardQueued, ref.ProfileID
	}
	return f.deliver(ctx, workspaceID, provider, domain.VeridianReplyForwardJob{ReplyKey: key, ProfileID: ref.ProfileID, Payload: string(raw), Attempts: claim.Attempts}), ref.ProfileID
}

// replyForwardPayload : la copie composee, gardee dans le journal le temps qu'elle n'est pas partie.
// MessageID est fixe : un rejeu emet le meme Message-ID (le destinataire dedoublonne).
type replyForwardPayload struct {
	IntegrationID string   `json:"i"`
	MessageID     string   `json:"m"`
	FromAddress   string   `json:"fa"`
	FromName      string   `json:"fn"`
	To            string   `json:"to"`
	CC            []string `json:"cc,omitempty"`
	ReplyTo       string   `json:"rt"`
	Subject       string   `json:"s"`
	Text          string   `json:"t"`
}

// deliver fait UNE tentative d'envoi d'une copie prise a bail et en consigne l'issue : sent, failed
// (definitif ou tentatives epuisees) ou retry (echec transitoire, rejeu a echeance).
func (f *VeridianReplyForwarder) deliver(ctx context.Context, workspaceID string, provider domain.EmailProvider, job domain.VeridianReplyForwardJob) VeridianReplyForwardOutcome {
	var p replyForwardPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		f.finish(ctx, workspaceID, job.ReplyKey, domain.VeridianReplyForwardStatusFailed, "bad_payload")
		return ReplyForwardFailed
	}
	if p.FromAddress == "" {
		f.finish(ctx, workspaceID, job.ReplyKey, domain.VeridianReplyForwardStatusFailed, "no_sender")
		return ReplyForwardFailed
	}
	req := domain.SendEmailProviderRequest{
		WorkspaceID:   workspaceID,
		IntegrationID: p.IntegrationID,
		MessageID:     p.MessageID,
		FromAddress:   p.FromAddress,
		FromName:      p.FromName,
		To:            p.To,
		Subject:       p.Subject,
		Content:       "<pre>" + html.EscapeString(p.Text) + "</pre>",
		TextContent:   p.Text,
		PlainTextOnly: true,
		Provider:      &provider,
		EmailOptions: domain.EmailOptions{
			ReplyTo: p.ReplyTo,
			CC:      p.CC,
		},
	}
	sendCtx := domain.WithVeridianExtraHeaders(ctx, map[string]string{domain.VeridianReplyForwardHeader: "1"})
	sendErr := f.mailer.SendEmail(sendCtx, req, false)
	if sendErr == nil {
		f.finish(ctx, workspaceID, job.ReplyKey, domain.VeridianReplyForwardStatusSent, "")
		return ReplyForwardSent
	}
	class, permanent := classifyReplyForwardError(sendErr)
	delays := domain.VeridianReplyForwardRetryDelays
	if permanent || job.Attempts < 1 || job.Attempts > len(delays) {
		f.finish(ctx, workspaceID, job.ReplyKey, domain.VeridianReplyForwardStatusFailed, class)
		return ReplyForwardFailed
	}
	if err := f.reschedule(workspaceID, job.ReplyKey, class, f.now().Add(delays[job.Attempts-1])); err != nil {
		f.logger.WithFields(map[string]interface{}{"workspace_id": workspaceID}).Warn("VeridianReplyForward: retry could not be scheduled, lease will expire")
	}
	f.ensureTask(workspaceID)
	return ReplyForwardRetrying
}

// ProcessDue est le travail de la tache native veridian_reply_forward_retry : place les copies reportees,
// prend a bail celles qui sont dues (rejeu ou bail expire) et tente l'envoi. Rend le nombre traite.
func (f *VeridianReplyForwarder) ProcessDue(ctx context.Context, workspaceID string, deadline time.Time) (int, error) {
	ws, err := f.workspaces.GetByID(ctx, workspaceID)
	if err != nil || ws == nil {
		return 0, fmt.Errorf("reply forward: workspace unavailable: %v", err)
	}
	caps := map[string]int{}
	for i := range ws.Integrations {
		if ws.Integrations[i].Type == domain.IntegrationTypeEmail {
			caps[ws.Integrations[i].ID] = ws.Integrations[i].EmailProvider.VeridianEffectiveReplyForwardDailyCap()
		}
	}
	jobs, err := f.repo.LeaseDue(ctx, workspaceID, caps, domain.VeridianReplyForwardDefaultDailyCap, f.now(), replyForwardBatch)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, job := range jobs {
		if ctx.Err() != nil || (!deadline.IsZero() && time.Now().After(deadline)) {
			break // le bail des copies restantes expire seul : elles reviennent au prochain passage
		}
		integ := ws.GetIntegrationByID(job.ProfileID)
		if integ == nil || integ.Type != domain.IntegrationTypeEmail || len(integ.EmailProvider.VeridianEffectiveReplyForwardTo()) == 0 || integ.EmailProvider.Kind != domain.EmailProviderKindSMTP {
			// Redirection retiree depuis : on n'envoie plus rien, et ca se voit (failed/disabled).
			f.finish(ctx, workspaceID, job.ReplyKey, domain.VeridianReplyForwardStatusFailed, "disabled")
			done++
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, replyForwardTimeout)
		out := f.deliver(sendCtx, workspaceID, integ.EmailProvider, job)
		cancel()
		f.logger.WithFields(map[string]interface{}{"workspace_id": workspaceID, "profile_id": job.ProfileID, "outcome": string(out), "attempt": job.Attempts}).Info("VeridianReplyForward: replay done")
		done++
	}
	return done, nil
}

func (f *VeridianReplyForwarder) ensureTask(workspaceID string) {
	if f.tasks == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.tasks.EnsureReplyForwardTask(ctx, workspaceID); err != nil {
		f.logger.WithFields(map[string]interface{}{"workspace_id": workspaceID}).Warn("VeridianReplyForward: retry task could not be ensured")
	}
}

func (f *VeridianReplyForwarder) reschedule(workspaceID, key, class string, next time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return f.repo.Reschedule(ctx, workspaceID, key, class, next)
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

var replyForwardSMTPCode = regexp.MustCompile(`(?:code:?\s*|^)([45]\d\d)\b`)

// classifyReplyForwardError : classe grossiere, sans donnee personnelle (une erreur SMTP brute peut
// contenir une adresse), et verdict transitoire / definitif. Transitoires (rejoues) : timeout, reseau,
// 4xx, authentification (une config que l'utilisateur peut corriger entre deux essais) et tout ce qu'on
// ne sait pas classer. Definitifs : 5xx hors authentification, refus local.
func classifyReplyForwardError(err error) (class string, permanent bool) {
	if err == nil {
		return "", false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout", false
	}
	m := strings.ToLower(err.Error())
	if strings.Contains(m, "invalid request") || strings.Contains(m, "veridian guard") ||
		strings.Contains(m, "invalid sender") || strings.Contains(m, "invalid recipient") || strings.Contains(m, "invalid cc") {
		return "refused_local", true
	}
	code := 0
	var tpe *textproto.Error
	if errors.As(err, &tpe) {
		code = tpe.Code
	} else if sub := replyForwardSMTPCode.FindStringSubmatch(m); sub != nil {
		code, _ = strconv.Atoi(sub[1])
	}
	switch {
	case code == 530 || code == 534 || code == 535 || strings.Contains(m, "auth"):
		return "auth", false
	case code >= 400 && code < 500:
		return "smtp_4xx", false
	case code >= 500 && code < 600:
		return "smtp_5xx", true
	case strings.Contains(m, "timeout") || strings.Contains(m, "deadline"):
		return "timeout", false
	case strings.Contains(m, "connect") || strings.Contains(m, "refused") || strings.Contains(m, "no such host") ||
		strings.Contains(m, "reset") || strings.Contains(m, "eof") || strings.Contains(m, "broken pipe") || strings.Contains(m, "tls"):
		return "network", false
	}
	return "send_error", false
}
