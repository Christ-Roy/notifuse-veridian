package http

// === Veridian patch ===
// VeridianHandler expose les endpoints /api/tenants/* appeles par le Hub
// Veridian. Toutes les routes sont protegees par middleware HMAC
// (X-Veridian-Hub-Signature). Voir veridian_hmac.go.
//
// Routes :
//   POST   /api/tenants/provision      -> ProvisionResponse
//   POST   /api/tenants/update-plan    -> {tenant_id, plan, applied_at}
//   POST   /api/tenants/suspend        -> {tenant_id, suspended_at}
//   POST   /api/tenants/resume         -> {tenant_id, resumed_at}
//   DELETE /api/tenants/{id}           -> {tenant_id, deleted_at}
//   GET    /api/tenants/{id}/status    -> StatusResponse

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/buildinfo"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/http/middleware"
	"github.com/Notifuse/notifuse/internal/service"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianHandler regroupe les handlers Hub-driven.
type VeridianHandler struct {
	service          domain.VeridianService
	logger           logger.Logger
	paywallCache     *middleware.PaywallCache              // Peut etre nil (mode self-hosted sans paywall)
	frozenCache      *middleware.FrozenMemberCache         // Peut etre nil (mode self-hosted sans freeze)
	idempotencyRepo  domain.VeridianIdempotencyRepository // Peut etre nil (passthrough du middleware)
	// pricingSync est le service de sync catalogue pricing Hub (lot O 2026-05-21).
	// Peut etre nil (mode self-hosted sans Hub) : handlePricingCache retourne
	// alors 503. Cf. veridian_pricing_cache_handler.go.
	pricingSync PricingCacheProvider
	// testTenantsCleanup est le cron auto-cleanup orphans staging (2026-05-24).
	// Peut etre nil (mode prod / boot partiel) : handleTestTenantsStats retourne
	// alors 503. Cf. veridian_test_tenants_stats_handler.go.
	testTenantsCleanup TestTenantsCleanupStatsProvider
}

// NewVeridianHandler cree un handler. Le paywallCache est optionnel : s'il
// est nil, l'endpoint /api/veridian/admin/cache/invalidate retournera 503.
func NewVeridianHandler(service domain.VeridianService, log logger.Logger) *VeridianHandler {
	return &VeridianHandler{
		service: service,
		logger:  log,
	}
}

// SetPaywallCache injecte un cache partage avec le middleware paywall pour
// que /api/veridian/admin/cache/invalidate puisse le purger sur demande Hub.
// Optionnel : si jamais set, l'endpoint retournera 503 Service Unavailable.
func (h *VeridianHandler) SetPaywallCache(cache *middleware.PaywallCache) {
	h.paywallCache = cache
}

// SetIdempotencyRepo injecte le repo idempotency_keys pour le middleware
// Idempotency-Key (CONTRAT-HUB sec. 5.11). Optionnel : si nil, le middleware
// est passthrough (mode self-hosted, ou avant la migration V35).
func (h *VeridianHandler) SetIdempotencyRepo(repo domain.VeridianIdempotencyRepository) {
	h.idempotencyRepo = repo
}

// SetFrozenCache injecte le cache freeze partage avec le middleware
// veridian_paywall_frozen pour que les handlers freeze/unfreeze puissent
// invalider une entree apres mutation (sans attendre TTL 60s). Optionnel :
// si nil, l'invalidation post-mutation est skip et le cache attend l'expiration.
func (h *VeridianHandler) SetFrozenCache(cache *middleware.FrozenMemberCache) {
	h.frozenCache = cache
}

// RegisterRoutes enregistre les 6 endpoints /api/tenants/* WRAPPES dans
// le middleware HMAC. hubSecret est la valeur de HUB_API_SECRET.
//
// Utilise les patterns Go 1.22+ "METHOD path" pour matcher la methode
// et extraire {id} via r.PathValue("id").
func (h *VeridianHandler) RegisterRoutes(mux *http.ServeMux, hubSecret string) {
	hmac := middleware.VeridianHMACMiddleware(hubSecret)
	// Idempotency middleware : se chaine APRES HMAC (auth d'abord) et AVANT
	// le handler. Passthrough si idempotencyRepo nil OU si le client n'envoie
	// pas le header Idempotency-Key. Voir middleware/veridian_idempotency.go.
	idem := middleware.VeridianIdempotencyMiddleware(h.idempotencyRepo, h.logger)
	// Helper pour les routes mutateurs (HMAC + idempotency).
	writeRoute := func(handler http.HandlerFunc) http.Handler {
		return hmac(idem(handler))
	}

	// === Veridian patch === Endpoint public (no HMAC) qui dit a la console UI
	// si on est en mode "Veridian-managed" (HUB_API_SECRET set) ou self-hosted.
	// En mode managed, la console doit cacher la page Create Workspace et
	// rediriger vers /console/signin (magic link). Voir console/src/pages/
	// CreateWorkspacePage.tsx.
	mux.HandleFunc("GET /api/veridian/mode", h.handleMode(hubSecret))

	// === Mutateurs : HMAC + Idempotency (CONTRAT-HUB sec. 5.11) ===
	mux.Handle("POST /api/tenants/provision", writeRoute(h.handleProvision))
	mux.Handle("POST /api/tenants/update-plan", writeRoute(h.handleUpdatePlan))
	mux.Handle("POST /api/tenants/suspend", writeRoute(h.handleSuspend))
	mux.Handle("POST /api/tenants/resume", writeRoute(h.handleResume))
	mux.Handle("DELETE /api/tenants/{id}", writeRoute(h.handleDelete))
	// === Reads : HMAC seulement (idempotency inutile pour les GET) ===
	mux.Handle("GET /api/tenants/{id}/status", hmac(http.HandlerFunc(h.handleStatus)))
	// === Lifecycle mutateurs (CONTRAT-HUB sec. 5.7-5.8) ===
	// POST /soft-delete : pendant explicite (body, audit reason) du DELETE legacy.
	// POST /restore     : annule un soft-delete dans la fenetre de 30j.
	// POST /purge       : hard delete definitif (exige confirm="PURGE"+reason).
	// POST /touch       : heartbeat anti-soft-delete (debounce 24h).
	// GET  /usage-summary : agrege utilisation effective (read).
	mux.Handle("POST /api/tenants/{id}/soft-delete", writeRoute(h.handleSoftDelete))
	mux.Handle("POST /api/tenants/{id}/restore", writeRoute(h.handleRestore))
	mux.Handle("POST /api/tenants/{id}/purge", writeRoute(h.handlePurge))
	mux.Handle("POST /api/tenants/{id}/touch", writeRoute(h.handleTouch))
	mux.Handle("GET /api/tenants/{id}/usage-summary", hmac(http.HandlerFunc(h.handleUsageSummary)))
	// === Veridian patch === Admin endpoint pour cleanup CI / tests.
	// Supprime DEFINITIVEMENT (hard delete) workspace + DB + plan row.
	// Refuse les tenants matchant safety_client_prefixes (clients reels).
	mux.Handle("POST /api/veridian/admin/wipe-test-tenants", writeRoute(h.handleWipeTestTenants))
	// === Veridian patch === Dry-run listing admin (lot G 2026-05-21).
	// Read-only. Permet d'inspecter avant wipe : projette managed vs orphans.
	// Indispensable pour cibler les workspaces orphelins (sans veridian_plan)
	// que le wipe historique rate. Pas d'effet de bord — pas d'idempotency.
	mux.Handle("GET /api/veridian/admin/tenants", hmac(http.HandlerFunc(h.handleListTenants)))
	// === Veridian patch === Cache invalidate pour eliminer le sleep 60s
	// des tests e2e paywall apres suspend/resume/update-plan/delete. En prod,
	// peut etre appele par le Hub pour propager rapidement un changement de
	// plan a Notifuse sans attendre l'expiration TTL.
	mux.Handle("POST /api/veridian/admin/cache/invalidate", writeRoute(h.handleInvalidateCache))
	// === Veridian patch === Repair endpoint pour les tenants existants dont
	// l'owner humain n'est pas attaché au workspace (workspaces créés avant
	// la feature Hub-Veridian). Idempotent : safe à appeler en boucle pour
	// réparer en batch. Voir todo/2026-05-17-provision-owner-attach.md.
	mux.Handle("POST /api/veridian/admin/attach-owner", writeRoute(h.handleAttachOwner))
	// === Veridian patch === Grant unlimited (equipe interne + clients fideles
	// + partenaires). Passe le tenant en plan=enterprise + quota=-1 +
	// plan_source=lifetime_partner. Immune au downgrade Stripe.
	mux.Handle("POST /api/veridian/admin/grant-unlimited", writeRoute(h.handleGrantUnlimited))
	// === Veridian patch === Health observable du tenant (livrable 3 contrat
	// intégrations Hub). Le Hub poll en cron 1×/h pour détecter régression
	// silencieuse du flow magic link Hub → app (bug 2026-05-17).
	mux.Handle("GET /api/tenants/{id}/health", hmac(http.HandlerFunc(h.handleHealth)))
	// === Veridian patch — hub-attach-member (2026-05-21) ===
	// Appelé par le Hub après acceptation d'une invitation cross-app Notifuse.
	// Attache un user Hub invité au workspace Notifuse d'un tenant. Idempotent.
	// Voir todo/2026-05-21-hub-attach-member-endpoint.md
	mux.Handle("POST /api/tenants/{tenantId}/attach-member", writeRoute(h.handleAttachMember))
	// === Veridian patch — sync v1.5 CONTRAT-HUB §5.22.2 (2026-05-23) ===
	// Route workspace-level prescrite par le contrat v1.4 (preferentielle pour
	// les apps multi-workspace, ex: Prospection). Notifuse etant mono-workspace
	// (tenantId == workspaceId), l'alias delegue au meme handler — meme
	// semantique (idempotent, HMAC, §5.22.4 souverainete locale role).
	// Voir todo/2026-05-21-contrat-hub-v15-sync.md.
	mux.Handle("POST /api/veridian/workspaces/{tenantId}/attach-member", writeRoute(h.handleAttachMember))
	// === Veridian patch — Lot K (2026-05-21) ===
	// CONTRAT-HUB §5.15 rotate-api-key + §5.16 transfer-owner.
	// Voir veridian_rotate_transfer_handler.go.
	// Mutateurs : HMAC + Idempotency.
	mux.Handle("POST /api/tenants/{id}/rotate-api-key", writeRoute(h.handleRotateAPIKey))
	mux.Handle("POST /api/tenants/{id}/transfer-owner", writeRoute(h.handleTransferOwner))
	// === Veridian patch — v1.3 Multi-membre cross-app (2026-05-19) ===
	// CONTRAT-HUB §5.18 sync-member + §5.19 remove-member + §5.20 restore-member.
	// Voir veridian_membership_handler.go et todo/2026-05-19-v13-multi-membre-cross-app.md.
	// Mutateurs : HMAC + Idempotency.
	mux.Handle("POST /api/tenants/{id}/sync-member", writeRoute(h.handleSyncMember))
	mux.Handle("POST /api/tenants/{id}/remove-member", writeRoute(h.handleRemoveMember))
	mux.Handle("POST /api/tenants/{id}/restore-member", writeRoute(h.handleRestoreMember))
	// === Veridian patch — Freeze member per-user (CONTRAT-HUB §5.21, 2026-05-25) ===
	// freeze : Hub a depasse son quota seats sur ce tenant et frozen les
	// derniers invites (mode degrade : reads obfusques, writes 402 user_frozen
	// via middleware paywall per-user). unfreeze : reversible.
	// Voir veridian_freeze_handler.go et todo/2026-05-23-membership-freeze-per-user.md.
	// Mutateurs : HMAC + Idempotency.
	mux.Handle("POST /api/tenants/{tenantId}/freeze-member", writeRoute(h.handleFreezeMember))
	mux.Handle("POST /api/tenants/{tenantId}/unfreeze-member", writeRoute(h.handleUnfreezeMember))
	// === Veridian patch — lot O (2026-05-21) === Endpoint debug pour le
	// cache pricing sync (catalog Hub mirror). Auth HMAC, read-only.
	mux.Handle("GET /api/veridian/admin/pricing-cache", hmac(http.HandlerFunc(h.handlePricingCache)))
	// === Veridian patch — 2026-05-24 === Stats cron auto-cleanup orphans
	// staging. Auth HMAC, read-only. Retourne 503 si pas de service injecte
	// (cas: prod ou self-hosted). Cf. todo/2026-05-24-staging-db-pool-orphan-cleanup-auto.md.
	mux.Handle("GET /api/veridian/admin/test-tenants-stats", hmac(http.HandlerFunc(h.handleTestTenantsStats)))
	// === Veridian patch V37 === Limites + dimensions feature d'un tenant
	// (lot 7 ticket pricing-plans-implementation). Source de verite pour la
	// console UI (widgets quota) et le paywall middleware. Auth HMAC.
	mux.Handle("GET /api/tenants/{id}/limits", hmac(http.HandlerFunc(h.handleLimits)))
	// === Veridian patch — Hub discovery cross-app (2026-05-20) ===
	// POST + GET supportes :
	//   - POST {"email":"..."}      : appel UI/SDK Notifuse, evite email en URL
	//   - GET  ?email=...           : client Hub `lib/sync/discovery.ts` (cron reconcile)
	// Semantique : toujours 200 — found:false si email inconnu, found:true + workspaces sinon.
	// Auth : HMAC read-only (signature sur body, body vide pour GET → `${ts}.`).
	// Fix 2026-05-25 : GET ajoute apres bug prod silencieux (200 body vide via root_handler catchall)
	// cf. todo/2026-05-25-discovery-by-email-prod-returns-empty-body.md.
	mux.Handle("POST /api/users/by-email", hmac(http.HandlerFunc(h.handleDiscovery)))
	mux.Handle("GET /api/users/by-email", hmac(http.HandlerFunc(h.handleDiscoveryGET)))

	// === Mail provider choice (V48) — routes SUPPRIMÉES 2026-05-31 ===
	// Le pipeline "envoi via Hub" a été retiré (dépendance Hub = aberration,
	// jamais câblé à l'envoi réel). L'envoi se configure via Settings >
	// Integrations (provider local par workspace). Cf. memory
	// project_mail_sending_standalone_decision.

	// === Veridian patch — Couche 4 Bounce OAuth Hub (CONTRAT-HUB §6bis.8, 2026-05-23) ===
	// Appele par le Hub apres OAuth Google/Microsoft reussi pour delivrer un
	// magic link self-contained sans 2eme tour OAuth cote app. Idempotent par
	// nature (chaque call genere un token TTL 60s), pas besoin d Idempotency-Key.
	// Auth : HMAC seul.
	mux.Handle("POST /api/sso/issue-magic-link", hmac(http.HandlerFunc(h.handleIssueMagicLink)))

	// === Veridian patch === Endpoint public (no HMAC) qui renvoie le tag
	// et le SHA git du binaire qui tourne. Permet à la CI de valider qu'un
	// redeploy a effectivement remplacé le container — défense contre le
	// faux positif "deploy success" sans changement réel d'image (bug
	// 2026-05-18 où compose.redeploy redéployait avec le même tag faute
	// de compose.update préalable). Utilisé par le step Verify prod runs
	// new code dans .github/workflows/veridian-ci.yml.
	mux.HandleFunc("GET /api/version", h.handleVersion)
}

func (h *VeridianHandler) handleProvision(w http.ResponseWriter, r *http.Request) {
	var input domain.ProvisionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.TenantID == "" || input.OwnerEmail == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id and owner_email are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missingFields(input.TenantID == "", "tenant_id", input.OwnerEmail == "", "owner_email"),
		})
		return
	}

	resp, err := h.service.Provision(r.Context(), input)
	if err != nil {
		h.logError("provision", err, map[string]interface{}{
			"tenant_id": input.TenantID,
			"email":     input.OwnerEmail,
		})
		// === Veridian patch === Sentinel ErrTenantSoftDeleted → 409 Conflict
		// (re-provision rejetee tant que purge 30j pas passee).
		if errors.Is(err, service.ErrTenantSoftDeleted) {
			WriteJSONErrorCode(w, ErrCodeTenantSoftDeleted, err.Error(), http.StatusConflict, nil)
			return
		}
		// === Veridian patch === Sentinel ErrOwnerMismatch → 409 Conflict
		// (re-provision avec owner_email different refusee — protection contre
		// prise de controle d'un tenant existant). Contrat §5.1.
		if errors.Is(err, service.ErrOwnerMismatch) {
			WriteJSONErrorCode(w, ErrCodeOwnerMismatch, err.Error(), http.StatusConflict, nil)
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *VeridianHandler) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	var input domain.UpdatePlanInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	// === CONTRAT-BILLING v2 §3.4.1 — versioning du payload ===
	// Rejette 400 si contract_version major inconnu. Chaîne vide tolérée
	// (back-compat legacy v1 — Hub pas encore migré côté lib/notifuse/client.ts).
	if !domain.IsSupportedContractVersion(input.ContractVersion) {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "unsupported contract_version major", http.StatusBadRequest, map[string]interface{}{
			"contract_version":           input.ContractVersion,
			"supported_contract_version": domain.CurrentContractVersion,
		})
		return
	}
	if input.TenantID == "" || input.Plan == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id and plan are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missingFields(input.TenantID == "", "tenant_id", input.Plan == "", "plan"),
		})
		return
	}
	// === CONTRAT-BILLING v2 §3.4.2 — enum `plan` fermé ===
	// Rejette 400 invalid_plan si hors {free, pro, business, enterprise}.
	if !domain.IsValidCanonicalPlan(input.Plan) {
		WriteJSONErrorCode(w, ErrCodeInvalidPlan, "plan must be one of free|pro|business|enterprise", http.StatusBadRequest, map[string]interface{}{
			"plan":          input.Plan,
			"allowed_plans": domain.AllowedCanonicalPlans,
		})
		return
	}
	// === CONTRAT-BILLING v2 §3.3 — enum `plan_source` v2 ===
	// Accepte v2 (stripe|stripe_trial|grant_manual|downgrade_auto) ET legacy
	// v1 (manual|lifetime_*|internal) pour back-compat des appels Hub pas
	// encore migrés. Vide → défaut "stripe" au repo upsert.
	if !domain.IsValidPlanSourceV2(input.PlanSource) {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid plan_source", http.StatusBadRequest, map[string]interface{}{
			"plan_source":          string(input.PlanSource),
			"allowed_plan_sources": domain.AllowedPlanSourcesV2,
			"hint":                 "v1 legacy values (manual, lifetime_*, internal) also accepted as back-compat — they map to grant_manual in responses",
		})
		return
	}

	resp, err := h.service.UpdatePlan(r.Context(), input)
	if err != nil {
		h.logError("update_plan", err, map[string]interface{}{"tenant_id": input.TenantID})
		// === Veridian patch === Sentinel ErrPlanImmune → 409 plan_locked
		// (downgrade Stripe webhook bloque sur un plan offert). Contrat sec. 3.3.
		if errors.Is(err, service.ErrPlanImmune) {
			WriteJSONErrorCode(w, ErrCodePlanLocked, err.Error(), http.StatusConflict, map[string]interface{}{
				"tenant_id":      input.TenantID,
				"requested_plan": input.Plan,
				"hint":           "plan_source is immune (lifetime_*/manual/internal); use a non-stripe plan_source to override",
			})
			return
		}
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": input.TenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	// === Veridian patch 2026-05-24 — audit trial résidus §C/F ===
	// Invalidation immédiate du cache paywall après UpdatePlan : sinon les
	// middlewares paywall + soft-deleted continueraient à servir l'ancien
	// plan/status jusqu'à 60s. Conséquence du gap : un tenant qui paie via
	// Stripe (Hub → update-plan plan=pro) pouvait voir l'UI Free + bandeau
	// trial 60s côté serveur (puis 5min côté React Query). Pattern identique
	// à handleGrantUnlimited (cf. veridian_grant_unlimited_handler.go §92).
	if h.paywallCache != nil {
		h.paywallCache.Invalidate(input.TenantID)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *VeridianHandler) handleSuspend(w http.ResponseWriter, r *http.Request) {
	var input domain.SuspendInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.TenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	if err := h.service.Suspend(r.Context(), input); err != nil {
		h.logError("suspend", err, map[string]interface{}{"tenant_id": input.TenantID})
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": input.TenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	// Invalidation cache : un envoi qui passait avant la suspension doit être
	// bloqué immédiatement par le paywall, pas après 60s TTL (sécurité côté
	// blocage — moins critique que Resume/Restore mais cohérence cross-route).
	if h.paywallCache != nil {
		h.paywallCache.Invalidate(input.TenantID)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":    input.TenantID,
		"suspended_at": time.Now().UTC(),
	})
}

func (h *VeridianHandler) handleResume(w http.ResponseWriter, r *http.Request) {
	var input domain.ResumeInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.TenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	if err := h.service.Resume(r.Context(), input); err != nil {
		h.logError("resume", err, map[string]interface{}{"tenant_id": input.TenantID})
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": input.TenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	// Invalidation cache : un Resume doit débloquer les envois immédiatement,
	// sans attendre 60s — sinon les writes restent 402 alors que la DB dit
	// status=active. Symétrique de handleSuspend.
	if h.paywallCache != nil {
		h.paywallCache.Invalidate(input.TenantID)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":  input.TenantID,
		"resumed_at": time.Now().UTC(),
	})
}

// handleDelete : endpoint DELETE legacy. Conserve pour back-compat avec le
// client Hub actuel (cf. veridian-hub/lib/notifuse/client.ts:92). En interne,
// delegue au nouveau handleSoftDelete avec un body vide (reason omise).
// CONTRAT-HUB sec. 5.7-5.8 : equivalent semantique a POST /soft-delete.
func (h *VeridianHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	resp, err := h.service.SoftDelete(r.Context(), domain.SoftDeleteInput{TenantID: tenantID})
	if err != nil {
		h.logError("soft_delete", err, map[string]interface{}{"tenant_id": tenantID})
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	// Invalidation cache : un SoftDelete (route legacy DELETE) doit basculer
	// les writes en 402 tenant_soft_deleted et les reads en mode dégradé
	// (obfuscation) immédiatement. Sans invalidation, le cache servait encore
	// l'ancien plan deleted_at=nil pendant ≤60s — les envois passaient alors
	// qu'ils devraient renvoyer le body standardisé tenant_soft_deleted.
	if h.paywallCache != nil {
		h.paywallCache.Invalidate(tenantID)
	}

	// Format response legacy preserve (champs additionnels purge_eligible_at
	// passes en plus pour les nouveaux consommateurs Hub qui les attendent).
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tenant_id":         tenantID,
		"deleted_at":        resp.DeletedAt,
		"purge_eligible_at": resp.PurgeEligibleAt,
	})
}

// === Lifecycle handlers (CONTRAT-HUB sec. 5.7-5.8) ===

func (h *VeridianHandler) handleSoftDelete(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	// Body optionnel : juste {reason: "..."}. Si pas de body ou JSON invalide,
	// reason reste vide (back-compat). On ne fail QUE si le body est present
	// mais syntaxiquement invalide.
	var body struct {
		Reason string `json:"reason,omitempty"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
			return
		}
	}

	resp, err := h.service.SoftDelete(r.Context(), domain.SoftDeleteInput{
		TenantID: tenantID,
		Reason:   body.Reason,
	})
	if err != nil {
		h.logError("soft_delete", err, map[string]interface{}{"tenant_id": tenantID})
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	// Invalidation cache (cf. handleDelete pour la justification).
	if h.paywallCache != nil {
		h.paywallCache.Invalidate(tenantID)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *VeridianHandler) handleRestore(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	var body struct {
		Reason string `json:"reason,omitempty"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
			return
		}
	}

	resp, err := h.service.Restore(r.Context(), domain.RestoreInput{
		TenantID: tenantID,
		Reason:   body.Reason,
	})
	if err != nil {
		h.logError("restore", err, map[string]interface{}{"tenant_id": tenantID})
		// ErrTenantNotSoftDeleted → 409 (tenant pas en etat soft-deleted).
		if errors.Is(err, service.ErrTenantNotSoftDeleted) {
			WriteJSONErrorCode(w, ErrCodeTenantSoftDeleted, err.Error(), http.StatusConflict, map[string]interface{}{
				"tenant_id": tenantID,
				"hint":      "tenant is not in soft_deleted state — nothing to restore",
			})
			return
		}
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	// === Veridian patch 2026-05-24 — audit trial résidus §C ===
	// Invalidation critique : Restore clear deleted_at en DB, mais le cache
	// garde la version `deleted_at != nil` jusqu'à 60s. Sans invalidation, le
	// middleware soft-deleted continue à obfusquer les reads et 402 les writes
	// alors que le tenant est officiellement actif. Scénario typique :
	// trial expiré → Hub a soft-deleted → user paie → Hub send Restore → fenêtre
	// 60s de "tenant déjà sauvé mais UI dégradée".
	if h.paywallCache != nil {
		h.paywallCache.Invalidate(tenantID)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *VeridianHandler) handlePurge(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	var body struct {
		Reason  string `json:"reason"`
		Confirm string `json:"confirm"`
	}
	if r.Body == nil || r.ContentLength == 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "body required with reason and confirm fields", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"reason", "confirm"},
		})
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if body.Reason == "" || body.Confirm == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "reason and confirm are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missingFields(body.Reason == "", "reason", body.Confirm == "", "confirm"),
		})
		return
	}

	resp, err := h.service.Purge(r.Context(), domain.PurgeInput{
		TenantID: tenantID,
		Reason:   body.Reason,
		Confirm:  body.Confirm,
	})
	if err != nil {
		h.logError("purge", err, map[string]interface{}{"tenant_id": tenantID})
		if errors.Is(err, service.ErrPurgeNotEligible) {
			WriteJSONErrorCode(w, ErrCodePurgeNotEligible, err.Error(), http.StatusConflict, map[string]interface{}{
				"tenant_id": tenantID,
				"hint":      "tenant must be soft-deleted for at least 30 days before purge",
			})
			return
		}
		// Erreurs validation (confirm != PURGE, reason vide) sont retournees
		// par le service avec messages "*required*" / "*must equal*". Mapping
		// vers 400 invalid_payload.
		msg := err.Error()
		if containsAny(msg, "confirm must equal", "reason required") {
			WriteJSONErrorCode(w, ErrCodeInvalidPayload, msg, http.StatusBadRequest, nil)
			return
		}
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, msg, http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *VeridianHandler) handleTouch(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	resp, err := h.service.Touch(r.Context(), tenantID)
	if err != nil {
		h.logError("touch", err, map[string]interface{}{"tenant_id": tenantID})
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *VeridianHandler) handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	resp, err := h.service.UsageSummary(r.Context(), tenantID)
	if err != nil {
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("usage_summary", err, map[string]interface{}{"tenant_id": tenantID})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *VeridianHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	resp, err := h.service.GetStatus(r.Context(), tenantID)
	if err != nil {
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("get_status", err, map[string]interface{}{"tenant_id": tenantID})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// isNotFoundErr renvoie true sur sql.ErrNoRows ou erreurs wrappant ce sentinel.
// Les UPDATEs des repos retournent des erreurs construites avec fmt.Errorf
// "veridian_plan: workspace X not found" — on les reconnait par sous-chaine
// (pas ideal mais evite de creer un type d'erreur dedie pour le seul besoin
// du handler).
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	msg := err.Error()
	return msg != "" && (containsAny(msg, "not found", "ErrNoRows"))
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

// indexOf est une variante locale de strings.Index pour eviter d'importer
// strings juste pour ce helper.
func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func (h *VeridianHandler) logError(op string, err error, fields map[string]interface{}) {
	if h.logger == nil {
		return
	}
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields["op"] = op
	fields["error"] = err.Error()
	h.logger.WithFields(fields).Error("veridian handler: operation failed")
}

// === Veridian patch ===
// handleWipeTestTenants supprime DEFINITIVEMENT les tenants matchant un prefix
// ou une liste explicite. Hard delete (DROP DATABASE upstream + DELETE plan row),
// pas un soft delete. Reserve aux tests CI / admin platform.
func (h *VeridianHandler) handleWipeTestTenants(w http.ResponseWriter, r *http.Request) {
	var input domain.WipeTestTenantsInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.Prefix == "" && len(input.TenantIDs) == 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "prefix or tenant_ids required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"prefix", "tenant_ids"},
		})
		return
	}

	resp, err := h.service.WipeTestTenants(r.Context(), input)
	if err != nil {
		h.logError("wipe_test_tenants", err, map[string]interface{}{
			"prefix":     input.Prefix,
			"tenant_ids": len(input.TenantIDs),
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// === Veridian patch — lot G admin listing (2026-05-21) ===
// handleListTenants projette les tenants en 2 buckets (managed / orphans)
// pour inspection admin AVANT action. Read-only. Pattern dry-run.
//
// Auth : HMAC Hub (mux wrapper). Pas d'idempotency — read-only.
//
// Query params :
//   - prefix : filtre prefix (min 3 chars, pas de %/_)
//   - include_orphans : "true" pour scanner workspaceRepo (source de verite globale)
//   - limit : cap entier sur Managed et Orphans (0 = pas de cap)
//
// Reponse 200 :
//
//	{
//	  "managed": [{tenant_id, has_plan, plan, status, deleted_at}],
//	  "orphans": [{tenant_id, has_plan: false}],
//	  "total": N
//	}
//
// 400 si prefix < 3 chars ou contient %/_.
func (h *VeridianHandler) handleListTenants(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	input := domain.ListTenantsInput{
		Prefix:         q.Get("prefix"),
		IncludeOrphans: q.Get("include_orphans") == "true",
	}
	if l := q.Get("limit"); l != "" {
		var parsed int
		if _, err := fmt.Sscanf(l, "%d", &parsed); err != nil || parsed < 0 {
			WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid limit (must be positive integer)", http.StatusBadRequest, nil)
			return
		}
		input.Limit = parsed
	}

	resp, err := h.service.ListTenants(r.Context(), input)
	if err != nil {
		// Erreurs de validation prefix (wildcards, longueur) → 400.
		// Erreurs DB → 500.
		if strings.Contains(err.Error(), "prefix") {
			WriteJSONErrorCode(w, ErrCodeInvalidPayload, err.Error(), http.StatusBadRequest, nil)
			return
		}
		h.logError("list_tenants", err, map[string]interface{}{
			"prefix":          input.Prefix,
			"include_orphans": input.IncludeOrphans,
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleInvalidateCache purge l'entree paywall cache pour un workspace_id
// donne. Idempotent : pas d'erreur si le workspace n'avait pas d'entree.
//
// Auth : middleware HMAC en amont (route enregistree dans hmac wrapper).
// Le timestamp drift de 5min + signature SHA256 reject les replays.
//
// Body :
//   {"workspace_id": "abc123"}
//
// Reponse :
//   {"workspace_id": "abc123", "invalidated": true}
//
// Si le PaywallCache n'a pas ete injecte (mode self-hosted sans paywall),
// on retourne 503 plutot qu'un 200 silencieusement faux : le caller (Hub)
// doit savoir que sa demande n'a pas eu d'effet.
func (h *VeridianHandler) handleInvalidateCache(w http.ResponseWriter, r *http.Request) {
	if h.paywallCache == nil {
		WriteJSONErrorCode(w, ErrCodePaywallUnavailable, "paywall cache not initialized (self-hosted mode)", http.StatusServiceUnavailable, nil)
		return
	}

	var input struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.WorkspaceID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "workspace_id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"workspace_id"},
		})
		return
	}

	h.paywallCache.Invalidate(input.WorkspaceID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"workspace_id": input.WorkspaceID,
		"invalidated":  true,
	})
}


// handleAttachOwner répare un workspace existant en y attachant un user humain
// comme owner (cf. todo/2026-05-17-provision-owner-attach.md). Idempotent.
//
// Body :
//   {"tenant_id": "robertbrunon", "owner_email": "robert.brunon@veridian.site"}
//
// Réponse 200 :
//   {"tenant_id":"robertbrunon","owner_email":"robert.brunon@veridian.site",
//    "user_id":"0cb49456-...","attached":true,"already_attached":false,
//    "owner_transferred":true}
func (h *VeridianHandler) handleAttachOwner(w http.ResponseWriter, r *http.Request) {
	var input domain.AttachOwnerInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}
	if input.TenantID == "" || input.OwnerEmail == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant_id and owner_email are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missingFields(input.TenantID == "", "tenant_id", input.OwnerEmail == "", "owner_email"),
		})
		return
	}

	resp, err := h.service.AttachOwner(r.Context(), input)
	if err != nil {
		h.logError("attach_owner", err, map[string]interface{}{
			"tenant_id":   input.TenantID,
			"owner_email": input.OwnerEmail,
		})
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": input.TenantID,
			})
			return
		}
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleHealth renvoie l'état réel observable du tenant (livrable 3 du
// contrat intégrations Hub). Auth HMAC. Renvoie 404 si tenant inexistant.
//
// Réponse 200 :
//
//	{"tenant_id":"...","workspace_id":"...","status":"active","owner_attached":true,
//	 "owner_email":"...","owner_user_id":"...","api_key_valid":true,
//	 "magic_link_capable":true,"members_count":2,"plan":"free","checked_at":"..."}
func (h *VeridianHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	resp, err := h.service.Health(r.Context(), tenantID)
	if err != nil {
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("health", err, map[string]interface{}{"tenant_id": tenantID})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleLimits renvoie les limites + dimensions feature pour un tenant
// (lot 7 ticket pricing-plans-implementation V37). Auth HMAC. 404 si
// tenant inexistant.
//
// Reponse 200 (domain.LimitsResponse) :
//
//	{
//	  "tenant_id": "client42",
//	  "plan": "pro",
//	  "plan_source": "stripe",
//	  "status": "active",
//	  "limits": {
//	    "MonthlyEmailQuota": -1,
//	    "MaxContacts": 5000,
//	    "MaxSeats": 5,
//	    "MaxOAuthAccounts": 5,
//	    "MaxCustomDomains": 1,
//	    "MaxActiveSequences": -1,
//	    "FeatureABTesting": true,
//	    "FeatureBrandingRemoved": true,
//	    "FeatureWhiteLabel": false,
//	    "HistoryRetentionDays": 365
//	  },
//	  "generated_at": "2026-05-21T08:42:11Z"
//	}
//
// Convention -1 = illimite (cf. domain.PlanLimits). Le caller (console UI,
// paywall middleware) decide quoi afficher / bloquer en fonction des
// valeurs. La struct PlanLimits utilise les noms de champs Go en JSON
// (pas de tag JSON sur les sous-champs) — c'est volontaire, les noms sont
// stables et la struct est interne au domain Veridian.
func (h *VeridianHandler) handleLimits(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenant id is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenant_id"},
		})
		return
	}

	resp, err := h.service.GetLimits(r.Context(), tenantID)
	if err != nil {
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		h.logError("limits", err, map[string]interface{}{"tenant_id": tenantID})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleVersion renvoie le tag et le SHA git de l'image qui tourne (injectés
// au build via -ldflags -X). Endpoint public, pas de HMAC, idempotent.
// Réponse :
//
//	{"tag":"v32.0-veridian.eb7a88e2","git_sha":"eb7a88e2bc...","build_date":"2026-05-18T12:36:00Z"}
//
// Les 3 champs sont "dev" si le binaire est compilé sans ldflags
// (run local via `go run` ou `make dev`).
func (h *VeridianHandler) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"tag":        buildinfo.Tag,
		"git_sha":    buildinfo.GitSHA,
		"build_date": buildinfo.BuildDate,
	})
}

// === Veridian patch — hub-attach-member (2026-05-21) ===
// handleAttachMember attache un user Hub invite au workspace Notifuse d'un
// tenant apres acceptation d'une invitation cross-app.
//
// Route : POST /api/tenants/{tenantId}/attach-member
// Auth  : middleware HMAC (X-Veridian-Hub-Signature) — identique aux autres
//         routes Hub→Notifuse. Meme secret HUB_API_SECRET.
//
// Securite : 404 est retourne APRES validation HMAC pour eviter l'enumeration
// de tenantId. Le body n'est pas loggue en clair (contient hub_user_email).
//
// Reponse 201 (premier attach) ou 200 (idempotent already_member=true) :
//
//	{"attached":true,"already_member":false,"workspace_id":"...","role":"member","login_url":"..."}
func (h *VeridianHandler) handleAttachMember(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantId")
	if tenantID == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "tenantId path param is required", http.StatusBadRequest, map[string]interface{}{
			"missing": []string{"tenantId"},
		})
		return
	}

	var body struct {
		HubUserID    string                  `json:"hub_user_id"`
		HubUserEmail string                  `json:"hub_user_email"`
		Role         domain.AttachMemberRole `json:"role"`
		InvitationID string                  `json:"invitation_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "invalid JSON body", http.StatusBadRequest, nil)
		return
	}

	// Validation champs obligatoires (avant d'appeler le service).
	missing := missingFields(
		body.HubUserID == "", "hub_user_id",
		body.HubUserEmail == "", "hub_user_email",
		string(body.Role) == "", "role",
	)
	if len(missing) > 0 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, "hub_user_id, hub_user_email and role are required", http.StatusBadRequest, map[string]interface{}{
			"missing": missing,
		})
		return
	}

	// Validation role (enum check) avant appel service.
	if !body.Role.IsValid() {
		WriteJSONErrorCode(w, ErrCodeInvalidRole, "role must be owner|admin|member", http.StatusBadRequest, map[string]interface{}{
			"role":    string(body.Role),
			"allowed": []string{"owner", "admin", "member"},
		})
		return
	}

	input := domain.AttachMemberInput{
		TenantID:     tenantID,
		HubUserID:    body.HubUserID,
		HubUserEmail: body.HubUserEmail,
		Role:         body.Role,
		InvitationID: body.InvitationID,
	}

	resp, err := h.service.AttachMember(r.Context(), input)
	if err != nil {
		// 404 APRES HMAC (voir note sécurité en en-tête).
		if isNotFoundErr(err) {
			WriteJSONErrorCode(w, ErrCodeTenantNotFound, "tenant not found", http.StatusNotFound, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		// 423 Locked si tenant suspendu ou deleted.
		if errors.Is(err, service.ErrTenantSuspended) {
			WriteJSONErrorCode(w, ErrCodeTenantSuspended, "tenant is suspended — member attachment refused", http.StatusLocked, map[string]interface{}{
				"tenant_id": tenantID,
			})
			return
		}
		// 409 race condition (très rare en pratique).
		if errors.Is(err, service.ErrUserRoleConflict) {
			WriteJSONErrorCode(w, ErrCodeUserRoleConflict, err.Error(), http.StatusConflict, map[string]interface{}{
				"tenant_id":   tenantID,
				"hub_user_id": body.HubUserID,
			})
			return
		}
		h.logError("attach_member", err, map[string]interface{}{
			"tenant_id":   tenantID,
			"hub_user_id": body.HubUserID,
		})
		WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
		return
	}

	// 200 si already_member (idempotent re-call), 201 si premier attach.
	statusCode := http.StatusCreated
	if resp.AlreadyMember {
		statusCode = http.StatusOK
	}
	writeJSON(w, statusCode, resp)
}

// handleMode renvoie le mode de deploiement Notifuse (managed vs self-hosted).
// Endpoint public (pas de HMAC) car consomme par la console UI pour decider si
// elle doit afficher la page Create Workspace ou rediriger vers /console/signin.
//
// Reponse :
//   { "mode": "veridian-managed" | "self-hosted",
//     "signin_url": "/console/signin",
//     "hub_url": "https://app.veridian.site" (uniquement si veridian-managed) }
//
// La presence de HUB_API_SECRET dans la config = mode veridian-managed.
func (h *VeridianHandler) handleMode(hubSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hubSecret != "" {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"mode":       "veridian-managed",
				"signin_url": "/console/signin",
				"hub_url":    "https://app.veridian.site",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"mode":       "self-hosted",
			"signin_url": "/console/signin",
		})
	}
}
