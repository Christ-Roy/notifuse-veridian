package middleware

// === Veridian patch — Lot J ===
// veridian_paywall_obfuscation : helpers d'obfuscation pour le mode dégradé
// soft-deleted. Quand un tenant a `deleted_at != NULL` (Hub a déclenché
// SoftDelete), Notifuse ne renvoie plus un mur béton 402 sur ses GET. À la
// place :
//
//   - Routes lecture (GET/HEAD/OPTIONS) : la response JSON est wrappée par
//     httptest.NewRecorder, désérialisée, walkée récursivement, et chaque
//     string value est obfusquée (33% des chars en clair + reste `•••••`).
//     Les champs `SENSITIVE_FIELDS` (password, api_key, billing_info,
//     stripe_*) sont TOUJOURS obfusqués à 100% (full `•••`) peu importe
//     leur position dans l'arbre.
//
//   - Routes écriture (POST/PUT/PATCH/DELETE) : 402 + body standardisé
//     {error: tenant_soft_deleted, restore_url, deleted_at,
//     purge_eligible_at} — voir veridian_paywall.go pour la branche.
//
// Pourquoi 33% en clair ? Le client doit pouvoir reconnaître ses données
// (les premières lettres d'un nom ou email suffisent à se repérer) sans
// pouvoir les exporter en clair. UX : « vos données sont là, en sursis,
// pas perdues — réactivez votre compte pour les récupérer en clair ».
//
// Le wiring du middleware est dans veridian_paywall.go (NewVeridianSoftDeletedMiddleware
// + VeridianSoftDeletedFilter).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// veridianDefaultHubURL est l'URL par défaut du Hub Veridian pour le
// restore_url. Hardcoded ici car aucune config HubURL n'existe encore dans
// config.Config — peut être surchargée via VeridianSetHubURL si besoin
// (staging vs prod). Voir CONTRAT-HUB §5.7 (lifecycle).
var veridianHubURL = "https://app.veridian.site"

// VeridianSetHubURL surcharge l'URL du Hub utilisée pour le restore_url
// dans la réponse 402 soft-deleted. Idempotent. Utilisable par app.Start()
// si une config HUB_URL est introduite plus tard.
func VeridianSetHubURL(url string) {
	if url != "" {
		veridianHubURL = strings.TrimRight(url, "/")
	}
}

// VeridianGetHubURL retourne l'URL du Hub courante (utile pour les tests).
func VeridianGetHubURL() string {
	return veridianHubURL
}

// veridianSensitiveFields liste les noms de champs JSON qui doivent être
// TOUJOURS obfusqués à 100% (full `•••`), peu importe leur niveau dans
// l'arbre JSON. Match case-insensitive sur le nom de clé exact OU prefix
// `stripe_` (les champs Stripe sont tous sensibles).
//
// Référence : CONTRAT-HUB §5.9 SENSITIVE_FIELDS (token HMAC, billing info,
// credentials providers SMTP/SES).
var veridianSensitiveFields = map[string]struct{}{
	"password":             {},
	"password_hash":        {},
	"api_key":              {},
	"apikey":               {},
	"secret":               {},
	"secret_key":           {},
	"token":                {},
	"access_token":         {},
	"refresh_token":        {},
	"client_secret":        {},
	"billing_info":         {},
	"billing":              {},
	"credit_card":          {},
	"card_number":          {},
	"cvv":                  {},
	"hmac":                 {},
	"hmac_secret":          {},
	"private_key":          {},
	"smtp_password":        {},
	"ses_secret_access_key": {},
}

// veridianSensitivePrefixes liste les préfixes de clés JSON qui doivent
// être TOUJOURS obfusqués (full). Ex : `stripe_customer_id`, `stripe_*`.
var veridianSensitivePrefixes = []string{
	"stripe_",
}

// veridianIsSensitiveField retourne true si le nom de clé JSON donné doit
// être obfusqué à 100% peu importe son contexte. Case-insensitive.
func veridianIsSensitiveField(key string) bool {
	if key == "" {
		return false
	}
	k := strings.ToLower(key)
	if _, ok := veridianSensitiveFields[k]; ok {
		return true
	}
	for _, prefix := range veridianSensitivePrefixes {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// veridianObfuscate retourne une version obfusquée d'une string : on
// garde 33% des chars en clair en début, puis on remplit le reste avec
// `•` (U+2022). Les strings <=3 chars sont entièrement remplacées par
// des bullets (sinon on lirait toute la valeur).
//
// Comptage en runes (pas bytes) pour gérer l'unicode correctement —
// les noms français avec accents (é, à, ç) ou les caractères CJK ne
// doivent pas être coupés au milieu d'un code point.
//
// Exemples :
//
//	veridianObfuscate("")            == ""
//	veridianObfuscate("a")           == "•"
//	veridianObfuscate("abc")         == "•••"
//	veridianObfuscate("alice@me.com") == "ali••••••••"
//	veridianObfuscate("éléphant")    == "é••••••" (rune-safe)
func veridianObfuscate(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	n := len(runes)
	if n <= 3 {
		return strings.Repeat("•", n)
	}
	keep := n / 3
	return string(runes[:keep]) + strings.Repeat("•", n-keep)
}

// veridianObfuscateFull retourne une chaîne entièrement masquée (full
// `•••`) de la même longueur (en runes) que la valeur d'entrée. Utilisé
// pour les SENSITIVE_FIELDS.
func veridianObfuscateFull(value string) string {
	if value == "" {
		return ""
	}
	return strings.Repeat("•", len([]rune(value)))
}

// veridianWalkAndObfuscate parcourt récursivement un arbre JSON
// (interface{} retourné par json.Unmarshal) et obfusque toutes les valeurs
// string. Les structures (map/slice) sont muté en place. Les valeurs nil,
// number, bool ne sont pas modifiées.
//
// Le paramètre `parentKey` est le nom de la clé sous laquelle se trouve
// la valeur (vide pour la racine ou pour les éléments de tableau). Il
// permet de détecter les SENSITIVE_FIELDS pour appliquer obfuscation
// totale au lieu de partielle.
func veridianWalkAndObfuscate(node interface{}, parentKey string) interface{} {
	switch v := node.(type) {
	case map[string]interface{}:
		for key, child := range v {
			v[key] = veridianWalkAndObfuscate(child, key)
		}
		return v
	case []interface{}:
		for i, child := range v {
			v[i] = veridianWalkAndObfuscate(child, parentKey)
		}
		return v
	case string:
		if veridianIsSensitiveField(parentKey) {
			return veridianObfuscateFull(v)
		}
		return veridianObfuscate(v)
	default:
		// nil, number, bool, json.Number : on laisse passer
		return v
	}
}

// veridianObfuscateJSONBody désérialise un body JSON, walke l'arbre,
// obfusque toutes les strings, et re-sérialise. Retourne (newBody,
// isJSON). Si le body n'est pas du JSON valide, retourne (originalBody,
// false) pour que l'appelant puisse passer-through sans toucher.
//
// L'usage typique est dans le middleware soft-deleted : on capture la
// response via httptest.NewRecorder, on tente d'obfusquer, et si non-
// JSON on log warn puis on passe le body tel quel (cas binaires :
// export CSV, ZIP, etc. → ce sont en théorie des routes mutation déjà
// bloquées en 402, mais belt & braces).
func veridianObfuscateJSONBody(body []byte) ([]byte, bool) {
	if len(body) == 0 {
		return body, false
	}
	// Heuristique rapide : le premier caractère non-whitespace doit être { ou [.
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return body, false
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return body, false
	}
	var parsed interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return body, false
	}
	obfuscated := veridianWalkAndObfuscate(parsed, "")
	out, err := json.Marshal(obfuscated)
	if err != nil {
		// Ne devrait pas arriver (on vient de désérialiser sans erreur),
		// mais belt & braces : on retourne le body original.
		return body, false
	}
	return out, true
}

// veridianResponseCapturer est un http.ResponseWriter qui capture la
// response (status + body) au lieu de l'écrire sur le wire. Permet au
// middleware soft-deleted d'intercepter la réponse du handler upstream,
// la désérialiser, l'obfusquer, et la ré-émettre. Inspiré de
// httptest.ResponseRecorder mais sans dépendance test.
type veridianResponseCapturer struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
	wroteHeader bool
}

func newVeridianResponseCapturer() *veridianResponseCapturer {
	return &veridianResponseCapturer{
		header:     make(http.Header),
		statusCode: http.StatusOK, // default si WriteHeader n'est jamais appelé
	}
}

// Header implémente http.ResponseWriter.
func (c *veridianResponseCapturer) Header() http.Header {
	return c.header
}

// WriteHeader implémente http.ResponseWriter.
func (c *veridianResponseCapturer) WriteHeader(statusCode int) {
	if c.wroteHeader {
		return
	}
	c.statusCode = statusCode
	c.wroteHeader = true
}

// Write implémente http.ResponseWriter (capture le body).
func (c *veridianResponseCapturer) Write(p []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	return c.body.Write(p)
}

// flushTo copie les headers + status + body capturé vers un vrai
// http.ResponseWriter. Si bodyOverride est non-nil, il remplace le body
// capturé (utilisé pour pousser la version obfusquée).
func (c *veridianResponseCapturer) flushTo(w http.ResponseWriter, bodyOverride []byte) {
	for k, vs := range c.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	body := bodyOverride
	if body == nil {
		body = c.body.Bytes()
	}
	// Mise à jour de Content-Length si on a changé le body. Le walker JSON
	// peut produire un body plus court (obfuscation = même longueur en
	// runes mais bytes peuvent diverger sur unicode → on recompute).
	if bodyOverride != nil {
		w.Header().Set("Content-Length", itoaPositive(len(body)))
	}
	w.WriteHeader(c.statusCode)
	_, _ = w.Write(body)
}

// itoaPositive est un petit helper sans dépendance fmt (évite l'overhead
// d'un format pour un cas chaud middleware).
func itoaPositive(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
