package folders

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"
)

// Querier : ce que partagent *sql.DB et *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Lecture des objets vivants dans les tables upstream. Seul endroit qui connait leur schema.
// Meme definition de « vivant » que les repertoires upstream (segments non supprimes, listes
// et modeles sans deleted_at, derniere version d'un modele).
var sourceSQL = map[Kind]string{
	KindSegments: `SELECT id, name FROM segments WHERE status <> 'deleted'`,
	KindLists:    `SELECT id, name FROM lists WHERE deleted_at IS NULL`,
	KindTemplates: `SELECT t.id, t.name FROM templates t
		JOIN (SELECT id, MAX(version) AS v FROM templates GROUP BY id) lv ON t.id = lv.id AND t.version = lv.v
		WHERE t.deleted_at IS NULL`,
}

// Ecriture du nom de compatibilite dans la table upstream (nom seul : ni version, ni date de
// modification, ni reconstruction du segment).
var renameSQL = map[Kind]string{
	KindSegments: `UPDATE segments s SET name = u.name FROM unnest($1::text[], $2::text[]) AS u(id, name) WHERE s.id = u.id`,
	KindLists:    `UPDATE lists l SET name = u.name FROM unnest($1::text[], $2::text[]) AS u(id, name) WHERE l.id = u.id`,
	KindTemplates: `UPDATE templates t SET name = u.name FROM unnest($1::text[], $2::text[]) AS u(id, name)
		WHERE t.id = u.id AND t.version = (SELECT MAX(version) FROM templates WHERE id = t.id)`,
}

// Lock prend le verrou consultatif de l'arbre d'un genre jusqu'a la fin de la transaction :
// deux operations simultanees sur le meme arbre se serialisent.
func Lock(ctx context.Context, q Querier, kind Kind) error {
	_, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "veridian_folders:"+string(kind))
	return err
}

// Load lit l'etat complet d'un genre (dossiers, appartenances, objets vivants).
func Load(ctx context.Context, q Querier, kind Kind) (*State, error) {
	src, ok := sourceSQL[kind]
	if !ok {
		return nil, invalid("genre inconnu %q", kind)
	}
	st := NewState(kind)
	rows, err := q.QueryContext(ctx, `SELECT id, COALESCE(parent_id, ''), name, position FROM veridian_folders WHERE kind = $1`, string(kind))
	if err != nil {
		return nil, fmt.Errorf("lecture des dossiers : %w", err)
	}
	for rows.Next() {
		f := &Folder{Kind: kind}
		if err := rows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Position); err != nil {
			_ = rows.Close()
			return nil, err
		}
		st.Folders[f.ID] = f
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT item_id, COALESCE(folder_id, ''), position FROM veridian_folder_items WHERE kind = $1`, string(kind))
	if err != nil {
		return nil, fmt.Errorf("lecture des appartenances : %w", err)
	}
	for rows.Next() {
		p := &Placement{}
		if err := rows.Scan(&p.ItemID, &p.FolderID, &p.Position); err != nil {
			_ = rows.Close()
			return nil, err
		}
		st.Place[p.ItemID] = p
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("lecture des %s : %w", kind, err)
	}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		st.Items[it.ID] = it
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	return st, nil
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

// Apply ecrit un diff. A appeler dans une transaction (les contraintes sont differees).
func Apply(ctx context.Context, q Querier, kind Kind, d Diff) error {
	if len(d.FolderUpserts) > 0 {
		ids, parents, names, positions := []string{}, []string{}, []string{}, []int64{}
		for _, f := range d.FolderUpserts {
			ids, parents, names, positions = append(ids, f.ID), append(parents, f.ParentID), append(names, f.Name), append(positions, int64(f.Position))
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO veridian_folders (id, kind, parent_id, name, position, updated_at)
			SELECT u.id, $1, NULLIF(u.parent_id, ''), u.name, u.position, NOW()
			FROM unnest($2::text[], $3::text[], $4::text[], $5::int[]) AS u(id, parent_id, name, position)
			ON CONFLICT (id) DO UPDATE SET parent_id = EXCLUDED.parent_id, name = EXCLUDED.name,
				position = EXCLUDED.position, updated_at = NOW()`,
			string(kind), pq.Array(ids), pq.Array(parents), pq.Array(names), pq.Array(positions)); err != nil {
			return fmt.Errorf("ecriture des dossiers : %w", err)
		}
	}
	if len(d.PlaceUpserts) > 0 {
		ids, folders, positions := []string{}, []string{}, []int64{}
		for _, p := range d.PlaceUpserts {
			ids, folders, positions = append(ids, p.ItemID), append(folders, p.FolderID), append(positions, int64(p.Position))
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO veridian_folder_items (kind, item_id, folder_id, position, updated_at)
			SELECT $1, u.item_id, NULLIF(u.folder_id, ''), u.position, NOW()
			FROM unnest($2::text[], $3::text[], $4::int[]) AS u(item_id, folder_id, position)
			ON CONFLICT (kind, item_id) DO UPDATE SET folder_id = EXCLUDED.folder_id,
				position = EXCLUDED.position, updated_at = NOW()`,
			string(kind), pq.Array(ids), pq.Array(folders), pq.Array(positions)); err != nil {
			return fmt.Errorf("ecriture des appartenances : %w", err)
		}
	}
	if len(d.PlaceDeletes) > 0 {
		if _, err := q.ExecContext(ctx, `DELETE FROM veridian_folder_items WHERE kind = $1 AND item_id = ANY($2::text[])`,
			string(kind), pq.Array(d.PlaceDeletes)); err != nil {
			return fmt.Errorf("suppression des appartenances : %w", err)
		}
	}
	if len(d.FolderDeletes) > 0 {
		if _, err := q.ExecContext(ctx, `DELETE FROM veridian_folders WHERE kind = $1 AND id = ANY($2::text[])`,
			string(kind), pq.Array(d.FolderDeletes)); err != nil {
			return fmt.Errorf("suppression des dossiers : %w", err)
		}
	}
	if len(d.Renames) > 0 {
		ids, names := make([]string, len(d.Renames)), make([]string, len(d.Renames))
		for i, r := range d.Renames {
			ids[i], names[i] = r.ItemID, r.Name
		}
		if _, err := q.ExecContext(ctx, renameSQL[kind], pq.Array(ids), pq.Array(names)); err != nil {
			return fmt.Errorf("ecriture des noms : %w", err)
		}
	}
	return nil
}

// EnsureSchema cree les tables (idempotent). Utilise par la migration V65.
func EnsureSchema(ctx context.Context, q Querier) error {
	for _, stmt := range SchemaStatements {
		if _, err := q.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("schema des dossiers : %w", err)
		}
	}
	return nil
}

// MigrateWorkspace : le pas de donnees de V65 pour une base workspace. Convertit les noms
// `A / B / C` existants en arbre, sans modifier un seul nom. Idempotent.
func MigrateWorkspace(ctx context.Context, q Querier) (map[Kind]AdoptReport, error) {
	reports := map[Kind]AdoptReport{}
	for _, kind := range Kinds {
		if err := Lock(ctx, q, kind); err != nil {
			return nil, err
		}
		loaded, err := Load(ctx, q, kind)
		if err != nil {
			return nil, err
		}
		work := loaded.Clone()
		rep := work.Adopt()
		reports[kind] = rep
		if err := Apply(ctx, q, kind, Between(loaded, work, nil)); err != nil {
			return nil, err
		}
	}
	return reports, nil
}
