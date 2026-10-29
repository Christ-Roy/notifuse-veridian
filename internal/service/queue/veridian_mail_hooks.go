package queue

import (
	"context"
	"sync/atomic"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork — crochets de la boîte unifiée (lot L1, 29/10/2026).
//
// Ce fichier est le SEUL point de contact entre le moteur d'envoi / le poller IMAP (fichiers
// hérités) et le module internal/veridian/mailbox : deux interfaces étroites, implémentées
// côté mailbox, branchées au démarrage. Sans module branché, aucun effet.

// SentObserver est prévenu de chaque mail sortant ACCEPTÉ par le relais, avec son rendu exact
// (sujet, HTML, texte, en-têtes de fil) tel qu'il vient de partir.
type SentObserver interface {
	OnSent(workspace *domain.Workspace, entry *domain.EmailQueueEntry)
}

type sentObserverHolder struct{ o SentObserver }

var sentObserver atomic.Pointer[sentObserverHolder]

// SetVeridianSentObserver branche (ou retire, avec nil) l'observateur d'envoi.
func SetVeridianSentObserver(o SentObserver) {
	sentObserver.Store(&sentObserverHolder{o: o})
}

// veridianObserveSent est appelé par le worker juste après l'enregistrement de l'envoi réussi.
// Il ne bloque jamais et n'échoue jamais : l'observateur travaille à part.
func veridianObserveSent(workspace *domain.Workspace, entry *domain.EmailQueueEntry) {
	if h := sentObserver.Load(); h != nil && h.o != nil && workspace != nil && entry != nil {
		h.o.OnSent(workspace, entry)
	}
}

// MailboxSyncer lit les boîtes IMAP (rattrapage par UID) et reprend l'historique envoyé. Il est
// appelé par le poller IMAP à son rythme : pas de daemon de plus.
type MailboxSyncer interface {
	SyncBox(ctx context.Context, ws *domain.Workspace, integ *domain.Integration)
	SyncWorkspace(ctx context.Context, ws *domain.Workspace)
}

// RegisterMailboxSyncer branche le syncer sur le poller (nil retire).
func (s *VeridianIMAPPollerService) RegisterMailboxSyncer(m MailboxSyncer) {
	s.mu.Lock()
	s.mailSyncer = m
	s.mu.Unlock()
}

func (s *VeridianIMAPPollerService) currentMailSyncer() MailboxSyncer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mailSyncer
}
