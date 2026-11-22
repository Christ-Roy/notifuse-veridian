package bounces

import (
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/internal/veridian/platform"
)

// host : ce que la chaîne des rebonds demande à l'application, en plus de platform.Host.
type host interface {
	InboundWebhookEventService() *service.InboundWebhookEventService
	AutomationRepository() domain.AutomationRepository
}

// FromHost construit l'applicateur, ou nil si l'hôte ne fournit pas la chaîne (assemblage de test).
func FromHost(h platform.Host) *Applier {
	bh, ok := h.(host)
	if !ok || h.WorkspaceRepository() == nil {
		return nil
	}
	wh := bh.InboundWebhookEventService()
	if wh == nil {
		return nil
	}
	return &Applier{Webhooks: wh, Workspaces: h.WorkspaceRepository(), Automations: bh.AutomationRepository(), Log: h.Logger()}
}
