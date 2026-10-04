package domain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVeridianIdempotencyEntry_Fields(t *testing.T) {
	now := time.Now().UTC()
	expires := now.Add(24 * time.Hour)
	entry := &VeridianIdempotencyEntry{
		Key:            "550e8400-e29b-41d4-a716-446655440000",
		Endpoint:       "/api/tenants/provision",
		TenantID:       "ws-1",
		RequestHash:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		ResponseStatus: 200,
		ResponseBody:   []byte(`{"workspace_id":"ws-1","created":true}`),
		CreatedAt:      now,
		ExpiresAt:      expires,
	}

	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", entry.Key)
	assert.Equal(t, "/api/tenants/provision", entry.Endpoint)
	assert.Equal(t, "ws-1", entry.TenantID)
	assert.Equal(t, 200, entry.ResponseStatus)
	assert.NotEmpty(t, entry.ResponseBody)
	assert.True(t, entry.ExpiresAt.After(entry.CreatedAt))
}

func TestVeridianIdempotencyEntry_OptionalTenantID(t *testing.T) {
	// Certains endpoints (ex: wipe-test-tenants) n'ont pas de tenant_id
	// dans le body — le champ doit pouvoir etre vide sans casser le repo.
	entry := &VeridianIdempotencyEntry{
		Key:            "abc",
		Endpoint:       "/api/veridian/admin/wipe-test-tenants",
		RequestHash:    "deadbeef",
		ResponseStatus: 200,
		ResponseBody:   []byte(`{}`),
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	assert.Empty(t, entry.TenantID, "TenantID optional (NULL en DB)")
}

// Mission 2026-10-04 (audit backend) : Get gagne un second paramètre
// tenantID (scope le lookup, cf. commentaire sur l'interface -- avant ce correctif,
// deux appelants avec la meme cle se rejouaient mutuellement leurs
// reponses, cross-tenant). Fige la FORME de l'interface : tout type qui
// pretend l'implementer doit porter ce second paramètre.
type fakeIdempotencyRepo struct {
	lastTenantID string
}

func (f *fakeIdempotencyRepo) Get(_ context.Context, _ string, tenantID string) (*VeridianIdempotencyEntry, error) {
	f.lastTenantID = tenantID
	return nil, nil
}
func (f *fakeIdempotencyRepo) Save(_ context.Context, _ *VeridianIdempotencyEntry) error { return nil }
func (f *fakeIdempotencyRepo) DeleteExpired(_ context.Context) (int64, error)            { return 0, nil }

func TestVeridianIdempotencyRepository_InterfaceShape(t *testing.T) {
	var repo VeridianIdempotencyRepository = &fakeIdempotencyRepo{}
	_, _ = repo.Get(context.Background(), "k", "tenant-x")
	assert.Equal(t, "tenant-x", repo.(*fakeIdempotencyRepo).lastTenantID)
}
