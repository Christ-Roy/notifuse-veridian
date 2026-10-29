package policy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Store lit et écrit les tables du module dans la base du workspace (V66 :
// veridian_policy_override, veridian_send_priority, veridian_policy_settings).
type Store interface {
	ListOverrides(ctx context.Context, workspaceID string) ([]Override, error)
	// UpsertOverride pose les valeurs non nulles de o et conserve les autres.
	UpsertOverride(ctx context.Context, workspaceID string, o Override) error
	// ClearOverride retire les règles nommées (toutes si rules est vide) ; la ligne disparaît
	// quand plus aucune valeur n'est posée. Rend vrai si quelque chose a été retiré.
	ClearOverride(ctx context.Context, workspaceID, class string, rules []string) (bool, error)

	// ListPriorities rend les priorités posées, clé "type:id".
	ListPriorities(ctx context.Context, workspaceID string) (map[string]PrioritySource, error)
	// ListSources rend toutes les campagnes et automations avec leur priorité effective.
	ListSources(ctx context.Context, workspaceID string) ([]PrioritySource, error)
	// SourceName rend le nom de la source ; ok=false si elle n'existe pas.
	SourceName(ctx context.Context, workspaceID, sourceType, sourceID string) (string, bool, error)
	// SetPriority pose la priorité (ligne + mails en attente) dans UNE transaction.
	SetPriority(ctx context.Context, workspaceID, sourceType, sourceID string, priority int, by string) (int64, error)
	// ClearPriority retire la ligne et remet les mails en attente à la priorité par défaut.
	ClearPriority(ctx context.Context, workspaceID, sourceType, sourceID string) (int64, error)

	// GetSetting / SetSetting : réglages du module (JSON), par clé.
	GetSetting(ctx context.Context, workspaceID, key string) ([]byte, error)
	SetSetting(ctx context.Context, workspaceID, key string, value []byte, by string) error
}

// ErrNotFound : la source (campagne ou automation) n'existe pas.
var ErrNotFound = errors.New("not found")

type pgStore struct {
	conn func(ctx context.Context, workspaceID string) (*sql.DB, error)
}

// NewStore crée le dépôt sur les connexions de workspace du dépôt de workspaces.
func NewStore(workspaces domain.WorkspaceRepository) Store {
	return &pgStore{conn: workspaces.GetConnection}
}

// NewStoreWithDB crée le dépôt sur une connexion directe (tests).
func NewStoreWithDB(db *sql.DB) Store {
	return &pgStore{conn: func(context.Context, string) (*sql.DB, error) { return db, nil }}
}

func (s *pgStore) ListOverrides(ctx context.Context, ws string) ([]Override, error) {
	db, err := s.conn(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("connexion workspace: %w", err)
	}
	rows, err := db.QueryContext(ctx, `SELECT class, COALESCE(daily_cap,0), COALESCE(rate_per_min,0)::float8,
		COALESCE(warmup_share,0)::float8, COALESCE(slowdown_threshold,0)::float8,
		COALESCE(reason,''), COALESCE(updated_by,''), updated_at
		FROM veridian_policy_override ORDER BY class`)
	if err != nil {
		return nil, fmt.Errorf("lecture des surcharges: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Override
	for rows.Next() {
		var o Override
		if err := rows.Scan(&o.Class, &o.DailyCap, &o.RatePerMin, &o.WarmupShare, &o.SlowdownThreshold, &o.Reason, &o.UpdatedBy, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *pgStore) UpsertOverride(ctx context.Context, ws string, o Override) error {
	db, err := s.conn(ctx, ws)
	if err != nil {
		return fmt.Errorf("connexion workspace: %w", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO veridian_policy_override
		(class, daily_cap, rate_per_min, warmup_share, slowdown_threshold, reason, updated_by, updated_at)
		VALUES ($1, NULLIF($2::int,0), NULLIF($3::numeric,0), NULLIF($4::numeric,0), NULLIF($5::numeric,0), NULLIF($6::text,''), NULLIF($7::text,''), NOW())
		ON CONFLICT (class) DO UPDATE SET
			daily_cap = COALESCE(EXCLUDED.daily_cap, veridian_policy_override.daily_cap),
			rate_per_min = COALESCE(EXCLUDED.rate_per_min, veridian_policy_override.rate_per_min),
			warmup_share = COALESCE(EXCLUDED.warmup_share, veridian_policy_override.warmup_share),
			slowdown_threshold = COALESCE(EXCLUDED.slowdown_threshold, veridian_policy_override.slowdown_threshold),
			reason = COALESCE(EXCLUDED.reason, veridian_policy_override.reason),
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()`,
		o.Class, o.DailyCap, o.RatePerMin, o.WarmupShare, o.SlowdownThreshold, o.Reason, o.UpdatedBy)
	if err != nil {
		return fmt.Errorf("écriture de la surcharge: %w", err)
	}
	return nil
}

var ruleColumn = map[string]string{
	RuleDailyCap: "daily_cap", RuleRatePerMin: "rate_per_min",
	RuleWarmupShare: "warmup_share", RuleSlowdownThreshold: "slowdown_threshold",
}

func (s *pgStore) ClearOverride(ctx context.Context, ws, class string, rules []string) (bool, error) {
	db, err := s.conn(ctx, ws)
	if err != nil {
		return false, fmt.Errorf("connexion workspace: %w", err)
	}
	if len(rules) == 0 {
		rules = AllRules
	}
	sets := make([]string, 0, len(rules))
	for _, r := range rules {
		col, ok := ruleColumn[r] // liste blanche : jamais de nom de colonne venu de l'extérieur
		if !ok {
			return false, fmt.Errorf("règle inconnue %q", r)
		}
		sets = append(sets, col+" = NULL")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE veridian_policy_override SET `+strings.Join(sets, ", ")+`, updated_at = NOW() WHERE class = $1`, class)
	if err != nil {
		return false, fmt.Errorf("retrait de la surcharge: %w", err)
	}
	n, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `DELETE FROM veridian_policy_override WHERE class = $1
		AND daily_cap IS NULL AND rate_per_min IS NULL AND warmup_share IS NULL AND slowdown_threshold IS NULL`, class); err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

func (s *pgStore) ListPriorities(ctx context.Context, ws string) (map[string]PrioritySource, error) {
	db, err := s.conn(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("connexion workspace: %w", err)
	}
	rows, err := db.QueryContext(ctx, `SELECT source_type, source_id, priority, COALESCE(updated_by,''), updated_at FROM veridian_send_priority`)
	if err != nil {
		return nil, fmt.Errorf("lecture des priorités: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]PrioritySource{}
	for rows.Next() {
		var p PrioritySource
		if err := rows.Scan(&p.SourceType, &p.SourceID, &p.Priority, &p.UpdatedBy, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Explicit = true
		out[p.SourceType+":"+p.SourceID] = p
	}
	return out, rows.Err()
}

func (s *pgStore) ListSources(ctx context.Context, ws string) ([]PrioritySource, error) {
	db, err := s.conn(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("connexion workspace: %w", err)
	}
	prios, err := s.ListPriorities(ctx, ws)
	if err != nil {
		return nil, err
	}
	pending := map[string]int{}
	prow, err := db.QueryContext(ctx, `SELECT source_type, source_id, COUNT(*) FROM email_queue WHERE status IN ('pending','paused') GROUP BY 1, 2`)
	if err != nil {
		return nil, fmt.Errorf("lecture de la file: %w", err)
	}
	for prow.Next() {
		var st, id string
		var n int
		if err := prow.Scan(&st, &id, &n); err != nil {
			_ = prow.Close()
			return nil, err
		}
		pending[st+":"+id] = n
	}
	_ = prow.Close()
	if err := prow.Err(); err != nil {
		return nil, err
	}

	var out []PrioritySource
	add := func(query, st string) error {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("lecture des %s: %w", st, err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			p := PrioritySource{SourceType: st, Priority: domain.EmailQueuePriorityMarketing}
			if err := rows.Scan(&p.SourceID, &p.Name, &p.Status); err != nil {
				return err
			}
			key := st + ":" + p.SourceID
			if e, ok := prios[key]; ok {
				p.Priority, p.Explicit, p.UpdatedBy, p.UpdatedAt = e.Priority, true, e.UpdatedBy, e.UpdatedAt
			}
			p.Pending = pending[key]
			out = append(out, p)
		}
		return rows.Err()
	}
	if err := add(`SELECT id, name, status FROM broadcasts ORDER BY created_at DESC`, SourceTypeBroadcast); err != nil {
		return nil, err
	}
	if err := add(`SELECT id, name, COALESCE(status,'') FROM automations WHERE deleted_at IS NULL ORDER BY created_at DESC`, SourceTypeAutomation); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *pgStore) SourceName(ctx context.Context, ws, sourceType, sourceID string) (string, bool, error) {
	db, err := s.conn(ctx, ws)
	if err != nil {
		return "", false, fmt.Errorf("connexion workspace: %w", err)
	}
	var q string
	switch sourceType {
	case SourceTypeBroadcast:
		q = `SELECT name FROM broadcasts WHERE id = $1`
	case SourceTypeAutomation:
		q = `SELECT name FROM automations WHERE id = $1 AND deleted_at IS NULL`
	default:
		return "", false, fmt.Errorf("source_type doit être broadcast ou automation")
	}
	var name string
	if err := db.QueryRowContext(ctx, q, sourceID).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return name, true, nil
}

// Le transactionnel de séquence (priorité 1) n'est jamais touché : seuls les mails de
// priorité >= 2 sont réécrits. Les mails en pause sont inclus : à la reprise ils repartent
// avec la nouvelle priorité.
const requeueSQL = `UPDATE email_queue SET priority = $1, updated_at = NOW()
	WHERE source_type = $2 AND source_id = $3 AND status IN ('pending','paused') AND priority >= 2 AND priority <> $1`

func (s *pgStore) SetPriority(ctx context.Context, ws, st, id string, priority int, by string) (int64, error) {
	if err := domain.VeridianValidateMarketingPriority(priority); err != nil {
		return 0, err
	}
	db, err := s.conn(ctx, ws)
	if err != nil {
		return 0, fmt.Errorf("connexion workspace: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO veridian_send_priority (source_type, source_id, priority, updated_by, updated_at)
		VALUES ($1, $2, $3, NULLIF($4,''), NOW())
		ON CONFLICT (source_type, source_id) DO UPDATE SET priority = EXCLUDED.priority, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		st, id, priority, by); err != nil {
		return 0, fmt.Errorf("écriture de la priorité: %w", err)
	}
	res, err := tx.ExecContext(ctx, requeueSQL, priority, st, id)
	if err != nil {
		return 0, fmt.Errorf("mise à jour des mails en attente: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, tx.Commit()
}

func (s *pgStore) ClearPriority(ctx context.Context, ws, st, id string) (int64, error) {
	db, err := s.conn(ctx, ws)
	if err != nil {
		return 0, fmt.Errorf("connexion workspace: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM veridian_send_priority WHERE source_type = $1 AND source_id = $2`, st, id); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, requeueSQL, domain.EmailQueuePriorityMarketing, st, id)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, tx.Commit()
}

func (s *pgStore) GetSetting(ctx context.Context, ws, key string) ([]byte, error) {
	db, err := s.conn(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("connexion workspace: %w", err)
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT value FROM veridian_policy_settings WHERE key = $1`, key).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return raw, nil
}

func (s *pgStore) SetSetting(ctx context.Context, ws, key string, value []byte, by string) error {
	db, err := s.conn(ctx, ws)
	if err != nil {
		return fmt.Errorf("connexion workspace: %w", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO veridian_policy_settings (key, value, updated_by, updated_at)
		VALUES ($1, $2::jsonb, NULLIF($3,''), NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = NOW()`,
		key, string(value), by)
	if err != nil {
		return fmt.Errorf("écriture du réglage: %w", err)
	}
	return nil
}
