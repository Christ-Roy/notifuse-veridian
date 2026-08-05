package queue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/emailerror"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

type quotaTestOutcome struct {
	result domain.VeridianDailyQuotaReservationResult
	err    error
}

type quotaTestRepository struct {
	domain.MessageHistoryRepository
	mu       sync.Mutex
	outcomes map[string]quotaTestOutcome
	reserved []string
	released []string
}

func (r *quotaTestRepository) ReserveDailyQuota(_ context.Context, _ string, reservation domain.VeridianDailyQuotaReservation) (domain.VeridianDailyQuotaReservationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reserved = append(r.reserved, reservation.Key.Kind)
	outcome := r.outcomes[reservation.Key.Kind]
	return outcome.result, outcome.err
}
func (r *quotaTestRepository) ReleaseDailyQuota(_ context.Context, _, _, kind string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.released = append(r.released, kind)
	return nil
}
func (r *quotaTestRepository) ListUnclassifiedSuccessfulMessagesSince(context.Context, string, time.Time) ([]domain.VeridianUnclassifiedSuccessfulMessage, error) {
	return nil, nil
}
func (r *quotaTestRepository) SetMessageProviderClassIfEmpty(context.Context, string, string, string) error {
	return nil
}

func TestVeridianReserveDailyQuota_AppliesClassAndWarmup(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	repo := &quotaTestRepository{outcomes: map[string]quotaTestOutcome{
		domain.VeridianDailyQuotaKindProviderClass: {result: domain.VeridianDailyQuotaReservationResult{Reserved: true, Used: 1}},
		domain.VeridianDailyQuotaKindWarmup:        {result: domain.VeridianDailyQuotaReservationResult{Reserved: true, Used: 1}},
	}}
	env.worker.messageHistoryRepo = repo
	workspace := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 1}, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{5}, 1)
	entry := veridianTestEntryFrom("double", "lead@corp.test", "bot@send.test", domain.EmailQueuePayload{VeridianProviderClass: "microsoft"})

	leases, delay, blocked := env.worker.veridianReserveDailyQuota(workspace, provider, entry)
	require.False(t, blocked)
	require.Zero(t, delay)
	require.Len(t, leases, 2)
	require.Equal(t, []string{domain.VeridianDailyQuotaKindProviderClass, domain.VeridianDailyQuotaKindWarmup}, repo.reserved)
}

func TestVeridianReserveDailyQuota_RollsBackFirstLeaseWhenSecondBlocks(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	repo := &quotaTestRepository{outcomes: map[string]quotaTestOutcome{
		domain.VeridianDailyQuotaKindProviderClass: {result: domain.VeridianDailyQuotaReservationResult{Reserved: true, Used: 1}},
		domain.VeridianDailyQuotaKindWarmup:        {result: domain.VeridianDailyQuotaReservationResult{Reserved: false, Used: 5}},
	}}
	env.worker.messageHistoryRepo = repo
	workspace := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 1}, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{5}, 1)
	entry := veridianTestEntryFrom("rollback", "lead@corp.test", "bot@send.test", domain.EmailQueuePayload{VeridianProviderClass: "microsoft"})

	leases, _, blocked := env.worker.veridianReserveDailyQuota(workspace, provider, entry)
	require.True(t, blocked)
	require.Nil(t, leases)
	require.Equal(t, []string{domain.VeridianDailyQuotaKindProviderClass}, repo.released)
}

func TestVeridianReserveDailyQuota_DoesNotReleaseAlreadyReservedLease(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	repo := &quotaTestRepository{outcomes: map[string]quotaTestOutcome{
		domain.VeridianDailyQuotaKindProviderClass: {result: domain.VeridianDailyQuotaReservationResult{Reserved: true, AlreadyReserved: true, Used: 1}},
		domain.VeridianDailyQuotaKindWarmup:        {err: errors.New("db down")},
	}}
	env.worker.messageHistoryRepo = repo
	workspace := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 1}, 0)
	provider := veridianWarmupTestProvider(time.Now().UTC(), []int{5}, 1)
	entry := veridianTestEntryFrom("owner", "lead@corp.test", "bot@send.test", domain.EmailQueuePayload{VeridianProviderClass: "microsoft"})

	_, _, blocked := env.worker.veridianReserveDailyQuota(workspace, provider, entry)
	require.True(t, blocked)
	require.Empty(t, repo.released)
}

func TestVeridianReserveDailyQuota_MissingSenderDomainFailsClosed(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	repo := &quotaTestRepository{outcomes: map[string]quotaTestOutcome{}}
	env.worker.messageHistoryRepo = repo
	workspace := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 1}, 0)
	entry := veridianTestEntry("missing-sender", "lead@corp.test", domain.EmailQueuePayload{VeridianProviderClass: "microsoft"})

	_, delay, blocked := env.worker.veridianReserveDailyQuota(workspace, nil, entry)
	require.True(t, blocked)
	require.Equal(t, veridianDailyCapRecheckInterval, delay)
	require.Empty(t, repo.reserved)
}

func TestEmailQueueWorker_AtomicQuotaBlocksSMTPAndRefundsClaim(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	repo := &quotaTestRepository{
		MessageHistoryRepository: env.mockMessageHistoryRepo,
		outcomes: map[string]quotaTestOutcome{
			domain.VeridianDailyQuotaKindProviderClass: {result: domain.VeridianDailyQuotaReservationResult{Reserved: false, Used: 1}},
		},
	}
	env.worker.messageHistoryRepo = repo
	workspace := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 1}, 0)
	entry := veridianTestEntryFrom("atomic-block", "lead@outlook.com", "bot@send.test", domain.EmailQueuePayload{VeridianProviderClass: "microsoft"})

	// The legacy COUNT optimization sees spare capacity; only the authoritative
	// atomic reservation observes the concurrent winner and blocks this worker.
	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomainsAndSenderDomain(gomock.Any(), "ws-1", gomock.Any(), false, "send.test", gomock.Any()).
		Return(0, nil)
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", entry.ID).Return(nil)
	env.mockQueueRepo.EXPECT().SetNextRetryAndRefundAttempt(gomock.Any(), "ws-1", entry.ID, gomock.Any()).Return(nil)
	// No SendEmail expectation: any SMTP call fails the test.
	env.worker.processEntry(workspace, entry)

	require.Equal(t, []string{domain.VeridianDailyQuotaKindProviderClass}, repo.reserved)
}

func TestEmailQueueWorker_OpenCircuitBreakerKeepsUnclaimedRetryPath(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	workspace := veridianTestWorkspace(nil, 6000)
	entry := veridianTestEntry("open-circuit", "lead@example.test", domain.EmailQueuePayload{})
	providerErr := &emailerror.ClassifiedError{Type: emailerror.ErrorTypeProvider}
	for i := 0; i < env.worker.circuitBreaker.GetConfig().Threshold; i++ {
		env.worker.circuitBreaker.RecordFailure(entry.IntegrationID, providerErr)
	}

	// The circuit breaker runs before MarkAsProcessing, so this path must not
	// call the refund variant, which only accepts a row already in processing.
	env.mockQueueRepo.EXPECT().
		SetNextRetry(gomock.Any(), workspace.ID, entry.ID, gomock.Any()).
		Return(nil)
	env.worker.processEntry(workspace, entry)
}

func TestEmailQueueWorker_PreAcceptanceSMTPFailureReleasesQuota(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	repo := &quotaTestRepository{
		MessageHistoryRepository: env.mockMessageHistoryRepo,
		outcomes: map[string]quotaTestOutcome{
			domain.VeridianDailyQuotaKindProviderClass: {result: domain.VeridianDailyQuotaReservationResult{Reserved: true, Used: 1}},
		},
	}
	env.worker.messageHistoryRepo = repo
	workspace := veridianTestWorkspaceWithCaps(map[string]int{"microsoft": 2}, 0)
	entry := veridianTestEntryFrom("preaccept", "lead@outlook.com", "bot@send.test", domain.EmailQueuePayload{VeridianProviderClass: "microsoft"})

	env.mockMessageHistoryRepo.EXPECT().
		CountSentSinceForDomainsAndSenderDomain(gomock.Any(), "ws-1", gomock.Any(), false, "send.test", gomock.Any()).
		Return(0, nil)
	env.mockQueueRepo.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", entry.ID).Return(nil)
	env.mockEmailService.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).
		Return(emailerror.BeforeAcceptance(errors.New("connection refused")))
	env.mockMessageHistoryRepo.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.mockQueueRepo.EXPECT().MarkAsFailed(gomock.Any(), "ws-1", entry.ID, gomock.Any(), gomock.Any()).Return(nil)

	env.worker.processEntry(workspace, entry)
	require.Equal(t, []string{domain.VeridianDailyQuotaKindProviderClass}, repo.released)
}
