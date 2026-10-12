package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Veridian (12/10/2026) : relances dans le fil du premier mail. Cf. veridian_thread_followup.go.

func threadTestHistory(id, automationID, sender string, sentAt time.Time, subject string) *domain.MessageHistory {
	aid := automationID
	sa := sentAt
	m := &domain.MessageHistory{
		ID:                  id,
		Channel:             "email",
		AutomationID:        &aid,
		SentAt:              &sa,
		VeridianSenderEmail: sender,
		VeridianProfileID:   "nord",
		TemplateID:          "tpl-" + id,
	}
	if subject != "" {
		m.MessageData.Metadata = map[string]interface{}{veridianMetadataSubjectKey: subject}
	}
	return m
}

func threadTestWorkspace(on bool) *domain.Workspace {
	ws := &domain.Workspace{ID: "ws-1"}
	if on {
		t := true
		ws.Settings.VeridianThreadFollowups = &t
	}
	return ws
}

const (
	threadUUID0  = "11111111-1111-1111-1111-111111111111"
	threadUUID4  = "44444444-4444-4444-4444-444444444444"
	threadSender = "robert.brunon@messagerie-nord-776.fr"
)

func TestVeridianThreadFollowupsEnabled(t *testing.T) {
	assert.False(t, veridianThreadFollowupsEnabled(nil))
	assert.False(t, veridianThreadFollowupsEnabled(threadTestWorkspace(false)))
	assert.True(t, veridianThreadFollowupsEnabled(threadTestWorkspace(true)))
	off := false
	ws := &domain.Workspace{}
	ws.Settings.VeridianThreadFollowups = &off
	assert.False(t, veridianThreadFollowupsEnabled(ws))
}

func TestVeridianThreadReplySubject(t *testing.T) {
	assert.Equal(t, "Re: La vente en ligne chez X", veridianThreadReplySubject("La vente en ligne chez X"))
	assert.Equal(t, "Re: Sujet", veridianThreadReplySubject("Re: Sujet"))
	assert.Equal(t, "Re: Sujet", veridianThreadReplySubject("  RE: re: Sujet "))
	assert.Equal(t, "", veridianThreadReplySubject("   "))
	assert.Equal(t, "", veridianThreadReplySubject("Re:"))
}

func TestVeridianThreadFromHistory_ChainOrderAndFilters(t *testing.T) {
	base := time.Date(2026, 10, 8, 13, 0, 0, 0, time.UTC)
	j0 := threadTestHistory(threadUUID0, "auto-1", threadSender, base, "Sujet du premier mail")
	j4 := threadTestHistory("ws-1_"+threadUUID4, "auto-1", threadSender, base.Add(96*time.Hour), "Re: Sujet du premier mail")

	other := threadTestHistory("99999999-9999-9999-9999-999999999999", "auto-AUTRE", threadSender, base.Add(time.Hour), "autre")
	unsent := threadTestHistory("88888888-8888-8888-8888-888888888888", "auto-1", threadSender, base, "pas parti")
	unsent.SentAt = nil
	current := threadTestHistory("77777777-7777-7777-7777-777777777777", "auto-1", threadSender, base.Add(200*time.Hour), "courant")
	transac := threadTestHistory("66666666-6666-6666-6666-666666666666", "auto-1", threadSender, base.Add(time.Hour), "transac")
	transac.VeridianMessageType = domain.VeridianMessageTypeTransactional
	sms := threadTestHistory("55555555-5555-5555-5555-555555555555", "auto-1", threadSender, base.Add(time.Hour), "sms")
	sms.Channel = "sms"

	// Historique volontairement dans le désordre (le repo trie par sent_at DESC).
	history := []*domain.MessageHistory{nil, j4, other, unsent, current, transac, sms, j0}
	tc, found := veridianThreadFromHistory(history, "auto-1", current.ID)

	require.True(t, found)
	assert.Equal(t, []string{
		threadUUID0 + "@messagerie-nord-776.fr",
		threadUUID4 + "@messagerie-nord-776.fr",
	}, tc.References, "chaîne complète, du premier au dernier, UUID nu (sans préfixe de workspace)")
	assert.Equal(t, threadUUID4+"@messagerie-nord-776.fr", tc.InReplyTo, "In-Reply-To = le plus récent")
	assert.Equal(t, threadUUID0, tc.Root.ID)
	assert.Equal(t, "Sujet du premier mail", tc.RootSubject, "l'objet vient du PREMIER mail, pas de la relance")
}

func TestVeridianThreadFromHistory_FirstMailHasNoThread(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	_, found := veridianThreadFromHistory(nil, "auto-1", "x")
	assert.False(t, found)
	only := threadTestHistory(threadUUID0, "auto-1", threadSender, base, "s")
	_, found = veridianThreadFromHistory([]*domain.MessageHistory{only}, "auto-1", only.ID)
	assert.False(t, found, "le message courant lui-même n'est pas son propre parent")
	noSender := threadTestHistory(threadUUID4, "auto-1", "", base, "s")
	_, found = veridianThreadFromHistory([]*domain.MessageHistory{noSender}, "auto-1", "x")
	assert.False(t, found, "sans adresse émettrice on ne peut pas reconstruire le Message-ID")
}

func TestEmailQueueWorker_ApplyThreading_FollowupJoinsFirstMailThread(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := threadTestWorkspace(true)
	entry := veridianTestAutomationEntry("relance1", "auto-1", "lead@exemple.fr", threadSender)
	entry.Payload.Subject = "Un sujet toujours ouvert chez vous ?"

	j0 := threadTestHistory(threadUUID0, "auto-1", threadSender, time.Now().Add(-96*time.Hour), "La vente en ligne chez Grand Ouest")
	env.mockMessageHistoryRepo.EXPECT().
		GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@exemple.fr", gomock.Any(), 0).
		Return([]*domain.MessageHistory{j0}, 1, nil)

	env.worker.veridianApplyThreading(ws, entry)

	assert.Equal(t, "Re: La vente en ligne chez Grand Ouest", entry.Payload.Subject)
	assert.Equal(t, threadUUID0+"@messagerie-nord-776.fr", entry.Payload.EmailOptions.InReplyTo)
	assert.Equal(t, []string{threadUUID0 + "@messagerie-nord-776.fr"}, entry.Payload.EmailOptions.References)
}

func TestEmailQueueWorker_ApplyThreading_ThirdMailCarriesWholeChain(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	ws := threadTestWorkspace(true)
	entry := veridianTestAutomationEntry("relance2", "auto-1", "lead@exemple.fr", threadSender)
	entry.Payload.Subject = "Dernier message de ma part"

	now := time.Now()
	j0 := threadTestHistory(threadUUID0, "auto-1", threadSender, now.Add(-240*time.Hour), "Question sur votre boutique")
	j4 := threadTestHistory(threadUUID4, "auto-1", threadSender, now.Add(-144*time.Hour), "Re: Question sur votre boutique")
	env.mockMessageHistoryRepo.EXPECT().
		GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@exemple.fr", gomock.Any(), 0).
		Return([]*domain.MessageHistory{j4, j0}, 2, nil)

	env.worker.veridianApplyThreading(ws, entry)

	assert.Equal(t, "Re: Question sur votre boutique", entry.Payload.Subject, "jamais 'Re: Re:'")
	assert.Equal(t, threadUUID4+"@messagerie-nord-776.fr", entry.Payload.EmailOptions.InReplyTo)
	assert.Equal(t, []string{threadUUID0 + "@messagerie-nord-776.fr", threadUUID4 + "@messagerie-nord-776.fr"}, entry.Payload.EmailOptions.References)
}

func TestEmailQueueWorker_ApplyThreading_NoOpCases(t *testing.T) {
	t.Run("reglage absent", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t) // aucun appel repo attendu : gomock échoue sinon
		entry := veridianTestAutomationEntry("r", "auto-1", "lead@exemple.fr", threadSender)
		entry.Payload.Subject = "Sujet relance"
		env.worker.veridianApplyThreading(threadTestWorkspace(false), entry)
		assert.Equal(t, "Sujet relance", entry.Payload.Subject)
		assert.Empty(t, entry.Payload.EmailOptions.InReplyTo)
	})
	t.Run("broadcast", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestAutomationEntry("r", "bc-1", "lead@exemple.fr", threadSender)
		entry.SourceType = domain.EmailQueueSourceBroadcast
		env.worker.veridianApplyThreading(threadTestWorkspace(true), entry)
		assert.Empty(t, entry.Payload.EmailOptions.InReplyTo)
	})
	t.Run("premier mail de la sequence", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestAutomationEntry("j0", "auto-1", "lead@exemple.fr", threadSender)
		entry.Payload.Subject = "Premier objet"
		env.mockMessageHistoryRepo.EXPECT().
			GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@exemple.fr", gomock.Any(), 0).
			Return(nil, 0, nil)
		env.worker.veridianApplyThreading(threadTestWorkspace(true), entry)
		assert.Equal(t, "Premier objet", entry.Payload.Subject)
		assert.Empty(t, entry.Payload.EmailOptions.InReplyTo)
		assert.Empty(t, entry.Payload.EmailOptions.References)
	})
	t.Run("historique illisible : le mail part sans fil", func(t *testing.T) {
		env := newVeridianThrottleTestEnv(t)
		entry := veridianTestAutomationEntry("r", "auto-1", "lead@exemple.fr", threadSender)
		entry.Payload.Subject = "Sujet relance"
		env.mockMessageHistoryRepo.EXPECT().
			GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@exemple.fr", gomock.Any(), 0).
			Return(nil, 0, errors.New("db down"))
		env.worker.veridianApplyThreading(threadTestWorkspace(true), entry)
		assert.Equal(t, "Sujet relance", entry.Payload.Subject)
		assert.Empty(t, entry.Payload.EmailOptions.InReplyTo)
	})
}

func TestEmailQueueWorker_ApplyThreading_LegacyRootSubjectIsReRendered(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	renderer := &threadFakeRenderer{subject: "Question sur la boutique chez Acme"}
	env.worker.SetQueuedEmailRenderer(renderer)
	ws := threadTestWorkspace(true)
	entry := veridianTestAutomationEntry("relance1", "auto-1", "lead@exemple.fr", threadSender)
	entry.TemplateID = "ecom-relance-j4"
	entry.Payload.Subject = "Un sujet toujours ouvert chez vous ?"

	j0 := threadTestHistory(threadUUID0, "auto-1", threadSender, time.Now().Add(-96*time.Hour), "") // parti avant le correctif : aucun objet consigné
	j0.TemplateID = "ecom-vetuste-a"
	env.mockMessageHistoryRepo.EXPECT().
		GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@exemple.fr", gomock.Any(), 0).
		Return([]*domain.MessageHistory{j0}, 1, nil)

	env.worker.veridianApplyThreading(ws, entry)

	assert.Equal(t, "ecom-vetuste-a", renderer.templateSeen, "on re-rend le modèle du PREMIER mail, pas celui de la relance")
	assert.Equal(t, "ecom-relance-j4", entry.TemplateID, "l'entrée de la relance n'est pas modifiée")
	assert.Equal(t, "Re: Question sur la boutique chez Acme", entry.Payload.Subject)
	assert.Equal(t, threadUUID0+"@messagerie-nord-776.fr", entry.Payload.EmailOptions.InReplyTo)
}

func TestEmailQueueWorker_ApplyThreading_UnknownRootSubjectKeepsHeaders(t *testing.T) {
	env := newVeridianThrottleTestEnv(t) // pas de renderer : objet du premier mail introuvable
	entry := veridianTestAutomationEntry("relance1", "auto-1", "lead@exemple.fr", threadSender)
	entry.Payload.Subject = "Sujet de la relance"
	j0 := threadTestHistory(threadUUID0, "auto-1", threadSender, time.Now().Add(-96*time.Hour), "")
	env.mockMessageHistoryRepo.EXPECT().
		GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@exemple.fr", gomock.Any(), 0).
		Return([]*domain.MessageHistory{j0}, 1, nil)

	env.worker.veridianApplyThreading(threadTestWorkspace(true), entry)

	assert.Equal(t, "Sujet de la relance", entry.Payload.Subject)
	assert.Equal(t, threadUUID0+"@messagerie-nord-776.fr", entry.Payload.EmailOptions.InReplyTo)
}

func TestEmailQueueWorker_ApplyThreading_SenderMismatchIsTracedNotBlocking(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	entry := veridianTestAutomationEntry("relance1", "auto-1", "lead@exemple.fr", "r.brunon@agence-veridian.fr")
	entry.Payload.Subject = "Relance"
	j0 := threadTestHistory(threadUUID0, "auto-1", threadSender, time.Now().Add(-96*time.Hour), "Premier")
	env.mockMessageHistoryRepo.EXPECT().
		GetByContact(gomock.Any(), "ws-1", gomock.Any(), "lead@exemple.fr", gomock.Any(), 0).
		Return([]*domain.MessageHistory{j0}, 1, nil)

	env.worker.veridianApplyThreading(threadTestWorkspace(true), entry)

	assert.Equal(t, "Re: Premier", entry.Payload.Subject, "l'écart d'expéditeur est tracé (log), il ne bloque pas l'envoi")
}

// L'objet réellement envoyé est consigné dans l'historique (clé veridian_subject) : c'est lui
// que les relances reprendront. Seulement pour les automations, seulement sur succès.
func TestEmailQueueWorker_UpsertMessageHistory_RecordsSentSubject(t *testing.T) {
	env := newVeridianThrottleTestEnv(t)
	entry := veridianTestAutomationEntry("j0", "auto-1", "lead@exemple.fr", threadSender)
	entry.Payload.Subject = "La vente en ligne chez Acme"

	var saved *domain.MessageHistory
	env.mockMessageHistoryRepo.EXPECT().
		Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ string, m *domain.MessageHistory) error {
			saved = m
			return nil
		})
	env.worker.upsertMessageHistory(context.Background(), "ws-1", "secret", entry, nil)
	require.NotNil(t, saved)
	assert.Equal(t, "La vente en ligne chez Acme", saved.MessageData.Metadata[veridianMetadataSubjectKey])

	// Échec d'envoi : rien à consigner.
	saved = nil
	env.mockMessageHistoryRepo.EXPECT().
		Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ string, m *domain.MessageHistory) error {
			saved = m
			return nil
		})
	env.worker.upsertMessageHistory(context.Background(), "ws-1", "secret", entry, errors.New("smtp 421"))
	require.NotNil(t, saved)
	assert.Nil(t, saved.MessageData.Metadata)
}

type threadFakeRenderer struct {
	subject      string
	templateSeen string
}

func (f *threadFakeRenderer) RenderQueuedEmail(_ context.Context, _ *domain.Workspace, e *domain.EmailQueueEntry) (*RenderedQueuedEmail, error) {
	f.templateSeen = e.TemplateID
	return &RenderedQueuedEmail{Subject: f.subject}, nil
}

// Garde-fou : un week-end (fenêtre d'envoi fermée du vendredi 19h au lundi 8h, soit ~61h)
// ne doit JAMAIS faire croire que l'expéditeur de J0 est indisponible. Le 12/10, 151
// relances sur 332 sont parties d'une autre adresse que J0 parce que la limite était de 48h.
func TestVeridianSequenceAnchorUnavailableAfter_SurvivesAWeekendAndLongHolidays(t *testing.T) {
	weekend := 61 * time.Hour
	assert.Greater(t, veridianSequenceAnchorUnavailableAfter, weekend)
	assert.GreaterOrEqual(t, veridianSequenceAnchorUnavailableAfter, 7*24*time.Hour)
}
