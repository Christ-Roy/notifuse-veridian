package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTenantBaseURL(t *testing.T) {
	for _, ok := range []string{"", "https://api.firecrawl.dev", "https://fc.example.com:8443/v1", "http://example.com", "https://8.8.8.8"} {
		assert.NoError(t, ValidateTenantBaseURL(ok), ok)
	}
	for _, bad := range []string{
		"http://169.254.169.254/latest", "http://127.0.0.1:11434/v1", "https://10.0.0.5",
		"http://100.108.136.89:4646", "https://[::1]/", "ftp://example.com", "not a url",
		"https://user:pass@example.com", "http://localhost-is-a-name-so-allowed-here@127.0.0.1/",
	} {
		assert.Error(t, ValidateTenantBaseURL(bad), bad)
	}
	err := ValidateTenantBaseURL("http://169.254.169.254/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal/private/link-local/metadata")
}

// Firecrawl et OpenAI refusent une URL interne A LA SAISIE, avec la raison.
func TestFirecrawlAndOpenAIValidateRefuseInternalBaseURL(t *testing.T) {
	fc := &FirecrawlSettings{APIKey: "fc-x", BaseURL: "http://169.254.169.254/"}
	err := fc.Validate("secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal/private/link-local/metadata")

	ok := &FirecrawlSettings{APIKey: "fc-x", BaseURL: "https://fc.example.com"}
	assert.NoError(t, ok.Validate("secret"))

	oa := &OpenAISettings{APIKey: "k", Model: "m", BaseURL: "http://127.0.0.1:11434/v1"}
	err = oa.Validate("secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal/private/link-local/metadata")
	assert.NoError(t, (&OpenAISettings{APIKey: "k", Model: "m", BaseURL: "https://api.example.com/v1"}).Validate("secret"))
}
