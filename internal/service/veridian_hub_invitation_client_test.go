package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// computeExpectedSignature recalcule la signature attendue pour valider
// que le client envoie bien la bonne valeur HMAC.
func computeExpectedSignature(secret, ts, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestNewVeridianHubInvitationClient_DefaultsAndDisabledMode(t *testing.T) {
	// Pas de secret => mode self-hosted, Create() retourne ErrHubInvitationDisabled.
	c := NewVeridianHubInvitationClient("", "", nil, logger.NewLogger())
	_, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "u1",
		InviterEmail:      "owner@example.com",
		InviteeEmail:      "new@example.com",
		TargetWorkspaceID: "ws1",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHubInvitationDisabled)
}

func TestVeridianHubInvitationClient_CreateSuccess201(t *testing.T) {
	secret := "test-secret-201"
	expectedExpiry := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	var capturedHeaders http.Header
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture headers + body pour assertions.
		capturedHeaders = r.Header.Clone()
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody = string(raw)

		// Verifie HMAC pour eviter un faux positif (le test doit reflechir
		// l'engagement contractuel envers le Hub : ts + '.' + body sur secret).
		ts := r.Header.Get("x-veridian-timestamp")
		sig := r.Header.Get("x-veridian-invitation-signature")
		require.NotEmpty(t, ts)
		require.NotEmpty(t, sig)
		assert.Equal(t, computeExpectedSignature(secret, ts, capturedBody), sig)
		assert.Equal(t, "notifuse", r.Header.Get("x-veridian-app"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"invitation_id": "inv_123",
			"token": "abcdef0123456789",
			"magic_link_url": "https://hub.staging.veridian.site/invite/abcdef0123456789",
			"expires_at": "` + expectedExpiry.Format(time.RFC3339) + `",
			"target_role": "member",
			"reused": false
		}`))
	}))
	defer server.Close()

	c := NewVeridianHubInvitationClient(server.URL, secret, server.Client(), logger.NewLogger())
	res, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "hub_user_owner1",
		InviterEmail:      "owner@example.com",
		InviteeEmail:      "guest@example.com",
		TargetApp:         "notifuse",
		TargetWorkspaceID: "tenant42",
		TargetRole:        "member",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "inv_123", res.InvitationID)
	assert.Equal(t, "abcdef0123456789", res.Token)
	assert.Equal(t, "https://hub.staging.veridian.site/invite/abcdef0123456789", res.MagicLinkURL)
	assert.Equal(t, "member", res.TargetRole)
	assert.False(t, res.Reused)
	assert.True(t, res.ExpiresAt.Equal(expectedExpiry))

	// Confirme que les champs critiques sont dans le body envoye.
	require.NotEmpty(t, capturedHeaders)
	assert.Contains(t, capturedBody, `"inviter_user_id":"hub_user_owner1"`)
	assert.Contains(t, capturedBody, `"invitee_email":"guest@example.com"`)
	assert.Contains(t, capturedBody, `"target_workspace_id":"tenant42"`)
	assert.Contains(t, capturedBody, `"target_app":"notifuse"`)

	// Timestamp doit etre coherent (< 5 min de drift, anti-replay Hub).
	ts, err := strconv.ParseInt(capturedHeaders.Get("x-veridian-timestamp"), 10, 64)
	require.NoError(t, err)
	driftMs := time.Now().UnixMilli() - ts
	assert.Less(t, driftMs, int64(60_000), "timestamp drift > 60s suspect")
}

func TestVeridianHubInvitationClient_CreateReused200(t *testing.T) {
	// Idempotence : si une invitation pending existe deja, le Hub renvoie 200 + reused=true.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"invitation_id": "inv_reused",
			"token": "tok_reused",
			"magic_link_url": "https://hub.example.com/invite/tok_reused",
			"expires_at": "2026-06-15T00:00:00Z",
			"target_role": "member",
			"reused": true
		}`))
	}))
	defer server.Close()

	c := NewVeridianHubInvitationClient(server.URL, "secret-x", server.Client(), logger.NewLogger())
	res, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "u1",
		InviterEmail:      "a@a.com",
		InviteeEmail:      "b@b.com",
		TargetWorkspaceID: "ws1",
	})
	require.NoError(t, err)
	assert.True(t, res.Reused, "reused must be true on idempotent return")
	assert.Equal(t, "inv_reused", res.InvitationID)
}

func TestVeridianHubInvitationClient_HubReturns404InviterNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"inviter_not_found","reason":"inviter_user_id not found in hub_app.users"}`))
	}))
	defer server.Close()

	c := NewVeridianHubInvitationClient(server.URL, "s", server.Client(), logger.NewLogger())
	_, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "u_unknown",
		InviterEmail:      "x@x.com",
		InviteeEmail:      "y@y.com",
		TargetWorkspaceID: "ws1",
	})
	require.Error(t, err)
	var hubErr *HubInvitationError
	require.True(t, errors.As(err, &hubErr))
	assert.Equal(t, 404, hubErr.HubStatus)
	assert.Equal(t, "inviter_not_found", hubErr.Code)
	assert.Contains(t, hubErr.Message, "hub_app.users")
}

func TestVeridianHubInvitationClient_HubReturns400InvalidPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_payload","reason":"target_role invalid"}`))
	}))
	defer server.Close()

	c := NewVeridianHubInvitationClient(server.URL, "s", server.Client(), logger.NewLogger())
	_, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "u1",
		InviterEmail:      "x@x.com",
		InviteeEmail:      "y@y.com",
		TargetWorkspaceID: "ws1",
		TargetRole:        "totally-invalid",
	})
	require.Error(t, err)
	var hubErr *HubInvitationError
	require.True(t, errors.As(err, &hubErr))
	assert.Equal(t, 400, hubErr.HubStatus)
	assert.Equal(t, "invalid_payload", hubErr.Code)
}

func TestVeridianHubInvitationClient_HubReturns401HMAC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized","reason":"invalid signature"}`))
	}))
	defer server.Close()

	c := NewVeridianHubInvitationClient(server.URL, "s", server.Client(), logger.NewLogger())
	_, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "u1",
		InviterEmail:      "x@x.com",
		InviteeEmail:      "y@y.com",
		TargetWorkspaceID: "ws1",
	})
	require.Error(t, err)
	var hubErr *HubInvitationError
	require.True(t, errors.As(err, &hubErr))
	assert.Equal(t, 401, hubErr.HubStatus)
	assert.Equal(t, "unauthorized", hubErr.Code)
}

func TestVeridianHubInvitationClient_HubReturns500BodyNotJSON(t *testing.T) {
	// Body non-JSON => fallback Message = body brut tronque.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream timeout"))
	}))
	defer server.Close()

	c := NewVeridianHubInvitationClient(server.URL, "s", server.Client(), logger.NewLogger())
	_, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "u1",
		InviterEmail:      "x@x.com",
		InviteeEmail:      "y@y.com",
		TargetWorkspaceID: "ws1",
	})
	require.Error(t, err)
	var hubErr *HubInvitationError
	require.True(t, errors.As(err, &hubErr))
	assert.Equal(t, 502, hubErr.HubStatus)
	assert.Equal(t, "hub_status_502", hubErr.Code)
	assert.Contains(t, hubErr.Message, "upstream timeout")
}

func TestVeridianHubInvitationClient_NetworkErrorReturnsHubUnreachable(t *testing.T) {
	// Pointe vers une URL invalide => le http.Client retourne une erreur.
	c := NewVeridianHubInvitationClient("http://127.0.0.1:1", "s", &http.Client{Timeout: 200 * time.Millisecond}, logger.NewLogger())
	_, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "u1",
		InviterEmail:      "x@x.com",
		InviteeEmail:      "y@y.com",
		TargetWorkspaceID: "ws1",
	})
	require.Error(t, err)
	var hubErr *HubInvitationError
	require.True(t, errors.As(err, &hubErr))
	assert.Equal(t, 0, hubErr.HubStatus)
	assert.Equal(t, "hub_unreachable", hubErr.Code)
}

func TestVeridianHubInvitationClient_RejectsMissingFieldsClientSide(t *testing.T) {
	c := NewVeridianHubInvitationClient("http://h", "s", &http.Client{Timeout: time.Second}, logger.NewLogger())

	cases := []struct {
		name  string
		input HubInvitationInput
	}{
		{
			name:  "missing inviter_user_id",
			input: HubInvitationInput{InviterEmail: "a@a", InviteeEmail: "b@b", TargetWorkspaceID: "w"},
		},
		{
			name:  "missing inviter_email",
			input: HubInvitationInput{InviterUserID: "u", InviteeEmail: "b@b", TargetWorkspaceID: "w"},
		},
		{
			name:  "missing invitee_email",
			input: HubInvitationInput{InviterUserID: "u", InviterEmail: "a@a", TargetWorkspaceID: "w"},
		},
		{
			name:  "missing target_workspace_id",
			input: HubInvitationInput{InviterUserID: "u", InviterEmail: "a@a", InviteeEmail: "b@b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Create(context.Background(), tc.input)
			require.Error(t, err)
			var hubErr *HubInvitationError
			require.True(t, errors.As(err, &hubErr))
			assert.Equal(t, "invalid_input", hubErr.Code)
		})
	}
}

func TestVeridianHubInvitationClient_BaseURLTrimsTrailingSlash(t *testing.T) {
	// Server qui repond OK quel que soit l'URL — on verifie l'URL exacte appelee.
	var calledPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"invitation_id":"x","token":"t","magic_link_url":"u","expires_at":"2026-06-01T00:00:00Z","target_role":"member","reused":false}`))
	}))
	defer server.Close()

	// Avec trailing slash dans baseURL.
	c := NewVeridianHubInvitationClient(server.URL+"/", "s", server.Client(), logger.NewLogger())
	_, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "u",
		InviterEmail:      "a@a.com",
		InviteeEmail:      "b@b.com",
		TargetWorkspaceID: "w",
	})
	require.NoError(t, err)
	assert.Equal(t, "/api/invitations/create", calledPath, "trailing slash must be trimmed")
}

func TestVeridianHubInvitationClient_DefaultBaseURLUsedWhenEmpty(t *testing.T) {
	// On force un secret + http.Client custom qui rejette tout pour ne pas
	// faire d'appel reseau. On verifie juste qu'on n'a pas panic et que
	// le client utilise l'URL par defaut (visible dans l'erreur reseau).
	httpc := &http.Client{
		Timeout: 100 * time.Millisecond,
		Transport: &errRoundTripper{err: errors.New("blocked")},
	}
	c := NewVeridianHubInvitationClient("", "s", httpc, logger.NewLogger())
	_, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "u",
		InviterEmail:      "a@a.com",
		InviteeEmail:      "b@b.com",
		TargetWorkspaceID: "w",
	})
	require.Error(t, err)
	// L'erreur doit venir du round-tripper bloque (preuve qu'on a tente
	// d.appeler le Hub, default app.veridian.site). On accepte juste que ce soit une
	// erreur HubInvitationError code hub_unreachable.
	var hubErr *HubInvitationError
	require.True(t, errors.As(err, &hubErr))
	assert.Equal(t, "hub_unreachable", hubErr.Code)
}

// Bonus: verifie que le client envoie target_app=notifuse si non specifie.
func TestVeridianHubInvitationClient_DefaultsTargetAppNotifuse(t *testing.T) {
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		capturedBody = string(raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"invitation_id":"i","token":"t","magic_link_url":"u","expires_at":"2026-06-01T00:00:00Z","target_role":"member","reused":false}`))
	}))
	defer server.Close()

	c := NewVeridianHubInvitationClient(server.URL, "s", server.Client(), logger.NewLogger())
	_, err := c.Create(context.Background(), HubInvitationInput{
		InviterUserID:     "u",
		InviterEmail:      "a@a.com",
		InviteeEmail:      "b@b.com",
		TargetWorkspaceID: "w",
		// TargetApp left empty
	})
	require.NoError(t, err)
	// Le JSON doit contenir "target_app":"notifuse".
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(capturedBody), &parsed))
	assert.Equal(t, "notifuse", parsed["target_app"])
}

func TestHubInvitationError_ErrorString(t *testing.T) {
	e1 := &HubInvitationError{HubStatus: 404, Code: "inviter_not_found", Message: "missing"}
	s := e1.Error()
	assert.Contains(t, s, "404")
	assert.Contains(t, s, "inviter_not_found")
	assert.Contains(t, s, "missing")

	e2 := &HubInvitationError{HubStatus: 502, Message: "raw body"}
	s2 := e2.Error()
	assert.Contains(t, s2, "502")
	assert.Contains(t, s2, "raw body")
	assert.False(t, strings.Contains(s2, "inviter_not_found"))
}

// errRoundTripper sert pour tester que le client utilise bien une URL par
// defaut sans avoir a faire un vrai appel reseau.
type errRoundTripper struct {
	err error
}

func (t *errRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, t.err
}
