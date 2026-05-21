package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// veridianPlanRepository implements domain.VeridianPlanRepository.
type veridianPlanRepository struct {
	systemDB *sql.DB
	// emitter : WebhookEmitter optionnel pour émettre le webhook
	// tenant.activity_threshold_reached quand le seuil 5 mails est franchi.
	// nil en mode self-hosted / tests unitaires.
	// Injecté via WithWebhookEmitter() après création.
	emitter domain.WebhookEmitter
	// log : logger optionnel pour les avertissements best-effort.
	log logger.Logger
}

// NewVeridianPlanRepository cree un repo PostgreSQL pour la table veridian_plan.
func NewVeridianPlanRepository(systemDB *sql.DB) domain.VeridianPlanRepository {
	return &veridianPlanRepository{systemDB: systemDB}
}

// WithWebhookEmitter retourne une copie du repo avec l'emitter injecté.
// Appelé dans app.go après la création du webhook emitter (post-ligne 435).
// Le repo reste utilisable sans emitter (best-effort, nil-safe).
func WithWebhookEmitter(repo domain.VeridianPlanRepository, emitter domain.WebhookEmitter, log logger.Logger) domain.VeridianPlanRepository {
	if r, ok := repo.(*veridianPlanRepository); ok {
		r.emitter = emitter
		r.log = log
	}
	return repo
}

// Get recupere une ligne par workspace_id. Retourne sql.ErrNoRows si absent.
//
// Les colonnes V37 (max_contacts, max_seats, ..., history_retention_days)
// sont scannees vers les champs ajoutes a VeridianPlan dans le lot 1
// (cf. todo/2026-05-20-pricing-plans-implementation.md). Convention
// -1 = illimite (cf. domain.PlanLimits).
func (r *veridianPlanRepository) Get(ctx context.Context, workspaceID string) (*domain.VeridianPlan, error) {
	const q = `
		SELECT workspace_id, plan, plan_source, status, monthly_email_quota, emails_sent_this_month,
		       last_reset_at, suspended_at, suspended_reason, deleted_at,
		       restored_at, purge_eligible_at, last_touched_at, lifecycle_reason,
		       max_contacts, max_seats, max_oauth_accounts, max_custom_domains, max_active_sequences,
		       feature_ab_testing, feature_branding_removed, feature_white_label, history_retention_days,
		       emails_sent_lifetime, activity_threshold_reached_at,
		       created_at, updated_at
		FROM veridian_plan
		WHERE workspace_id = $1
	`
	var p domain.VeridianPlan
	var status, planSource string
	var suspendedAt, deletedAt, restoredAt, purgeEligibleAt, lastTouchedAt sql.NullTime
	var suspendedReason, lifecycleReason sql.NullString
	// V38 : activity_threshold_reached_at est TIMESTAMP WITH TIME ZONE (nullable).
	// Utilise sql.NullTime pour le scan null-safe.
	var activityThresholdReachedAt sql.NullTime

	err := r.systemDB.QueryRowContext(ctx, q, workspaceID).Scan(
		&p.WorkspaceID, &p.Plan, &planSource, &status, &p.MonthlyEmailQuota, &p.EmailsSentThisMonth,
		&p.LastResetAt, &suspendedAt, &suspendedReason, &deletedAt,
		&restoredAt, &purgeEligibleAt, &lastTouchedAt, &lifecycleReason,
		&p.MaxContacts, &p.MaxSeats, &p.MaxOAuthAccounts, &p.MaxCustomDomains, &p.MaxActiveSequences,
		&p.FeatureABTesting, &p.FeatureBrandingRemoved, &p.FeatureWhiteLabel, &p.HistoryRetentionDays,
		&p.EmailsSentLifetime, &activityThresholdReachedAt,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	p.Status = domain.PlanStatus(status)
	p.PlanSource = domain.PlanSource(planSource)
	if suspendedAt.Valid {
		t := suspendedAt.Time
		p.SuspendedAt = &t
	}
	if suspendedReason.Valid {
		p.SuspendedReason = suspendedReason.String
	}
	if deletedAt.Valid {
		t := deletedAt.Time
		p.DeletedAt = &t
	}
	if restoredAt.Valid {
		t := restoredAt.Time
		p.RestoredAt = &t
	}
	if purgeEligibleAt.Valid {
		t := purgeEligibleAt.Time
		p.PurgeEligibleAt = &t
	}
	if lastTouchedAt.Valid {
		t := lastTouchedAt.Time
		p.LastTouchedAt = &t
	}
	if lifecycleReason.Valid {
		p.LifecycleReason = lifecycleReason.String
	}
	if activityThresholdReachedAt.Valid {
		t := activityThresholdReachedAt.Time
		p.ActivityThresholdReachedAt = &t
	}
	return &p, nil
}

// Upsert insere ou remplace une ligne complete.
//
// Important : plan_source utilise COALESCE pour PRESERVER la valeur existante
// au moment du ON CONFLICT si l'appelant passe une chaine vide. Cela evite
// qu'un re-provision via Hub (qui peut ne pas envoyer plan_source) ecrase
// silencieusement un lifetime_partner par 'stripe'. Sur INSERT pur, la
// chaine vide est convertie en 'stripe' par le COALESCE($2, 'stripe').
//
// V37 dimensions (max_contacts, max_seats, ..., history_retention_days) :
//   - Si toutes a zero dans la struct → auto-fill via domain.LimitsForPlan(p.Plan)
//     a l'INSERT (provisioning standard depuis plan free/pro/business/enterprise).
//   - Si l'appelant a fixe au moins un champ V37 non-zero → on persiste la struct
//     telle quelle (custom override par Robert ou le Hub).
//   - Au ON CONFLICT, on NE met PAS a jour les dimensions V37 : un Upsert
//     idempotent ne doit pas regresser silencieusement un tenant pro vers
//     les defaults free. Pour changer les limites, passer par UpdatePlan
//     (qui applique LimitsForPlan du nouveau plan) ou un futur SetLimits.
func (r *veridianPlanRepository) Upsert(ctx context.Context, p *domain.VeridianPlan) error {
	if p.WorkspaceID == "" {
		return errors.New("workspace_id required")
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.LastResetAt.IsZero() {
		p.LastResetAt = now
	}
	if p.Status == "" {
		p.Status = domain.PlanStatusActive
	}

	// Auto-fill V37 dimensions si toutes a zero (provisioning standard).
	// Si l'appelant a fixe au moins une dimension, on respecte ses overrides.
	if isZeroPricingDimensions(p) {
		applyDefaultLimits(p)
	}

	// On passe NULL si vide pour que COALESCE prenne la valeur existante en UPDATE
	// (ou 'stripe' en INSERT initial).
	var planSourceArg interface{}
	if p.PlanSource != "" {
		planSourceArg = string(p.PlanSource)
	} else {
		planSourceArg = nil
	}

	const q = `
		INSERT INTO veridian_plan (
			workspace_id, plan, plan_source, status, monthly_email_quota, emails_sent_this_month,
			last_reset_at, suspended_at, suspended_reason, deleted_at,
			max_contacts, max_seats, max_oauth_accounts, max_custom_domains, max_active_sequences,
			feature_ab_testing, feature_branding_removed, feature_white_label, history_retention_days,
			emails_sent_lifetime,
			created_at, updated_at
		) VALUES ($1,$2,COALESCE($3,'stripe'),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
		ON CONFLICT (workspace_id) DO UPDATE SET
			plan = EXCLUDED.plan,
			plan_source = COALESCE(EXCLUDED.plan_source, veridian_plan.plan_source),
			status = EXCLUDED.status,
			monthly_email_quota = EXCLUDED.monthly_email_quota,
			updated_at = EXCLUDED.updated_at
	`
	// Note : activity_threshold_reached_at n'est PAS inclus dans l'Upsert.
	// Ce champ est géré exclusivement par MarkActivityThresholdReached
	// (écriture idempotente one-shot). L'Upsert ne doit pas écraser un
	// timestamp déjà set lors d'un re-provision ou update-plan.
	_, err := r.systemDB.ExecContext(ctx, q,
		p.WorkspaceID, p.Plan, planSourceArg, string(p.Status), p.MonthlyEmailQuota, p.EmailsSentThisMonth,
		p.LastResetAt, p.SuspendedAt, p.SuspendedReason, p.DeletedAt,
		p.MaxContacts, p.MaxSeats, p.MaxOAuthAccounts, p.MaxCustomDomains, p.MaxActiveSequences,
		p.FeatureABTesting, p.FeatureBrandingRemoved, p.FeatureWhiteLabel, p.HistoryRetentionDays,
		p.EmailsSentLifetime,
		p.CreatedAt, p.UpdatedAt,
	)
	return err
}

// isZeroPricingDimensions retourne true si toutes les dimensions V37 sont
// a zero / false dans la struct. Sert a detecter un appelant qui ne fixe
// pas les dimensions (cas standard Provision sans custom override).
//
// Note semantique : -1 = illimite, 0 = "non fixe" (cas Free legitime aussi
// pour MaxCustomDomains qui est a 0 par defaut). Pour distinguer ces deux
// cas, on regarde si **tous** les champs sont a zero, pas un seul. Un
// appelant qui veut explicitement set "0 domains" mais 500 contacts
// declarera MaxContacts=500 et le check tombera a false.
func isZeroPricingDimensions(p *domain.VeridianPlan) bool {
	return p.MaxContacts == 0 &&
		p.MaxSeats == 0 &&
		p.MaxOAuthAccounts == 0 &&
		p.MaxCustomDomains == 0 &&
		p.MaxActiveSequences == 0 &&
		!p.FeatureABTesting &&
		!p.FeatureBrandingRemoved &&
		!p.FeatureWhiteLabel &&
		p.HistoryRetentionDays == 0
}

// applyDefaultLimits ecrit les dimensions V37 de la struct depuis
// domain.LimitsForPlan(p.Plan). Fallback Free si plan inconnu (cf.
// semantique safe LimitsForPlan).
func applyDefaultLimits(p *domain.VeridianPlan) {
	l := domain.LimitsForPlan(p.Plan)
	p.MaxContacts = l.MaxContacts
	p.MaxSeats = l.MaxSeats
	p.MaxOAuthAccounts = l.MaxOAuthAccounts
	p.MaxCustomDomains = l.MaxCustomDomains
	p.MaxActiveSequences = l.MaxActiveSequences
	p.FeatureABTesting = l.FeatureABTesting
	p.FeatureBrandingRemoved = l.FeatureBrandingRemoved
	p.FeatureWhiteLabel = l.FeatureWhiteLabel
	p.HistoryRetentionDays = l.HistoryRetentionDays
}

// UpdatePlan change le plan + quota + plan_source d'un workspace existant.
// No-op si absent.
//
// Si planSource est vide, la colonne plan_source est preservee via COALESCE
// (pas d'ecrasement implicite par 'stripe' — protection contre les appels Hub
// legacy qui n'envoient pas plan_source).
//
// V37 — Application automatique des dimensions du nouveau plan :
// changement de plan = changement de tier business → on ecrase TOUTES les
// dimensions (max_contacts, max_seats, ..., history_retention_days) avec
// domain.LimitsForPlan(plan). Si plan inconnu, fallback Free (no privilege
// escalation, cf. semantique LimitsForPlan).
//
// Cas d'usage qui motive cette decision :
//   - Hub envoie update-plan(pro→business) → 25k contacts + 25 seats appliques
//   - Hub envoie update-plan(pro→free) downgrade → repli aux limites Free
//   - Stripe webhook subscription_deleted → free → re-applique Free strict
//
// Pour des limites custom (deal Enterprise avec quotas hors-grille), passer
// par un endpoint dedie (lot 3) qui appellera Upsert directement avec une
// struct VeridianPlan pre-remplie.
func (r *veridianPlanRepository) UpdatePlan(ctx context.Context, workspaceID, plan string, quota int64, planSource domain.PlanSource) error {
	var planSourceArg interface{}
	if planSource != "" {
		planSourceArg = string(planSource)
	} else {
		planSourceArg = nil
	}

	limits := domain.LimitsForPlan(plan)

	const q = `
		UPDATE veridian_plan
		SET plan = $2,
		    monthly_email_quota = $3,
		    plan_source = COALESCE($4, plan_source),
		    max_contacts = $5,
		    max_seats = $6,
		    max_oauth_accounts = $7,
		    max_custom_domains = $8,
		    max_active_sequences = $9,
		    feature_ab_testing = $10,
		    feature_branding_removed = $11,
		    feature_white_label = $12,
		    history_retention_days = $13,
		    updated_at = $14
		WHERE workspace_id = $1
	`
	res, err := r.systemDB.ExecContext(ctx, q, workspaceID, plan, quota, planSourceArg,
		limits.MaxContacts, limits.MaxSeats, limits.MaxOAuthAccounts, limits.MaxCustomDomains, limits.MaxActiveSequences,
		limits.FeatureABTesting, limits.FeatureBrandingRemoved, limits.FeatureWhiteLabel, limits.HistoryRetentionDays,
		time.Now().UTC())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("veridian_plan: workspace %s not found", workspaceID)
	}
	return nil
}

// Suspend marque le tenant suspended (envois bloques par paywall middleware).
func (r *veridianPlanRepository) Suspend(ctx context.Context, workspaceID, reason string) error {
	now := time.Now().UTC()
	const q = `
		UPDATE veridian_plan
		SET status = 'suspended', suspended_at = $2, suspended_reason = $3, updated_at = $2
		WHERE workspace_id = $1
	`
	res, err := r.systemDB.ExecContext(ctx, q, workspaceID, now, reason)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("veridian_plan: workspace %s not found", workspaceID)
	}
	return nil
}

// Resume reactive un tenant suspended.
func (r *veridianPlanRepository) Resume(ctx context.Context, workspaceID string) error {
	const q = `
		UPDATE veridian_plan
		SET status = 'active', suspended_at = NULL, suspended_reason = NULL, updated_at = $2
		WHERE workspace_id = $1
	`
	res, err := r.systemDB.ExecContext(ctx, q, workspaceID, time.Now().UTC())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("veridian_plan: workspace %s not found", workspaceID)
	}
	return nil
}

// SoftDelete marque le tenant deleted (purge cron 30j).
// CONTRAT-HUB sec. 5.7-5.8 : set deleted_at + purge_eligible_at + lifecycle_reason.
func (r *veridianPlanRepository) SoftDelete(ctx context.Context, workspaceID, reason string) error {
	now := time.Now().UTC()
	purgeEligibleAt := now.Add(veridianPurgeDelayPostgres)
	// reason "" envoye comme NULL pour distinction audit "pas de raison fournie"
	// vs "raison fournie vide" (le second cas est en pratique impossible mais
	// le NULL est plus propre).
	var reasonArg interface{}
	if reason != "" {
		reasonArg = reason
	}
	const q = `
		UPDATE veridian_plan
		SET status = 'deleted',
		    deleted_at = $2,
		    purge_eligible_at = $3,
		    lifecycle_reason = $4,
		    updated_at = $2
		WHERE workspace_id = $1
	`
	res, err := r.systemDB.ExecContext(ctx, q, workspaceID, now, purgeEligibleAt, reasonArg)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("veridian_plan: workspace %s not found", workspaceID)
	}
	return nil
}

// Restore annule un soft-delete : clear deleted_at + purge_eligible_at, set
// restored_at = NOW + lifecycle_reason. Le tenant repasse en active.
// CONTRAT-HUB sec. 5.7-5.8.
func (r *veridianPlanRepository) Restore(ctx context.Context, workspaceID, reason string) error {
	now := time.Now().UTC()
	var reasonArg interface{}
	if reason != "" {
		reasonArg = reason
	}
	// Note : restored_at et updated_at sont passes via deux parametres
	// distincts ($2 et $4) bien que de meme valeur. Cela evite le bug Postgres
	// "inconsistent types deduced for parameter $N" quand le meme parametre
	// est utilise pour deux colonnes de types differents
	// (restored_at TIMESTAMP WITH TIME ZONE vs updated_at TIMESTAMP WITHOUT
	// TIME ZONE — drift V34 vs schema legacy upstream Notifuse).
	const q = `
		UPDATE veridian_plan
		SET status = 'active',
		    deleted_at = NULL,
		    purge_eligible_at = NULL,
		    restored_at = $2,
		    lifecycle_reason = $3,
		    updated_at = $4
		WHERE workspace_id = $1
	`
	res, err := r.systemDB.ExecContext(ctx, q, workspaceID, now, reasonArg, now)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("veridian_plan: workspace %s not found", workspaceID)
	}
	return nil
}

// Purge supprime DEFINITIVEMENT la ligne veridian_plan (hard delete). Le
// service applique la garde "purge_eligible_at < NOW" en amont — ce repo
// l'execute sans condition supplementaire. La raison est passe pour parite
// de signature avec SoftDelete/Restore mais n'est pas stockee (la ligne
// est supprimee). L'audit doit etre fait via le webhook emit cote service.
//
// NB : la suppression de la DB workspace et du user owner est faite par
// WorkspaceService.DeleteWorkspace en amont (cf. service.Purge).
func (r *veridianPlanRepository) Purge(ctx context.Context, workspaceID, reason string) error {
	_ = reason // reason audit-only emitted via webhook
	const q = `DELETE FROM veridian_plan WHERE workspace_id = $1`
	res, err := r.systemDB.ExecContext(ctx, q, workspaceID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("veridian_plan: workspace %s not found", workspaceID)
	}
	return nil
}

// Touch met a jour last_touched_at = NOW. Pas d'autre effet de bord — le
// service applique le debouncing 24h en amont.
//
// Note : last_touched_at et updated_at sont passes via deux parametres
// distincts ($2 et $3) bien que de meme valeur. Cela evite le bug Postgres
// "inconsistent types deduced for parameter $N" quand le meme parametre
// est utilise pour deux colonnes de types differents
// (last_touched_at TIMESTAMP WITH TIME ZONE vs updated_at TIMESTAMP WITHOUT
// TIME ZONE — drift V34 vs schema legacy upstream Notifuse).
func (r *veridianPlanRepository) Touch(ctx context.Context, workspaceID string) error {
	now := time.Now().UTC()
	const q = `
		UPDATE veridian_plan
		SET last_touched_at = $2,
		    updated_at = $3
		WHERE workspace_id = $1
	`
	res, err := r.systemDB.ExecContext(ctx, q, workspaceID, now, now)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("veridian_plan: workspace %s not found", workspaceID)
	}
	return nil
}

// veridianPurgeDelayPostgres : duree entre soft-delete et eligibilite a la
// purge definitive. Constante locale au repo pour eviter une dependance
// circulaire avec le service (qui declare le meme via veridianPurgeDelay).
// Doit rester ALIGNE avec internal/service/veridian_service.go:veridianPurgeDelay.
const veridianPurgeDelayPostgres = 30 * 24 * time.Hour

// IncrementEmailsSent ajoute delta aux compteurs mensuel ET lifetime (atomique),
// puis détecte le franchissement du seuil d'activation trial (5 mails).
//
// Si le seuil vient d'être franchi (lifetimeAfter >= ActivityThresholdEmails
// AND !alreadyReached) :
//  1. MarkActivityThresholdReached (idempotent) — persiste le timestamp
//  2. Émet EventTenantActivityThresholdReached via l'emitter (best-effort)
//
// L'émission webhook est best-effort : si l'emitter est nil ou échoue,
// le timestamp activity_threshold_reached_at reste set en DB — le Hub
// peut réconcilier via polling de GET /api/veridian/limits.
// Si la ligne n'existe pas, no-op silencieux.
func (r *veridianPlanRepository) IncrementEmailsSent(ctx context.Context, workspaceID string, delta int64) error {
	lifetimeAfter, alreadyReached, err := r.IncrementEmailsSentReturning(ctx, workspaceID, delta)
	if err != nil {
		return err
	}
	// Seuil non encore atteint ET on vient de le franchir avec cet incrément.
	if !alreadyReached && lifetimeAfter >= domain.ActivityThresholdEmails {
		now := time.Now().UTC()
		if markErr := r.MarkActivityThresholdReached(ctx, workspaceID, now); markErr != nil {
			// Best-effort : log warn, ne bloque pas l'envoi.
			if r.log != nil {
				r.log.WithFields(map[string]interface{}{
					"workspace_id": workspaceID,
					"error":        markErr.Error(),
				}).Warn("veridian: MarkActivityThresholdReached failed (best-effort, activity_threshold_reached_at not set)")
			}
			return nil
		}
		if r.emitter != nil {
			r.emitter.Emit(ctx, domain.EventTenantActivityThresholdReached, workspaceID, map[string]interface{}{
				"emails_sent_lifetime": lifetimeAfter,
				"threshold":            domain.ActivityThresholdEmails,
				"reached_at":           now.UTC().Format("2006-01-02T15:04:05Z07:00"),
			})
		}
	}
	return nil
}

// IncrementEmailsSentReturning incrémente ATOMIQUEMENT emails_sent_this_month
// ET emails_sent_lifetime, puis retourne (lifetimeAfter, alreadyReached, err).
//
//   - lifetimeAfter : valeur de emails_sent_lifetime APRÈS l'incrément
//   - alreadyReached : true si activity_threshold_reached_at IS NOT NULL
//     avant cet incrément (= seuil déjà marqué, pas d'émission webhook)
//
// Si la ligne n'existe pas (workspace non Veridian-managed), retourne (0, false, nil).
//
// ⚠️ Piège TZ : updated_at est TIMESTAMP WITHOUT TIME ZONE (legacy upstream),
// activity_threshold_reached_at est TIMESTAMP WITH TIME ZONE (V38 additif).
// Ne JAMAIS partager le même $N entre ces deux colonnes dans un même UPDATE
// — bug "inconsistent types deduced for parameter $N" Postgres. updated_at
// est passé via $3, distinct du RETURNING qui lit activity_threshold_reached_at.
func (r *veridianPlanRepository) IncrementEmailsSentReturning(ctx context.Context, workspaceID string, delta int64) (lifetimeAfter int64, alreadyReached bool, err error) {
	const q = `
		UPDATE veridian_plan
		SET emails_sent_this_month = emails_sent_this_month + $2,
		    emails_sent_lifetime   = emails_sent_lifetime + $2,
		    updated_at = $3
		WHERE workspace_id = $1
		RETURNING emails_sent_lifetime, activity_threshold_reached_at
	`
	now := time.Now().UTC()
	var thresholdReachedAt sql.NullTime
	scanErr := r.systemDB.QueryRowContext(ctx, q, workspaceID, delta, now).
		Scan(&lifetimeAfter, &thresholdReachedAt)
	if scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			// Workspace non géré par Veridian — no-op silencieux.
			return 0, false, nil
		}
		return 0, false, scanErr
	}
	alreadyReached = thresholdReachedAt.Valid
	return lifetimeAfter, alreadyReached, nil
}

// MarkActivityThresholdReached set activity_threshold_reached_at = at pour
// le workspace donné, uniquement si le champ est encore NULL (idempotent).
//
//   - Si le champ est déjà set (seuil déjà marqué lors d'un incrément
//     concurrent), l'UPDATE retourne 0 rows affected — no-op silencieux.
//   - Pas de vérification du nombre de rows affectées : l'idempotence est
//     garantie par le WHERE, le caller n'a pas besoin de distinguer "first set"
//     de "already set" à ce stade (la détection se fait via alreadyReached
//     dans IncrementEmailsSentReturning).
//
// ⚠️ Piège TZ : activity_threshold_reached_at est TIMESTAMP WITH TIME ZONE (V38).
// updated_at est TIMESTAMP WITHOUT TIME ZONE (legacy). Même valeur `at`, mais
// passée via deux paramètres distincts ($2 et $3) pour éviter le bug Postgres
// "inconsistent types". Cf. memory feedback_sqlmock_does_not_validate_postgres_types.
func (r *veridianPlanRepository) MarkActivityThresholdReached(ctx context.Context, workspaceID string, at time.Time) error {
	const q = `
		UPDATE veridian_plan
		SET activity_threshold_reached_at = $2,
		    updated_at = $3
		WHERE workspace_id = $1 AND activity_threshold_reached_at IS NULL
	`
	_, err := r.systemDB.ExecContext(ctx, q, workspaceID, at, at)
	return err
}

// === Veridian patch === HardDelete supprime definitivement la ligne veridian_plan
// (pas un soft delete). Reserve aux tests / admin platform. La purge des donnees
// workspace (table workspaces upstream + DB postgres dediee) est faite en amont
// par WorkspaceService.DeleteWorkspace.
func (r *veridianPlanRepository) HardDelete(ctx context.Context, workspaceID string) error {
	const q = `DELETE FROM veridian_plan WHERE workspace_id = $1`
	_, err := r.systemDB.ExecContext(ctx, q, workspaceID)
	return err
}

// ListByPrefix retourne tous les workspace_id matchant un prefix SQL LIKE.
// Utilise par WipeTestTenants pour trouver les tenants de test a supprimer.
// Le caller est responsable d'echapper les wildcards SQL ('%', '_') s'ils ne
// sont pas voulus.
func (r *veridianPlanRepository) ListByPrefix(ctx context.Context, prefix string) ([]string, error) {
	if prefix == "" {
		return nil, nil
	}
	const q = `SELECT workspace_id FROM veridian_plan WHERE workspace_id LIKE $1 ORDER BY workspace_id`
	rows, err := r.systemDB.QueryContext(ctx, q, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ResetMonthlyCounters remet a zero emails_sent_this_month pour tous les workspaces
// dont last_reset_at < debut du mois courant. Appele par cron (1er du mois).
// Retourne le nombre de lignes mises a jour.
func (r *veridianPlanRepository) ResetMonthlyCounters(ctx context.Context) (int64, error) {
	const q = `
		UPDATE veridian_plan
		SET emails_sent_this_month = 0, last_reset_at = $1, updated_at = $1
		WHERE date_trunc('month', last_reset_at) < date_trunc('month', $1::timestamp)
	`
	res, err := r.systemDB.ExecContext(ctx, q, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
