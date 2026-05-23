// Package hub_discovery — client HTTP signe HMAC pour appeler
// `GET <hub>/api/users/by-email` depuis Notifuse au login d'un user.
//
// Sens du flux : **Notifuse (downstream) -> Hub**, contrairement au flux
// historique Hub -> Notifuse (cf. internal/service/veridian_webhook_emitter.go).
// On reutilise toutefois le *meme* secret partage HUB_API_SECRET :
// un secret HMAC est symetrique entre les 2 parties (gravage §6.5
// CONTRAT-HUB Veridian).
//
// String canonique signee (alignee strictement sur Hub `lib/discovery/hmac.ts`) :
//
//	"${timestamp_ms}.GET.${pathname}?${sortedQueryString}"
//
// avec :
//   - timestamp_ms : Date.now() unix epoch en millisecondes
//   - method       : "GET" en majuscule
//   - pathname     : "/api/users/by-email"
//   - sortedQuery  : params URL-encodes apres tri alphabetique par cle
//     (anti-malleabilite de l'ordre des params si un proxy reordonne)
//
// Cas d'usage : Notifuse appelle ce client apres une auth reussie (verify
// magic code) pour decouvrir si le user a d'autres apps Veridian actives
// et pre-charger les liens dashboard cross-app. Le call est BEST-EFFORT :
// tout echec (timeout, Hub down, 5xx, secret manquant) doit retourner
// (false, nil, nil) — l'appelant continue le login normalement.
package hub_discovery

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// Constantes du contrat. Toute modification doit etre synchronisee avec
// `veridian-hub/lib/discovery/hmac.ts` du Hub.
const (
	// DefaultHubBaseURL est l'URL canonique du Hub Veridian en prod. La
	// config Notifuse peut surcharger (utile pour staging, tests E2E,
	// httptest.NewServer en unit tests).
	DefaultHubBaseURL = "https://hub.veridian.site"

	// DiscoveryByEmailPath est le chemin du endpoint Hub.
	DiscoveryByEmailPath = "/api/users/by-email"

	// AppHeaderName : nom de l'app downstream (notifuse).
	AppHeaderName = "x-veridian-app"
	// TimestampHeaderName : unix epoch ms.
	TimestampHeaderName = "x-veridian-timestamp"
	// SignatureHeaderName : hex sha256 de la string canonique.
	SignatureHeaderName = "x-veridian-hub-signature"

	// CallerApp est l'identifiant de Notifuse cote Hub (cf. SUPPORTED_APPS).
	CallerApp = "notifuse"

	// DefaultTimeout = 2s. Aligne avec ticket : si le Hub depasse 2s,
	// on continue le login Notifuse sans bloquer.
	DefaultTimeout = 2 * time.Second
)

// DiscoveryTenant — entree de la liste tenants renvoyee par le Hub.
//
// Schema renvoye par le Hub (intentionnellement minimaliste pour privacy) :
//
//	{"app": "notifuse"|"prospection"|"analytics"|"cms", "role": "owner"|"member"}
//
// On expose les champs en CamelCase pour l'usage Go, avec tags json pour
// le decode.
type DiscoveryTenant struct {
	App  string `json:"app"`
	Role string `json:"role"`
}

// DiscoveryResponse est la reponse brute du Hub. Stable cross-app.
type DiscoveryResponse struct {
	Exists  bool              `json:"exists"`
	Tenants []DiscoveryTenant `json:"tenants"`
}

// Client interface — facilite le mock dans les tests unitaires des callers.
//
// LookupByEmail retourne toujours (false, nil, nil) en cas d'erreur
// non bloquante (timeout, 5xx, connection-refused, secret manquant) — le
// caller ne doit JAMAIS interrompre le login a cause d'une defaillance
// Hub. Seuls 200 + decode reussi donnent un resultat exploitable.
type Client interface {
	LookupByEmail(ctx context.Context, email string) (exists bool, tenants []DiscoveryTenant, err error)
}

// HTTPClient — abstraction pour permettre l'injection d'un http.Client
// custom (timeout reduit en tests, transport mocke, etc.).
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// httpClient est l'implementation concrete de Client.
type httpClient struct {
	baseURL string
	secret  string
	http    HTTPClient
	logger  logger.Logger
	timeout time.Duration

	// now permet d'injecter une clock fixe en test (sinon time.Now).
	now func() time.Time
}

// Config — parametres de construction d'un Client.
//
// Tous les champs sont optionnels avec des defaults raisonnables :
//   - BaseURL vide  -> DefaultHubBaseURL ("https://hub.veridian.site")
//   - Timeout zero  -> DefaultTimeout (2s)
//   - HTTPClient nil -> &http.Client{Timeout: timeout}
//   - Logger nil    -> les warnings best-effort sont silencieux
//
// Secret est OBLIGATOIRE : si vide, NewClient retourne un client
// "disabled" qui repond (false, nil, nil) en silence. Permet le mode
// self-hosted ou dev local sans Hub.
type Config struct {
	BaseURL    string
	Secret     string
	Timeout    time.Duration
	HTTPClient HTTPClient
	Logger     logger.Logger
}

// disabledClient retourne (false, nil, nil) sans appel HTTP. Utilise
// quand HUB_API_SECRET est vide (mode self-hosted ou dev local).
type disabledClient struct {
	logger logger.Logger
}

func (d *disabledClient) LookupByEmail(_ context.Context, _ string) (bool, []DiscoveryTenant, error) {
	if d.logger != nil {
		d.logger.Debug("hub_discovery: disabled (HUB_API_SECRET missing)")
	}
	return false, nil, nil
}

// NewClient construit un Client. Si cfg.Secret est vide, retourne un
// client noop qui n'appelle jamais le Hub (mode self-hosted).
//
// Le constructeur n'effectue pas d'appel reseau. Toutes les erreurs de
// runtime (DNS, TLS, timeout) sont gerees au call et avalees silencieusement
// (log warn rate-limite) pour ne pas bloquer le login.
func NewClient(cfg Config) Client {
	if strings.TrimSpace(cfg.Secret) == "" {
		return &disabledClient{logger: cfg.Logger}
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultHubBaseURL
	}
	// Trim trailing slash pour ne pas avoir un double slash dans le path.
	baseURL = strings.TrimRight(baseURL, "/")

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	var h HTTPClient = cfg.HTTPClient
	if h == nil {
		h = &http.Client{Timeout: timeout}
	}

	return &httpClient{
		baseURL: baseURL,
		secret:  cfg.Secret,
		http:    h,
		logger:  cfg.Logger,
		timeout: timeout,
		now:     time.Now,
	}
}

// LookupByEmail appelle le Hub. Best-effort : tout echec retourne
// (false, nil, nil). Aucun panic, aucune erreur ne remonte au caller
// sauf erreur de programmation (email vide, etc.).
//
// La signature renvoie quand meme un `error` non-nil dans certains cas
// rares (4xx clients qui indiquent un bug Notifuse — secret desynchro,
// drift d'horloge), pour permettre au caller d'au moins le logguer. Le
// caller doit AUSSI ignorer cet error et continuer le login. C'est juste
// un signal pour observability, pas un control-flow.
func (c *httpClient) LookupByEmail(ctx context.Context, email string) (bool, []DiscoveryTenant, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return false, nil, errors.New("hub_discovery: empty email")
	}

	// Construire la query string sans tri (un seul param), mais on passe
	// quand meme par buildCanonicalGetString pour garantir la coherence
	// avec le format Hub si on ajoute des params plus tard.
	q := url.Values{}
	q.Set("email", email)

	timestamp := strconv.FormatInt(c.now().UnixMilli(), 10)
	canonical := buildCanonicalGetString(http.MethodGet, DiscoveryByEmailPath, q, timestamp)

	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(canonical))
	signature := hex.EncodeToString(mac.Sum(nil))

	fullURL := c.baseURL + DiscoveryByEmailPath + "?" + encodeSortedQuery(q)

	// Timeout per-call (en plus du Timeout du http.Client) : permet
	// d'appliquer le 2s contractual meme si l'appelant fournit un context
	// sans deadline.
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fullURL, nil)
	if err != nil {
		c.warn("hub_discovery: build request failed", map[string]interface{}{
			"error": err.Error(),
		})
		return false, nil, nil
	}
	req.Header.Set(AppHeaderName, CallerApp)
	req.Header.Set(TimestampHeaderName, timestamp)
	req.Header.Set(SignatureHeaderName, signature)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// Timeout, DNS error, TLS error, refused connection — best-effort.
		c.warn("hub_discovery: HTTP call failed", map[string]interface{}{
			"error": err.Error(),
		})
		return false, nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// Lire body en limitant la taille (256KB plus que suffisant pour
		// un payload `{exists, tenants}` avec quelques entrees).
		limited := io.LimitReader(resp.Body, 256*1024)
		body, err := io.ReadAll(limited)
		if err != nil {
			c.warn("hub_discovery: read body failed", map[string]interface{}{
				"error": err.Error(),
			})
			return false, nil, nil
		}
		var parsed DiscoveryResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			c.warn("hub_discovery: decode body failed", map[string]interface{}{
				"error": err.Error(),
			})
			return false, nil, nil
		}
		return parsed.Exists, parsed.Tenants, nil
	}

	// 4xx / 5xx — drain body pour reutiliser la connection, puis log
	// rate-limite. On retourne un error pour signal observability mais
	// le caller doit l'ignorer (best-effort).
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
	statusErr := fmt.Errorf("hub_discovery: status %d", resp.StatusCode)
	c.warn("hub_discovery: non-200 from Hub", map[string]interface{}{
		"status": resp.StatusCode,
	})
	return false, nil, statusErr
}

// warn log un warning si logger non-nil. Centralise pour permettre une
// implementation future de rate-limit (par ex. token bucket en memoire)
// sans avoir a toucher tous les call sites.
func (c *httpClient) warn(msg string, fields map[string]interface{}) {
	if c.logger == nil {
		return
	}
	c.logger.WithFields(fields).Warn(msg)
}

// buildCanonicalGetString reproduit a l'identique la fonction TypeScript
// `buildCanonicalGetString` cote Hub (`lib/discovery/hmac.ts`).
//
// Format : "${timestamp}.${METHOD}.${pathname}?${sortedQueryString}"
//
// Si la query est vide, le suffixe "?..." est omis pour permettre la
// signature de requetes sans query. Pour le endpoint by-email, email est
// toujours present donc ce cas n'arrive pas en pratique.
func buildCanonicalGetString(method, pathname string, params url.Values, timestamp string) string {
	if len(params) == 0 {
		return timestamp + "." + strings.ToUpper(method) + "." + pathname
	}
	return timestamp + "." + strings.ToUpper(method) + "." + pathname + "?" + encodeSortedQuery(params)
}

// encodeSortedQuery encode les query params en triant les cles
// alphabetiquement. Pour multi-value (meme cle, plusieurs valeurs),
// l'ordre d'apparition de la valeur dans le slice est conserve (stable
// vs le Hub qui fait pareil avec `searchParams.forEach`).
//
// CRITIQUE — Compatibilite encodeURIComponent : on N'UTILISE PAS
// url.QueryEscape qui encode les espaces en "+" alors que TypeScript
// `encodeURIComponent` les encode en "%20". On utilise encodeURIComponentGo
// qui replique a l'identique le comportement de encodeURIComponent (RFC
// 3986 unreserved chars laisses litteraux : A-Z a-z 0-9 - _ . ! ~ * ' ( )).
//
// Pour un email RFC valide ce piege ne se manifeste pas (les emails ne
// contiennent ni espace ni caracteres lateraux), mais on respecte le
// contrat a la lettre pour eviter une regression silencieuse si le
// payload evolue (ex: query param "filter=foo bar" plus tard).
func encodeSortedQuery(params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	first := true
	for _, k := range keys {
		for _, v := range params[k] {
			if !first {
				b.WriteByte('&')
			}
			b.WriteString(encodeURIComponentGo(k))
			b.WriteByte('=')
			b.WriteString(encodeURIComponentGo(v))
			first = false
		}
	}
	return b.String()
}

// encodeURIComponentGo replique exactement le comportement de la fonction
// JavaScript `encodeURIComponent` :
//   - laisse litteraux : A-Z a-z 0-9 - _ . ! ~ * ' ( )
//   - encode tout le reste en %XX (UTF-8 byte par byte)
//
// Diverge de url.QueryEscape (Go) qui encode " " en "+" et n'epargne pas
// les memes caracteres reserves (notamment ! ~ * ' ( ) ).
func encodeURIComponentGo(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' ||
			c == '!' || c == '~' || c == '*' || c == '\'' ||
			c == '(' || c == ')' {
			b.WriteByte(c)
		} else {
			b.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return b.String()
}
