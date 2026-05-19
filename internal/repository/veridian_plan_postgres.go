package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// veridianPlanRepository implements domain.VeridianPlanRepository.
type veridianPlanRepository struct {
	systemDB *sql.DB
}

// NewVeridianPlanRepository cree un repo PostgreSQL pour la table veridian_plan.
func NewVeridianPlanRepository(systemDB *sql.DB) domain.VeridianPlanRepository {
	return &veridianPlanRepository{systemDB: systemDB}
}

// Get recupere une ligne par workspace_id. Retourne sql.ErrNoRows si absent.
func (r *veridianPlanRepository) Get(ctx context.Context, workspaceID string) (*domain.VeridianPlan, error) {
	const q = `
		SELECT workspace_id, plan, plan_source, status, monthly_email_quota, emails_sent_this_month,
		       last_reset_at, suspended_at, suspended_reason, deleted_at, created_at, updated_at
		FROM veridian_plan
		WHERE workspace_id = $1
	`
	var p domain.VeridianPlan
	var status, planSource string
	var suspendedAt, deletedAt sql.NullTime
	var suspendedReason sql.NullString

	err := r.systemDB.QueryRowContext(ctx, q, workspaceID).Scan(
		&p.WorkspaceID, &p.Plan, &planSource, &status, &p.MonthlyEmailQuota, &p.EmailsSentThisMonth,
		&p.LastResetAt, &suspendedAt, &suspendedReason, &deletedAt, &p.CreatedAt, &p.UpdatedAt,
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
	return &p, nil
}

// Upsert insere ou remplace une ligne complete.
//
// Important : plan_source utilise COALESCE pour PRESERVER la valeur existante
// au moment du ON CONFLICT si l'appelant passe une chaine vide. Cela evite
// qu'un re-provision via Hub (qui peut ne pas envoyer plan_source) ecrase
// silencieusement un lifetime_partner par 'stripe'. Sur INSERT pur, la
// chaine vide est convertie en 'stripe' par le COALESCE($2, 'stripe').
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
			last_reset_at, suspended_at, suspended_reason, deleted_at, created_at, updated_at
		) VALUES ($1,$2,COALESCE($3,'stripe'),$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (workspace_id) DO UPDATE SET
			plan = EXCLUDED.plan,
			plan_source = COALESCE(EXCLUDED.plan_source, veridian_plan.plan_source),
			status = EXCLUDED.status,
			monthly_email_quota = EXCLUDED.monthly_email_quota,
			updated_at = EXCLUDED.updated_at
	`
	_, err := r.systemDB.ExecContext(ctx, q,
		p.WorkspaceID, p.Plan, planSourceArg, string(p.Status), p.MonthlyEmailQuota, p.EmailsSentThisMonth,
		p.LastResetAt, p.SuspendedAt, p.SuspendedReason, p.DeletedAt, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

// UpdatePlan change le plan + quota + plan_source d'un workspace existant.
// No-op si absent.
//
// Si planSource est vide, la colonne plan_source est preservee via COALESCE
// (pas d'ecrasement implicite par 'stripe' — protection contre les appels Hub
// legacy qui n'envoient pas plan_source).
func (r *veridianPlanRepository) UpdatePlan(ctx context.Context, workspaceID, plan string, quota int64, planSource domain.PlanSource) error {
	var planSourceArg interface{}
	if planSource != "" {
		planSourceArg = string(planSource)
	} else {
		planSourceArg = nil
	}

	const q = `
		UPDATE veridian_plan
		SET plan = $2,
		    monthly_email_quota = $3,
		    plan_source = COALESCE($4, plan_source),
		    updated_at = $5
		WHERE workspace_id = $1
	`
	res, err := r.systemDB.ExecContext(ctx, q, workspaceID, plan, quota, planSourceArg, time.Now().UTC())
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
func (r *veridianPlanRepository) SoftDelete(ctx context.Context, workspaceID string) error {
	now := time.Now().UTC()
	const q = `
		UPDATE veridian_plan
		SET status = 'deleted', deleted_at = $2, updated_at = $2
		WHERE workspace_id = $1
	`
	res, err := r.systemDB.ExecContext(ctx, q, workspaceID, now)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("veridian_plan: workspace %s not found", workspaceID)
	}
	return nil
}

// IncrementEmailsSent ajoute delta au compteur de mails envoyes ce mois (atomique).
// Si la ligne n'existe pas, no-op silencieux (le workspace n'est pas suivi par Veridian).
func (r *veridianPlanRepository) IncrementEmailsSent(ctx context.Context, workspaceID string, delta int64) error {
	const q = `
		UPDATE veridian_plan
		SET emails_sent_this_month = emails_sent_this_month + $2, updated_at = $3
		WHERE workspace_id = $1
	`
	_, err := r.systemDB.ExecContext(ctx, q, workspaceID, delta, time.Now().UTC())
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
