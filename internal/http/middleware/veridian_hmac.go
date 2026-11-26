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
	"os"
	"strconv"
	"strings"
	"sync"
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

			if status, msg := VerifyVeridianHubSignature(r, hubSecret); status != 0 {
				writeJSONError(w, msg, status)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// VerifyVeridianHubSignature vérifie X-Veridian-Hub-Signature et rend (0, "") si elle est valide ; le corps est alors
// remis en place pour la suite. Sinon : le statut HTTP et le message du refus. Seule définition d'une signature Hub
// valide : le middleware et l'exemption du rate-limit global s'y reportent tous deux (26/11 : l'exemption ne lisait
// que la PRÉSENCE de l'en-tête, « X-Veridian-Hub-Signature: x » suffisait à sortir de toute limite).
//
// Deux versions (26/11, chasse sécu lot A) :
//   - v1 (sans X-Veridian-Sig-Version) : HMAC(secret, "<ts>.<body>"). Ne lie ni la méthode, ni le chemin, ni la query :
//     une signature captée se rejoue sur une autre route ou un autre tenant pendant la fenêtre. Acceptée tant que
//     VERIDIAN_HMAC_REQUIRE_V2 n'est pas vraie, le temps que le Hub migre.
//   - v2 (X-Veridian-Sig-Version: 2) : HMAC(secret, "<ts>\n<METHOD>\n<chemin encodé>\n<query brute>\n<sha256 hex du corps>"),
//     et une signature v2 ne sert qu'UNE fois (cache anti-rejeu sur la fenêtre).
//
// Fiche 46.
func VerifyVeridianHubSignature(r *http.Request, hubSecret string) (int, string) {
	return verifyVeridianHubSignature(r, hubSecret, true)
}

// veridianHubSignatureValid : même vérification, sans consommer la signature v2 (exemption du rate-limit, qui passe
// AVANT le middleware : la consommer ici ferait refuser la vraie requête comme un rejeu).
func veridianHubSignatureValid(r *http.Request, hubSecret string) bool {
	status, _ := verifyVeridianHubSignature(r, hubSecret, false)
	return status == 0
}

func verifyVeridianHubSignature(r *http.Request, hubSecret string, consume bool) (int, string) {
	if hubSecret == "" {
		return http.StatusServiceUnavailable, "Veridian HMAC not configured (HUB_API_SECRET missing)"
	}
	signature := r.Header.Get("X-Veridian-Hub-Signature")
	timestampStr := r.Header.Get("X-Veridian-Timestamp")
	if signature == "" || timestampStr == "" {
		return http.StatusUnauthorized, "Missing X-Veridian-Hub-Signature or X-Veridian-Timestamp"
	}
	v2 := strings.TrimSpace(r.Header.Get(VeridianSigVersionHeader)) == "2"
	if !v2 && veridianRequireV2() {
		return http.StatusUnauthorized, "Signature v1 refused (X-Veridian-Sig-Version: 2 required)"
	}
	// Fenêtre temporelle (anti-rejeu).
	tsMs, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return http.StatusUnauthorized, "Invalid X-Veridian-Timestamp (must be unix ms)"
	}
	if drift := time.Since(time.UnixMilli(tsMs)); drift > MaxClockDrift || drift < -MaxClockDrift {
		return http.StatusUnauthorized, "Timestamp drift exceeds tolerance"
	}
	var body []byte
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, MaxBodySize+1))
		_ = r.Body.Close()
		if err != nil {
			return http.StatusBadRequest, "Failed to read request body"
		}
	}
	// Le corps est rendu AVANT tout refus restant : un appelant qui l'ignore (exemption du rate-limit) laisse la
	// requête intacte pour le handler.
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	if len(body) > MaxBodySize {
		return http.StatusRequestEntityTooLarge, "Request body too large"
	}
	var expected []byte
	if v2 {
		expected = veridianHubMACv2(hubSecret, timestampStr, r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body)
	} else {
		mac := hmac.New(sha256.New, []byte(hubSecret))
		mac.Write([]byte(timestampStr))
		mac.Write([]byte("."))
		mac.Write(body)
		expected = mac.Sum(nil)
	}
	provided, err := hex.DecodeString(signature)
	if err != nil || !hmac.Equal(provided, expected) {
		return http.StatusUnauthorized, "Invalid signature"
	}
	if v2 && consume && !veridianSeenSignatures.firstUse(hex.EncodeToString(provided), time.Now()) {
		return http.StatusUnauthorized, "Signature already used"
	}
	return 0, ""
}

// VeridianSigVersionHeader porte la version de la signature Hub ("2").
const VeridianSigVersionHeader = "X-Veridian-Sig-Version"

func veridianHubMACv2(secret, ts, method, path, rawQuery string, body []byte) []byte {
	sum := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + strings.ToUpper(method) + "\n" + path + "\n" + rawQuery + "\n" + hex.EncodeToString(sum[:])))
	return mac.Sum(nil)
}

// SignVeridianHubRequest pose une signature v2 sur r (corps = body, déjà attaché à r par l'appelant). Pour les appels
// signés émis par Notifuse lui-même.
func SignVeridianHubRequest(r *http.Request, secret string, body []byte) {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	r.Header.Set("X-Veridian-Timestamp", ts)
	r.Header.Set(VeridianSigVersionHeader, "2")
	r.Header.Set("X-Veridian-Hub-Signature", hex.EncodeToString(veridianHubMACv2(secret, ts, r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body)))
}

func veridianRequireV2() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VERIDIAN_HMAC_REQUIRE_V2"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// seenSignatures : les signatures v2 déjà servies, gardées le temps de la fenêtre (2 x MaxClockDrift couvre une
// horloge en avance comme en retard). En mémoire, donc par instance : un rejeu vers une AUTRE allocation (canari)
// passe encore ; la liaison méthode/chemin/query reste, elle, absolue.
type seenSignatures struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

var veridianSeenSignatures = &seenSignatures{seen: map[string]time.Time{}}

const veridianSeenSignaturesMax = 200000

func (s *seenSignatures) firstUse(sig string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[sig]; ok {
		return false
	}
	if len(s.seen) >= veridianSeenSignaturesMax/2 {
		for k, at := range s.seen {
			if now.Sub(at) > 2*MaxClockDrift {
				delete(s.seen, k)
			}
		}
	}
	if len(s.seen) >= veridianSeenSignaturesMax {
		return false // saturé de signatures fraîches : refuser plutôt qu'oublier (fail-closed)
	}
	s.seen[sig] = now
	return true
}
