package relayguard

import (
	"context"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service/queue"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// AlertMessage : message stable de l'alerte (niveau erreur, relevé par l'observabilité).
const AlertMessage = "VeridianRelayGuard: relay IP listed on Spamhaus ZEN, sending profile suspended"

func init() {
	platform.Register(platform.Capability{Name: "relayguard", Wire: func(h platform.Host) error {
		repo := h.WorkspaceRepository()
		if repo == nil {
			return nil // assemblage de test sans dépôt
		}
		queue.SetVeridianRelayChecker(NewZEN(), Suspender{Repo: repo, Log: h.Logger()}.OnListed)
		return nil
	}})
}

// Suspender suspend le profil dont le relais est listé et lève l'alerte.
type Suspender struct {
	Repo domain.WorkspaceRepository
	Log  logger.Logger
	Now  func() time.Time
}

// OnListed : queue.RelayListedHandler. La porte bloque déjà l'envoi ; la suspension tient le profil
// à l'arrêt jusqu'à une reprise humaine (profiles:resume), après radiation de l'IP.
func (s Suspender) OnListed(workspaceID, integrationID, host string, c queue.RelayCheck) {
	err := s.suspend(workspaceID, integrationID)
	if s.Log == nil {
		return
	}
	f := map[string]interface{}{
		"workspace_id": workspaceID, "integration_id": integrationID, "relay_host": host,
		"relay_ips": strings.Join(c.IPs, ","), "detail": c.Detail, "suspended": err == nil,
	}
	if err != nil {
		f["error"] = err.Error()
	}
	s.Log.WithFields(f).Error(AlertMessage)
}

func (s Suspender) suspend(workspaceID, integrationID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	w, err := s.Repo.GetByID(ctx, workspaceID)
	if err != nil {
		return err
	}
	if _, err := w.VeridianApplyPause(integrationID, true); err != nil {
		return err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	w.UpdatedAt = now().UTC()
	return s.Repo.Update(ctx, w)
}
