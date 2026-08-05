package domain

import (
	"context"
	"time"
)

const (
	VeridianDailyQuotaKindProviderClass = "provider_class"
	VeridianDailyQuotaKindWarmup        = "warmup"
	VeridianDailyQuotaKindProfile       = "profile"
)

// VeridianDailyQuotaKey identifies one durable daily counter. WorkspaceID is
// persisted even though every tenant currently has its own database: this
// keeps the key safe if tenant storage is consolidated later.
type VeridianDailyQuotaKey struct {
	WorkspaceID   string
	Day           time.Time
	Kind          string
	SenderDomain  string
	ProfileID     string
	ProviderClass string
}

type VeridianDailyQuotaReservation struct {
	MessageID string
	Key       VeridianDailyQuotaKey
	Cap       int
}

type VeridianDailyQuotaReservationResult struct {
	Reserved        bool
	AlreadyReserved bool
	Used            int
}

type VeridianUnclassifiedSuccessfulMessage struct {
	ID           string
	ContactEmail string
}

// VeridianDailyQuotaRepository is deliberately separate from the upstream
// MessageHistoryRepository contract. Old self-hosted implementations remain
// source-compatible; configured caps fail closed when this capability is not
// wired.
type VeridianDailyQuotaRepository interface {
	ReserveDailyQuota(ctx context.Context, workspaceID string, reservation VeridianDailyQuotaReservation) (VeridianDailyQuotaReservationResult, error)
	ReleaseDailyQuota(ctx context.Context, workspaceID, messageID, quotaKind string) error
	ListUnclassifiedSuccessfulMessagesSince(ctx context.Context, workspaceID string, since time.Time) ([]VeridianUnclassifiedSuccessfulMessage, error)
	SetMessageProviderClassIfEmpty(ctx context.Context, workspaceID, messageID, providerClass string) error
}
