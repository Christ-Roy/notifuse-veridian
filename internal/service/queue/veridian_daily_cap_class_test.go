package queue

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/domain/mocks"
	pkgmocks "github.com/Notifuse/notifuse/pkg/mocks"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

// Incident du 28/09 (workspace robertbrunon) : les automations natives, avec un
// plafond journalier par classe configuré (other_hoster), ont laissé passer 20
// envois vers cette classe en une journée. Diagnostic : other_hoster est une
// classe DÉRIVÉE DU MX (Lot 4) — domain.VeridianDomainsForClass("other_hoster")
// renvoie une liste de domaines VIDE (non-exclude), donc le COUNT-par-domaine du
// gate 2b (veridian_daily_cap.go) matche 0 lignes quel que soit le volume réel
// envoyé : le plafond devient un no-op PERMANENT pour toute classe MX (ovh,
// ionos, apple_icloud, security_gateway, other_hoster, corporate_selfhost).
//
// Ce fichier reproduit le défaut avec un FAUX repository qui rejoue FIDÈLEMENT
// la sémantique SQL de production (message_history_postgre.go) :
//   - CountSentSinceForDomains(domains=[], exclude=false) -> 0, TOUJOURS (même
//     garde que le repo réel : un ensemble vide de domaines ne matche jamais en
//     PostgreSQL, `col = ANY('{}')` est FALSE) ;
//   - CountSentSinceForClass(class) -> compte EXACT sur la colonne persistée
//     message_history.veridian_provider_class (V55), qui existe déjà en DB et
//     est déjà posée par worker.go AVANT ce gate -- seul le COUNT ne l'utilisait
//     pas encore.
//
// Le test simule 21 envois successifs (comme l'incident réel) vers une classe
// MX avec un plafond configuré à 3/jour et vérifie que le gate bloque bien à
// partir du 4e envoi. Sur le code d'AVANT le correctif, ce test ÉCHOUE (aucun
// envoi n'est jamais bloqué). Sur le code corrigé, il PASSE.

type veridianFakeSentRecord struct {
	class        string
	senderDomain string
	contactEmail string
	sentAt       time.Time
}

// veridianFakeClassCountRepo implémente les seules méthodes de COUNT exercées
// par veridianDailyCapGate, avec la sémantique EXACTE des requêtes SQL de
// internal/repository/message_history_postgre.go (y compris le garde-fou
// "domaines vides => 0" reproduit ici volontairement). Toute autre méthode de
// l'interface n'est jamais appelée par ce gate -- non implémentée (embedding nil).
type veridianFakeClassCountRepo struct {
	domain.MessageHistoryRepository
	sent []veridianFakeSentRecord
}

func (f *veridianFakeClassCountRepo) record(class, senderDomain, contactEmail string, at time.Time) {
	f.sent = append(f.sent, veridianFakeSentRecord{class: class, senderDomain: senderDomain, contactEmail: contactEmail, sentAt: at})
}

func veridianFakeDomainOf(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

func (f *veridianFakeClassCountRepo) CountSentSinceForContact(ctx context.Context, workspaceID, contactEmail string, since time.Time) (int, error) {
	n := 0
	for _, s := range f.sent {
		if s.contactEmail == contactEmail && !s.sentAt.Before(since) {
			n++
		}
	}
	return n, nil
}

// CountSentSinceForDomains reproduit fidèlement message_history_postgre.go : un
// ensemble de domaines VIDE en mode non-exclude ne matche jamais (0 sans
// requête), exactement comme `col = ANY('{}')` en PostgreSQL.
func (f *veridianFakeClassCountRepo) CountSentSinceForDomains(ctx context.Context, workspaceID string, domains []string, exclude bool, since time.Time) (int, error) {
	if len(domains) == 0 && !exclude {
		return 0, nil
	}
	set := make(map[string]bool, len(domains))
	for _, d := range domains {
		set[d] = true
	}
	n := 0
	for _, s := range f.sent {
		if s.sentAt.Before(since) {
			continue
		}
		matches := set[veridianFakeDomainOf(s.contactEmail)]
		if exclude {
			matches = !matches
		}
		if matches {
			n++
		}
	}
	return n, nil
}

func (f *veridianFakeClassCountRepo) CountSentSinceForDomainsAndSenderDomain(ctx context.Context, workspaceID string, domains []string, exclude bool, senderDomain string, since time.Time) (int, error) {
	if senderDomain == "" {
		return 0, nil
	}
	if len(domains) == 0 && !exclude {
		return 0, nil
	}
	set := make(map[string]bool, len(domains))
	for _, d := range domains {
		set[d] = true
	}
	n := 0
	for _, s := range f.sent {
		if s.sentAt.Before(since) || s.senderDomain != senderDomain {
			continue
		}
		matches := set[veridianFakeDomainOf(s.contactEmail)]
		if exclude {
			matches = !matches
		}
		if matches {
			n++
		}
	}
	return n, nil
}

func (f *veridianFakeClassCountRepo) CountSentSinceForSenderDomain(ctx context.Context, workspaceID, senderDomain string, since time.Time) (int, error) {
	if senderDomain == "" {
		return 0, nil
	}
	n := 0
	for _, s := range f.sent {
		if s.senderDomain == senderDomain && !s.sentAt.Before(since) {
			n++
		}
	}
	return n, nil
}

// CountSentSinceForClass / CountSentSinceForClassAndSenderDomain : le correctif
// attendu. Comptage EXACT par la colonne persistée, robuste à la façon dont la
// classe a été obtenue (suffixe ou MX).
func (f *veridianFakeClassCountRepo) CountSentSinceForClass(ctx context.Context, workspaceID, class string, since time.Time) (int, error) {
	n := 0
	for _, s := range f.sent {
		if s.class == class && !s.sentAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (f *veridianFakeClassCountRepo) CountSentSinceForClassAndSenderDomain(ctx context.Context, workspaceID, class, senderDomain string, since time.Time) (int, error) {
	if senderDomain == "" {
		return 0, nil
	}
	n := 0
	for _, s := range f.sent {
		if s.class == class && s.senderDomain == senderDomain && !s.sentAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func newVeridianDailyCapFakeEnv(t *testing.T) (*EmailQueueWorker, *veridianFakeClassCountRepo) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockQueueRepo := mocks.NewMockEmailQueueRepository(ctrl)
	mockWorkspaceRepo := mocks.NewMockWorkspaceRepository(ctrl)
	mockEmailService := mocks.NewMockEmailServiceInterface(ctrl)
	mockLogger := pkgmocks.NewMockLogger(ctrl)

	mockLogger.EXPECT().WithFields(gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().WithField(gomock.Any(), gomock.Any()).Return(mockLogger).AnyTimes()
	mockLogger.EXPECT().Debug(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Info(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Warn(gomock.Any()).AnyTimes()
	mockLogger.EXPECT().Error(gomock.Any()).AnyTimes()

	fakeRepo := &veridianFakeClassCountRepo{}

	worker := NewEmailQueueWorker(
		mockQueueRepo,
		mockWorkspaceRepo,
		mockEmailService,
		fakeRepo,
		DefaultWorkerConfig(),
		mockLogger,
	)
	worker.ctx = context.Background()
	worker.SetVeridianMXClassifier(domain.NewVeridianMXClassifier(&queueTestNXDOMAINResolver{}))
	return worker, fakeRepo
}

func TestVeridianDailyCapGate_MXDerivedClass_EnforcesPerDayCap(t *testing.T) {
	for _, class := range []string{
		domain.ProviderClassOVH,
		domain.ProviderClassIonos,
		domain.ProviderClassAppleICloud,
		domain.ProviderClassSecurityGateway,
		domain.ProviderClassOtherHoster,
		domain.ProviderClassCorporateSelfhost,
	} {
		class := class
		t.Run(class, func(t *testing.T) {
			worker, fakeRepo := newVeridianDailyCapFakeEnv(t)
			ws := veridianTestWorkspaceWithCaps(map[string]int{class: 3}, 0)

			blocked := 0
			const attempts = 21
			for i := 0; i < attempts; i++ {
				entry := veridianTestEntryFrom(
					fmt.Sprintf("e%d", i),
					fmt.Sprintf("lead%d@custom-hoster-%s.example", i, class),
					"bot@send.fr",
					domain.EmailQueuePayload{VeridianProviderClass: class},
				)
				_, capped := worker.veridianDailyCapGate(ws, nil, entry)
				if capped {
					blocked++
					continue
				}
				fakeRepo.record(class, "send.fr", entry.ContactEmail, time.Now().UTC())
			}

			// Incident du 28/09 : sur le code AVANT correctif, blocked == 0 quel
			// que soit le cap configuré (VeridianDomainsForClass(class) == [] pour
			// toute classe MX -> le COUNT ne matche jamais rien). Le plafond de
			// 3/jour doit bloquer 18 des 21 tentatives (les 3 premières passent).
			assert.Equal(t, attempts-3, blocked,
				"le plafond journalier par classe doit s'enforcer pour %s (classe dérivée du MX), pas seulement pour les classes à suffixe connu", class)
		})
	}
}
