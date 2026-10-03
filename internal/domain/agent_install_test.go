package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// === Veridian patch — mission "API & agents" (2026-10-03) ===
//
// Ce fichier porte des types et trois erreurs sentinelles, pas de logique —
// mais le garde-fou CI §1 (mapping source↔test) exige un fichier colocalisé
// pour toute source sous internal/domain/. On vérifie ici le contrat exact
// que le reste du système (service, handler) suppose : les trois erreurs
// sont bien DISTINCTES par errors.Is, et les structs portent les champs que
// le repository/service lisent par leur nom.

func TestAgentInstallTokenSentinelErrors_AreDistinct(t *testing.T) {
	assert.False(t, errors.Is(ErrAgentInstallTokenNotFound, ErrAgentInstallTokenUsed))
	assert.False(t, errors.Is(ErrAgentInstallTokenNotFound, ErrAgentInstallTokenExpired))
	assert.False(t, errors.Is(ErrAgentInstallTokenUsed, ErrAgentInstallTokenExpired))

	assert.Equal(t, "install token not found", ErrAgentInstallTokenNotFound.Error())
	assert.Equal(t, "install token already used", ErrAgentInstallTokenUsed.Error())
	assert.Equal(t, "install token expired", ErrAgentInstallTokenExpired.Error())
}

func TestAgentInstallToken_Fields(t *testing.T) {
	now := time.Now()
	meta := &AgentInstallToken{
		WorkspaceID:  "ws-1",
		APIKeyUserID: "api-user-1",
		CreatedBy:    "owner-1",
		CreatedAt:    now,
		ExpiresAt:    now.Add(10 * time.Minute),
	}
	assert.Equal(t, "ws-1", meta.WorkspaceID)
	assert.True(t, meta.ExpiresAt.After(meta.CreatedAt))
}

func TestAgentInstallCredentials_Fields(t *testing.T) {
	creds := &AgentInstallCredentials{
		APIKey:      "jwt-token",
		APIURL:      "https://notifuse.app.veridian.site",
		WorkspaceID: "ws-1",
	}
	assert.Equal(t, "jwt-token", creds.APIKey)
	assert.Equal(t, "https://notifuse.app.veridian.site", creds.APIURL)
	assert.Equal(t, "ws-1", creds.WorkspaceID)
}

func TestAgentInstallTokenRecord_Fields(t *testing.T) {
	now := time.Now()
	rec := &AgentInstallTokenRecord{
		TokenHash:       "hash-1",
		WorkspaceID:     "ws-1",
		APIKeyUserID:    "api-user-1",
		EncryptedAPIKey: "enc-1",
		CreatedBy:       "owner-1",
		CreatedAt:       now,
		ExpiresAt:       now.Add(10 * time.Minute),
	}
	assert.Nil(t, rec.UsedAt, "a freshly built record must not be pre-marked as used")
	assert.Equal(t, "hash-1", rec.TokenHash)

	usedAt := now.Add(time.Minute)
	rec.UsedAt = &usedAt
	assert.NotNil(t, rec.UsedAt)
}
