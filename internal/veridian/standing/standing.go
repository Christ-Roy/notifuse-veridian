// Package standing dit, d'une seule lecture, ce qu'une personne a encore le droit de faire dans un espace : espace vivant, membre, ni gelée,
// ni supprimée au Hub, espace ni fermé ni suspendu (fiche 91). Relu à chaque passage par tout ce qui dure plus qu'une requête : accès de
// boîte (IMAP, SMTP) et flux de la boîte unifiée. Retirer quelqu'un coupe ces portes sans action de plus.
package standing

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Notifuse/notifuse/internal/veridian/accountgate"
	"github.com/google/uuid"
)

// State : situation relue en base.
type State struct {
	Workspace bool // l'espace existe
	Closed    bool // espace supprimé côté Hub (soft-delete) ou organisation suspendue (admin_action) : ni lecture ni écriture
	Suspended bool // espace suspendu : lecture seule (CONTRAT-HUB §5.4)
	Member    bool
	Frozen    bool // membre gelé (§5.21)
	Deleted   bool // compte supprimé côté Hub (V71), à partir de accountgate.Since
}

// Space : l'espace est ouvert en lecture, sans regarder la personne.
func (s State) Space() bool { return s.Workspace && !s.Closed }

// Person : l'espace est ouvert et la personne y a encore sa place.
func (s State) Person() bool { return s.Space() && s.Member && !s.Frozen && !s.Deleted }

// Store lit la base système.
type Store struct{ DB *sql.DB }

// Of relit la situation. userID vide ou qui n'est pas un UUID : jamais membre (l'appelant décide s'il regarde la personne).
func (s *Store) Of(ctx context.Context, workspaceID, userID string) (State, error) {
	var st State
	uid := uuid.Nil
	if id, err := uuid.Parse(userID); err == nil {
		uid = id
	}
	err := s.DB.QueryRowContext(ctx, `
		SELECT COALESCE(p.deleted_at IS NOT NULL OR p.status = 'deleted' OR (p.status = 'suspended' AND p.suspended_reason = 'admin_action'), FALSE),
			COALESCE(p.status = 'suspended' AND p.suspended_reason IS DISTINCT FROM 'admin_action', FALSE),
			EXISTS (SELECT 1 FROM user_workspaces uw WHERE uw.workspace_id = w.id AND uw.user_id = $2::uuid),
			EXISTS (SELECT 1 FROM veridian_frozen_members f WHERE f.workspace_id = w.id AND f.user_id = $2::text),
			EXISTS (SELECT 1 FROM users u WHERE u.id = $2::uuid AND u.hub_deleted_at >= $3)
		FROM workspaces w LEFT JOIN veridian_plan p ON p.workspace_id = w.id WHERE w.id = $1`, workspaceID, uid.String(), accountgate.Since()).
		Scan(&st.Closed, &st.Suspended, &st.Member, &st.Frozen, &st.Deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	st.Workspace = true
	return st, nil
}
