package http

// === Veridian patch — lot O (2026-05-21) ===
// Handler pour GET /api/veridian/admin/pricing-cache.
//
// Expose l'etat courant du cache pricing (catalog en memoire + metadonnees
// de fraicheur) au debug / monitoring. Auth HMAC (meme middleware que les
// autres endpoints admin Hub).
//
// Use case :
//   - Robert curl pour verifier que le cache pricing est aligne sur le Hub
//   - Smoke test CI pour valider que le fetch boot a bien tourne
//   - Diagnostic apres alerte "Stale" (Hub down depuis 2 cycles)
//
// Reponse 200 : PricingCacheSnapshot (catalog + last_fetched_at + last_error
// + source_url + stale flag). Si le service n'a pas ete branche (mode
// self-hosted, HUB_PRICING_URL desactive volontairement), retourne 503.

import (
	"net/http"

	"github.com/Notifuse/notifuse/internal/service"
)

// PricingCacheProvider est l'interface minimale consommee par le handler.
// Permet au test d'injecter un fake sans demarrer un vrai service de cron.
type PricingCacheProvider interface {
	Snapshot() service.PricingCacheSnapshot
}

// SetPricingSync injecte le service pricing sync pour l'endpoint debug.
// Optionnel : si nil, /api/veridian/admin/pricing-cache retourne 503.
func (h *VeridianHandler) SetPricingSync(p PricingCacheProvider) {
	h.pricingSync = p
}

// handlePricingCache renvoie l'etat courant du cache pricing. Auth HMAC
// (gere par le wrapper de la route — pas de check supplementaire ici).
//
// Reponse 200 :
//
//	{
//	  "catalog": { ... },
//	  "last_fetched_at": "2026-05-21T12:00:00Z",
//	  "last_success_at": "2026-05-21T12:00:00Z",
//	  "last_error": "",
//	  "source_url": "https://hub.veridian.site/api/pricing/plans",
//	  "stale": false,
//	  "stale_threshold": "2h0m0s"
//	}
//
// 503 si SetPricingSync n'a pas ete appele (mode self-hosted, ou service
// volontairement desactive). Permet au caller de distinguer "vide a cause
// d'une erreur runtime" vs "service jamais branche".
func (h *VeridianHandler) handlePricingCache(w http.ResponseWriter, r *http.Request) {
	if h.pricingSync == nil {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "pricing sync not initialized (self-hosted mode)", http.StatusServiceUnavailable, nil)
		return
	}
	snap := h.pricingSync.Snapshot()
	writeJSON(w, http.StatusOK, snap)
}
