package organisation

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

const (
	hubSubscriptionID   = "veridianhubevenements"
	hubSubscriptionName = "Veridian Hub"
	hubEventsPath       = "/api/evenements/notifuse"
	defaultHubURL       = "https://app.veridian.site"
	eventMessage        = "message.received"
)

// subscriptionURL : l'adresse du Hub, jamais celle d'un appelant.
func (h *Handler) subscriptionURL() string {
	base := strings.TrimRight(strings.TrimSpace(h.HubURL), "/")
	if base == "" {
		base = defaultHubURL
	}
	return base + hubEventsPath
}

func (h *Handler) findSubscription(ctx context.Context, ws string) (*domain.WebhookSubscription, error) {
	subs, err := h.Subs.List(ctx, ws)
	if err != nil {
		return nil, err
	}
	for _, s := range subs {
		if s.ID == hubSubscriptionID {
			return s, nil
		}
	}
	return nil, nil
}

// Subscription : POST /api/hub/evenements/abonnement { hub_tenant_id, actif }. Rejouable. Allumé : l'abonnement message.received vers le Hub
// existe, actif, à son adresse ; le secret Standard Webhooks n'est rendu qu'au Hub (signé, no-store). Éteint : il est retiré, rien n'est rejoué.
func (h *Handler) Subscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HubTenantID string `json:"hub_tenant_id"`
		Active      *bool  `json:"actif"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.HubTenantID == "" || req.Active == nil {
		refuse(w, http.StatusBadRequest, "invalid_payload", "hub_tenant_id et actif requis")
		return
	}
	ctx := r.Context()
	ws, err := h.Store.WorkspaceOf(ctx, req.HubTenantID)
	if err != nil {
		h.fail(w, err, "abonnement")
		return
	}
	sub, err := h.findSubscription(ctx, ws)
	if err != nil {
		h.fail(w, err, "abonnement")
		return
	}
	if !*req.Active {
		if sub != nil {
			if err := h.Subs.Delete(ctx, ws, sub.ID); err != nil {
				h.fail(w, err, "abonnement")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspace_id": ws, "actif": false})
		return
	}
	want := domain.WebhookSubscriptionSettings{EventTypes: []string{eventMessage}}
	if sub == nil {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			h.fail(w, err, "secret")
			return
		}
		sub = &domain.WebhookSubscription{ID: hubSubscriptionID, Name: hubSubscriptionName, URL: h.subscriptionURL(),
			Secret: "whsec_" + base64.StdEncoding.EncodeToString(key), Settings: want, Enabled: true}
		err = h.Subs.Create(ctx, ws, sub)
	} else if !sub.Enabled || sub.URL != h.subscriptionURL() || len(sub.Settings.EventTypes) != 1 || sub.Settings.EventTypes[0] != eventMessage {
		sub.Enabled, sub.URL, sub.Settings = true, h.subscriptionURL(), want // un propriétaire a pu le modifier à l'écran
		err = h.Subs.Update(ctx, ws, sub)
	}
	if err != nil {
		h.fail(w, err, "abonnement")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace_id": ws, "actif": true, "subscription_id": sub.ID, "url": sub.URL, "events": sub.Settings.EventTypes, "secret": sub.Secret})
}
