package queue

import (
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/emailerror"
)

// Lot A du socle modulaire (29/10/2026) : le coeur d'envoi expose cinq points d'extension a
// processEntry (worker.go) au lieu de vingt appels de portes Veridian en ligne. L'ORDRE des
// etapes est celui d'avant l'extraction ; le test TestProcessEntry_StageOrder le fige.
//
//	Pre       entree transactionnelle de sequence, porte du premier mail (J0)
//	Select    classe du destinataire, choix de l'integration (bascule de pool), report ou echec permanent
//	PreRender pre-filtre, rendu au depilage, fil de conversation, filet anti-hash
//	PreSend   garde automation, garde broadcast, prise de l'entree (sc.Claim), reservation du quota du jour
//	Record    issue du transport : liberation du quota si le transport n'a rien accepte, journal de decision si envoye
//
// Entre ces points, worker.go garde ce qui est amont : integration introuvable, attente du rate limiter,
// construction de la requete, SendEmail, classification d'erreur, disjoncteur, MarkAsSent, historique.

// SendPipeline : les points d'extension. Pre, Select, PreRender et PreSend rendent true pour poursuivre ;
// false veut dire que l'etape a DEJA regle l'entree (reportee, echouee, sortie de sequence, prise par un
// autre worker) et que processEntry s'arrete. Une implementation ne doit jamais rendre false sans avoir
// regle l'entree, sinon elle reste en file et sera reexaminee.
//
// Priorite et ordre : l'etape Select choisit l'integration pour une entree (ordre des candidats : continuite
// de sequence d'abord, puis l'integration assignee, puis le reste du pool). La priorite entre entrees se
// pose a l'enqueue (email_queue.priority, depilage priority ASC) et n'a pas besoin de ce point.
type SendPipeline interface {
	Pre(sc *SendContext) bool
	Select(sc *SendContext) bool
	PreRender(sc *SendContext) bool
	PreSend(sc *SendContext) bool
	Record(sc *SendContext, res SendResult)
}

// SendContext : l'etat d'un envoi qui traverse les etapes.
type SendContext struct {
	Workspace *domain.Workspace
	Entry     *domain.EmailQueueEntry
	// Integration : l'integration assignee a l'entree (posee avant Select), remplacee par celle du pool retenue.
	Integration *domain.Integration
	// Claim marque l'entree « en cours » (MarkAsProcessing) ; false = deja prise ou erreur, l'etape s'arrete.
	Claim func() bool

	selection   veridianSelectionResult
	quotaLeases []*veridianDailyQuotaLease
}

// SendResult : issue du transport. Err nil = accepte par le relais.
type SendResult struct{ Err error }

// veridianSendPipeline : l'implementation Veridian, les portes historiques deplacees telles quelles.
type veridianSendPipeline struct{ w *EmailQueueWorker }

var _ SendPipeline = (*veridianSendPipeline)(nil)

// SetSendPipeline remplace le pipeline (tests de caracterisation, variantes). nil = pipeline Veridian.
func (w *EmailQueueWorker) SetSendPipeline(p SendPipeline) { w.pipeline = p }

func (w *EmailQueueWorker) sendPipeline() SendPipeline {
	if w.pipeline != nil {
		return w.pipeline
	}
	return &veridianSendPipeline{w: w}
}

// Pre : voir SendPipeline.
func (p *veridianSendPipeline) Pre(sc *SendContext) bool {
	w, workspace, entry := p.w, sc.Workspace, sc.Entry

	// Veridian fork (lot 4, 08/10/2026) : un mail transactionnel de sequence part par
	// le seul profil transactionnel reserve, sans aucune porte commerciale. Sans
	// profil reserve, l'entree suit le chemin commercial ci-dessous, comme avant.
	// Cf. veridian_transactional_entry.go.
	if w.veridianProcessTransactionalEntry(workspace, entry) {
		return false
	}
	// Veridian fork (12/10/2026) : une relance ne part pas si le premier mail (J0) de la
	// meme automation n'a jamais ete livre a ce contact (echec, rebond dur, refus de
	// politique, aucun J0). Le contact sort de la sequence, la raison est journalisee.
	// Un rebond mou ne sort personne. Cf. veridian_first_mail_gate.go.
	if !w.veridianFirstMailGate(workspace, entry) {
		return false
	}
	return true
}

// Select : voir SendPipeline.
func (p *veridianSendPipeline) Select(sc *SendContext) bool {
	w, workspace, entry := p.w, sc.Workspace, sc.Entry
	integration := sc.Integration

	// Resolve once and freeze the final class used by every policy gate and by
	// message_history. Payload tags remain authoritative; otherwise this is the
	// cached real-MX classification.
	// Exception (22/10, fiche 69) : le tag historique freemail_fr est ré-éclaté en FAI.
	if entry.Payload.VeridianProviderClass == "" || entry.Payload.VeridianProviderClass == domain.ProviderClassFreemailFR {
		entry.Payload.VeridianProviderClass = w.veridianClassifyRecipient(entry)
	}

	// Veridian fork (correctif 2026-10-05, incident robertbrunon 05/10) — POOL
	// FAILOVER AT SEND TIME. The circuit breaker check and every coarse policy
	// gate below (exclusion, reputation, class throttle, daily cap, per-sender
	// cap, sending window) used to run ONCE against the integration frozen at
	// enqueue: a capped/unavailable integration simply rescheduled the entry
	// to tomorrow, even when a sibling in the workspace's rotation pool
	// (VeridianMarketingEmailProviderIDs) had room. They now run PER CANDIDATE,
	// in priority order (sequence continuity for automation relances, then the
	// assigned integration, then the rest of the pool) via
	// veridianSelectSendableIntegration — same gates, same thresholds, just
	// replayed for each pool member until one accepts. No pool configured =
	// exactly one candidate (the assigned integration): strict non-regression.
	// Cf. veridian_pool_failover.go.
	selection := w.veridianSelectSendableIntegration(workspace, entry, integration)
	sc.selection = selection

	if selection.Permanent {
		// Every reachable pool member excludes this recipient's class: same
		// permanent-failure contract as before (MarkAsProcessing first so
		// handleError, which assumes the attempt counter has advanced, deletes
		// the entry instead of leaving it half-processed).
		if err := w.queueRepo.MarkAsProcessing(w.ctx, workspace.ID, entry.ID); err != nil {
			w.logger.WithFields(map[string]interface{}{
				"entry_id": entry.ID,
				"error":    err.Error(),
			}).Warn("Failed to mark entry as processing for excluded-class skip")
			return false
		}
		w.logger.WithFields(map[string]interface{}{
			"entry_id":       entry.ID,
			"recipient":      entry.ContactEmail,
			"provider_class": selection.ExcludedClass,
		}).Info("Excluded provider class on every reachable pool member, skipping SMTP and failing permanently")
		excludedErr := &emailerror.ClassifiedError{
			Original:  fmt.Errorf("excluded_provider_class:%s", selection.ExcludedClass),
			Type:      emailerror.ErrorTypeRecipient,
			Provider:  string(integration.EmailProvider.Kind),
			Retryable: false,
		}
		w.handleError(workspace, entry, excludedErr, excludedErr)
		return false
	}

	if selection.Candidate == nil {
		// No pool member currently has room for this entry (every gate, or the
		// circuit breaker, rejected every reachable candidate). Reschedule
		// WITHOUT incrementing attempts, same contract as every coarse gate
		// this fork replaced — the shortest delay observed across candidates
		// is used so the next re-check happens as soon as the most promising
		// one might open up.
		w.logger.WithFields(map[string]interface{}{
			"entry_id":       entry.ID,
			"integration_id": entry.IntegrationID,
			"retry_in":       selection.RetryDelay.String(),
		}).Debug("No pool member has room for this entry, rescheduling without attempt increment")
		// Fiche 62 : le report porte desormais sa RAISON (la vraie : toutes les portes
		// ont ete evaluees) et la decision est journalisee selon le niveau du workspace.
		w.veridianDeferEntry(workspace, entry, veridianDeferral{
			Reason:  selection.Reason,
			Detail:  selection.ReasonDetail,
			Profile: selection.ReasonProfile,
			Delay:   selection.RetryDelay,
		}, &selection, func() error {
			return w.queueRepo.SetNextRetry(w.ctx, workspace.ID, entry.ID, time.Now().Add(selection.RetryDelay))
		})
		return false
	}

	// A pool member accepted this entry: commit it. Rewriting Payload.FromAddress
	// also rewrites, downstream, the Message-ID host (veridianMessageIDForSend
	// derives it from FromAddress) and the DKIM signature applied by THIS
	// integration's own relay — integration, sender, Message-ID domain and DKIM
	// domain move together, exactly the fix for the bug this fork closes.
	chosen := selection.Candidate
	if chosen.IntegrationID != entry.IntegrationID {
		w.logger.WithFields(map[string]interface{}{
			"entry_id":                entry.ID,
			"assigned_integration_id": entry.IntegrationID,
			"failover_integration_id": chosen.IntegrationID,
			"recipient":               entry.ContactEmail,
		}).Info("Pool failover: rerouting entry to a sibling integration with room")
	}
	entry.IntegrationID = chosen.IntegrationID
	entry.Payload.FromAddress = chosen.FromAddress
	entry.Payload.FromName = chosen.FromName
	if resolved := workspace.GetIntegrationByID(chosen.IntegrationID); resolved != nil {
		integration = resolved
	}
	entry.ProviderKind = integration.EmailProvider.Kind
	sc.Integration = integration
	return true
}

// PreRender : voir SendPipeline.
func (p *veridianSendPipeline) PreRender(sc *SendContext) bool {
	w, workspace, entry := p.w, sc.Workspace, sc.Entry
	integration := sc.Integration

	// Veridian fork (Lot 7): PRE-FILTER gate (cold outbound). Skips addresses we
	// KNOW are dead (invalid syntax / disposable domain / DNS-undeliverable)
	// BEFORE hitting SMTP, to protect IP reputation and quota. Unlike the
	// throttle/cap gates above, an invalid address never becomes valid: we route
	// it to PERMANENT failure (not a reschedule) via the same path as a
	// non-retryable `550 user unknown` provider error, so it is never re-tried in
	// a loop. Best-effort STRICT: any indeterminate verdict lets the send through.
	// Cf. veridian_prefilter.go.
	if reason, invalid := w.veridianPrefilterRecipient(entry); invalid {
		// MarkAsProcessing first (increments attempts) so handleError, which
		// assumes the attempt counter has already advanced, deletes the entry as
		// a permanent failure instead of leaving it half-processed.
		if err := w.queueRepo.MarkAsProcessing(w.ctx, workspace.ID, entry.ID); err != nil {
			w.logger.WithFields(map[string]interface{}{
				"entry_id": entry.ID,
				"error":    err.Error(),
			}).Warn("Failed to mark entry as processing for pre-filter skip")
			return false
		}
		w.logger.WithFields(map[string]interface{}{
			"entry_id":  entry.ID,
			"recipient": entry.ContactEmail,
			"reason":    string(reason),
		}).Info("Pre-filtered recipient, skipping SMTP and failing permanently")
		// Non-retryable recipient error → handleError marks it permanent (delete
		// + message_history FailedAt). No SMTP connection is ever opened.
		prefilterErr := &emailerror.ClassifiedError{
			Original:  fmt.Errorf("pre-filtered recipient: %s", reason),
			Type:      emailerror.ErrorTypeRecipient,
			Provider:  string(integration.EmailProvider.Kind),
			Retryable: false,
		}
		w.handleError(workspace, entry, prefilterErr, prefilterErr)
		return false
	}

	// Veridian fork: RENDU AU DEPILAGE. Le contenu (sujet, texte, html) est
	// re-rendu depuis le modele COURANT et le contact COURANT juste avant
	// l'envoi ; un echec de rendu = le message ne part pas (echec lisible).
	// Cf. veridian_render_at_send.go.
	if !w.veridianRenderAtSend(workspace, entry) {
		return false
	}

	// Veridian fork (12/10/2026) : une relance de sequence part DANS LE FIL du premier
	// mail (In-Reply-To, References, "Re: <objet du premier mail>"), quand le workspace
	// l'a demande. Apres le rendu (qui fixe l'objet) et le choix du profil (qui fixe
	// l'expediteur). Cf. veridian_thread_followup.go.
	w.veridianApplyThreading(workspace, entry)

	// Veridian fork: ANTI-HASH residual net (cold outbound). Best-effort,
	// LOG-ONLY filet : la variété est garantie à l'enqueue (re-spin) ; ce gate ne
	// fait que CONSTATER + TRACER une collision résiduelle (template sans spintax)
	// sans jamais bloquer l'envoi (pas de perte de mail). No-op sans hash sur le
	// payload. Placé après le pré-filtre, avant MarkAsProcessing.
	// Cf. veridian_content_hash_gate.go.
	w.veridianContentHashGate(workspace, &integration.EmailProvider, entry)
	return true
}

// PreSend : voir SendPipeline.
func (p *veridianSendPipeline) PreSend(sc *SendContext) bool {
	w, workspace, entry := p.w, sc.Workspace, sc.Entry
	integration := sc.Integration

	// Last possible policy check before constructing the provider request and
	// opening SMTP. This closes the race where the row was enqueued or even
	// claimed before a reply, unsubscribe or automation pause was persisted.
	if !w.veridianAutomationSendAllowed(workspace, entry) {
		return false
	}
	if !w.veridianBroadcastSendAllowed(workspace, entry) {
		return false
	}

	// Claim the queue row (sc.Claim, posed by the worker) before reserving quota.
	if !sc.Claim() {
		return false
	}

	// Atomic daily quota authorization is the LAST database gate before SMTP.
	// Exclusions, prefilter, window and final automation checks never consume it.
	quotaLeases, delay, capped := w.veridianReserveDailyQuota(workspace, &integration.EmailProvider, entry)
	sc.quotaLeases = quotaLeases
	if capped {
		w.veridianDeferEntry(workspace, entry, veridianDeferral{
			Reason:        domain.VeridianReasonQuotaDenied,
			Profile:       entry.IntegrationID,
			Delay:         delay,
			RefundAttempt: true,
		}, &sc.selection, func() error {
			return w.queueRepo.SetNextRetryAndRefundAttempt(w.ctx, workspace.ID, entry.ID, time.Now().Add(delay))
		})
		return false
	}
	return true
}

// Record : voir SendPipeline.
func (p *veridianSendPipeline) Record(sc *SendContext, res SendResult) {
	w, workspace, entry := p.w, sc.Workspace, sc.Entry
	if res.Err != nil {
		// Release only when the transport proves the remote did not accept DATA.
		// Ambiguous post-DATA timeouts keep capacity consumed to avoid oversending.
		if emailerror.IsBeforeAcceptance(res.Err) {
			w.veridianReleaseDailyQuotas(workspace.ID, sc.quotaLeases)
		}
		return
	}
	// Fiche 62 : journal des decisions, l'envoi accepte avec les portes qui l'ont laisse passer.
	w.veridianRecordTerminal(workspace, entry, domain.VeridianOutcomeSent, "", "", entry.IntegrationID, &sc.selection)
}
