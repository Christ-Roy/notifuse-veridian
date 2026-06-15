package http

// === Veridian patch — 2026-06-15 — endpoint de test cold lifecycle (staging-only) ===
//
// POST /api/veridian/admin/cold-simulate. Auth HMAC Hub. STRICTEMENT staging :
// 503 sur prod / dev / self-hosted (même garde-fou que test-tenants-stats).
//
// POURQUOI cet endpoint existe :
//   Deux comportements cold end-to-end que Robert veut validés en E2E déroulé ne
//   sont atteignables par AUCUN endpoint applicatif :
//     1. stop-on-reply  : VeridianReplyService.ProcessInboundMessage n'est appelé
//        QUE par le poller IMAP (push) ou le gate Lot 9 (pull). Pas de vraie boîte
//        IMAP en CI, et CONTRAINTE DURE : zéro mail réel (les alias test routent
//        vers la boîte perso de Robert).
//     2. cap destinataire : veridianDailyCapGate vit dans le worker et lit
//        message_history, qui ne se peuple QUE via un vrai envoi SMTP (interdit).
//
//   Plutôt qu'un test "vert" mensonger (mock) ou un contournement (SQLite/cron),
//   on expose UN point d'injection propre qui frappe le VRAI code métier :
//     - inbound_reply       : construit un VeridianIMAPMessage et le passe au VRAI
//       ProcessInboundMessage (détection forte par Message-ID, signal 'replied',
//       exit actif des automations) — exactement ce que ferait le poller IMAP, sans
//       la couche transport IMAP. AUCUN mail n'est envoyé.
//     - seed_sent           : pose N entrées message_history "sent" via le VRAI repo
//       Create (le worker fait la même chose après un envoi). AUCUN mail envoyé.
//     - daily_cap_decision  : renvoie le COUNT RÉEL (CountSentSinceForContact) et la
//       décision count>=cap — le prédicat EXACT du gate worker (veridian_daily_cap.go).
//
// Garde-fous : staging-only (503 sinon), HMAC Hub, opère sur un workspace fourni
// par le caller (les E2E provisionnent un tenant jetable wipé en afterAll). Aucune
// donnée n'est créée hors du workspace ciblé. Best-effort, idempotent côté reply
// (ON CONFLICT DO NOTHING dans MarkReplied).

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/google/uuid"
)

// VeridianColdReplyProcessor est le contrat minimal consommé par le handler pour
// le mode inbound_reply. Implémenté par *service.VeridianReplyService (le VRAI
// service stop-on-reply). On ne dépend que de ces deux méthodes pour ne pas
// coupler le handler au service concret (testabilité + pas d'import cycle).
type VeridianColdReplyProcessor interface {
	ProcessInboundMessage(ctx context.Context, msg *domain.VeridianIMAPMessage) error
	HasReplied(ctx context.Context, workspaceID, email string) (bool, error)
}

// veridianColdSimulateDeps regroupe les dépendances optionnelles de l'endpoint
// cold-simulate. Toutes nil par défaut (mode self-hosted / prod) → 503.
type veridianColdSimulateDeps struct {
	replyProcessor     VeridianColdReplyProcessor
	messageHistoryRepo domain.MessageHistoryRepository
	workspaceRepo      domain.WorkspaceRepository
	environment        string
}

// SetColdSimulate injecte les dépendances de l'endpoint de test cold lifecycle.
// Optionnel : si non appelé (ou environment != staging), l'endpoint retourne 503.
func (h *VeridianHandler) SetColdSimulate(
	replyProcessor VeridianColdReplyProcessor,
	messageHistoryRepo domain.MessageHistoryRepository,
	workspaceRepo domain.WorkspaceRepository,
	environment string,
) {
	h.coldSimulate = &veridianColdSimulateDeps{
		replyProcessor:     replyProcessor,
		messageHistoryRepo: messageHistoryRepo,
		workspaceRepo:      workspaceRepo,
		environment:        environment,
	}
}

// veridianColdSimulateStagingEnv est la SEULE valeur de cfg.Environment qui active
// l'endpoint. Toute autre (production, development, demo, "") → 503.
const veridianColdSimulateStagingEnv = "staging"

// veridianColdSimulateRequest est le corps POST. `mode` aiguille le comportement.
type veridianColdSimulateRequest struct {
	Mode        string `json:"mode"`         // inbound_reply | seed_sent | daily_cap_decision
	WorkspaceID string `json:"workspace_id"` // tenant ciblé (jetable, E2E)

	// --- mode seed_sent / daily_cap_decision : adresse destinataire ---
	ContactEmail string `json:"contact_email,omitempty"`

	// --- mode seed_sent : combien d'entrées message_history "sent" poser ---
	Count int `json:"count,omitempty"`

	// --- mode daily_cap_decision : cap par destinataire à évaluer ---
	PerRecipientCap int `json:"per_recipient_cap,omitempty"`

	// --- mode inbound_reply : enveloppe du message entrant simulé ---
	From      string `json:"from,omitempty"`       // expéditeur (= contact qui répond)
	InReplyTo string `json:"in_reply_to,omitempty"` // header In-Reply-To : <message_history.id@...>
	Subject   string `json:"subject,omitempty"`
}

// veridianColdSimulateResponse couvre les trois modes (champs omitempty).
type veridianColdSimulateResponse struct {
	Mode string `json:"mode"`

	// inbound_reply
	IsReply    bool   `json:"is_reply,omitempty"`
	HasReplied bool   `json:"has_replied,omitempty"`
	SeededMsgID string `json:"seeded_message_id,omitempty"`

	// seed_sent
	Seeded int `json:"seeded,omitempty"`

	// seed_sent + daily_cap_decision : COUNT réel via CountSentSinceForContact
	SentToday int `json:"sent_today"`

	// daily_cap_decision : décision EXACTE du gate (count >= cap)
	WouldBeCapped bool `json:"would_be_capped,omitempty"`
}

// handleColdSimulate aiguille selon `mode`. Tous les modes opèrent sur le VRAI
// code métier (pas de mock) ; aucun envoi de mail n'est jamais déclenché.
func (h *VeridianHandler) handleColdSimulate(w http.ResponseWriter, r *http.Request) {
	deps := h.coldSimulate
	if deps == nil || deps.environment != veridianColdSimulateStagingEnv {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable,
			"cold-simulate disabled (staging-only endpoint)",
			http.StatusServiceUnavailable, nil)
		return
	}

	var req veridianColdSimulateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if req.WorkspaceID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "workspace_id required", http.StatusBadRequest, nil)
		return
	}

	switch req.Mode {
	case "inbound_reply":
		h.coldSimulateInboundReply(w, r, deps, &req)
	case "seed_sent":
		h.coldSimulateSeedSent(w, r, deps, &req)
	case "daily_cap_decision":
		h.coldSimulateDailyCapDecision(w, r, deps, &req)
	default:
		WriteJSONErrorCode(w, ErrCodeInvalidPayload,
			"mode must be inbound_reply|seed_sent|daily_cap_decision",
			http.StatusBadRequest, map[string]interface{}{"mode": req.Mode})
	}
}

// coldSimulateInboundReply joue le scénario stop-on-reply de bout en bout :
//
//  1. seed une entrée message_history "sent" vers le contact (notre envoi initial),
//     avec un message_history.id connu ;
//  2. construit un VeridianIMAPMessage dont In-Reply-To cite cet id (réponse du
//     prospect) et From = le contact ;
//  3. passe le message au VRAI ProcessInboundMessage → match fort par Message-ID,
//     pose le signal 'replied', exit actif des automations ;
//  4. relit HasReplied (source de vérité consommée par le gate Lot 9) et le renvoie.
//
// Le timeline event email.replied posé par le service est observable par le test
// via /api/timeline.list (auth JWT owner).
func (h *VeridianHandler) coldSimulateInboundReply(
	w http.ResponseWriter, r *http.Request, deps *veridianColdSimulateDeps, req *veridianColdSimulateRequest,
) {
	if deps.replyProcessor == nil || deps.messageHistoryRepo == nil || deps.workspaceRepo == nil {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "reply processor not wired", http.StatusServiceUnavailable, nil)
		return
	}
	if req.From == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "from required (the prospect replying)", http.StatusBadRequest, nil)
		return
	}

	ctx := r.Context()
	contact := domain.VeridianNormalizeEmail(req.From)

	secretKey, err := h.coldSimulateSecretKey(ctx, deps, req.WorkspaceID)
	if err != nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	// 1. Seed l'envoi initial (notre mail) avec un id stable → le match fort le citera.
	msgID := uuid.NewString()
	if err := h.coldSimulateSeedOne(ctx, deps, secretKey, req.WorkspaceID, contact, msgID, time.Now().UTC()); err != nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, "seed initial send failed: "+err.Error(), http.StatusInternalServerError, nil)
		return
	}

	// 2/3. Construit la réponse entrante et la passe au VRAI service stop-on-reply.
	inReplyTo := req.InReplyTo
	if inReplyTo == "" {
		inReplyTo = "<" + msgID + "@reply.example.com>"
	}
	msg := &domain.VeridianIMAPMessage{
		WorkspaceID: req.WorkspaceID,
		Folder:      "INBOX",
		MessageID:   "<incoming-" + uuid.NewString() + "@prospect.example.com>",
		InReplyTo:   inReplyTo,
		From:        contact,
		Subject:     req.Subject,
		Date:        time.Now().UTC(),
	}
	if err := deps.replyProcessor.ProcessInboundMessage(ctx, msg); err != nil {
		// Best-effort côté service mais on remonte l'erreur ici (test = veut savoir).
		WriteJSONErrorCode(w, ErrCodeInternalError, "process inbound failed: "+err.Error(), http.StatusInternalServerError, nil)
		return
	}

	// 4. Source de vérité : le contact est-il marqué 'replied' ?
	hasReplied, err := deps.replyProcessor.HasReplied(ctx, req.WorkspaceID, contact)
	if err != nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, "has-replied check failed: "+err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, veridianColdSimulateResponse{
		Mode:        "inbound_reply",
		IsReply:     hasReplied, // détecté + signalé = réponse
		HasReplied:  hasReplied,
		SeededMsgID: msgID,
	})
}

// coldSimulateSeedSent pose N entrées message_history "sent" vers une adresse
// (notre envoi cold), puis renvoie le COUNT du jour. Sert à amener le compteur
// du cap destinataire à un niveau donné SANS envoi réel.
func (h *VeridianHandler) coldSimulateSeedSent(
	w http.ResponseWriter, r *http.Request, deps *veridianColdSimulateDeps, req *veridianColdSimulateRequest,
) {
	if deps.messageHistoryRepo == nil || deps.workspaceRepo == nil {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "message history repo not wired", http.StatusServiceUnavailable, nil)
		return
	}
	if req.ContactEmail == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "contact_email required", http.StatusBadRequest, nil)
		return
	}
	if req.Count <= 0 || req.Count > 50 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "count must be 1..50", http.StatusBadRequest, nil)
		return
	}

	ctx := r.Context()
	contact := domain.VeridianNormalizeEmail(req.ContactEmail)

	secretKey, err := h.coldSimulateSecretKey(ctx, deps, req.WorkspaceID)
	if err != nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	now := time.Now().UTC()
	for i := 0; i < req.Count; i++ {
		if err := h.coldSimulateSeedOne(ctx, deps, secretKey, req.WorkspaceID, contact, uuid.NewString(), now); err != nil {
			WriteJSONErrorCode(w, ErrCodeInternalError, "seed failed: "+err.Error(), http.StatusInternalServerError, nil)
			return
		}
	}

	count, err := deps.messageHistoryRepo.CountSentSinceForContact(ctx, req.WorkspaceID, contact, veridianColdSimulateStartOfDay(now))
	if err != nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, "count failed: "+err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, veridianColdSimulateResponse{
		Mode:      "seed_sent",
		Seeded:    req.Count,
		SentToday: count,
	})
}

// coldSimulateDailyCapDecision renvoie la décision EXACTE du gate destinataire :
// COUNT réel des envois du jour vers le contact (CountSentSinceForContact, la
// requête lue par le worker) vs le cap fourni. would_be_capped = count >= cap,
// le prédicat strict de veridian_daily_cap.go. Lecture pure (aucun side-effect).
func (h *VeridianHandler) coldSimulateDailyCapDecision(
	w http.ResponseWriter, r *http.Request, deps *veridianColdSimulateDeps, req *veridianColdSimulateRequest,
) {
	if deps.messageHistoryRepo == nil {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "message history repo not wired", http.StatusServiceUnavailable, nil)
		return
	}
	if req.ContactEmail == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "contact_email required", http.StatusBadRequest, nil)
		return
	}
	if req.PerRecipientCap <= 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "per_recipient_cap must be > 0", http.StatusBadRequest, nil)
		return
	}

	ctx := r.Context()
	contact := domain.VeridianNormalizeEmail(req.ContactEmail)
	now := time.Now().UTC()

	count, err := deps.messageHistoryRepo.CountSentSinceForContact(ctx, req.WorkspaceID, contact, veridianColdSimulateStartOfDay(now))
	if err != nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, "count failed: "+err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, veridianColdSimulateResponse{
		Mode:          "daily_cap_decision",
		SentToday:     count,
		WouldBeCapped: count >= req.PerRecipientCap, // prédicat exact du gate worker
	})
}

// coldSimulateSecretKey charge le workspace et retourne son SecretKey déchiffré
// (nécessaire au chiffrement de message_data dans message_history.Create —
// même secret que celui passé par le worker : workspace.Settings.SecretKey).
func (h *VeridianHandler) coldSimulateSecretKey(ctx context.Context, deps *veridianColdSimulateDeps, workspaceID string) (string, error) {
	ws, err := deps.workspaceRepo.GetByID(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	return ws.Settings.SecretKey, nil
}

// veridianColdSimulateStartOfDay retourne minuit UTC du jour de `now`. Doit être
// strictement identique à veridianStartOfDayUTC (package queue) consommé par le
// gate cap : le "jour" du compteur = jour calendaire UTC (cohérent avec sent_at
// TIMESTAMPTZ et le now() serveur des conteneurs, en UTC).
func veridianColdSimulateStartOfDay(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// coldSimulateSeedOne pose UNE entrée message_history "sent" (sent_at=now,
// failed_at=nil) via le VRAI repo Create. C'est la donnée exacte que lit le cap
// (contact_email + sent_at) et que cite le match fort stop-on-reply (id).
func (h *VeridianHandler) coldSimulateSeedOne(
	ctx context.Context, deps *veridianColdSimulateDeps, secretKey, workspaceID, contact, msgID string, sentAt time.Time,
) error {
	msg := &domain.MessageHistory{
		ID:           msgID,
		ContactEmail: contact,
		TemplateID:   "cold-simulate-e2e",
		Channel:      "email",
		MessageData:  domain.MessageData{Data: map[string]interface{}{"veridian_cold_simulate": true}},
		SentAt:       sentAt,
		CreatedAt:    sentAt,
		UpdatedAt:    sentAt,
	}
	return deps.messageHistoryRepo.Create(ctx, workspaceID, secretKey, msg)
}
