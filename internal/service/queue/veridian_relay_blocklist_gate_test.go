package queue

import (
	"context"
	"testing"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

type fakeRelayChecker struct {
	check RelayCheck
	calls int
}

func (f *fakeRelayChecker) CheckRelay(context.Context, string) RelayCheck {
	f.calls++
	return f.check
}

func relayCand() veridianFailoverCandidate {
	return veridianFailoverCandidate{IntegrationID: "nord", Provider: &domain.EmailProvider{
		Kind: domain.EmailProviderKindSMTP, SMTP: &domain.SMTPSettings{Host: "smtp.messagerie-nord-776.fr"}}}
}

func relayEntry(src domain.EmailQueueSourceType) *domain.EmailQueueEntry {
	return &domain.EmailQueueEntry{ID: "e1", SourceType: src, ContactEmail: "lead@x.fr"}
}

// VER-225 : relais listé, l'entrée attend, le profil est suspendu et l'alerte part UNE fois.
func TestRelayGate_ListedBlocksAndSuspendsOnce(t *testing.T) {
	f := &fakeRelayChecker{check: RelayCheck{State: RelayListed, IPs: []string{"51.210.7.44"}, Detail: "127.0.0.2 SBL"}}
	var listed []string
	SetVeridianRelayChecker(f, func(ws, integ, host string, c RelayCheck) { listed = append(listed, ws+"|"+integ+"|"+host) })
	t.Cleanup(func() { SetVeridianRelayChecker(nil, nil) })
	w := &EmailQueueWorker{ctx: context.Background()}
	ws := &domain.Workspace{ID: "gbc"}
	for i := 0; i < 3; i++ {
		v := w.veridianRelayBlocklistVerdict(ws, relayCand(), relayEntry(domain.EmailQueueSourceAutomation))
		if !v.Blocked() || v.Reason != domain.VeridianReasonRelayBlocklisted || v.Value != "51.210.7.44" {
			t.Fatalf("un relais listé doit bloquer avec sa raison et son IP: %+v", v)
		}
	}
	if len(listed) != 1 || listed[0] != "gbc|nord|smtp.messagerie-nord-776.fr" {
		t.Fatalf("suspension et alerte une seule fois, pour ce profil: %v", listed)
	}
}

// Mesure non aboutie : rien ne part, nouvel essai à court terme, aucune suspension.
func TestRelayGate_UnverifiedSendsNothing(t *testing.T) {
	f := &fakeRelayChecker{check: RelayCheck{State: RelayUnverified, Detail: "témoin 127.0.0.2 non listé"}}
	called := false
	SetVeridianRelayChecker(f, func(string, string, string, RelayCheck) { called = true })
	t.Cleanup(func() { SetVeridianRelayChecker(nil, nil) })
	w := &EmailQueueWorker{ctx: context.Background()}
	v := w.veridianRelayBlocklistVerdict(&domain.Workspace{ID: "gbc"}, relayCand(), relayEntry(domain.EmailQueueSourceBroadcast))
	if !v.Blocked() || v.Reason != domain.VeridianReasonRelayUnverified || v.Delay != relayUnverifiedRetry {
		t.Fatalf("une mesure non aboutie doit bloquer, réessai court: %+v", v)
	}
	if called {
		t.Fatal("non abouti n'est pas listé : pas de suspension")
	}
	// Remesuré à court terme (pas une heure d'arrêt sur un hoquet réseau).
	h := relayGuard.Load()
	later := time.Now().Add(relayUnverifiedRetry + time.Minute)
	h.now = func() time.Time { return later }
	w.veridianRelayBlocklistVerdict(&domain.Workspace{ID: "gbc"}, relayCand(), relayEntry(domain.EmailQueueSourceBroadcast))
	if f.calls != 2 {
		t.Fatalf("une mesure non aboutie se refait après %s : %d appels", relayUnverifiedRetry, f.calls)
	}
}

// Propre : passe, et le verdict est réutilisé pendant relayCleanTTL ; au-delà, nouvelle mesure.
func TestRelayGate_CleanPassesAndIsCached(t *testing.T) {
	f := &fakeRelayChecker{check: RelayCheck{State: RelayClean, IPs: []string{"51.210.7.44"}}}
	SetVeridianRelayChecker(f, nil)
	t.Cleanup(func() { SetVeridianRelayChecker(nil, nil) })
	h := relayGuard.Load()
	now := time.Date(2026, 11, 23, 7, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	w := &EmailQueueWorker{ctx: context.Background()}
	for i := 0; i < 5; i++ {
		if v := w.veridianRelayBlocklistVerdict(&domain.Workspace{ID: "gbc"}, relayCand(), relayEntry(domain.EmailQueueSourceAutomation)); v.Blocked() {
			t.Fatalf("un relais propre passe: %+v", v)
		}
	}
	if f.calls != 1 {
		t.Fatalf("une mesure pour 5 envois rapprochés, %d", f.calls)
	}
	now = now.Add(relayCleanTTL + time.Minute)
	w.veridianRelayBlocklistVerdict(&domain.Workspace{ID: "gbc"}, relayCand(), relayEntry(domain.EmailQueueSourceAutomation))
	if f.calls != 2 {
		t.Fatalf("passé le délai, le relais est remesuré : %d", f.calls)
	}
}

// Le transactionnel et un fournisseur qui n'est pas un relais SMTP ne sont jamais retenus.
func TestRelayGate_TransactionalAndNonSMTPAreNeverHeld(t *testing.T) {
	f := &fakeRelayChecker{check: RelayCheck{State: RelayListed}}
	SetVeridianRelayChecker(f, nil)
	t.Cleanup(func() { SetVeridianRelayChecker(nil, nil) })
	w := &EmailQueueWorker{ctx: context.Background()}
	if v := w.veridianRelayBlocklistVerdict(&domain.Workspace{ID: "ws"}, relayCand(), relayEntry("transactional")); v.Blocked() {
		t.Fatalf("transactionnel retenu: %+v", v)
	}
	brevo := veridianFailoverCandidate{IntegrationID: "b", Provider: &domain.EmailProvider{Kind: domain.EmailProviderKindSES}}
	if v := w.veridianRelayBlocklistVerdict(&domain.Workspace{ID: "ws"}, brevo, relayEntry(domain.EmailQueueSourceAutomation)); v.Blocked() {
		t.Fatalf("fournisseur non SMTP retenu: %+v", v)
	}
	if f.calls != 0 {
		t.Fatal("aucune requête pour ce qui n'est pas concerné")
	}
}

// Sans vérificateur branché (assemblage de test), la porte ne bloque rien.
func TestRelayGate_NotWiredPasses(t *testing.T) {
	SetVeridianRelayChecker(nil, nil)
	w := &EmailQueueWorker{ctx: context.Background()}
	if v := w.veridianRelayBlocklistVerdict(&domain.Workspace{ID: "ws"}, relayCand(), relayEntry(domain.EmailQueueSourceAutomation)); v.Blocked() {
		t.Fatalf("%+v", v)
	}
}
