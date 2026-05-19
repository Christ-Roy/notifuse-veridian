package middleware

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// veridianIdempotencyTTL : duree de vie d'une entree idempotency_keys.
// 24h matche le contrat sec. 5.11 (replay sur retry reseau, court terme).
const veridianIdempotencyTTL = 24 * time.Hour

// veridianIdempotencyHeader : nom canonique du header (HTTP case-insensitive).
const veridianIdempotencyHeader = "Idempotency-Key"

// veridianIdempotencyMaxBody : limite de body capture pour calculer le
// request_hash. Le contrat actuel n'a pas de payload depasse 64KB ; on cape
// a 256KB pour la marge sans permettre de leak DoS via body geant.
const veridianIdempotencyMaxBody = 256 * 1024

// VeridianIdempotencyMiddleware intercepte les endpoints d'ecriture pour
// implementer le contrat sec. 5.11 (Idempotency-Key header).
//
// Comportement par requete :
//
//  1. Si repo nil → passthrough (mode self-hosted sans veridian_plan).
//  2. Si pas de header Idempotency-Key → passthrough sans tracking (Hub legacy).
//  3. Lookup la cle :
//     - Hit + same request_hash → replay status + body cache (header
//     X-Idempotent-Replay: true), handler PAS execute.
//     - Hit + different request_hash → 422 idempotency_key_mismatch
//     (le client a reutilise la meme cle pour une requete differente).
//     - Miss (sql.ErrNoRows) → execute le handler, INSERT la reponse (status
//     + body) avec TTL 24h.
//     - Erreur DB transitoire → fail-open (log + passthrough sans tracking)
//     pour ne pas bloquer toutes les mutations sur un incident DB.
//
// Status code policy :
//   - 2xx + 4xx (sauf 422 lui-meme) sont caches : reponses metier stables.
//   - 5xx ne sont PAS caches : incidents transitoires, le client doit pouvoir
//     retry et obtenir un succes.
//   - 422 (idempotency_key_mismatch) ne se cache pas lui-meme (eviterait le
//     bug de "key reutilise differemment" mais piegerait l'usage normal).
func VeridianIdempotencyMiddleware(repo domain.VeridianIdempotencyRepository, log logger.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if repo == nil {
				next.ServeHTTP(w, r)
				return
			}

			key := r.Header.Get(veridianIdempotencyHeader)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Lire et rebufferer le body pour pouvoir le passer au handler ET
			// calculer le hash sans le consommer.
			body, err := io.ReadAll(io.LimitReader(r.Body, veridianIdempotencyMaxBody+1))
			if err != nil {
				writeJSONError(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			if len(body) > veridianIdempotencyMaxBody {
				writeJSONError(w, "Request body too large for idempotency tracking", http.StatusRequestEntityTooLarge)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))

			requestHash := hashRequest(r.Method, r.URL.Path, body)

			existing, getErr := repo.Get(r.Context(), key)
			if getErr != nil && !errors.Is(getErr, sql.ErrNoRows) {
				if log != nil {
					log.WithFields(map[string]interface{}{
						"key":   key,
						"error": getErr.Error(),
					}).Warn("veridian idempotency: Get failed, falling back to passthrough (fail-open)")
				}
				next.ServeHTTP(w, r)
				return
			}

			if existing != nil {
				// Hit : verifier hash.
				if existing.RequestHash != requestHash {
					// Meme cle, body different = client bug. 422 + code machine.
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnprocessableEntity)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error":   "idempotency key reused with a different request body",
						"code":    "idempotency_key_mismatch",
						"message": "the same Idempotency-Key was previously used with a different request body — fix your client",
					})
					return
				}
				// Hit + match : replay.
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Idempotent-Replay", "true")
				w.WriteHeader(existing.ResponseStatus)
				_, _ = w.Write(existing.ResponseBody)
				return
			}

			// Miss : wrapper la ResponseWriter pour capturer status + body.
			capture := &capturingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(capture, r)

			if !shouldCacheResponse(capture.statusCode) {
				return
			}

			entry := &domain.VeridianIdempotencyEntry{
				Key:            key,
				Endpoint:       r.URL.Path,
				TenantID:       extractTenantIDFromBody(body),
				RequestHash:    requestHash,
				ResponseStatus: capture.statusCode,
				ResponseBody:   capture.body.Bytes(),
				CreatedAt:      time.Now().UTC(),
				ExpiresAt:      time.Now().UTC().Add(veridianIdempotencyTTL),
			}
			if saveErr := repo.Save(r.Context(), entry); saveErr != nil {
				// Save echoue (probablement race PK : un autre requete avec
				// la meme cle a INSERT entre Get et Save). Pas fatal : la
				// reponse est deja envoyee au client, on log seulement.
				if log != nil {
					log.WithFields(map[string]interface{}{
						"key":   key,
						"error": saveErr.Error(),
					}).Warn("veridian idempotency: Save failed (race?), response sent OK")
				}
			}
		})
	}
}

// hashRequest calcule SHA-256 hex de "<method>\n<path>\n<body>". Le hash
// distingue method + path + body — toute difference produit un hash different.
func hashRequest(method, path string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte("\n"))
	h.Write([]byte(path))
	h.Write([]byte("\n"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// extractTenantIDFromBody parse minimaliste pour extraire tenant_id si present.
// Best-effort : si body non-JSON ou champ absent, retourne "" (le repo accepte
// le NULL). Sert UNIQUEMENT au traceability — pas a une logique critique.
func extractTenantIDFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		TenantID *string `json:"tenant_id"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	if probe.TenantID == nil {
		return ""
	}
	return *probe.TenantID
}

// shouldCacheResponse decide quels status codes sont mis en cache.
// 2xx + 4xx (sauf 422) = stables, on cache. 5xx + 422 = on ne cache pas.
// Voir docstring VeridianIdempotencyMiddleware pour la justification.
func shouldCacheResponse(status int) bool {
	switch {
	case status >= 200 && status < 300:
		return true
	case status == http.StatusUnprocessableEntity:
		return false
	case status >= 400 && status < 500:
		return true
	default:
		return false
	}
}

// capturingResponseWriter wrapper qui capture status + body en parallele
// de l'ecriture au client. Permet de stocker la reponse en idempotency_keys
// apres execution du handler.
type capturingResponseWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	written    bool
}

func (c *capturingResponseWriter) WriteHeader(statusCode int) {
	if c.written {
		return
	}
	c.statusCode = statusCode
	c.written = true
	c.ResponseWriter.WriteHeader(statusCode)
}

func (c *capturingResponseWriter) Write(b []byte) (int, error) {
	if !c.written {
		c.WriteHeader(http.StatusOK)
	}
	// Capturer un duplicate pour le repo.
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}
