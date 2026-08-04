package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

const veridianGmailSendEndpoint = "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"

type veridianGmailHTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

func veridianUsesGmailAPI(settings *domain.SMTPSettings) bool {
	return settings != nil && settings.AuthType == "oauth2" && settings.OAuth2Provider == "google"
}

func veridianSendViaGmailAPI(
	ctx context.Context,
	settings *domain.SMTPSettings,
	mimeMessage []byte,
	tokens OAuth2TokenProvider,
	doer veridianGmailHTTPDoer,
) error {
	if !veridianUsesGmailAPI(settings) {
		return fmt.Errorf("Gmail API transport requires Google OAuth2 settings")
	}
	if tokens == nil {
		return fmt.Errorf("Gmail API transport requires an OAuth2 token provider")
	}
	if len(mimeMessage) == 0 {
		return fmt.Errorf("Gmail API transport requires a MIME message")
	}
	if doer == nil {
		doer = &http.Client{Timeout: 30 * time.Second}
	}

	accessToken, err := tokens.GetAccessToken(settings)
	if err != nil {
		return fmt.Errorf("failed to get Google OAuth2 token: %w", err)
	}

	status, responseBody, err := veridianPostGmailMessage(ctx, doer, accessToken, mimeMessage)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		tokens.InvalidateCacheForSettings(settings)
		accessToken, err = tokens.GetAccessToken(settings)
		if err != nil {
			return fmt.Errorf("failed to refresh Google OAuth2 token: %w", err)
		}
		status, responseBody, err = veridianPostGmailMessage(ctx, doer, accessToken, mimeMessage)
		if err != nil {
			return err
		}
	}

	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("Gmail API send failed with HTTP %d: %s", status, veridianGmailErrorMessage(responseBody))
	}
	return nil
}

func veridianPostGmailMessage(
	ctx context.Context,
	doer veridianGmailHTTPDoer,
	accessToken string,
	mimeMessage []byte,
) (int, []byte, error) {
	payload, err := json.Marshal(map[string]string{
		"raw": base64.RawURLEncoding.EncodeToString(mimeMessage),
	})
	if err != nil {
		return 0, nil, fmt.Errorf("failed to encode Gmail API payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, veridianGmailSendEndpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create Gmail API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := doer.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("Gmail API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read Gmail API response: %w", err)
	}
	return resp.StatusCode, body, nil
}

func veridianGmailErrorMessage(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if payload.Error.Message != "" {
			return payload.Error.Message
		}
		if payload.Error.Status != "" {
			return payload.Error.Status
		}
	}
	if len(body) == 0 {
		return "empty response"
	}
	return "Google rejected the request"
}
