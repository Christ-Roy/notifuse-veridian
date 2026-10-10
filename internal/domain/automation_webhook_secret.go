package domain

import (
	"fmt"

	"github.com/Notifuse/notifuse/pkg/crypto"
)

// Veridian fork — lot 0 (2026-10-10). Le secret d'un nœud webhook était stocké
// EN CLAIR dans le JSON du nœud et renvoyé tel quel par automations.get/list.
// Désormais :
//   - au repos : `secret_encrypted` (AES via pkg/crypto, même passphrase et
//     même mécanisme que les secrets des intégrations) ; jamais de `secret`
//     persisté ;
//   - dans l'API : jamais ni `secret` ni `secret_encrypted`, seulement
//     `has_secret` (bool) ;
//   - en entrée : `secret` en clair (nouveau secret), absent ou vide ou égal au
//     masque (on garde l'ancien), `clear_secret: true` (on le supprime).

const (
	// WebhookSecretMask est la valeur de masque qu'un client peut renvoyer à
	// l'identique pour signifier « ne change pas le secret ».
	WebhookSecretMask = "********"

	webhookCfgSecret          = "secret"
	webhookCfgSecretEncrypted = "secret_encrypted"
	webhookCfgHasSecret       = "has_secret"
	webhookCfgClearSecret     = "clear_secret"
)

func cfgString(cfg map[string]interface{}, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

// ApplyWebhookNodeSecretsOnSave prépare les nœuds webhook à la persistance :
// chiffre un secret fourni en clair, conserve l'ancien quand aucun nouveau
// n'est fourni, ignore tout `secret_encrypted` venu du client (il ne peut
// provenir que du stockage). `existing` est l'état déjà persisté (nil à la
// création). passphrase vide + secret à chiffrer = erreur (échec fermé).
func ApplyWebhookNodeSecretsOnSave(nodes []*AutomationNode, existing []*AutomationNode, passphrase string) error {
	old := map[string]*AutomationNode{}
	for _, n := range existing {
		if n != nil && n.Type == NodeTypeWebhook {
			old[n.ID] = n
		}
	}

	for _, n := range nodes {
		if n == nil || n.Type != NodeTypeWebhook || n.Config == nil {
			continue
		}
		cfg := n.Config

		incoming := cfgString(cfg, webhookCfgSecret)
		clear, _ := cfg[webhookCfgClearSecret].(bool)

		// Rien de ce qui suit ne doit venir du client.
		delete(cfg, webhookCfgSecret)
		delete(cfg, webhookCfgSecretEncrypted)
		delete(cfg, webhookCfgHasSecret)
		delete(cfg, webhookCfgClearSecret)

		switch {
		case clear:
			// secret supprimé
		case incoming != "" && incoming != WebhookSecretMask:
			if passphrase == "" {
				return fmt.Errorf("webhook node %s: cannot store secret: server encryption key is not configured", n.ID)
			}
			enc, err := crypto.EncryptString(incoming, passphrase)
			if err != nil {
				return fmt.Errorf("webhook node %s: failed to encrypt secret: %w", n.ID, err)
			}
			cfg[webhookCfgSecretEncrypted] = enc
		default:
			prev := old[n.ID]
			if prev == nil || prev.Config == nil {
				break
			}
			if enc := cfgString(prev.Config, webhookCfgSecretEncrypted); enc != "" {
				cfg[webhookCfgSecretEncrypted] = enc
			} else if legacy := cfgString(prev.Config, webhookCfgSecret); legacy != "" {
				// Nœud antérieur au chiffrement : on migre au passage.
				if passphrase == "" {
					return fmt.Errorf("webhook node %s: cannot migrate secret: server encryption key is not configured", n.ID)
				}
				enc, err := crypto.EncryptString(legacy, passphrase)
				if err != nil {
					return fmt.Errorf("webhook node %s: failed to encrypt secret: %w", n.ID, err)
				}
				cfg[webhookCfgSecretEncrypted] = enc
			}
		}
	}
	return nil
}

// RedactWebhookNodeSecretsForAPI retire, EN PLACE, tout secret des nœuds
// webhook et pose `has_secret`. À appeler sur tout objet Automation qui part
// vers un client (réponses de l'API).
func RedactWebhookNodeSecretsForAPI(nodes []*AutomationNode) {
	for _, n := range nodes {
		if n == nil || n.Type != NodeTypeWebhook || n.Config == nil {
			continue
		}
		// On travaille sur une copie de la config : l'objet peut être partagé.
		cfg := make(map[string]interface{}, len(n.Config)+1)
		for k, v := range n.Config {
			cfg[k] = v
		}
		has := cfgString(cfg, webhookCfgSecretEncrypted) != "" || cfgString(cfg, webhookCfgSecret) != ""
		delete(cfg, webhookCfgSecret)
		delete(cfg, webhookCfgSecretEncrypted)
		delete(cfg, webhookCfgClearSecret)
		cfg[webhookCfgHasSecret] = has
		n.Config = cfg
	}
}

// ResolveWebhookNodeSecret rend le secret en clair d'un nœud webhook, pour
// signer l'appel. Renvoie "" si le nœud n'a pas de secret. Accepte l'ancien
// format (secret en clair) tant qu'un nœud n'a pas été ré-enregistré.
func ResolveWebhookNodeSecret(cfg map[string]interface{}, passphrase string) (string, error) {
	if enc := cfgString(cfg, webhookCfgSecretEncrypted); enc != "" {
		if passphrase == "" {
			return "", fmt.Errorf("cannot decrypt webhook secret: server encryption key is not configured")
		}
		secret, err := crypto.DecryptFromHexString(enc, passphrase)
		if err != nil {
			return "", fmt.Errorf("cannot decrypt webhook secret: %w", err)
		}
		return secret, nil
	}
	return cfgString(cfg, webhookCfgSecret), nil
}
