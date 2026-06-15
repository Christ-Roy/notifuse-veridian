package broadcast

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/Notifuse/notifuse/pkg/veridian_spintax"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dedupTestLogger(ctrl *gomock.Controller) *pkgmocks.MockLogger {
	l := pkgmocks.NewMockLogger(ctrl)
	l.EXPECT().WithFields(gomock.Any()).Return(l).AnyTimes()
	l.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(l).AnyTimes()
	l.EXPECT().Debug(gomock.Any()).AnyTimes()
	l.EXPECT().Info(gomock.Any()).AnyTimes()
	l.EXPECT().Warn(gomock.Any()).AnyTimes()
	l.EXPECT().Error(gomock.Any()).AnyTimes()
	return l
}

// Note : coldBroadcast() (broadcast en contexte cold, rates par classe posés →
// VeridianIsColdContext = true) est défini dans veridian_sender_rotation_test.go,
// même package — on le réutilise ici pour activer le dedup.

func TestVeridianContentDedup_NilOrNoRepoIsNoOp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// dedup nil → renvoie le rendu initial sans hash, zéro I/O.
	var d *veridianContentDedup
	res := d.Resolve(context.Background(), veridianContentDedupParams{
		InitialSubject: "S", InitialBody: "B",
	})
	assert.Equal(t, "S", res.Subject)
	assert.Equal(t, "B", res.Body)
	assert.Empty(t, res.ContentHash)

	// repo nil → idem.
	d2 := newVeridianContentDedup(nil, dedupTestLogger(ctrl))
	res2 := d2.Resolve(context.Background(), veridianContentDedupParams{
		InitialSubject: "S", InitialBody: "B", Broadcast: coldBroadcast(),
		Email: "a@gmail.com",
	})
	assert.Empty(t, res2.ContentHash)
}

func TestVeridianContentDedup_NonColdIsNoOp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockMessageHistoryRepository(ctrl)
	// Aucun appel repo attendu hors contexte cold.
	d := newVeridianContentDedup(repo, dedupTestLogger(ctrl))

	res := d.Resolve(context.Background(), veridianContentDedupParams{
		WorkspaceID:    "ws-1",
		Email:          "a@gmail.com",
		Broadcast:      &domain.Broadcast{ID: "b-nocold"}, // pas de config cold
		InitialSubject: "S", InitialBody: "B",
	})
	assert.Empty(t, res.ContentHash, "hors contexte cold = no-op strict")
}

func TestVeridianContentDedup_NoCollisionKeepsInitial(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockMessageHistoryRepository(ctrl)
	d := newVeridianContentDedup(repo, dedupTestLogger(ctrl))

	// Pas de collision : EXISTS retourne false → on retient le rendu initial + hash.
	repo.EXPECT().
		ExistsContentHashSince(gomock.Any(), "ws-1", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(false, nil)

	res := d.Resolve(context.Background(), veridianContentDedupParams{
		WorkspaceID:    "ws-1",
		Email:          "a@gmail.com",
		Broadcast:      coldBroadcast(),
		InitialSubject: "Bonjour", InitialBody: "Corps",
	})
	assert.Equal(t, "Bonjour", res.Subject)
	assert.Equal(t, "Corps", res.Body)
	assert.Equal(t, domain.VeridianContentHash("Bonjour", "Corps"), res.ContentHash)
}

func TestVeridianContentDedup_CollisionRespinProducesNewHash(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockMessageHistoryRepository(ctrl)
	d := newVeridianContentDedup(repo, dedupTestLogger(ctrl))

	// Template AVEC variété spintax. Le respin re-rend avec le seed perturbé.
	subjectTpl := "{Bonjour|Salut|Coucou|Hey}"
	bodyTpl := "{Corps A|Corps B|Corps C|Corps D}"
	initialSubject := veridian_spintax.ResolveSpintax(subjectTpl, "a@gmail.com")
	initialBody := veridian_spintax.ResolveSpintax(bodyTpl, "a@gmail.com")

	// 1er hash (rendu initial) = collision → EXISTS true. 2e hash (après respin r1)
	// = neuf → EXISTS false. Granularité : on accepte n'importe quel hash en arg.
	gomock.InOrder(
		repo.EXPECT().ExistsContentHashSince(gomock.Any(), "ws-1", domain.VeridianContentHash(initialSubject, initialBody), gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil),
		repo.EXPECT().ExistsContentHashSince(gomock.Any(), "ws-1", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil),
	)

	res := d.Resolve(context.Background(), veridianContentDedupParams{
		WorkspaceID:    "ws-1",
		Email:          "a@gmail.com",
		Broadcast:      coldBroadcast(),
		InitialSubject: initialSubject,
		InitialBody:    initialBody,
		Respin: func(seed string) (string, string) {
			return veridian_spintax.ResolveSpintax(subjectTpl, seed),
				veridian_spintax.ResolveSpintax(bodyTpl, seed)
		},
	})

	// Le hash retenu doit être DIFFÉRENT du hash initial (la re-spin a varié).
	assert.NotEqual(t, domain.VeridianContentHash(initialSubject, initialBody), res.ContentHash)
	assert.NotEmpty(t, res.ContentHash)
}

func TestVeridianContentDedup_NoVarietySendsAnyway(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockMessageHistoryRepository(ctrl)
	d := newVeridianContentDedup(repo, dedupTestLogger(ctrl))

	// Template SANS spintax : la re-spin renvoie toujours le même rendu → le hash
	// ne change pas. Le dedup détecte que respin n'aide pas et SORT (mail PAS
	// perdu : on garde le rendu initial + son hash).
	subject := "Bonjour, un message fixe"
	body := "Corps strictement identique pour tous"

	// 1er EXISTS = collision. La re-spin ne change rien (même hash) → la boucle
	// sort immédiatement après la 1re tentative. Donc UN seul appel EXISTS.
	repo.EXPECT().
		ExistsContentHashSince(gomock.Any(), "ws-1", domain.VeridianContentHash(subject, body), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(true, nil)

	res := d.Resolve(context.Background(), veridianContentDedupParams{
		WorkspaceID:    "ws-1",
		Email:          "a@gmail.com",
		Broadcast:      coldBroadcast(),
		InitialSubject: subject,
		InitialBody:    body,
		Respin: func(seed string) (string, string) {
			// Pas de spintax → rendu identique quel que soit le seed.
			return subject, body
		},
	})

	// Mail PAS perdu : rendu initial conservé + hash posé (pour la traçabilité).
	assert.Equal(t, subject, res.Subject)
	assert.Equal(t, body, res.Body)
	assert.Equal(t, domain.VeridianContentHash(subject, body), res.ContentHash)
}

func TestVeridianContentDedup_LookupErrorIsBestEffort(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockMessageHistoryRepository(ctrl)
	d := newVeridianContentDedup(repo, dedupTestLogger(ctrl))

	// Erreur DB sur l'EXISTS → best-effort : on garde le rendu courant + son hash,
	// pas de blocage d'enqueue.
	repo.EXPECT().
		ExistsContentHashSince(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(false, errors.New("db down"))

	res := d.Resolve(context.Background(), veridianContentDedupParams{
		WorkspaceID:    "ws-1",
		Email:          "a@gmail.com",
		Broadcast:      coldBroadcast(),
		InitialSubject: "S", InitialBody: "B",
	})
	assert.Equal(t, domain.VeridianContentHash("S", "B"), res.ContentHash)
}

func TestVeridianContentDedup_DisabledExplicitlyIsNoOp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repo := mocks.NewMockMessageHistoryRepository(ctrl)
	// Aucun appel repo : anti-hash désactivé explicitement sur le broadcast.
	d := newVeridianContentDedup(repo, dedupTestLogger(ctrl))

	bc := coldBroadcast()
	bc.Metadata[domain.VeridianAntiHashMetadataKeyEnabled] = false

	res := d.Resolve(context.Background(), veridianContentDedupParams{
		WorkspaceID:    "ws-1",
		Email:          "a@gmail.com",
		Broadcast:      bc,
		InitialSubject: "S", InitialBody: "B",
	})
	assert.Empty(t, res.ContentHash, "anti-hash désactivé = no-op")
}

func TestVeridianRespinSeed(t *testing.T) {
	assert.Equal(t, "a@gmail.com:r1", veridianRespinSeed("a@gmail.com", 1))
	assert.Equal(t, "a@gmail.com:r2", veridianRespinSeed("a@gmail.com", 2))
	// Déterministe : même (email, attempt) → même seed.
	assert.Equal(t, veridianRespinSeed("x", 3), veridianRespinSeed("x", 3))
	require.NotEqual(t, veridianRespinSeed("x", 1), veridianRespinSeed("x", 2))
}
