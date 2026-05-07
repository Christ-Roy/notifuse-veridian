package service

// === Veridian patch ===
// VeridianWebhookEmitter envoie des events (tenant.*, email.*) vers le Hub
// Veridian via HTTP POST signe HMAC-SHA256.
//
// Schema headers (identique a veridian_hmac.go cote inverse) :
//   X-Veridian-Notifuse-Signature : HMAC-SHA256(secret, "<timestamp>.<rawBody>") en hex
//   X-Veridian-Timestamp          : timestamp Unix milliseconds
//
// L'emitter est best-effort : Emit ne bloque jamais le caller. La requete
// HTTP part dans une goroutine, retry exponential 3x sur reseau ou 5xx, puis
// abandonne en loggant. Le Hub a son propre healthcheck pour detecter les
// pertes prolongees.
//
// Voir docs/saas-standards.md §6.1 (HMAC) et §7 (audit log) dans le monorepo.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/google/uuid"
)

const (
	veridianWebhookTimeout      = 5 * time.Second
	veridianWebhookMaxRetries   = 3
	veridianWebhookBackoffBase  = 1 * time.Second
	veridianWebhookSignatureHdr = "X-Veridian-Notifuse-Signature"
	veridianWebhookTimestampHdr = "X-Veridian-Timestamp"
)

// veridianWebhookEmitter implements domain.WebhookEmitter via HTTP POST.
type veridianWebhookEmitter struct {
	hubURL    string
	hubSecret string
	logger    logger.Logger
	client    *http.Client
}

// noopWebhookEmitter implements domain.WebhookEmitter mais ne fait rien.
// Utilise quand HUB_WEBHOOK_URL ou HUB_WEBHOOK_SECRET ne sont pas configures
// (mode self-hosted ou dev local).
type noopWebhookEmitter struct {
	logger logger.Logger
}

// NewVeridianWebhookEmitter cree un emitter HTTP. Si hubURL ou hubSecret
// sont vides, retourne un noop emitter qui log "disabled" et ne fait rien.
func NewVeridianWebhookEmitter(hubURL, hubSecret string, log logger.Logger) domain.WebhookEmitter {
	if hubURL == "" || hubSecret == "" {
		if log != nil {
			log.Info("veridian webhook emitter disabled (HUB_WEBHOOK_URL or HUB_WEBHOOK_SECRET missing)")
		}
		return &noopWebhookEmitter{logger: log}
	}
	return &veridianWebhookEmitter{
		hubURL:    hubURL,
		hubSecret: hubSecret,
		logger:    log,
		client: &http.Client{
			Timeout: veridianWebhookTimeout,
		},
	}
}

// Emit pour le noop : log debug et c'est tout.
func (e *noopWebhookEmitter) Emit(ctx context.Context, eventType domain.VeridianEvent, tenantID string, data map[string]interface{}) {
	if e.logger != nil {
		e.logger.WithFields(map[string]interface{}{
			"event_type": string(eventType),
			"tenant_id":  tenantID,
		}).Debug("veridian webhook emitter (noop) skipping event")
	}
}

// Emit lance la requete HTTP en goroutine et retourne immediatement.
// Le caller n'est jamais bloque ni informe d'une erreur.
func (e *veridianWebhookEmitter) Emit(ctx context.Context, eventType domain.VeridianEvent, tenantID string, data map[string]interface{}) {
	payload := domain.VeridianEventPayload{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		TenantID:   tenantID,
		OccurredAt: time.Now().UTC(),
		Data:       data,
	}

	// Marshal en synchrone pour qu'un panic eventuel sur des data non-serialisables
	// remonte au caller (mieux qu'avaler en silence dans la goroutine).
	body, err := json.Marshal(payload)
	if err != nil {
		if e.logger != nil {
			e.logger.WithFields(map[string]interface{}{
				"event_type": string(eventType),
				"tenant_id":  tenantID,
				"error":      err.Error(),
			}).Error("veridian webhook: failed to marshal payload")
		}
		return
	}

	go e.sendWithRetry(payload.EventID, eventType, tenantID, body)
}

// sendWithRetry POST le body avec backoff exponentiel sur erreurs reseau / 5xx.
// Abandonne sur 4xx (le Hub a refuse, retry inutile).
func (e *veridianWebhookEmitter) sendWithRetry(eventID string, eventType domain.VeridianEvent, tenantID string, body []byte) {
	var lastErr error
	var lastStatus int

	for attempt := 0; attempt < veridianWebhookMaxRetries; attempt++ {
		if attempt > 0 {
			backoff := veridianWebhookBackoffBase * time.Duration(1<<(attempt-1))
			time.Sleep(backoff)
		}

		status, err := e.sendOnce(body)
		if err == nil && status >= 200 && status < 300 {
			return
		}

		lastErr = err
		lastStatus = status

		// 4xx = refus client, retry inutile
		if err == nil && status >= 400 && status < 500 {
			break
		}
	}

	if e.logger != nil {
		fields := map[string]interface{}{
			"event_id":   eventID,
			"event_type": string(eventType),
			"tenant_id":  tenantID,
			"status":     lastStatus,
		}
		if lastErr != nil {
			fields["error"] = lastErr.Error()
		}
		e.logger.WithFields(fields).Error("veridian webhook: gave up after retries")
	}
}

// sendOnce execute une seule requete HTTP signee. Retourne (statusCode, error).
// Sur erreur reseau, statusCode = 0.
func (e *veridianWebhookEmitter) sendOnce(body []byte) (int, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	mac := hmac.New(sha256.New, []byte(e.hubSecret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	ctx, cancel := context.WithTimeout(context.Background(), veridianWebhookTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.hubURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(veridianWebhookSignatureHdr, signature)
	req.Header.Set(veridianWebhookTimestampHdr, timestamp)

	resp, err := e.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, fmt.Errorf("hub returned status %d", resp.StatusCode)
}
