package repository

// Veridian fork — repo Postgres pour le tracking d'idempotence IMAP (Lot 1
// sprint cold outbound, 2026-06-15). Table SYSTÈME veridian_imap_uid_seen
// (migration V50). Voir internal/domain/veridian_imap_integration.go.
//
// Clé logique : (workspace_id, integration_id, folder, uid_validity, uid).
// uid_validity dans la clé est obligatoire (cf. RFC 3501) : si le serveur
// change le UIDVALIDITY d'un dossier, les anciens UID ne désignent plus les
// mêmes messages.

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/Notifuse/notifuse/internal/domain"
)

// veridianIMAPUIDSeenRepository implémente domain.VeridianIMAPUIDSeenRepository.
type veridianIMAPUIDSeenRepository struct {
	systemDB *sql.DB
}

// NewVeridianIMAPUIDSeenRepository crée le repo (table système).
func NewVeridianIMAPUIDSeenRepository(systemDB *sql.DB) domain.VeridianIMAPUIDSeenRepository {
	return &veridianIMAPUIDSeenRepository{systemDB: systemDB}
}

// FilterUnseen retourne, parmi `uids`, ceux qui ne sont PAS encore dans la table
// pour la clé donnée. Implémentation : SELECT des UID connus dans le lot, puis
// différence en mémoire (le lot est borné par maxIMAPMessagesPerPoll côté
// service). On utilise Squirrel pour l'IN(...) paramétré (jamais de concat).
func (r *veridianIMAPUIDSeenRepository) FilterUnseen(
	ctx context.Context,
	workspaceID, integrationID, folder string,
	uidValidity uint32,
	uids []uint32,
) ([]uint32, error) {
	if len(uids) == 0 {
		return nil, nil
	}

	// Convertir en []interface{} pour l'IN clause Squirrel.
	uidArgs := make([]interface{}, len(uids))
	for i, u := range uids {
		uidArgs[i] = int64(u)
	}

	query, args, err := sq.
		Select("uid").
		From("veridian_imap_uid_seen").
		Where(sq.Eq{
			"workspace_id":   workspaceID,
			"integration_id": integrationID,
			"folder":         folder,
			"uid_validity":   int64(uidValidity),
			"uid":            uidArgs,
		}).
		PlaceholderFormat(sq.Dollar).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("build filter unseen query: %w", err)
	}

	rows, err := r.systemDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query seen uids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	seen := make(map[uint32]struct{})
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan seen uid: %w", err)
		}
		seen[uint32(uid)] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate seen uids: %w", err)
	}

	unseen := make([]uint32, 0, len(uids))
	for _, u := range uids {
		if _, ok := seen[u]; !ok {
			unseen = append(unseen, u)
		}
	}
	return unseen, nil
}

// MarkSeen insère le lot d'UID (idempotent : ON CONFLICT DO NOTHING).
func (r *veridianIMAPUIDSeenRepository) MarkSeen(
	ctx context.Context,
	workspaceID, integrationID, folder string,
	uidValidity uint32,
	uids []uint32,
) error {
	if len(uids) == 0 {
		return nil
	}

	insert := sq.
		Insert("veridian_imap_uid_seen").
		Columns("workspace_id", "integration_id", "folder", "uid_validity", "uid")
	for _, u := range uids {
		insert = insert.Values(workspaceID, integrationID, folder, int64(uidValidity), int64(u))
	}

	query, args, err := insert.
		Suffix("ON CONFLICT (workspace_id, integration_id, folder, uid_validity, uid) DO NOTHING").
		PlaceholderFormat(sq.Dollar).
		ToSql()
	if err != nil {
		return fmt.Errorf("build mark seen query: %w", err)
	}

	if _, err := r.systemDB.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("insert seen uids: %w", err)
	}
	return nil
}
