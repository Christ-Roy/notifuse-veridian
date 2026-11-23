package relayguard

import (
	"context"
	"errors"
	"testing"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type memRepo struct {
	domain.WorkspaceRepository
	ws      *domain.Workspace
	updated *domain.Workspace
	failOn  error
}

func (m *memRepo) GetByID(context.Context, string) (*domain.Workspace, error) { return m.ws, nil }
func (m *memRepo) Update(_ context.Context, w *domain.Workspace) error {
	if m.failOn != nil {
		return m.failOn
	}
	m.updated = w
	return nil
}

type recLog struct {
	logger.Logger
	fields map[string]interface{}
	errMsg string
}

func (r *recLog) WithFields(f map[string]interface{}) logger.Logger { r.fields = f; return r }
func (r *recLog) Error(msg string)                                  { r.errMsg = msg }

func commercialWS() *domain.Workspace {
	return &domain.Workspace{ID: "gbc", Integrations: []domain.Integration{{
		ID: "nord", Type: domain.IntegrationTypeEmail,
		EmailProvider: domain.EmailProvider{Kind: domain.EmailProviderKindSMTP, SMTP: &domain.SMTPSettings{Host: "smtp.messagerie-nord-776.fr"}},
	}}}
}

// VER-225 : relais listé, le profil est suspendu (veridian_paused, enregistré) et l'alerte part.
func TestSuspender_PausesProfileAndAlerts(t *testing.T) {
	repo := &memRepo{ws: commercialWS()}
	log := &recLog{}
	Suspender{Repo: repo, Log: log}.OnListed("gbc", "nord", "smtp.messagerie-nord-776.fr", queue.RelayCheck{State: queue.RelayListed, IPs: []string{"51.210.7.44"}, Detail: "127.0.0.2"})
	if repo.updated == nil || !repo.updated.GetIntegrationByID("nord").EmailProvider.VeridianPaused {
		t.Fatal("le profil doit être suspendu et enregistré")
	}
	if log.errMsg != AlertMessage || log.fields["suspended"] != true || log.fields["relay_ips"] != "51.210.7.44" {
		t.Fatalf("alerte manquante ou incomplète: %q %v", log.errMsg, log.fields)
	}
}

// Une suspension impossible se dit dans l'alerte (la porte, elle, bloque déjà l'envoi).
func TestSuspender_FailureIsReportedInTheAlert(t *testing.T) {
	log := &recLog{}
	Suspender{Repo: &memRepo{ws: commercialWS(), failOn: errors.New("db down")}, Log: log}.OnListed("gbc", "nord", "h", queue.RelayCheck{})
	if log.errMsg != AlertMessage || log.fields["suspended"] != false || log.fields["error"] != "db down" {
		t.Fatalf("%q %v", log.errMsg, log.fields)
	}
}

func TestCapabilityIsRegistered(t *testing.T) {
	for _, c := range platform.Default.List() {
		if c.Name == "relayguard" && c.Wire != nil {
			return
		}
	}
	t.Fatal("la capacité relayguard n'est pas enregistrée")
}
