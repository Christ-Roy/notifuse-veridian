package domain

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUser(t *testing.T) {
	user := User{
		ID:        "user123",
		Email:     "test@example.com",
		Name:      "Test User",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Basic assertion that the struct fields are set correctly
	assert.Equal(t, "user123", user.ID)
	assert.Equal(t, "test@example.com", user.Email)
	assert.Equal(t, "Test User", user.Name)
}

func TestWorkspaceUserKey(t *testing.T) {
	// Test with a regular workspace ID
	workspaceID := "workspace123"
	key := WorkspaceUserKey(workspaceID)
	assert.Equal(t, contextKey("workspace_user_workspace123"), key)

	// Test with empty string
	emptyKey := WorkspaceUserKey("")
	assert.Equal(t, contextKey("workspace_user_"), emptyKey)

	// Test with special characters
	specialKey := WorkspaceUserKey("workspace-123_456@example")
	assert.Equal(t, contextKey("workspace_user_workspace-123_456@example"), specialKey)
}

func TestSession(t *testing.T) {
	now := time.Now()
	expiry := now.Add(time.Hour * 24)
	magicCode := "ABCDEF"
	magicCodeExpires := now.Add(time.Minute * 15)

	session := Session{
		ID:               "session123",
		UserID:           "user123",
		ExpiresAt:        expiry,
		CreatedAt:        now,
		MagicCode:        &magicCode,
		MagicCodeExpires: &magicCodeExpires,
	}

	// Basic assertion that the struct fields are set correctly
	assert.Equal(t, "session123", session.ID)
	assert.Equal(t, "user123", session.UserID)
	assert.Equal(t, expiry, session.ExpiresAt)
	assert.Equal(t, now, session.CreatedAt)
	assert.NotNil(t, session.MagicCode)
	assert.Equal(t, "ABCDEF", *session.MagicCode)
	assert.NotNil(t, session.MagicCodeExpires)
	assert.Equal(t, magicCodeExpires, *session.MagicCodeExpires)
}

func TestSession_NullMagicCode(t *testing.T) {
	now := time.Now()
	expiry := now.Add(time.Hour * 24)

	// Test session without magic code (e.g., after successful verification)
	session := Session{
		ID:               "session123",
		UserID:           "user123",
		ExpiresAt:        expiry,
		CreatedAt:        now,
		MagicCode:        nil,
		MagicCodeExpires: nil,
	}

	// Basic assertion that the struct fields are set correctly
	assert.Equal(t, "session123", session.ID)
	assert.Equal(t, "user123", session.UserID)
	assert.Equal(t, expiry, session.ExpiresAt)
	assert.Equal(t, now, session.CreatedAt)
	assert.Nil(t, session.MagicCode, "Magic code should be nil after verification")
	assert.Nil(t, session.MagicCodeExpires, "Magic code expiration should be nil after verification")
}

func TestErrUserNotFound_Error(t *testing.T) {
	err := &ErrUserNotFound{Message: "test error"}
	assert.Equal(t, "test error", err.Error())
}

func TestErrUserExists_Error(t *testing.T) {
	err := &ErrUserExists{Message: "user already exists"}
	assert.Equal(t, "user already exists", err.Error())
}

func TestErrSessionNotFound_Error(t *testing.T) {
	err := &ErrSessionNotFound{Message: "test error"}
	assert.Equal(t, "test error", err.Error())
}

// === Veridian patch === Smoke test sur le champ VeridianManaged ajoute pour
// le hardening anti-sabotage Team Settings (cf migration V32). Verifie le
// default false + flag explicite quand le user est gere par le Hub.
func TestUser_VeridianManagedDefault(t *testing.T) {
	u := User{ID: "u-1", Email: "human@x.test", Type: UserTypeUser}
	assert.False(t, u.VeridianManaged, "default doit etre false")

	managed := User{
		ID:              "u-2",
		Email:           "veridian-api-ws@notifuse.test",
		Type:            UserTypeAPIKey,
		VeridianManaged: true,
	}
	assert.True(t, managed.VeridianManaged)
}

// === Veridian patch === Smoke test sur le champ HubUserID (V46,
// CONTRAT-HUB §3.7). Verifie default nil + binding explicite quand le user
// a ete backfille via /provision ou /attach-member.
func TestUser_HubUserIDDefault(t *testing.T) {
	u := User{ID: "u-1", Email: "human@x.test", Type: UserTypeUser}
	assert.Nil(t, u.HubUserID, "default doit etre nil (pre-V46 / pas encore backfille)")

	hubID := "hub-user-uuid-abc"
	bound := User{
		ID:        "u-2",
		Email:     "alice@example.com",
		Type:      UserTypeUser,
		HubUserID: &hubID,
	}
	require.NotNil(t, bound.HubUserID)
	assert.Equal(t, "hub-user-uuid-abc", *bound.HubUserID)
}

// === Veridian patch === ErrHubUserIDMismatch est une sentinel `errors.Is`
// qui signale qu'on tente d'ecraser le hub_user_id existant d'un user local
// avec une valeur differente. Le repository la renvoie via BackfillHubUserID,
// le service doit l'attraper en non-bloquant et logger warn (cf CONTRAT-HUB
// §3.7 — l'email reste la cle canonique, le binding hub_user_id est
// informationnel).
func TestErrHubUserIDMismatch_Sentinel(t *testing.T) {
	assert.NotNil(t, ErrHubUserIDMismatch)
	assert.Contains(t, ErrHubUserIDMismatch.Error(), "hub_user_id")

	wrapped := fmt.Errorf("backfill hub_user_id: %w", ErrHubUserIDMismatch)
	assert.True(t, errors.Is(wrapped, ErrHubUserIDMismatch), "doit etre attrapable via errors.Is meme apres wrap")
}
