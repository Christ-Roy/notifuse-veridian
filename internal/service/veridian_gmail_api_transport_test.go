package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type veridianGmailTokenStub struct {
	tokens      []string
	getCalls    int
	invalidated int
}

func (s *veridianGmailTokenStub) GetAccessToken(_ *domain.SMTPSettings) (string, error) {
	token := s.tokens[s.getCalls]
	s.getCalls++
	return token, nil
}

func (s *veridianGmailTokenStub) InvalidateCacheForSettings(_ *domain.SMTPSettings) {
	s.invalidated++
}

type veridianGmailDoerFunc func(*http.Request) (*http.Response, error)

func (f veridianGmailDoerFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func veridianGmailResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func TestVeridianUsesGmailAPI(t *testing.T) {
	t.Parallel()

	assert.True(t, veridianUsesGmailAPI(&domain.SMTPSettings{AuthType: "oauth2", OAuth2Provider: "google"}))
	assert.False(t, veridianUsesGmailAPI(&domain.SMTPSettings{AuthType: "oauth2", OAuth2Provider: "microsoft"}))
	assert.False(t, veridianUsesGmailAPI(&domain.SMTPSettings{AuthType: "basic", OAuth2Provider: "google"}))
	assert.False(t, veridianUsesGmailAPI(nil))
}

func TestVeridianSendViaGmailAPIEncodesMIMEAndAuthorizes(t *testing.T) {
	t.Parallel()

	settings := &domain.SMTPSettings{AuthType: "oauth2", OAuth2Provider: "google"}
	tokens := &veridianGmailTokenStub{tokens: []string{"access-one"}}
	mimeMessage := []byte("From: sender@example.com\r\nTo: recipient@example.com\r\n\r\nBonjour")

	doer := veridianGmailDoerFunc(func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, veridianGmailSendEndpoint, req.URL.String())
		assert.Equal(t, "Bearer access-one", req.Header.Get("Authorization"))

		var payload map[string]string
		require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
		decoded, err := base64.RawURLEncoding.DecodeString(payload["raw"])
		require.NoError(t, err)
		assert.Equal(t, mimeMessage, decoded)
		return veridianGmailResponse(http.StatusOK, `{"id":"gmail-message-id"}`), nil
	})

	require.NoError(t, veridianSendViaGmailAPI(context.Background(), settings, mimeMessage, tokens, doer))
	assert.Equal(t, 1, tokens.getCalls)
	assert.Zero(t, tokens.invalidated)
}

func TestVeridianSendViaGmailAPIRetriesOneUnauthorized(t *testing.T) {
	t.Parallel()

	settings := &domain.SMTPSettings{AuthType: "oauth2", OAuth2Provider: "google"}
	tokens := &veridianGmailTokenStub{tokens: []string{"expired", "fresh"}}
	calls := 0
	doer := veridianGmailDoerFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			assert.Equal(t, "Bearer expired", req.Header.Get("Authorization"))
			return veridianGmailResponse(http.StatusUnauthorized, `{"error":{"message":"Invalid Credentials"}}`), nil
		}
		assert.Equal(t, "Bearer fresh", req.Header.Get("Authorization"))
		return veridianGmailResponse(http.StatusOK, `{"id":"gmail-message-id"}`), nil
	})

	require.NoError(t, veridianSendViaGmailAPI(context.Background(), settings, []byte("MIME"), tokens, doer))
	assert.Equal(t, 2, tokens.getCalls)
	assert.Equal(t, 1, tokens.invalidated)
	assert.Equal(t, 2, calls)
}

func TestVeridianSendViaGmailAPIReturnsSanitizedGoogleError(t *testing.T) {
	t.Parallel()

	settings := &domain.SMTPSettings{AuthType: "oauth2", OAuth2Provider: "google"}
	tokens := &veridianGmailTokenStub{tokens: []string{"access-one"}}
	doer := veridianGmailDoerFunc(func(_ *http.Request) (*http.Response, error) {
		return veridianGmailResponse(http.StatusForbidden, `{"error":{"message":"Insufficient Permission","status":"PERMISSION_DENIED"}}`), nil
	})

	err := veridianSendViaGmailAPI(context.Background(), settings, []byte("MIME"), tokens, doer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
	assert.Contains(t, err.Error(), "Insufficient Permission")
	assert.NotContains(t, err.Error(), "access-one")
}
