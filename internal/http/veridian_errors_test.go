package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSONErrorCode_basic(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteJSONErrorCode(rec, ErrCodeInvalidPayload, "tenant_id required", http.StatusBadRequest, nil)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, "tenant_id required", body.Error, "champ 'error' doit etre le message humain (retro-compat Hub)")
	assert.Equal(t, "invalid_payload", body.Code, "champ 'code' doit etre le code machine")
	assert.Equal(t, "tenant_id required", body.Message, "champ 'message' duplique 'error' pour cibler la v2 strict")
	assert.Nil(t, body.Details)
}

func TestWriteJSONErrorCode_withDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	details := map[string]interface{}{
		"field":   "tenant_id",
		"present": false,
	}

	WriteJSONErrorCode(rec, ErrCodeInvalidPayload, "tenant_id required", http.StatusBadRequest, details)

	var body VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	require.NotNil(t, body.Details)
	assert.Equal(t, "tenant_id", body.Details["field"])
	assert.Equal(t, false, body.Details["present"])
}

func TestWriteJSONErrorCode_omitsDetailsWhenEmpty(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteJSONErrorCode(rec, ErrCodeInternalError, "boom", http.StatusInternalServerError, nil)

	// On verifie que le JSON serialise n'a pas la cle `details` quand on passe nil.
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	_, present := raw["details"]
	assert.False(t, present, "champ 'details' doit etre omis si nil (omitempty)")
}

func TestWriteJSONErrorCode_codesEnum(t *testing.T) {
	// Sanity check : les codes obligatoires du contrat sec. 5.10 sont tous declares.
	required := []string{
		ErrCodeInvalidPayload,
		ErrCodeUnauthorized,
		ErrCodeForbidden,
		ErrCodeTenantNotFound,
		ErrCodeTenantSoftDeleted,
		ErrCodeOwnerMismatch,
		ErrCodeQuotaExceeded,
		ErrCodePlanNotFound,
		ErrCodePlanLocked,
		ErrCodeApiKeyMultiWorkspace,
		ErrCodeIdempotencyKeyMismatch,
		ErrCodePurgeNotEligible,
		ErrCodeInternalError,
	}
	for _, code := range required {
		assert.NotEmpty(t, code, "code obligatoire non declare")
	}
}

func TestMissingFields(t *testing.T) {
	t.Run("empty when no condition true", func(t *testing.T) {
		got := missingFields(false, "a", false, "b")
		assert.Empty(t, got)
	})
	t.Run("single field missing", func(t *testing.T) {
		got := missingFields(true, "tenant_id", false, "owner_email")
		assert.Equal(t, []string{"tenant_id"}, got)
	})
	t.Run("multiple fields missing", func(t *testing.T) {
		got := missingFields(true, "tenant_id", true, "owner_email")
		assert.Equal(t, []string{"tenant_id", "owner_email"}, got)
	})
	t.Run("odd argument count ignored gracefully", func(t *testing.T) {
		got := missingFields(true, "a", true)
		assert.Equal(t, []string{"a"}, got)
	})
	t.Run("wrong types ignored", func(t *testing.T) {
		got := missingFields("not_bool", "a", true, 42)
		assert.Empty(t, got)
	})
}

func TestWriteJSONError_backwardCompat(t *testing.T) {
	// Le helper upstream WriteJSONError doit continuer a fonctionner sans
	// champ `code` pour preserver la retro-compat des endpoints upstream.
	rec := httptest.NewRecorder()

	WriteJSONError(rec, "legacy message", http.StatusBadRequest)

	var raw map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	assert.Equal(t, "legacy message", raw["error"])
	_, hasCode := raw["code"]
	assert.False(t, hasCode, "WriteJSONError upstream ne doit pas emettre 'code'")
}

// TestVeridianErrors_AttachMemberCodes valide que les codes erreur attach-member
// sont bien declares (regression guard — Constitution §1 mapping errors.go).
func TestVeridianErrors_AttachMemberCodes(t *testing.T) {
	assert.Equal(t, "tenant_suspended", ErrCodeTenantSuspended, "ErrCodeTenantSuspended doit valoir tenant_suspended")
	assert.Equal(t, "invalid_role", ErrCodeInvalidRole, "ErrCodeInvalidRole doit valoir invalid_role")
	assert.Equal(t, "user_role_conflict", ErrCodeUserRoleConflict, "ErrCodeUserRoleConflict doit valoir user_role_conflict")
}

// TestVeridianErrors_HubSyncDeadCode — V39 résilience billing.
// hub_sync_dead est un nouveau code machine lisible distinct des autres
// codes paywall. String figée pour les consommateurs Hub.
func TestVeridianErrors_HubSyncDeadCode(t *testing.T) {
	assert.Equal(t, "hub_sync_dead", ErrCodeHubSyncDead,
		"ErrCodeHubSyncDead doit valoir hub_sync_dead — string figée pour les consommateurs Hub")
}

func TestWriteJSONErrorCode_HubSyncDead_503(t *testing.T) {
	rec := httptest.NewRecorder()
	now := "2026-05-21T10:00:00Z"
	WriteJSONErrorCode(rec, ErrCodeHubSyncDead,
		"Service degraded — Veridian Hub unreachable since > 72h. Writes paused for safety.",
		http.StatusServiceUnavailable,
		map[string]interface{}{
			"last_hub_sync_at": now,
			"retry_after_s":    3600,
		})

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body VeridianErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, ErrCodeHubSyncDead, body.Code)
	require.NotNil(t, body.Details)
	assert.Equal(t, float64(3600), body.Details["retry_after_s"])
}
