package relaycert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
)

// cloudflareAPI est l'URL de base de l'API Cloudflare v4 (surchargeable pour les tests).
const cloudflareAPI = "https://api.cloudflare.com/client/v4"

// retryPause : délai avant l'unique nouvel essai sur 5xx ou 429.
var retryPause = 2 * time.Second

// CloudflareDNS implémente challenge.Provider (lego) par l'API REST Cloudflare, en bibliothèque standard.
// Il ne manipule QUE le TXT _acme-challenge qu'il a créé : la suppression se fait par l'identifiant
// d'enregistrement conservé à la création, jamais par recherche de nom. Le jeton vient de
// CLOUDFLARE_DNS_API_TOKEN et n'entre jamais dans une URL, un journal ou une erreur.
type CloudflareDNS struct {
	token   string
	baseURL string
	client  *http.Client

	mu      sync.Mutex
	created map[string]createdRecord // clé : fqdn|valeur
}

type createdRecord struct {
	zoneID string
	id     string
}

// NewCloudflareDNS lit le jeton fourni (issu de l'environnement). Sans jeton : erreur, aucun appel réseau.
func NewCloudflareDNS(token string) (*CloudflareDNS, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("relaycert: CLOUDFLARE_DNS_API_TOKEN absent de l'environnement")
	}
	return &CloudflareDNS{
		token:   token,
		baseURL: cloudflareAPI,
		client:  &http.Client{Timeout: 30 * time.Second},
		created: map[string]createdRecord{},
	}, nil
}

// Present crée le TXT de validation et garde son identifiant pour la suppression exacte.
func (c *CloudflareDNS) Present(domain, _, keyAuth string) error {
	fqdn, value := dns01.GetRecord(domain, keyAuth)
	zoneID, zoneName, err := c.zoneFor(fqdn)
	if err != nil {
		return err
	}
	name := strings.TrimSuffix(fqdn, ".")
	body := map[string]interface{}{"type": "TXT", "name": name, "content": value, "ttl": 120}
	var out struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := c.do(http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), body, &out); err != nil {
		return fmt.Errorf("relaycert: création du TXT %s dans %s: %w", name, zoneName, err)
	}
	if out.Result.ID == "" {
		return fmt.Errorf("relaycert: Cloudflare n'a pas rendu l'identifiant du TXT %s", name)
	}
	c.mu.Lock()
	c.created[name+"|"+value] = createdRecord{zoneID: zoneID, id: out.Result.ID}
	c.mu.Unlock()
	return nil
}

// CleanUp supprime le TXT créé par Present, par son identifiant. Sans trace de création : rien à faire.
func (c *CloudflareDNS) CleanUp(domain, _, keyAuth string) error {
	fqdn, value := dns01.GetRecord(domain, keyAuth)
	name := strings.TrimSuffix(fqdn, ".")
	key := name + "|" + value
	c.mu.Lock()
	rec, ok := c.created[key]
	c.mu.Unlock()
	if !ok {
		return nil
	}
	if err := c.do(http.MethodDelete, fmt.Sprintf("/zones/%s/dns_records/%s", rec.zoneID, rec.id), nil, nil); err != nil {
		return fmt.Errorf("relaycert: suppression du TXT %s (id %s): %w", name, rec.id, err)
	}
	c.mu.Lock()
	delete(c.created, key)
	c.mu.Unlock()
	return nil
}

// CleanUpAll supprime tout TXT encore créé. Appelé en defer par l'émetteur, succès, erreur ou panic.
func (c *CloudflareDNS) CleanUpAll() error {
	c.mu.Lock()
	pending := make(map[string]createdRecord, len(c.created))
	for k, v := range c.created {
		pending[k] = v
	}
	c.mu.Unlock()
	var firstErr error
	for k, rec := range pending {
		if err := c.do(http.MethodDelete, fmt.Sprintf("/zones/%s/dns_records/%s", rec.zoneID, rec.id), nil, nil); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("relaycert: nettoyage du TXT %s: %w", strings.SplitN(k, "|", 2)[0], err)
			}
			continue
		}
		c.mu.Lock()
		delete(c.created, k)
		c.mu.Unlock()
	}
	return firstErr
}

// zoneFor retrouve l'identifiant de la zone parente de fqdn, du nom le plus long au plus court.
func (c *CloudflareDNS) zoneFor(fqdn string) (string, string, error) {
	labels := strings.Split(strings.TrimSuffix(fqdn, "."), ".")
	for i := 0; i < len(labels)-1; i++ {
		candidate := strings.Join(labels[i:], ".")
		var out struct {
			Result []struct {
				ID string `json:"id"`
			} `json:"result"`
		}
		if err := c.do(http.MethodGet, "/zones?"+url.Values{"name": {candidate}}.Encode(), nil, &out); err != nil {
			return "", "", fmt.Errorf("relaycert: recherche de zone %s: %w", candidate, err)
		}
		if len(out.Result) > 0 {
			return out.Result[0].ID, candidate, nil
		}
	}
	return "", "", fmt.Errorf("relaycert: aucune zone Cloudflare pour %s (le jeton voit-il la zone ?)", fqdn)
}

// do exécute une requête authentifiée. Un seul nouvel essai sur 5xx ou 429. Le jeton n'entre dans
// aucune erreur : seuls le code HTTP et la nature de l'échec sont remontés.
func (c *CloudflareDNS) do(method, path string, body interface{}, out interface{}) error {
	raw, err := c.send(method, path, body)
	if err != nil {
		return err
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return errors.New("réponse Cloudflare illisible")
		}
	}
	return nil
}

func (c *CloudflareDNS) send(method, path string, body interface{}) ([]byte, error) {
	var payload []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = raw
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(retryPause)
		}
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequest(method, c.baseURL+path, reader)
		if err != nil {
			return nil, errors.New("requête Cloudflare invalide")
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = errors.New("échec réseau vers l'API Cloudflare")
			continue
		}
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if rerr != nil {
			lastErr = errors.New("lecture de la réponse Cloudflare interrompue")
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode/100 == 5 {
			lastErr = fmt.Errorf("HTTP %d (réessai)", resp.StatusCode)
			continue
		}
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		var env struct {
			Success bool `json:"success"`
		}
		if err := json.Unmarshal(raw, &env); err == nil && !env.Success {
			return nil, errors.New("réponse Cloudflare sans succès")
		}
		return raw, nil
	}
	return nil, lastErr
}

// Garde le contrat de challenge.Provider (vérifié à la compilation).
var _ challenge.Provider = (*CloudflareDNS)(nil)
