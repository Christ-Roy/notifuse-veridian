package activite

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Verdict : ce que la réponse du Hub commande (§5.25).
type Verdict int

const (
	// Accepted : 2xx, le lot est lu (acceptés, doublons et ignorés compris).
	Accepted Verdict = iota
	// Refused : 400, 409, 413, refus définitif du lot entier ; on ne rejoue pas, on journalise.
	Refused
	// Unauthorized : 401 (ou 403), signature refusée ; alerte, rejeu après resynchronisation de l'abonnement.
	Unauthorized
	// Retry : 429, 5xx, route absente (404 tant que le Hub ne sert pas /lot), réseau ; rejeu avec attente croissante.
	Retry
)

func (v Verdict) String() string {
	return [...]string{"accepte", "refuse", "non_autorise", "rejeu"}[v]
}

// Classify : le verdict d'un statut HTTP.
func Classify(status int) Verdict {
	switch {
	case status >= 200 && status < 300:
		return Accepted
	case status == http.StatusBadRequest || status == http.StatusConflict || status == http.StatusRequestEntityTooLarge:
		return Refused
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return Unauthorized
	default:
		return Retry
	}
}

// Reply : le compte rendu d'un 200.
type Reply struct {
	Accepted   int `json:"acceptes"`
	Duplicates int `json:"doublons"`
	Ignored    []struct {
		Index  int    `json:"index"`
		Reason string `json:"raison"`
	} `json:"ignores"`
	// Reason : un 200 qui n'a rien relayé (evenements_fermes, paire_eteinte, organisation_inactive) ; traité, jamais rejoué.
	Reason string `json:"raison"`
}

// ClosedReason : la raison d'un 200 qui n'a rien relayé, réduite aux valeurs connues du Hub (étiquette de métrique bornée).
func ClosedReason(r string) string {
	switch r = strings.TrimSpace(r); r {
	case "":
		return ""
	case "evenements_fermes", "paire_eteinte", "organisation_inactive":
		return r
	}
	return "autre"
}

// Result : l'issue d'un envoi.
type Result struct {
	Verdict    Verdict
	Status     int           // 0 = pas de réponse (réseau)
	RetryAfter time.Duration // Retry-After lu, 0 sinon
	Reply      Reply
	Detail     string // extrait borné du corps d'une réponse en erreur, ou l'erreur réseau
}

// Sign : signature Standard Webhooks `v1,<base64 HMAC-SHA256("id.timestamp.corps")>`, clé = le secret `whsec_<base64>` décodé.
func Sign(secret, msgID string, ts int64, body []byte) (string, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(key) == 0 {
		return "", fmt.Errorf("secret d'abonnement illisible")
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msgID + "." + strconv.FormatInt(ts, 10) + "."))
	m.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil)), nil
}

// Sender poste un lot au Hub.
type Sender struct {
	Client *http.Client
	Now    func() time.Time
}

// NewClient : sans redirection suivie (le corps porte des adresses), borné dans le temps.
func NewClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Send poste le lot signé : webhook-id = lot_id, horodatage du moment de l'envoi (le Hub refuse au-delà de 5 min).
func (s *Sender) Send(ctx context.Context, url, secret string, b Batch) Result {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	ts := now().Unix()
	sig, err := Sign(secret, b.Lot.LotID, ts, b.Body)
	if err != nil {
		return Result{Verdict: Unauthorized, Detail: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b.Body))
	if err != nil {
		return Result{Verdict: Retry, Detail: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", b.Lot.LotID)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("webhook-signature", sig)
	c := s.Client
	if c == nil {
		c = NewClient()
	}
	resp, err := c.Do(req)
	if err != nil {
		return Result{Verdict: Retry, Detail: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	res := Result{Verdict: Classify(resp.StatusCode), Status: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"), now())}
	if res.Verdict == Accepted {
		_ = json.Unmarshal(raw, &res.Reply)
	} else {
		res.Detail = clip(string(raw), 300)
	}
	return res
}

// retryAfter : secondes ou date HTTP ; 0 si absent ou illisible.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// Backoff : attente avant le n-ième rejeu (n >= 1) : 30 s, 1, 2, 4... min, au plus max ; Retry-After l'emporte s'il est plus long.
func Backoff(n int, retryAfter, max time.Duration) time.Duration {
	if n < 1 {
		n = 1
	}
	d := 30 * time.Second
	for i := 1; i < n && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	if retryAfter > d {
		d = retryAfter
	}
	if d > 6*time.Hour {
		d = 6 * time.Hour
	}
	return d
}
