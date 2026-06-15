package repository

// === Veridian patch ===
// VeridianMessageHistoryDecorator wraps the upstream MessageHistoryRepository
// and increments veridian_plan.emails_sent_this_month after each successful
// Create. Cette decoration est la SEULE source d'increment du compteur quota
// dans le code Notifuse aujourd'hui — sans elle, le paywall middleware
// (veridian_paywall.go) ne bloquerait jamais sur le quota mensuel car
// emails_sent_this_month resterait a 0 pour tous les tenants.
//
// Design choices :
//   - Best-effort sur l'increment : si planRepo retourne une erreur, on log
//     un warning mais on ne fait PAS echouer le Create — l'envoi mail est
//     deja ecrit en message_history et l'utilisateur attend un 200. Un
//     compteur quota qui derive est acceptable face a un envoi qui plante.
//   - Atomicite : UPDATE veridian_plan SET emails_sent_this_month = ... + $2
//     est atomique cote Postgres, donc safe sous concurrence (worker async
//     broadcast). Le TOCTOU avec IsBlocked() est accepte (overspend marginal
//     de quelques mails sur burst concurrent — borne par le cache paywall
//     60s qui re-resync apres expiration).
//   - Workspaces non-Veridian (mode self-hosted, ou anciens workspaces sans
//     ligne veridian_plan) : le repo IncrementEmailsSent retourne
//     "workspace X not found" — on log Debug et on passe. Pas d'erreur
//     remontee a l'appelant.
//   - Upsert (utilise sur retry des envois ayant deja une row) : on NE re-
//     increment PAS. Un retry n'est pas un nouvel envoi — c'est la SECONDE
//     tentative d'envoi du meme message, donc deja compte au premier Upsert.
//     Si on re-incrementait, un retry-storm sur une erreur SES gonflerait
//     artificiellement le compteur quota. Pour distinguer first-write vs
//     retry, le decorator ne peut PAS interroger l'upstream (qui a deja
//     fait l'upsert), donc on adopte la regle simple : Create seul
//     increment, Upsert jamais. Si du code upstream switch demain de
//     Create vers Upsert pour le first send, il faudra revoir.
//
// Wiring : voir internal/app/app.go vers la ligne 427 ou messageHistoryRepo
// est cree. Le decorateur wrap l'instance avant de l'injecter dans tous les
// services consommateurs (transactional_service, broadcast_service, etc.).

import (
	"context"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// VeridianMessageHistoryDecorator implements domain.MessageHistoryRepository
// by delegating to an upstream impl and triggering quota increment side
// effects on Create.
type VeridianMessageHistoryDecorator struct {
	upstream domain.MessageHistoryRepository
	planRepo domain.VeridianPlanRepository
	logger   logger.Logger
}

// NewVeridianMessageHistoryDecorator wraps upstream with quota increment
// side-effects. Si planRepo est nil (mode self-hosted), le decorateur est
// transparent : pure passthrough vers upstream.
func NewVeridianMessageHistoryDecorator(
	upstream domain.MessageHistoryRepository,
	planRepo domain.VeridianPlanRepository,
	log logger.Logger,
) *VeridianMessageHistoryDecorator {
	return &VeridianMessageHistoryDecorator{
		upstream: upstream,
		planRepo: planRepo,
		logger:   log,
	}
}

// Create delegue a l'upstream puis increment le compteur quota Veridian.
// L'increment est best-effort : un echec log un warn mais ne propage pas
// d'erreur (l'envoi mail a ete persiste, l'utilisateur attend 200).
func (d *VeridianMessageHistoryDecorator) Create(ctx context.Context, workspaceID string, secretKey string, message *domain.MessageHistory) error {
	if err := d.upstream.Create(ctx, workspaceID, secretKey, message); err != nil {
		return err
	}
	d.incrementQuota(ctx, workspaceID, 1)
	return nil
}

// Upsert : pas d'increment (retry handling — l'envoi a deja ete compte au
// Create initial, cf. design note en tete de fichier).
func (d *VeridianMessageHistoryDecorator) Upsert(ctx context.Context, workspaceID string, secretKey string, message *domain.MessageHistory) error {
	return d.upstream.Upsert(ctx, workspaceID, secretKey, message)
}

func (d *VeridianMessageHistoryDecorator) Update(ctx context.Context, workspaceID string, message *domain.MessageHistory) error {
	return d.upstream.Update(ctx, workspaceID, message)
}

func (d *VeridianMessageHistoryDecorator) Get(ctx context.Context, workspaceID string, secretKey string, id string) (*domain.MessageHistory, error) {
	return d.upstream.Get(ctx, workspaceID, secretKey, id)
}

func (d *VeridianMessageHistoryDecorator) GetByExternalID(ctx context.Context, workspaceID string, secretKey string, externalID string) (*domain.MessageHistory, error) {
	return d.upstream.GetByExternalID(ctx, workspaceID, secretKey, externalID)
}

func (d *VeridianMessageHistoryDecorator) GetByContact(ctx context.Context, workspaceID string, secretKey string, contactEmail string, limit, offset int) ([]*domain.MessageHistory, int, error) {
	return d.upstream.GetByContact(ctx, workspaceID, secretKey, contactEmail, limit, offset)
}

func (d *VeridianMessageHistoryDecorator) GetByBroadcast(ctx context.Context, workspaceID string, secretKey string, broadcastID string, limit, offset int) ([]*domain.MessageHistory, int, error) {
	return d.upstream.GetByBroadcast(ctx, workspaceID, secretKey, broadcastID, limit, offset)
}

func (d *VeridianMessageHistoryDecorator) ListMessages(ctx context.Context, workspaceID string, secretKey string, params domain.MessageListParams) ([]*domain.MessageHistory, string, error) {
	return d.upstream.ListMessages(ctx, workspaceID, secretKey, params)
}

func (d *VeridianMessageHistoryDecorator) SetStatusesIfNotSet(ctx context.Context, workspaceID string, updates []domain.MessageEventUpdate) error {
	return d.upstream.SetStatusesIfNotSet(ctx, workspaceID, updates)
}

func (d *VeridianMessageHistoryDecorator) SetClicked(ctx context.Context, workspaceID, id string, timestamp time.Time) error {
	return d.upstream.SetClicked(ctx, workspaceID, id, timestamp)
}

func (d *VeridianMessageHistoryDecorator) SetOpened(ctx context.Context, workspaceID, id string, timestamp time.Time) error {
	return d.upstream.SetOpened(ctx, workspaceID, id, timestamp)
}

func (d *VeridianMessageHistoryDecorator) GetBroadcastStats(ctx context.Context, workspaceID, broadcastID string) (*domain.MessageHistoryStatusSum, error) {
	return d.upstream.GetBroadcastStats(ctx, workspaceID, broadcastID)
}

func (d *VeridianMessageHistoryDecorator) GetBroadcastVariationStats(ctx context.Context, workspaceID, broadcastID, templateID string) (*domain.MessageHistoryStatusSum, error) {
	return d.upstream.GetBroadcastVariationStats(ctx, workspaceID, broadcastID, templateID)
}

func (d *VeridianMessageHistoryDecorator) DeleteForEmail(ctx context.Context, workspaceID, email string) error {
	return d.upstream.DeleteForEmail(ctx, workspaceID, email)
}

// CountSentSinceForContact : pur passthrough (lecture, aucun side-effect quota).
func (d *VeridianMessageHistoryDecorator) CountSentSinceForContact(ctx context.Context, workspaceID, contactEmail string, since time.Time) (int, error) {
	return d.upstream.CountSentSinceForContact(ctx, workspaceID, contactEmail, since)
}

// CountSentSinceForDomains : pur passthrough (lecture, aucun side-effect quota).
func (d *VeridianMessageHistoryDecorator) CountSentSinceForDomains(ctx context.Context, workspaceID string, domains []string, exclude bool, since time.Time) (int, error) {
	return d.upstream.CountSentSinceForDomains(ctx, workspaceID, domains, exclude, since)
}

// FindContactEmailByMessageID : pur passthrough (lecture, aucun side-effect quota).
func (d *VeridianMessageHistoryDecorator) FindContactEmailByMessageID(ctx context.Context, workspaceID, messageID string) (string, bool, error) {
	return d.upstream.FindContactEmailByMessageID(ctx, workspaceID, messageID)
}

// incrementQuota appelle planRepo.IncrementEmailsSent en best-effort.
// Erreur "workspace not found" = workspace pas gere par Veridian (self-hosted
// ou ancien workspace avant migration) → log Debug et passe. Autres erreurs
// = log Warn (DB transitoire, etc.) mais pas de propagation.
func (d *VeridianMessageHistoryDecorator) incrementQuota(ctx context.Context, workspaceID string, delta int64) {
	if d.planRepo == nil {
		return
	}
	if err := d.planRepo.IncrementEmailsSent(ctx, workspaceID, delta); err != nil {
		if d.logger == nil {
			return
		}
		if isWorkspaceNotFoundErr(err) {
			d.logger.WithFields(map[string]interface{}{
				"workspace_id": workspaceID,
			}).Debug("veridian quota increment: workspace not Veridian-managed, skipping")
			return
		}
		d.logger.WithFields(map[string]interface{}{
			"workspace_id": workspaceID,
			"delta":        delta,
			"error":        err.Error(),
		}).Warn("veridian quota increment failed (best-effort, email already sent)")
	}
}

// isWorkspaceNotFoundErr detecte les erreurs "workspace X not found"
// retournees par veridian_plan_postgres.go.
func isWorkspaceNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "veridian_plan: workspace") && strings.Contains(msg, "not found")
}

// Compile-time check: decorator satisfait l'interface upstream.
var _ domain.MessageHistoryRepository = (*VeridianMessageHistoryDecorator)(nil)
