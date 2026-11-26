// Package iafils range les fils de l'assistant « Mon IA » dans la base de l'app (VER-203, CONTRAT-HUB §5.24) et dit à la console quand
// montrer la fenêtre (§5.24.1). Fiche 99.
//
// Le Hub lit et écrit le fil par GET/PUT /api/hub/ia/fils/{session}, serveur à serveur, signé v2. Le fil est une donnée de l'organisation
// et de la personne : il suit leur sort (organisation supprimée, membre retiré), et une personne qui n'est plus membre ne le lit plus.
package iafils

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Refus de résolution : l'espace de l'organisation, puis la place de la personne dedans.
var (
	ErrUnlinked  = errors.New("iafils: organisation non reliée à un espace")
	ErrNotMember = errors.New("iafils: la personne n'est pas membre de l'espace")
)

// Store lit et écrit la table veridian_ia_fils (V87) et résout l'espace.
type Store struct{ DB *sql.DB }

// Resolve rend l'espace VIVANT relié à l'organisation du Hub, si la personne (hub_user_id, jamais l'adresse) en est membre
// et que son compte n'est pas supprimé au Hub. Le lien est veridian_plan.hub_tenant_id (fiche 88), comme pour les actions.
func (s *Store) Resolve(ctx context.Context, hubTenantID, hubUserID string) (string, error) {
	var ws string
	err := s.DB.QueryRowContext(ctx, `SELECT p.workspace_id FROM veridian_plan p JOIN workspaces w ON w.id = p.workspace_id
		WHERE p.hub_tenant_id = $1 AND p.deleted_at IS NULL ORDER BY p.workspace_id LIMIT 1`, hubTenantID).Scan(&ws)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnlinked
	}
	if err != nil {
		return "", err
	}
	var member bool
	err = s.DB.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM users u JOIN user_workspaces uw ON uw.user_id = u.id
		WHERE u.hub_user_id::text = $1 AND u.type = 'user' AND u.hub_deleted_at IS NULL AND uw.workspace_id = $2)`, hubUserID, ws).Scan(&member)
	if err != nil {
		return "", err
	}
	if !member {
		return "", ErrNotMember
	}
	return ws, nil
}

// Owner rend l'auteur d'une session déjà rangée. found=false : session libre.
func (s *Store) Owner(ctx context.Context, session string) (hubUserID, hubTenantID string, found bool, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT hub_user_id, hub_tenant_id FROM veridian_ia_fils WHERE session_id = $1::uuid`, session).Scan(&hubUserID, &hubTenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return hubUserID, hubTenantID, err == nil, err
}

// Get rend le fil de CE triplet (session, personne, organisation). ok=false : absent OU d'un autre (même réponse).
func (s *Store) Get(ctx context.Context, session, hubUserID, hubTenantID string) (json.RawMessage, bool, error) {
	var raw []byte
	err := s.DB.QueryRowContext(ctx, `SELECT messages FROM veridian_ia_fils WHERE session_id = $1::uuid AND hub_user_id = $2 AND hub_tenant_id = $3`,
		session, hubUserID, hubTenantID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// Put remplace le fil entier, le crée s'il manque. ok=false : la session appartient à un autre triplet, rien n'est écrit
// (une seule instruction : la course entre deux auteurs ne laisse jamais passer l'un par-dessus l'autre).
func (s *Store) Put(ctx context.Context, session, workspaceID, hubUserID, hubTenantID string, messages json.RawMessage, count int) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO veridian_ia_fils AS f (session_id, workspace_id, hub_user_id, hub_tenant_id, messages, message_count)
		VALUES ($1::uuid, $2, $3, $4, $5::json, $6)
		ON CONFLICT (session_id) DO UPDATE
			SET messages = EXCLUDED.messages, message_count = EXCLUDED.message_count, workspace_id = EXCLUDED.workspace_id, updated_at = NOW()
			WHERE f.hub_user_id = EXCLUDED.hub_user_id AND f.hub_tenant_id = EXCLUDED.hub_tenant_id`,
		session, workspaceID, hubUserID, hubTenantID, string(messages), count)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteWorkspace supprime les fils d'un espace (organisation supprimée). Rend le nombre supprimé.
func (s *Store) DeleteWorkspace(ctx context.Context, workspaceID string) (int, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM veridian_ia_fils WHERE workspace_id = $1`, workspaceID)
	return affected(res, err)
}

// DeleteMember supprime les fils d'une personne dans un espace (retrait du membre). Rend le nombre supprimé.
func (s *Store) DeleteMember(ctx context.Context, workspaceID, hubUserID string) (int, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM veridian_ia_fils WHERE workspace_id = $1 AND hub_user_id = $2`, workspaceID, hubUserID)
	return affected(res, err)
}

func affected(res sql.Result, err error) (int, error) {
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// HubUserOf rend le hub_user_id de l'utilisateur de l'app, "" si son compte n'est pas relié au Hub ou y est supprimé.
func (s *Store) HubUserOf(ctx context.Context, userID string) (string, error) {
	var v sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT hub_user_id::text FROM users WHERE id = $1 AND hub_deleted_at IS NULL`, userID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v.String, err
}

// HubTenantOf rend l'organisation du Hub reliée à l'espace (veridian_plan.hub_tenant_id), "" sans lien.
func (s *Store) HubTenantOf(ctx context.Context, workspaceID string) (string, error) {
	var v sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT hub_tenant_id FROM veridian_plan WHERE workspace_id = $1 AND deleted_at IS NULL`, workspaceID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v.String, err
}
