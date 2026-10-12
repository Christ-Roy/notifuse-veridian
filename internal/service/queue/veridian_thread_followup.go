package queue

import (
	"sort"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork (12/10/2026, demande Robert) : les RELANCES d'une sequence partent
// DANS LE FIL du premier mail.
//
// Constat mesure le 12/10 sur 2 510 copies Gmail (dossier Agences Veridian/Envoye) : 2
// mails sur 2 510 portaient un In-Reply-To, aucun un References, l'objet de la relance
// n'avait aucun rapport avec celui du premier mail ("Un sujet toujours ouvert chez vous ?"
// apres "La vente en ligne chez ..."), et 151 relances sur 332 de ce jour partaient d'une
// AUTRE adresse que le premier mail. Gmail, Outlook et Apple Mail rangeaient donc chaque
// relance dans une conversation separee, depuis une adresse inconnue du prospect.
//
// Mecanisme, applique par le worker juste avant l'envoi (apres le rendu au depilage et le
// choix du profil), pour toute entree d'automation quand le workspace porte
// veridian_thread_followups=true :
//   - on lit, dans message_history, les mails DEJA PARTIS (sent_at pose) vers ce contact
//     par cette automation, du plus ancien au plus recent ;
//   - In-Reply-To = Message-ID du plus recent ; References = la chaine complete ;
//   - l'objet devient "Re: <objet du premier mail>" ;
//   - le Message-ID d'un mail deja parti se recalcule a l'identique (id de l'historique +
//     adresse emettrice, cf. domain.VeridianRFCMessageID) : rien a stocker en plus, donc
//     aucune migration, et les sequences DEJA en cours (premier mail parti avant ce
//     correctif) sont couvertes elles aussi ;
//   - l'objet du premier mail est lu dans message_history.message_data.metadata
//     (cle veridian_subject, posee a chaque envoi d'automation) ; pour un mail parti avant
//     ce correctif il est re-rendu depuis le modele de ce premier mail (version courante,
//     contact courant), les sujets de ces modeles etant deterministes (Liquid, sans spintax) ;
//   - le meme expediteur est garanti en amont par l'ancre de sequence
//     (veridianSequenceAnchor) ; ici on controle seulement et on trace l'ecart.
//
// Premier mail d'une sequence (aucun envoi anterieur) : rien ne change. Echec de lecture
// de l'historique : le mail part sans fil plutot que de ne pas partir (best-effort, tracé).

// veridianMetadataSubjectKey est la cle de message_history.message_data.metadata qui porte
// l'objet reellement envoye.
const veridianMetadataSubjectKey = "veridian_subject"

// veridianThreadLookupLimit borne l'historique du contact relu pour reconstruire le fil.
const veridianThreadLookupLimit = 100

// veridianThreadContext est le fil reconstruit pour une entree.
type veridianThreadContext struct {
	// InReplyTo : Message-ID (sans chevrons) du mail precedent.
	InReplyTo string
	// References : chaine complete (sans chevrons), du premier mail au precedent.
	References []string
	// Root : premier mail de la sequence pour ce contact.
	Root *domain.MessageHistory
	// RootSubject : objet du premier mail ("" si introuvable).
	RootSubject string
}

// veridianThreadFollowupsEnabled lit le reglage du workspace (nil = faux).
func veridianThreadFollowupsEnabled(workspace *domain.Workspace) bool {
	return workspace != nil &&
		workspace.Settings.VeridianThreadFollowups != nil &&
		*workspace.Settings.VeridianThreadFollowups
}

// veridianThreadReplySubject fabrique "Re: <objet>" sans empiler les "Re:".
func veridianThreadReplySubject(root string) string {
	s := strings.TrimSpace(root)
	for {
		l := strings.ToLower(s)
		if strings.HasPrefix(l, "re:") {
			s = strings.TrimSpace(s[3:])
			continue
		}
		break
	}
	if s == "" {
		return ""
	}
	return "Re: " + s
}

// veridianThreadFromHistory est la partie pure (sans I/O) : elle ordonne les envois de
// l'automation vers ce contact et en tire le fil. found=false s'il n'y a aucun envoi
// anterieur. currentMessageID est exclu (relance d'une entree deja ecrite en historique).
func veridianThreadFromHistory(history []*domain.MessageHistory, automationID, currentMessageID string) (veridianThreadContext, bool) {
	sent := make([]*domain.MessageHistory, 0, len(history))
	for _, m := range history {
		if m == nil || m.AutomationID == nil || *m.AutomationID != automationID {
			continue
		}
		if m.SentAt == nil || m.ID == currentMessageID || m.VeridianSenderEmail == "" {
			continue
		}
		if m.Channel != "" && m.Channel != "email" {
			continue
		}
		if m.VeridianMessageType == domain.VeridianMessageTypeTransactional {
			continue
		}
		sent = append(sent, m)
	}
	if len(sent) == 0 {
		return veridianThreadContext{}, false
	}
	sort.SliceStable(sent, func(i, j int) bool { return sent[i].SentAt.Before(*sent[j].SentAt) })

	ctx := veridianThreadContext{Root: sent[0]}
	for _, m := range sent {
		if id := domain.VeridianRFCMessageID(m.ID, m.VeridianSenderEmail); id != "" {
			ctx.References = append(ctx.References, id)
		}
	}
	if n := len(ctx.References); n > 0 {
		ctx.InReplyTo = ctx.References[n-1]
	}
	if sub, ok := sent[0].MessageData.Metadata[veridianMetadataSubjectKey].(string); ok {
		ctx.RootSubject = strings.TrimSpace(sub)
	}
	return ctx, len(ctx.References) > 0
}

// veridianApplyThreading pose le fil sur l'entree. Appele par processEntry apres le rendu
// au depilage. Sans effet si le reglage est absent, pour un envoi hors automation, ou pour
// le premier mail d'une sequence.
func (w *EmailQueueWorker) veridianApplyThreading(workspace *domain.Workspace, entry *domain.EmailQueueEntry) {
	if !veridianThreadFollowupsEnabled(workspace) || entry == nil ||
		entry.SourceType != domain.EmailQueueSourceAutomation || entry.SourceID == "" ||
		entry.ContactEmail == "" || w.messageHistoryRepo == nil {
		return
	}
	history, _, err := w.messageHistoryRepo.GetByContact(w.ctx, workspace.ID, workspace.Settings.SecretKey, entry.ContactEmail, veridianThreadLookupLimit, 0)
	if err != nil {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID, "recipient": entry.ContactEmail, "error": err.Error(),
		}).Warn("Thread follow-up: history unreadable, sending without thread headers")
		return
	}
	tc, found := veridianThreadFromHistory(history, entry.SourceID, entry.MessageID)
	if !found {
		return // premier mail de la sequence
	}

	rootSubject := tc.RootSubject
	if rootSubject == "" {
		rootSubject = w.veridianLegacyRootSubject(workspace, entry, tc.Root)
	}
	if reply := veridianThreadReplySubject(rootSubject); reply != "" {
		entry.Payload.Subject = reply
	} else {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID, "recipient": entry.ContactEmail, "root_message_id": tc.Root.ID,
		}).Warn("Thread follow-up: root subject unknown, keeping the follow-up subject (headers still set)")
	}
	entry.Payload.EmailOptions.InReplyTo = tc.InReplyTo
	entry.Payload.EmailOptions.References = tc.References

	if want := strings.ToLower(strings.TrimSpace(tc.Root.VeridianSenderEmail)); want != "" &&
		want != strings.ToLower(strings.TrimSpace(entry.Payload.FromAddress)) {
		w.logger.WithFields(map[string]interface{}{
			"entry_id": entry.ID, "recipient": entry.ContactEmail,
			"first_sender": tc.Root.VeridianSenderEmail, "this_sender": entry.Payload.FromAddress,
		}).Warn("Thread follow-up: sender differs from the first mail of the sequence")
	}
}

// veridianLegacyRootSubject retrouve l'objet d'un premier mail parti AVANT que l'objet soit
// consigne dans l'historique : on re-rend le modele de ce premier mail pour ce contact.
// "" si le rendu est impossible (modele supprime, etc.).
func (w *EmailQueueWorker) veridianLegacyRootSubject(workspace *domain.Workspace, entry *domain.EmailQueueEntry, root *domain.MessageHistory) string {
	if w.queuedEmailRenderer == nil || root == nil || root.TemplateID == "" {
		return ""
	}
	rootEntry := *entry
	rootEntry.TemplateID = root.TemplateID
	rootEntry.MessageID = root.ID
	out, err := w.queuedEmailRenderer.RenderQueuedEmail(w.ctx, workspace, &rootEntry)
	if err != nil || out == nil {
		return ""
	}
	return strings.TrimSpace(out.Subject)
}
