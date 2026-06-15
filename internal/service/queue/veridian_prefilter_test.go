package queue

import (
	"context"
	"net"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

// prefilterTestResolver est un MXResolver de test avec capacité host (implémente
// LookupHostAddrs), pour piloter chaque cas de délivrabilité DNS du pré-filtre.
type prefilterTestResolver struct {
	mxByDomain   map[string][]string
	mxErr        map[string]error
	addrByDomain map[string][]string
	addrErr      map[string]error
}

func (r *prefilterTestResolver) LookupMXHosts(_ context.Context, d string) ([]string, error) {
	if err, ok := r.mxErr[d]; ok {
		return nil, err
	}
	if hosts, ok := r.mxByDomain[d]; ok {
		return hosts, nil
	}
	return nil, nil
}

func (r *prefilterTestResolver) LookupHostAddrs(_ context.Context, d string) ([]string, error) {
	if err, ok := r.addrErr[d]; ok {
		return nil, err
	}
	if addrs, ok := r.addrByDomain[d]; ok {
		return addrs, nil
	}
	return nil, nil
}

func prefilterNXDOMAIN() error {
	return &net.DNSError{Err: "no such host", IsNotFound: true}
}

// newPrefilterEnv construit un worker de test avec un resolver MX contrôlé.
type prefilterEnv struct {
	worker  *EmailQueueWorker
	queue   *mocks.MockEmailQueueRepository
	email   *mocks.MockEmailServiceInterface
	history *mocks.MockMessageHistoryRepository
}

func newPrefilterEnv(t *testing.T, res domain.MXResolver) *prefilterEnv {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	queue := mocks.NewMockEmailQueueRepository(ctrl)
	ws := mocks.NewMockWorkspaceRepository(ctrl)
	email := mocks.NewMockEmailServiceInterface(ctrl)
	history := mocks.NewMockMessageHistoryRepository(ctrl)
	log := pkgmocks.NewMockLogger(ctrl)
	log.EXPECT().WithFields(gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(log).AnyTimes()
	log.EXPECT().Debug(gomock.Any()).AnyTimes()
	log.EXPECT().Info(gomock.Any()).AnyTimes()
	log.EXPECT().Warn(gomock.Any()).AnyTimes()
	log.EXPECT().Error(gomock.Any()).AnyTimes()

	w := NewEmailQueueWorker(queue, ws, email, history, DefaultWorkerConfig(), log)
	w.ctx = context.Background()
	if res != nil {
		w.SetVeridianMXClassifier(domain.NewVeridianMXClassifier(res))
	}
	return &prefilterEnv{worker: w, queue: queue, email: email, history: history}
}

// expectPermanentSkip cale les mocks pour le chemin d'échec PERMANENT du
// pré-filtre : MarkAsProcessing (incrémente attempts) → message_history avec
// FailedAt (via upsertMessageHistory) → Delete (permanent). JAMAIS SendEmail
// (ctrl.Finish() refuse tout appel non déclaré, c'est la garantie de non-envoi).
func (e *prefilterEnv) expectPermanentSkip(t *testing.T, id string) {
	t.Helper()
	e.queue.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", id).Return(nil)
	e.history.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ string, msg *domain.MessageHistory) error {
			// La trace durable doit marquer l'échec (FailedAt posé par handleError),
			// pas un succès silencieux qui laisserait croire que le mail est parti.
			assert.NotNil(t, msg)
			assert.NotNil(t, msg.FailedAt, "message_history doit porter FailedAt (échec permanent)")
			return nil
		}).Times(1)
	e.queue.EXPECT().Delete(gomock.Any(), "ws-1", id).Return(nil)
}

func prefilterWorkspace() *domain.Workspace {
	return &domain.Workspace{
		ID: "ws-1",
		Integrations: []domain.Integration{
			{
				ID: "int-1",
				EmailProvider: domain.EmailProvider{
					Kind:               domain.EmailProviderKindSMTP,
					RateLimitPerMinute: 6000,
				},
			},
		},
	}
}

// TestPrefilter_InvalidSyntaxSkipped : une adresse syntaxiquement invalide est
// pré-filtrée en échec permanent, sans jamais taper SMTP.
func TestPrefilter_InvalidSyntaxSkipped(t *testing.T) {
	cases := []struct {
		name  string
		email string
	}{
		{"vide", ""},
		{"pas de @", "notanemail"},
		{"double @", "a@@b.com"},
		{"espace", "a b@c.com"},
		{"domaine vide", "user@"},
		{"local vide", "@domain.com"},
		{"display name", "Bob <bob@x.com>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newPrefilterEnv(t, &prefilterTestResolver{})
			entry := veridianTestEntry("e1", tc.email, domain.EmailQueuePayload{})
			env.expectPermanentSkip(t, "e1")
			env.worker.processEntry(prefilterWorkspace(), entry)
		})
	}
}

// TestPrefilter_DisposableDomainSkipped : un domaine jetable connu est pré-filtré.
func TestPrefilter_DisposableDomainSkipped(t *testing.T) {
	env := newPrefilterEnv(t, &prefilterTestResolver{})
	// 0-mail.com est dans la liste embarquée pkg/disposable_emails.
	entry := veridianTestEntry("e1", "throwaway@0-mail.com", domain.EmailQueuePayload{})
	env.expectPermanentSkip(t, "e1")
	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestPrefilter_NXDOMAINSkipped : un domaine DNS-mort (NXDOMAIN, ni MX ni A) est
// pré-filtré en échec permanent.
func TestPrefilter_NXDOMAINSkipped(t *testing.T) {
	res := &prefilterTestResolver{
		mxErr:   map[string]error{"dead-domain-xyz.invalid": prefilterNXDOMAIN()},
		addrErr: map[string]error{"dead-domain-xyz.invalid": prefilterNXDOMAIN()},
	}
	env := newPrefilterEnv(t, res)
	entry := veridianTestEntry("e1", "ghost@dead-domain-xyz.invalid", domain.EmailQueuePayload{})
	env.expectPermanentSkip(t, "e1")
	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestPrefilter_ValidAddressPasses : NON-RÉGRESSION CRITIQUE. Une adresse
// valide, domaine non jetable, MX résoluble, PASSE le pré-filtre et part en
// envoi complet (MarkAsProcessing → SendEmail → MarkAsSent).
func TestPrefilter_ValidAddressPasses(t *testing.T) {
	res := &prefilterTestResolver{
		mxByDomain: map[string][]string{"vraie-pme.fr": {"mx1.mail.ovh.net"}},
	}
	env := newPrefilterEnv(t, res)
	entry := veridianTestEntry("e1", "contact@vraie-pme.fr", domain.EmailQueuePayload{})

	env.queue.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Return(nil)
	env.history.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.queue.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)
	// Pas de Delete : ctrl.Finish() garantit qu'on n'a pas pris le chemin permanent.

	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestPrefilter_KnownSuffixPassesWithoutLookup : une adresse grand public (gmail)
// passe sans aucun lookup DNS (hot path), même avec un resolver qui exploserait.
func TestPrefilter_KnownSuffixPassesWithoutLookup(t *testing.T) {
	// Resolver qui renvoie NXDOMAIN pour TOUT : si le pré-filtre déclenchait un
	// lookup sur gmail.com, il le classerait undeliverable à tort. Le suffixe
	// connu doit court-circuiter le lookup.
	res := &prefilterTestResolver{
		mxErr:   map[string]error{},
		addrErr: map[string]error{},
	}
	// Tout domaine non mappé renvoie (nil, nil) = pas de MX décisif ; on force
	// le pire en mettant gmail.com en NXDOMAIN explicite.
	res.mxErr["gmail.com"] = prefilterNXDOMAIN()
	res.addrErr["gmail.com"] = prefilterNXDOMAIN()

	env := newPrefilterEnv(t, res)
	entry := veridianTestEntry("e1", "real.person@gmail.com", domain.EmailQueuePayload{})

	env.queue.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Return(nil)
	env.history.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.queue.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)

	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestPrefilter_DNSTimeoutDoesNotBlock : BEST-EFFORT STRICT. Un timeout DNS
// (transitoire) sur un domaine custom NE bloque PAS l'envoi — l'adresse passe.
func TestPrefilter_DNSTimeoutDoesNotBlock(t *testing.T) {
	res := &prefilterTestResolver{
		mxErr: map[string]error{
			"custom-glitchy.fr": &net.DNSError{Err: "i/o timeout", IsTimeout: true},
		},
	}
	env := newPrefilterEnv(t, res)
	entry := veridianTestEntry("e1", "ceo@custom-glitchy.fr", domain.EmailQueuePayload{})

	env.queue.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Return(nil)
	env.history.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.queue.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)

	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestPrefilter_NoMXResolverNeverBlocks : si le classifier MX est absent (worker
// construit hors constructeur normal), le pré-filtre DNS est sauté et l'adresse
// (syntaxe OK, domaine non jetable) passe. Best-effort, jamais de blocage.
func TestPrefilter_NoMXResolverNeverBlocks(t *testing.T) {
	env := newPrefilterEnv(t, nil)
	env.worker.providerMXClassifier = nil // simulate absence
	entry := veridianTestEntry("e1", "contact@some-custom-domain.fr", domain.EmailQueuePayload{})

	env.queue.EXPECT().MarkAsProcessing(gomock.Any(), "ws-1", "e1").Return(nil)
	env.email.EXPECT().SendEmail(gomock.Any(), gomock.Any(), true).Return(nil)
	env.history.EXPECT().Upsert(gomock.Any(), "ws-1", gomock.Any(), gomock.Any()).Return(nil)
	env.queue.EXPECT().MarkAsSent(gomock.Any(), "ws-1", "e1").Return(nil)

	env.worker.processEntry(prefilterWorkspace(), entry)
}

// TestVeridianValidEmailSyntax : table-driven unitaire de la validation pure.
func TestVeridianValidEmailSyntax(t *testing.T) {
	valid := []string{
		"a@b.com",
		"first.last@sub.domain.fr",
		"user+tag@gmail.com",
		"x_y@corp-name.co.uk",
	}
	for _, e := range valid {
		assert.True(t, veridianValidEmailSyntax(e), "%q devrait être valide", e)
	}
	invalid := []string{
		"",
		"plainstring",
		"a@",
		"@b.com",
		"a@@b.com",
		"a b@c.com",
		"Name <a@b.com>",
		"a@b@c.com",
		"trailing.dot@",
	}
	for _, e := range invalid {
		assert.False(t, veridianValidEmailSyntax(e), "%q devrait être invalide", e)
	}
}

// TestVeridianEmailDomain : extraction du domaine lowercase normalisé.
func TestVeridianEmailDomain(t *testing.T) {
	assert.Equal(t, "gmail.com", veridianEmailDomain("a@gmail.com"))
	assert.Equal(t, "gmail.com", veridianEmailDomain("a@Gmail.Com"))
	assert.Equal(t, "ovh.net", veridianEmailDomain("a@ovh.net."))
	assert.Equal(t, "", veridianEmailDomain("noat"))
	assert.Equal(t, "", veridianEmailDomain("a@"))
}
