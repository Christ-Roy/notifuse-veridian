package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const webhookTestPassphrase = "lot0-domain-passphrase-32-chars!"

func whNode(id string, cfg map[string]interface{}) *AutomationNode {
	return &AutomationNode{ID: id, AutomationID: "a1", Type: NodeTypeWebhook, Config: cfg}
}

func TestApplyWebhookNodeSecretsOnSave(t *testing.T) {
	t.Run("chiffre le secret fourni, jamais de clair persisté", func(t *testing.T) {
		n := whNode("w1", map[string]interface{}{"url": "https://h.example.com", "secret": "clear-value"})
		require.NoError(t, ApplyWebhookNodeSecretsOnSave([]*AutomationNode{n}, nil, webhookTestPassphrase))
		b, _ := json.Marshal(n)
		assert.NotContains(t, string(b), "clear-value")
		assert.NotContains(t, n.Config, "secret")
		sec, err := ResolveWebhookNodeSecret(n.Config, webhookTestPassphrase)
		require.NoError(t, err)
		assert.Equal(t, "clear-value", sec)
		_, err = ResolveWebhookNodeSecret(n.Config, "mauvaise-passphrase-32-caracteres!!")
		assert.Error(t, err, "une autre passphrase ne déchiffre pas")
	})

	t.Run("ignore un secret_encrypted et un has_secret venus du client", func(t *testing.T) {
		n := whNode("w1", map[string]interface{}{"url": "https://h.example.com", "secret_encrypted": "deadbeef", "has_secret": true})
		require.NoError(t, ApplyWebhookNodeSecretsOnSave([]*AutomationNode{n}, nil, webhookTestPassphrase))
		assert.NotContains(t, n.Config, "secret_encrypted")
		assert.NotContains(t, n.Config, "has_secret")
	})

	t.Run("sans nouveau secret: conserve l'ancien (absent, vide, masque)", func(t *testing.T) {
		prev := whNode("w1", map[string]interface{}{"url": "https://h.example.com", "secret": "keep-me"})
		require.NoError(t, ApplyWebhookNodeSecretsOnSave([]*AutomationNode{prev}, nil, webhookTestPassphrase))
		for name, incoming := range map[string]map[string]interface{}{
			"absent": {"url": "https://h.example.com/2"},
			"vide":   {"url": "https://h.example.com/2", "secret": ""},
			"masque": {"url": "https://h.example.com/2", "secret": WebhookSecretMask},
		} {
			n := whNode("w1", incoming)
			require.NoError(t, ApplyWebhookNodeSecretsOnSave([]*AutomationNode{n}, []*AutomationNode{prev}, webhookTestPassphrase), name)
			sec, err := ResolveWebhookNodeSecret(n.Config, webhookTestPassphrase)
			require.NoError(t, err, name)
			assert.Equal(t, "keep-me", sec, name)
		}
	})

	t.Run("clear_secret supprime", func(t *testing.T) {
		prev := whNode("w1", map[string]interface{}{"url": "https://h.example.com", "secret": "x"})
		require.NoError(t, ApplyWebhookNodeSecretsOnSave([]*AutomationNode{prev}, nil, webhookTestPassphrase))
		n := whNode("w1", map[string]interface{}{"url": "https://h.example.com", "clear_secret": true})
		require.NoError(t, ApplyWebhookNodeSecretsOnSave([]*AutomationNode{n}, []*AutomationNode{prev}, webhookTestPassphrase))
		sec, err := ResolveWebhookNodeSecret(n.Config, webhookTestPassphrase)
		require.NoError(t, err)
		assert.Empty(t, sec)
	})

	t.Run("un autre nœud ne récupère pas le secret d'un voisin", func(t *testing.T) {
		prev := whNode("w1", map[string]interface{}{"url": "https://h.example.com", "secret": "mine"})
		require.NoError(t, ApplyWebhookNodeSecretsOnSave([]*AutomationNode{prev}, nil, webhookTestPassphrase))
		other := whNode("w2", map[string]interface{}{"url": "https://h.example.com"})
		require.NoError(t, ApplyWebhookNodeSecretsOnSave([]*AutomationNode{other}, []*AutomationNode{prev}, webhookTestPassphrase))
		assert.NotContains(t, other.Config, "secret_encrypted")
	})

	t.Run("sans passphrase: échec fermé", func(t *testing.T) {
		n := whNode("w1", map[string]interface{}{"url": "https://h.example.com", "secret": "x"})
		assert.Error(t, ApplyWebhookNodeSecretsOnSave([]*AutomationNode{n}, nil, ""))
	})

	t.Run("ne touche pas aux autres types de nœuds", func(t *testing.T) {
		n := &AutomationNode{ID: "d1", Type: NodeTypeDelay, Config: map[string]interface{}{"secret": "garde", "seconds": 3}}
		require.NoError(t, ApplyWebhookNodeSecretsOnSave([]*AutomationNode{n}, nil, webhookTestPassphrase))
		assert.Equal(t, "garde", n.Config["secret"])
	})
}

func TestRedactWebhookNodeSecretsForAPI(t *testing.T) {
	n := whNode("w1", map[string]interface{}{"url": "https://h.example.com", "secret": "clear", "secret_encrypted": "abcd"})
	orig := n.Config
	RedactWebhookNodeSecretsForAPI([]*AutomationNode{n})
	b, _ := json.Marshal(n)
	assert.NotContains(t, string(b), "clear")
	assert.NotContains(t, string(b), "abcd")
	assert.Equal(t, true, n.Config["has_secret"])
	assert.Equal(t, "clear", orig["secret"], "la config d'origine (partagée) n'est pas mutée")

	none := whNode("w2", map[string]interface{}{"url": "https://h.example.com"})
	RedactWebhookNodeSecretsForAPI([]*AutomationNode{none})
	assert.Equal(t, false, none.Config["has_secret"])
}
