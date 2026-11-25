package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Notifuse/notifuse/internal/service"
)

func TestRequireHubUserID(t *testing.T) {
	for v, ok := range map[string]bool{
		"6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01": true, "": false, "user_hub_abc": false, "old@example.test": false,
		"{6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01}": false, "urn:uuid:6b3c1f4e-2a51-4c1e-9a77-0d9f2c5b1a01": false,
	} {
		w := httptest.NewRecorder()
		assert.Equal(t, ok, requireHubUserID(w, "hub_user_id", v), v)
		if !ok {
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), ErrCodeInvalidPayload)
		}
	}
}

func TestHubPersonAddress(t *testing.T) {
	assert.Equal(t, "a@x", hubPerson{Email: "a@x", UserEmail: "b@x"}.address())
	assert.Equal(t, "b@x", hubPerson{UserEmail: "b@x"}.address())
	assert.Equal(t, "c@x", hubPerson{HubUserEmail: "c@x"}.address())
	assert.Empty(t, hubPerson{}.address())
}

func TestWriteHubPersonError(t *testing.T) {
	for err, want := range map[error]struct {
		status int
		code   string
	}{
		service.ErrUserNotInApp:      {404, "user_not_in_app"},
		service.ErrHubUserIDConflict: {409, "hub_user_conflict"},
		service.ErrOwnerNotMember:    {409, "not_a_member"},
	} {
		w := httptest.NewRecorder()
		assert.True(t, writeHubPersonError(w, err, "ws-1", "id"))
		assert.Equal(t, want.status, w.Code)
		assert.Contains(t, w.Body.String(), want.code)
	}
	assert.False(t, writeHubPersonError(httptest.NewRecorder(), errors.New("x"), "ws-1", "id"))
}
