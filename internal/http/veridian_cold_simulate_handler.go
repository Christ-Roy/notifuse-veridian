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
	"strings"
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
	Mode        string `json:"mode"`         // inbound_reply | seed_sent | daily_cap_decision | class_cap_decision | per_sender_cap_decision | sending_window_decision | warmup_cap_decision
	WorkspaceID string `json:"workspace_id"` // tenant ciblé (jetable, E2E)

	// --- mode seed_sent / daily_cap_decision : adresse destinataire ---
	ContactEmail string `json:"contact_email,omitempty"`

	// --- mode seed_sent : combien d'entrées message_history "sent" poser ---
	Count int `json:"count,omitempty"`

	// --- mode seed_sent : adresse émettrice (FROM) posée sur les entrées seedées.
	// Vide = pas de sender (veridian_sender_email NULL). Sert au cap per-sender. ---
	SenderEmail string `json:"sender_email,omitempty"`

	// --- mode daily_cap_decision : cap par destinataire à évaluer ---
	PerRecipientCap int `json:"per_recipient_cap,omitempty"`

	// --- mode class_cap_decision : classe destinataire + cap par classe à évaluer.
	// La décision est le prédicat EXACT du gate veridian_daily_cap.go (cap classe).
	// Deux variantes, selon que l'INFRA ÉMETTRICE est précisée :
	//   - sans sender_domain → COUNT workspace-global (toutes infras confondues) :
	//     CountSentSinceForDomains(domains de la classe) >= cap (legacy / fallback).
	//   - avec sender_domain → COUNT keyé PAR INFRA ÉMETTRICE (warm-up multi-domaine,
	//     2026-06-18) : CountSentSinceForDomainsAndSenderDomain(domains, senderDomain)
	//     >= cap, le prédicat EXACT de veridianCountClassForInfra. C'est ce chemin que
	//     l'E2E 2-infras exerce : deux domaines émetteurs frappant la même classe ont
	//     chacun leur propre compteur. ---
	ProviderClass string `json:"provider_class,omitempty"`
	ClassCap      int    `json:"class_cap,omitempty"`

	// SenderDomain : domaine de l'infra ÉMETTRICE pour le mode class_cap_decision.
	// Si renseigné (directement, ou dérivé de sender_email), le COUNT de classe est
	// attribué à CETTE infra (couple domaine-émetteur × classe). Vide = COUNT
	// workspace-global (fallback legacy, comportement antérieur strict).
	SenderDomain string `json:"sender_domain,omitempty"`

	// --- mode per_sender_cap_decision : cap journalier par adresse émettrice.
	// Prédicat EXACT de veridian_per_sender_cap.go : CountSentSinceForSender >= cap. ---
	PerSenderCap int `json:"per_sender_cap,omitempty"`

	// --- mode warmup_cap_decision : cap WARMUP TOTAL par DOMAINE émetteur, toutes
	// classes destinataires confondues. Prédicat EXACT de la branche warmup de
	// veridian_daily_cap.go : CountSentSinceForSenderDomain(senderDomain) >= warmupCap.
	// Le sender_domain est normalisé comme le gate (domaine nu OU adresse complète).
	// C'est ce qui rend le warmup robuste aux classes MX : AUCUN filtre de classe
	// destinataire n'intervient (à l'inverse de class_cap_decision). ---
	WarmupCap int `json:"warmup_cap,omitempty"`

	// --- mode sending_window_decision : fenêtre d'envoi + timezone de fallback.
	// Prédicat EXACT de veridian_sending_window_gate.go : IsWithinWindow(now). ---
	SendingWindow *domain.VeridianSendingWindow `json:"sending_window,omitempty"`
	FallbackTZ    string                        `json:"fallback_tz,omitempty"`

	// --- mode inbound_reply : enveloppe du message entrant simulé ---
	From      string `json:"from,omitempty"`        // expéditeur (= contact qui répond)
	InReplyTo string `json:"in_reply_to,omitempty"` // header In-Reply-To : <message_history.id@...>
	Subject   string `json:"subject,omitempty"`
}

// veridianColdSimulateResponse couvre tous les modes (champs omitempty).
type veridianColdSimulateResponse struct {
	Mode string `json:"mode"`

	// inbound_reply
	IsReply     bool   `json:"is_reply,omitempty"`
	HasReplied  bool   `json:"has_replied,omitempty"`
	SeededMsgID string `json:"seeded_message_id,omitempty"`

	// seed_sent
	Seeded int `json:"seeded,omitempty"`

	// seed_sent + *_cap_decision : COUNT réel via le repo (la valeur lue par le gate)
	SentToday int `json:"sent_today"`

	// *_cap_decision : décision EXACTE du gate (count >= cap).
	// PAS d'omitempty : c'est un booléen de décision, false doit être présent
	// dans le JSON (sinon le client lit `undefined` au lieu de `false` quand
	// l'envoi est autorisé — piège omitempty sur bool, attrapé par l'E2E cap).
	WouldBeCapped bool `json:"would_be_capped"`

	// class_cap_decision : domaine de l'infra émettrice effectivement utilisé pour
	// attribuer le COUNT (vide = COUNT workspace-global, sinon = compteur par infra).
	// Permet à l'E2E 2-infras de VÉRIFIER que la décision est bien keyée par infra
	// (deux sender_domain distincts → deux compteurs distincts).
	SenderDomain string `json:"sender_domain,omitempty"`

	// class_cap_decision : true si le COUNT a été attribué à une infra émettrice
	// précise (sender_domain non vide), false si c'est le COUNT workspace-global
	// (fallback legacy). PAS d'omitempty : doit être présent pour que l'E2E lise
	// explicitement quel chemin a été emprunté, même quand c'est le global (false).
	PerInfra bool `json:"per_infra"`

	// sending_window_decision : la fenêtre laisse-t-elle passer MAINTENANT ?
	// within = IsWithinWindow(now) ; would_be_skipped = !within (le gate reschedule).
	// next_opening_unix = NextOpening(now).Unix() (instant absolu de réouverture).
	Within          bool  `json:"within"`
	WouldBeSkipped  bool  `json:"would_be_skipped"`
	NextOpeningUnix int64 `json:"next_opening_unix,omitempty"`
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
	case "class_cap_decision":
		h.coldSimulateClassCapDecision(w, r, deps, &req)
	case "per_sender_cap_decision":
		h.coldSimulatePerSenderCapDecision(w, r, deps, &req)
	case "warmup_cap_decision":
		h.coldSimulateWarmupCapDecision(w, r, deps, &req)
	case "sending_window_decision":
		h.coldSimulateSendingWindowDecision(w, r, &req)
	default:
		WriteJSONErrorCode(w, ErrCodeInvalidPayload,
			"mode must be inbound_reply|seed_sent|daily_cap_decision|class_cap_decision|per_sender_cap_decision|warmup_cap_decision|sending_window_decision",
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
	if err := h.coldSimulateSeedOne(ctx, deps, secretKey, req.WorkspaceID, contact, "", msgID, time.Now().UTC()); err != nil {
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

	sender := domain.VeridianNormalizeEmail(req.SenderEmail)
	now := time.Now().UTC()
	for i := 0; i < req.Count; i++ {
		if err := h.coldSimulateSeedOne(ctx, deps, secretKey, req.WorkspaceID, contact, sender, uuid.NewString(), now); err != nil {
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
// failed_at=nil) via le VRAI repo Create. C'est la donnée exacte que lisent les
// caps : contact_email + sent_at (cap destinataire), domaine (cap classe),
// veridian_sender_email (cap per-sender), et que cite le match fort stop-on-reply
// (id). `sender` vide = veridian_sender_email NULL (comme un envoi sans rotation).
func (h *VeridianHandler) coldSimulateSeedOne(
	ctx context.Context, deps *veridianColdSimulateDeps, secretKey, workspaceID, contact, sender, msgID string, sentAt time.Time,
) error {
	msg := &domain.MessageHistory{
		ID:                  msgID,
		ContactEmail:        contact,
		TemplateID:          "cold-simulate-e2e",
		Channel:             "email",
		MessageData:         domain.MessageData{Data: map[string]interface{}{"veridian_cold_simulate": true}},
		SentAt:              sentAt,
		CreatedAt:           sentAt,
		UpdatedAt:           sentAt,
		VeridianSenderEmail: sender,
	}
	return deps.messageHistoryRepo.Create(ctx, workspaceID, secretKey, msg)
}

// coldSimulateClassCapDecision renvoie la décision EXACTE du gate cap-CLASSE
// (veridian_daily_cap.go §2) : COUNT des envois du jour vers les domaines de la
// classe demandée vs le cap. would_be_capped = count >= class_cap.
//
// Deux chemins, EXACTEMENT comme veridianCountClassForInfra du gate :
//   - sender_domain VIDE → COUNT workspace-global CountSentSinceForDomains
//     (toutes infras émettrices confondues ; chemin legacy / fallback).
//   - sender_domain PRÉSENT → COUNT keyé PAR INFRA ÉMETTRICE
//     CountSentSinceForDomainsAndSenderDomain (couple domaine-émetteur × classe).
//     C'est le prédicat du nouveau cap par infra (warm-up multi-domaine) : deux
//     domaines d'envoi frappant la même classe ont chacun leur propre compteur.
//
// Le sender_domain fourni est normalisé comme le gate (veridianEmailDomain) : on
// accepte soit un domaine nu (`infra-a.fr`), soit une adresse complète
// (`bot@infra-a.fr`) dont on extrait le domaine, puis lowercase/trim.
//
// ⚠️ Pour une classe MX (ovh/ionos/…), VeridianDomainsForClass renvoie une liste
// vide → le COUNT ne s'enforce pas (dégradation gracieuse documentée) : la décision
// reflète FIDÈLEMENT ce comportement (count 0 → jamais capé), ce que l'E2E doit
// constater honnêtement.
func (h *VeridianHandler) coldSimulateClassCapDecision(
	w http.ResponseWriter, r *http.Request, deps *veridianColdSimulateDeps, req *veridianColdSimulateRequest,
) {
	if deps.messageHistoryRepo == nil {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "message history repo not wired", http.StatusServiceUnavailable, nil)
		return
	}
	if req.ProviderClass == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "provider_class required", http.StatusBadRequest, nil)
		return
	}
	if req.ClassCap <= 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "class_cap must be > 0", http.StatusBadRequest, nil)
		return
	}

	ctx := r.Context()
	since := veridianColdSimulateStartOfDay(time.Now().UTC())
	domains, exclude := domain.VeridianDomainsForClass(req.ProviderClass)

	senderDomain := veridianColdSimulateSenderDomain(req.SenderDomain)

	var (
		count int
		err   error
	)
	if senderDomain != "" {
		// Chemin par infra émettrice : exactement le COUNT de veridianCountClassForInfra.
		count, err = deps.messageHistoryRepo.CountSentSinceForDomainsAndSenderDomain(
			ctx, req.WorkspaceID, domains, exclude, senderDomain, since)
	} else {
		// Fallback workspace-global (legacy) : comportement antérieur strict.
		count, err = deps.messageHistoryRepo.CountSentSinceForDomains(
			ctx, req.WorkspaceID, domains, exclude, since)
	}
	if err != nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, "count failed: "+err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, veridianColdSimulateResponse{
		Mode:          "class_cap_decision",
		SentToday:     count,
		WouldBeCapped: count >= req.ClassCap, // prédicat exact du gate worker (cap classe)
		SenderDomain:  senderDomain,
		PerInfra:      senderDomain != "",
	})
}

// veridianColdSimulateSenderDomain normalise le sender_domain fourni au handler
// EXACTEMENT comme le gate dérive le domaine émetteur (queue.veridianEmailDomain) :
// on accepte un domaine nu OU une adresse complète, on extrait la part après le
// dernier '@' le cas échéant, lowercase + trim + retrait du point FQDN final.
// Retourne "" si rien d'exploitable (l'appelant retombe alors sur le COUNT global).
func veridianColdSimulateSenderDomain(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if at := strings.LastIndex(s, "@"); at >= 0 {
		if at == len(s)-1 {
			return ""
		}
		s = s[at+1:]
	}
	return strings.TrimSuffix(strings.TrimSpace(s), ".")
}

// coldSimulatePerSenderCapDecision renvoie la décision EXACTE du gate per-sender
// (veridian_per_sender_cap.go) : COUNT des envois du jour DEPUIS l'adresse
// émettrice (CountSentSinceForSender) vs le cap. would_be_capped = count >= cap.
// L'adresse émettrice est portée par sender_email (à seeder au préalable via
// mode seed_sent + sender_email pour amener le compteur au niveau voulu).
func (h *VeridianHandler) coldSimulatePerSenderCapDecision(
	w http.ResponseWriter, r *http.Request, deps *veridianColdSimulateDeps, req *veridianColdSimulateRequest,
) {
	if deps.messageHistoryRepo == nil {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "message history repo not wired", http.StatusServiceUnavailable, nil)
		return
	}
	if req.SenderEmail == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "sender_email required", http.StatusBadRequest, nil)
		return
	}
	if req.PerSenderCap <= 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "per_sender_cap must be > 0", http.StatusBadRequest, nil)
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()
	sender := domain.VeridianNormalizeEmail(req.SenderEmail)
	count, err := deps.messageHistoryRepo.CountSentSinceForSender(ctx, req.WorkspaceID, sender, veridianColdSimulateStartOfDay(now))
	if err != nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, "count failed: "+err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, veridianColdSimulateResponse{
		Mode:          "per_sender_cap_decision",
		SentToday:     count,
		WouldBeCapped: count >= req.PerSenderCap, // prédicat exact du gate worker (cap émetteur)
	})
}

// coldSimulateWarmupCapDecision renvoie la décision EXACTE de la branche WARMUP du
// gate (veridian_daily_cap.go) : COUNT TOTAL des envois du jour DEPUIS le DOMAINE
// émetteur (CountSentSinceForSenderDomain), TOUTES classes destinataires confondues,
// vs le cap warmup courant. would_be_capped = count >= warmup_cap.
//
// C'est le prédicat qui PROUVE le fix du ticket warmup-total/MX :
//   - cap TOTAL par infra (Bug 1) : le COUNT n'a AUCUN filtre de classe destinataire
//     → un envoi google + un envoi microsoft depuis le même domaine comptent ensemble.
//   - enforcement sur classes MX (Bug 2) : aucune dérivation de classe destinataire
//     (donc aucune dégradation gracieuse MX) → le warmup s'enforce quel que soit le MX.
//
// Le sender_domain fourni est normalisé comme le gate (veridianEmailDomain) : domaine
// nu (`send.fr`) OU adresse complète (`bot@send.fr`). Vide = pas d'attribution infra
// → le warmup n'est pas enforçable (le gate dégrade en pass) : on renvoie alors
// would_be_capped=false sans COUNT (fidèle au comportement du gate).
func (h *VeridianHandler) coldSimulateWarmupCapDecision(
	w http.ResponseWriter, r *http.Request, deps *veridianColdSimulateDeps, req *veridianColdSimulateRequest,
) {
	if deps.messageHistoryRepo == nil {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "message history repo not wired", http.StatusServiceUnavailable, nil)
		return
	}
	if req.WarmupCap <= 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "warmup_cap must be > 0", http.StatusBadRequest, nil)
		return
	}

	ctx := r.Context()
	since := veridianColdSimulateStartOfDay(time.Now().UTC())
	senderDomain := veridianColdSimulateSenderDomain(req.SenderDomain)
	if senderDomain == "" {
		// Pas de domaine émetteur exploitable → le gate ne peut pas enforcer le warmup
		// (best-effort pass). On reflète FIDÈLEMENT ce comportement : jamais capé, 0 COUNT.
		writeJSON(w, http.StatusOK, veridianColdSimulateResponse{
			Mode:          "warmup_cap_decision",
			SentToday:     0,
			WouldBeCapped: false,
			SenderDomain:  "",
			PerInfra:      false,
		})
		return
	}

	count, err := deps.messageHistoryRepo.CountSentSinceForSenderDomain(ctx, req.WorkspaceID, senderDomain, since)
	if err != nil {
		WriteJSONErrorCode(w, ErrCodeInternalError, "count failed: "+err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, veridianColdSimulateResponse{
		Mode:          "warmup_cap_decision",
		SentToday:     count,
		WouldBeCapped: count >= req.WarmupCap, // prédicat exact de la branche warmup du gate
		SenderDomain:  senderDomain,
		PerInfra:      true,
	})
}

// coldSimulateSendingWindowDecision renvoie la décision EXACTE du gate fenêtre
// d'envoi (veridian_sending_window_gate.go) : within = IsWithinWindow(now,
// fallbackTZ), would_be_skipped = !within (le gate reschedule à NextOpening hors
// fenêtre). Prédicat PUR (aucune DB) : on évalue la VRAIE méthode domaine sur la
// fenêtre fournie et l'instant serveur courant. Une fenêtre invalide laisse tout
// passer (within=true, non-régression) — exactement ce que fait le gate.
func (h *VeridianHandler) coldSimulateSendingWindowDecision(
	w http.ResponseWriter, r *http.Request, req *veridianColdSimulateRequest,
) {
	if req.SendingWindow == nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "sending_window required", http.StatusBadRequest, nil)
		return
	}

	now := time.Now()
	within := req.SendingWindow.IsWithinWindow(now, req.FallbackTZ)
	resp := veridianColdSimulateResponse{
		Mode:           "sending_window_decision",
		Within:         within,
		WouldBeSkipped: !within, // hors fenêtre → le gate skip+reschedule
	}
	if !within {
		resp.NextOpeningUnix = req.SendingWindow.NextOpening(now, req.FallbackTZ).Unix()
	}
	writeJSON(w, http.StatusOK, resp)
}
