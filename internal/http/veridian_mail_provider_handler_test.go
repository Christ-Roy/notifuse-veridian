package http

// === Veridian patch — Mail provider choice per workspace (V48, 2026-05-25) ===
// Tests unitaires des handlers handleSetMailProviderChoice / handleGetMailProviderChoice.
// Couvre : 200 OK POST/GET, 400 body invalide, 400 choice manquant,
// 400 choice hors enum, 404 workspace inconnu, 503 service nil,
// 500 erreur service, path param vide.
//
// Note HMAC : les tests de middleware HMAC sont dans veridian_hmac_test.go
// — le handler est teste sans middleware (isole), exactement comme les autres
// handlers Hub→Notifuse du repo.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/repository"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMailProviderService implements service.VeridianMailProviderService
// pour les tests handler. Pas de mockgen dedie (interface tres petite, fake
// in-process plus lisible).
type fakeMailProviderService struct {
	getResult domain.MailProviderChoice
	getErr    error
	setResult *domain.MailProviderChoiceResponse
	setErr    error

	getCalls []string
	setCalls []struct {
		workspaceID string
		choice      domain.MailProviderChoice
	}
}

func (f *fakeMailProviderService) GetMailProviderChoice(ctx context.Context, workspaceID string) (domain.MailProviderChoice, error) {
	f.getCalls = append(f.getCalls, workspaceID)
	return f.getResult, f.getErr
}

func (f *fakeMailProviderService) SetMailProviderChoice(ctx context.Context, workspaceID string, choice domain.MailProviderChoice) (*domain.MailProviderChoiceResponse, error) {
	f.setCalls = append(f.setCalls, struct {
		workspaceID string
		choice      domain.MailProviderChoice
	}{workspaceID, choice})
	return f.setResult, f.setErr
}

func newHandlerWithMailProvider(svc service.VeridianMailProviderService) *VeridianHandler {
	h := &VeridianHandler{
		logger: logger.NewLogger(),
	}
	if svc != nil {
		h.SetMailProviderService(svc)
	}
	return h
}

func postMailProvider(t *testing.T, h func(http.ResponseWriter, *http.Request), workspaceID, body string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != "" {
		bodyReader = bytes.NewReader([]byte(body))
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+workspaceID+"/mail-provider-choice", bodyReader)
	req.SetPathValue("id", workspaceID)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func getMailProvider(t *testing.T, h func(http.ResponseWriter, *http.Request), workspaceID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+workspaceID+"/mail-provider-choice", nil)
	req.SetPathValue("id", workspaceID)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// === handleSetMailProviderChoice ===

func TestHandleSetMailProviderChoice_Success_HubGmail(t *testing.T) {
	svc := &fakeMailProviderService{
		setResult: &domain.MailProviderChoiceResponse{
			WorkspaceID: "ws-1",
			Choice:      domain.MailProviderHubGmail,
			UpdatedAt:   "2026-05-25T12:00:00Z",
		},
	}
	h := newHandlerWithMailProvider(svc)
	rec := postMailProvider(t, h.handleSetMailProviderChoice, "ws-1", `{"choice":"hub_gmail"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.MailProviderChoiceResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp.WorkspaceID)
	assert.Equal(t, domain.MailProviderHubGmail, resp.Choice)
	assert.Equal(t, "2026-05-25T12:00:00Z", resp.UpdatedAt)

	require.Len(t, svc.setCalls, 1)
	assert.Equal(t, "ws-1", svc.setCalls[0].workspaceID)
	assert.Equal(t, domain.MailProviderHubGmail, svc.setCalls[0].choice)
}

func TestHandleSetMailProviderChoice_Success_SMTPGeneric(t *testing.T) {
	svc := &fakeMailProviderService{
		setResult: &domain.MailProviderChoiceResponse{
			WorkspaceID: "ws-1",
			Choice:      domain.MailProviderSMTPGeneric,
			UpdatedAt:   "2026-05-25T12:00:00Z",
		},
	}
	h := newHandlerWithMailProvider(svc)
	rec := postMailProvider(t, h.handleSetMailProviderChoice, "ws-1", `{"choice":"smtp_generic"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleSetMailProviderChoice_InvalidJSON(t *testing.T) {
	svc := &fakeMailProviderService{}
	h := newHandlerWithMailProvider(svc)
	rec := postMailProvider(t, h.handleSetMailProviderChoice, "ws-1", `not-a-json{`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, svc.setCalls, "pas d'appel service sur body malforme")
}

func TestHandleSetMailProviderChoice_MissingChoice(t *testing.T) {
	svc := &fakeMailProviderService{}
	h := newHandlerWithMailProvider(svc)
	rec := postMailProvider(t, h.handleSetMailProviderChoice, "ws-1", `{}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "invalid_payload", body["code"])
	assert.Empty(t, svc.setCalls)
}

func TestHandleSetMailProviderChoice_InvalidChoiceEnum(t *testing.T) {
	svc := &fakeMailProviderService{setErr: service.ErrInvalidMailProviderChoice}
	h := newHandlerWithMailProvider(svc)
	rec := postMailProvider(t, h.handleSetMailProviderChoice, "ws-1", `{"choice":"microsoft_via_hub"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "invalid_payload", body["code"])
	details, _ := body["details"].(map[string]interface{})
	require.NotNil(t, details)
	assert.Equal(t, "microsoft_via_hub", details["got"])
}

func TestHandleSetMailProviderChoice_WorkspaceNotFound(t *testing.T) {
	svc := &fakeMailProviderService{setErr: repository.ErrWorkspaceNotFoundForMailProvider}
	h := newHandlerWithMailProvider(svc)
	rec := postMailProvider(t, h.handleSetMailProviderChoice, "missing-ws", `{"choice":"hub_gmail"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "tenant_not_found", body["code"])
}

func TestHandleSetMailProviderChoice_ServiceError500(t *testing.T) {
	svc := &fakeMailProviderService{setErr: errors.New("db down")}
	h := newHandlerWithMailProvider(svc)
	rec := postMailProvider(t, h.handleSetMailProviderChoice, "ws-1", `{"choice":"hub_gmail"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleSetMailProviderChoice_EmptyPathParam(t *testing.T) {
	svc := &fakeMailProviderService{}
	h := newHandlerWithMailProvider(svc)
	rec := postMailProvider(t, h.handleSetMailProviderChoice, "", `{"choice":"hub_gmail"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, svc.setCalls)
}

func TestHandleSetMailProviderChoice_ServiceNotConfigured503(t *testing.T) {
	h := newHandlerWithMailProvider(nil)
	rec := postMailProvider(t, h.handleSetMailProviderChoice, "ws-1", `{"choice":"hub_gmail"}`)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// === handleGetMailProviderChoice ===

func TestHandleGetMailProviderChoice_Success(t *testing.T) {
	svc := &fakeMailProviderService{getResult: domain.MailProviderHubGmail}
	h := newHandlerWithMailProvider(svc)
	rec := getMailProvider(t, h.handleGetMailProviderChoice, "ws-1")

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.MailProviderChoiceResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "ws-1", resp.WorkspaceID)
	assert.Equal(t, domain.MailProviderHubGmail, resp.Choice)
	assert.Empty(t, resp.UpdatedAt, "GET ne renvoie pas updated_at (omitempty)")

	require.Equal(t, []string{"ws-1"}, svc.getCalls)
}

func TestHandleGetMailProviderChoice_DefaultSMTPGeneric(t *testing.T) {
	svc := &fakeMailProviderService{getResult: domain.MailProviderSMTPGeneric}
	h := newHandlerWithMailProvider(svc)
	rec := getMailProvider(t, h.handleGetMailProviderChoice, "ws-default")

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.MailProviderChoiceResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, domain.MailProviderSMTPGeneric, resp.Choice)
}

func TestHandleGetMailProviderChoice_EmptyPathParam(t *testing.T) {
	svc := &fakeMailProviderService{}
	h := newHandlerWithMailProvider(svc)
	rec := getMailProvider(t, h.handleGetMailProviderChoice, "")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Empty(t, svc.getCalls)
}

func TestHandleGetMailProviderChoice_ServiceError500(t *testing.T) {
	svc := &fakeMailProviderService{getErr: errors.New("db down")}
	h := newHandlerWithMailProvider(svc)
	rec := getMailProvider(t, h.handleGetMailProviderChoice, "ws-1")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleGetMailProviderChoice_ServiceNotConfigured503(t *testing.T) {
	h := newHandlerWithMailProvider(nil)
	rec := getMailProvider(t, h.handleGetMailProviderChoice, "ws-1")

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
