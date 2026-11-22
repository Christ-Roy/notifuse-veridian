package bounces

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWebhooks struct {
	integ string
	got   []domain.SMTPWebhookPayload
	auth  []domain.InboundWebhookAuth
}

func (f *fakeWebhooks) ProcessWebhook(_ context.Context, _, integ string, raw []byte, auth domain.InboundWebhookAuth) error {
	var p domain.SMTPWebhookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	f.integ, f.got, f.auth = integ, append(f.got, p), append(f.auth, auth)
	return nil
}

type fakeWorkspaces struct{ domain.WorkspaceRepository }

func (fakeWorkspaces) GetByID(_ context.Context, id string) (*domain.Workspace, error) {
	return &domain.Workspace{ID: id, Integrations: []domain.Integration{
		{ID: "ses1", Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSES}},
		{ID: "smtp-a", Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}},
		{ID: "smtp-b", Type: domain.IntegrationTypeEmail, EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP}},
	}}, nil
}

type fakeAutomations struct {
	domain.AutomationRepository
	cas      []*domain.ContactAutomation
	updated  []domain.ContactAutomation
	counters map[string]int
}

func (f *fakeAutomations) ListContactAutomations(_ context.Context, _ string, flt domain.ContactAutomationFilter) ([]*domain.ContactAutomation, int, error) {
	var out []*domain.ContactAutomation
	for _, ca := range f.cas {
		if ca.ContactEmail != flt.ContactEmail {
			continue
		}
		for _, st := range flt.Status {
			if ca.Status == st {
				out = append(out, ca)
			}
		}
	}
	return out, len(out), nil
}

func (f *fakeAutomations) UpdateContactAutomation(_ context.Context, _ string, ca *domain.ContactAutomation) error {
	f.updated = append(f.updated, *ca)
	return nil
}

func (f *fakeAutomations) IncrementAutomationStat(_ context.Context, _, automationID, stat string) error {
	if f.counters == nil {
		f.counters = map[string]int{}
	}
	f.counters[automationID+"/"+stat]++
	return nil
}

func ptr(s string) *string { return &s }

func newApplier() (*Applier, *fakeWebhooks, *fakeAutomations) {
	wh := &fakeWebhooks{}
	au := &fakeAutomations{cas: []*domain.ContactAutomation{
		{ID: "ca1", AutomationID: "seq1", ContactEmail: "mort@x.fr", Status: domain.ContactAutomationStatusActive, CurrentNodeID: ptr("j4")},
		{ID: "ca2", AutomationID: "seq2", ContactEmail: "mort@x.fr", Status: domain.ContactAutomationStatusSending, CurrentNodeID: ptr("j0a")},
		{ID: "ca3", AutomationID: "seq3", ContactEmail: "mort@x.fr", Status: domain.ContactAutomationStatusCompleted},
		{ID: "ca4", AutomationID: "seq1", ContactEmail: "autre@x.fr", Status: domain.ContactAutomationStatusActive},
	}}
	return &Applier{Webhooks: wh, Workspaces: fakeWorkspaces{}, Automations: au}, wh, au
}

// Rebond dur (5.1.1) : la chaîne existante le reçoit (envoi cité, profil d'envoi du mail), et le
// contact sort de ses inscriptions en cours, active ET sending, étape gardée.
func TestApply_HardBounceFeedsChainAndExitsSequences(t *testing.T) {
	a, wh, au := newApplier()
	out, err := a.Apply(context.Background(), "ws", mailbox.Bounce{
		ProfileID: "smtp-b", SentMessageID: "ws_abc", Recipient: " Mort@X.fr ", Code: "5.1.1", Diagnostic: "550 5.1.1 user unknown", Severity: "hard",
	})
	require.NoError(t, err)
	require.Len(t, wh.got, 1)
	assert.Equal(t, "smtp-b", wh.integ, "le profil d'envoi du mail rebondi")
	assert.Equal(t, domain.SMTPWebhookPayload{Event: "bounce", Timestamp: wh.got[0].Timestamp, MessageID: "ws_abc", Recipient: "mort@x.fr",
		BounceCategory: "5.1.1", DiagnosticCode: "550 5.1.1 user unknown", Reason: "hard"}, wh.got[0])
	assert.True(t, wh.auth[0].Internal)
	assert.Equal(t, domain.BounceClassificationHard, out.Class)
	assert.Equal(t, 2, out.Exited)
	require.Len(t, au.updated, 2)
	for _, ca := range au.updated {
		assert.Equal(t, domain.ContactAutomationStatusExited, ca.Status)
		assert.Equal(t, ExitReasonHard, *ca.ExitReason)
		assert.NotNil(t, ca.CurrentNodeID, "l'étape reste posée : la sortie se défait")
	}
	assert.Equal(t, map[string]int{"seq1/exited": 1, "seq2/exited": 1}, au.counters)
}

// Refus de politique (5.7.1, 81 des 126 en prod) : le contact n'est pas supprimé de ses listes
// (décision du 06/10, la chaîne le garde), mais il sort de ses séquences en cours.
func TestApply_PolicyRefusalExitsSequences(t *testing.T) {
	a, _, au := newApplier()
	out, err := a.Apply(context.Background(), "ws", mailbox.Bounce{Recipient: "mort@x.fr", Code: "5.7.1", Diagnostic: "554 5.7.1 rejected by policy"})
	require.NoError(t, err)
	assert.Equal(t, domain.BounceClassificationPolicyRefusal, out.Class)
	assert.Equal(t, 2, out.Exited)
	assert.Equal(t, ExitReasonPolicy, *au.updated[0].ExitReason)
}

// Boîte pleine (5.2.2) et temporaire (4.x) : comptés par la chaîne vers son seuil, aucune sortie.
func TestApply_SoftBounceExitsNothing(t *testing.T) {
	for _, code := range []string{"5.2.2", "4.2.2"} {
		a, wh, au := newApplier()
		out, err := a.Apply(context.Background(), "ws", mailbox.Bounce{Recipient: "mort@x.fr", Code: code, OriginalMessageID: "<abc@x>"})
		require.NoError(t, err)
		assert.Equal(t, domain.BounceClassificationSoftCount, out.Class, code)
		assert.Zero(t, out.Exited, code)
		assert.Empty(t, au.updated, code)
		assert.Equal(t, "<abc@x>", wh.got[0].MessageID, "sans envoi relié, le Message-ID cité")
		assert.Equal(t, "smtp-a", wh.integ, "profil inconnu : le premier profil SMTP")
	}
}

func TestApply_NoRecipientIsAnError(t *testing.T) {
	a, wh, _ := newApplier()
	_, err := a.Apply(context.Background(), "ws", mailbox.Bounce{Code: "5.1.1"})
	assert.Error(t, err)
	assert.Empty(t, wh.got)
}

func TestClassify_FollowsTheChainRule(t *testing.T) {
	assert.Equal(t, domain.BounceClassificationHard, Classify("5.1.1", ""))
	assert.Equal(t, domain.BounceClassificationPolicyRefusal, Classify("5.7.1", ""))
	assert.Equal(t, domain.BounceClassificationSoftCount, Classify("5.2.2", ""))
}

func TestBounceReceived_IsApply(t *testing.T) {
	a, wh, _ := newApplier()
	require.NoError(t, a.BounceReceived(context.Background(), "ws", mailbox.Bounce{Recipient: "mort@x.fr", Code: "5.1.1"}))
	assert.Len(t, wh.got, 1)
}

type bareHost struct{ platform.Host }

func (bareHost) WorkspaceRepository() domain.WorkspaceRepository { return fakeWorkspaces{} }

type chainHost struct {
	bareHost
	wh *service.InboundWebhookEventService
}

func (h chainHost) InboundWebhookEventService() *service.InboundWebhookEventService { return h.wh }
func (chainHost) AutomationRepository() domain.AutomationRepository                 { return &fakeAutomations{} }
func (chainHost) Logger() logger.Logger                                             { return logger.NewLoggerWithLevel("disabled") }

// Un hôte qui ne fournit pas la chaîne n'en reçoit pas (la boîte range sans appliquer, et le dit) ;
// l'application la fournit.
func TestFromHost_OnlyWhenTheChainExists(t *testing.T) {
	assert.Nil(t, FromHost(bareHost{}))
	assert.Nil(t, FromHost(chainHost{}), "service des webhooks absent")
	a := FromHost(chainHost{wh: &service.InboundWebhookEventService{}})
	require.NotNil(t, a)
	assert.NotNil(t, a.Webhooks)
	assert.NotNil(t, a.Automations)
}
