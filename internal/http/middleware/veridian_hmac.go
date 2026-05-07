package middleware

// === Veridian patch ===
// Middleware verifiant la signature HMAC-SHA256 envoyee par le Hub Veridian
// sur les endpoints /api/tenants/* (provisioning, suspend, resume, delete, status).
//
// Headers attendus :
//   X-Veridian-Hub-Signature : HMAC-SHA256(secret, "<timestamp>.<rawBody>") en hex
//   X-Veridian-Timestamp     : timestamp Unix milliseconds (drift max 5 min)
//
// Voir docs/saas-standards.md §6.1 dans le monorepo veridian-platform.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"time"
)

// MaxClockDrift est la fenetre de tolerance entre timestamp client et server.
// Limite le risque de replay attack.
const MaxClockDrift = 5 * time.Minute

// MaxBodySize limite la taille du body HMAC-verifiable. 1 MiB est tres
// generaux pour des payloads JSON de provisioning.
const MaxBodySize = 1 << 20 // 1 MiB

// VeridianHMACMiddleware retourne un middleware qui verifie X-Veridian-Hub-Signature.
// Si la signature est valide, le body est rebuffere pour le handler suivant
// (parce qu'on doit le lire pour le verifier).
//
// hubSecret est la valeur de l'env var HUB_API_SECRET. Si vide, le middleware
// renvoie 503 Service Unavailable (l'app est mal configuree, pas une 401 qui
// laisserait penser a un mauvais secret cote client).
func VeridianHMACMiddleware(hubSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hubSecret == "" {
				writeJSONError(w, "Veridian HMAC not configured (HUB_API_SECRET missing)", http.StatusServiceUnavailable)
				return
			}

			signature := r.Header.Get("X-Veridian-Hub-Signature")
			timestampStr := r.Header.Get("X-Veridian-Timestamp")
			if signature == "" || timestampStr == "" {
				writeJSONError(w, "Missing X-Veridian-Hub-Signature or X-Veridian-Timestamp", http.StatusUnauthorized)
				return
			}

			// Verifier la fenetre temporelle (anti-replay).
			tsMs, err := strconv.ParseInt(timestampStr, 10, 64)
			if err != nil {
				writeJSONError(w, "Invalid X-Veridian-Timestamp (must be unix ms)", http.StatusUnauthorized)
				return
			}
			ts := time.UnixMilli(tsMs)
			if drift := time.Since(ts); drift > MaxClockDrift || drift < -MaxClockDrift {
				writeJSONError(w, "Timestamp drift exceeds tolerance", http.StatusUnauthorized)
				return
			}

			// Lire le body avec une limite stricte.
			body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodySize+1))
			if err != nil {
				writeJSONError(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			if len(body) > MaxBodySize {
				writeJSONError(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			_ = r.Body.Close()

			// Calculer la signature attendue : HMAC-SHA256(secret, "<timestamp>.<rawBody>")
			mac := hmac.New(sha256.New, []byte(hubSecret))
			mac.Write([]byte(timestampStr))
			mac.Write([]byte("."))
			mac.Write(body)
			expected := mac.Sum(nil)

			provided, err := hex.DecodeString(signature)
			if err != nil || !hmac.Equal(provided, expected) {
				writeJSONError(w, "Invalid signature", http.StatusUnauthorized)
				return
			}

			// Restituer le body au handler suivant.
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))

			next.ServeHTTP(w, r)
		})
	}
}
