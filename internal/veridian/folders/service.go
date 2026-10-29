package folders

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
)

// DBProvider rend la connexion de la base du workspace.
type DBProvider func(ctx context.Context, workspaceID string) (*sql.DB, error)

// Authorizer verifie que l'appelant peut lire (write=false) ou ecrire (write=true) ce genre
// d'objet dans ce workspace (kind "" : simple appartenance au workspace). Segments : droit « contacts » ; listes : « lists » ; modeles :
// « templates » (le branchement est dans wiring.go).
type Authorizer func(ctx context.Context, workspaceID string, kind Kind, write bool) error

// Service porte toutes les operations. Chaque ecriture est UNE transaction, sous verrou, qui
// relit l'etat, applique l'adoption des noms a l'ancienne, puis l'operation, puis le diff.
type Service struct {
	DB    DBProvider
	Authz Authorizer
}

// MovedItem : un objet deplace, avec son nom de compatibilite apres l'operation.
type MovedItem struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Label    string  `json:"label"`
	FolderID *string `json:"folder_id"`
	Position int     `json:"position"`
	PathText string  `json:"path_text"`
}

// Result : retour commun des ecritures.
type Result struct {
	Folder  *FolderView  `json:"folder,omitempty"`
	Moved   []MovedItem  `json:"moved,omitempty"`
	Renamed []Rename     `json:"renamed,omitempty"` // noms de compatibilite reecrits dans la table upstream
	Deleted string       `json:"deleted,omitempty"`
	Adopt   *AdoptReport `json:"adopt,omitempty"`
}

func (s *Service) open(ctx context.Context, workspaceID string, kind Kind, write bool) (*sql.DB, error) {
	if workspaceID == "" {
		return nil, invalid("workspace_id est requis")
	}
	if !kind.Valid() {
		return nil, invalid("kind doit valoir segments, lists ou templates")
	}
	if err := s.Authz(ctx, workspaceID, kind, write); err != nil {
		return nil, err
	}
	return s.DB(ctx, workspaceID)
}

// View lit l'arbre. Si des objets portent encore un chemin dans leur nom sans etre ranges
// (client a l'ancienne), ils sont ranges ici, une fois, avant la reponse.
func (s *Service) View(ctx context.Context, workspaceID string, kind Kind) (*View, error) {
	db, err := s.open(ctx, workspaceID, kind, false)
	if err != nil {
		return nil, err
	}
	st, err := Load(ctx, db, kind)
	if err != nil {
		return nil, err
	}
	work := st.Clone()
	rep := work.Adopt()
	if rep.Changed() {
		// Il y a quelque chose a ecrire : droit d'ecriture, transaction, relecture sous verrou.
		if err := s.Authz(ctx, workspaceID, kind, true); err == nil {
			res, werr := s.mutate(ctx, db, kind, func(w *State) (Result, []Rename, error) { return Result{}, nil, nil })
			if werr == nil {
				v := res.view
				v.Adopt = res.Adopt
				return &v, nil
			}
		}
		// Pas le droit d'ecrire (ou echec) : on sert la vue calculee, sans rien persister.
	}
	v := work.View()
	if rep.Changed() {
		v.Adopt = &rep
	}
	return &v, nil
}

type mutation struct {
	Result
	view View
}

// mutate : verrou, relecture, adoption, operation, diff, ecriture, dans une transaction.
func (s *Service) mutate(ctx context.Context, db *sql.DB, kind Kind, op func(w *State) (Result, []Rename, error)) (*mutation, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := Lock(ctx, tx, kind); err != nil {
		return nil, err
	}
	loaded, err := Load(ctx, tx, kind)
	if err != nil {
		return nil, err
	}
	work := loaded.Clone()
	rep := work.Adopt()
	res, renames, err := op(work)
	if err != nil {
		return nil, err
	}
	if err := Apply(ctx, tx, kind, Between(loaded, work, renames)); err != nil {
		return nil, translateDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, translateDBError(err)
	}
	if rep.Changed() {
		res.Adopt = &rep
	}
	res.Renamed = renames
	return &mutation{Result: res, view: work.View()}, nil
}

// translateDBError : une violation d'unicite ou de cle etrangere releve du conflit, pas de la panne.
func translateDBError(err error) error {
	var pe *pq.Error
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505":
			return conflict("un element du meme nom existe deja a cet endroit")
		case "23503":
			return conflict("l'etat des dossiers a change pendant l'operation, reessayez")
		}
	}
	return err
}

func (s *Service) write(ctx context.Context, workspaceID string, kind Kind, op func(w *State) (Result, []Rename, error)) (*Result, error) {
	db, err := s.open(ctx, workspaceID, kind, true)
	if err != nil {
		return nil, err
	}
	m, err := s.mutate(ctx, db, kind, op)
	if err != nil {
		return nil, err
	}
	res := m.Result
	return &res, nil
}

func folderViewOf(w *State, id string) *FolderView {
	v := w.View()
	for i := range v.Folders {
		if v.Folders[i].ID == id {
			return &v.Folders[i]
		}
	}
	return nil
}

// CreateFolder cree un dossier.
func (s *Service) CreateFolder(ctx context.Context, ws string, kind Kind, name, parentID string, at Position) (*Result, error) {
	return s.write(ctx, ws, kind, func(w *State) (Result, []Rename, error) {
		f, err := w.CreateFolder(name, parentID, at)
		if err != nil {
			return Result{}, nil, err
		}
		return Result{Folder: folderViewOf(w, f.ID)}, nil, nil
	})
}

// folderKind retrouve le genre d'un dossier (les routes folders.rename|move|delete ne le demandent pas).
func (s *Service) folderKind(ctx context.Context, ws, id string) (Kind, error) {
	if ws == "" {
		return "", invalid("workspace_id est requis")
	}
	if id == "" {
		return "", invalid("id est requis")
	}
	// Autorisation minimale avant de toucher a la base (genre "" = simple appartenance au
	// workspace) ; le droit d'ecriture du genre trouve est reverifie par write().
	if err := s.Authz(ctx, ws, "", false); err != nil {
		return "", err
	}
	db, err := s.DB(ctx, ws)
	if err != nil {
		return "", err
	}
	var k string
	if err := db.QueryRowContext(ctx, `SELECT kind FROM veridian_folders WHERE id = $1`, id).Scan(&k); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", notFound("dossier %q", id)
		}
		return "", err
	}
	return Kind(k), nil
}

// RenameFolder renomme un dossier.
func (s *Service) RenameFolder(ctx context.Context, ws, id, name string) (*Result, error) {
	kind, err := s.folderKind(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	return s.write(ctx, ws, kind, func(w *State) (Result, []Rename, error) {
		renames, err := w.RenameFolder(id, name)
		if err != nil {
			return Result{}, nil, err
		}
		return Result{Folder: folderViewOf(w, id)}, renames, nil
	})
}

// MoveFolder deplace un dossier.
func (s *Service) MoveFolder(ctx context.Context, ws, id, parentID string, at Position) (*Result, error) {
	kind, err := s.folderKind(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	return s.write(ctx, ws, kind, func(w *State) (Result, []Rename, error) {
		renames, err := w.MoveFolder(id, parentID, at)
		if err != nil {
			return Result{}, nil, err
		}
		return Result{Folder: folderViewOf(w, id)}, renames, nil
	})
}

// DeleteFolder supprime un dossier (jamais ses objets).
func (s *Service) DeleteFolder(ctx context.Context, ws, id string, toParent bool) (*Result, error) {
	kind, err := s.folderKind(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	return s.write(ctx, ws, kind, func(w *State) (Result, []Rename, error) {
		renames, err := w.DeleteFolder(id, toParent)
		if err != nil {
			return Result{}, nil, err
		}
		return Result{Deleted: id}, renames, nil
	})
}

// MoveItems range des objets (segments, listes ou modeles) dans un dossier ("" = racine).
func (s *Service) MoveItems(ctx context.Context, ws string, kind Kind, ids []string, folderID string, at Position, label string) (*Result, error) {
	return s.write(ctx, ws, kind, func(w *State) (Result, []Rename, error) {
		renames, err := w.MoveItems(ids, folderID, at, label)
		if err != nil {
			return Result{}, nil, err
		}
		var moved []MovedItem
		for _, id := range ids {
			p := w.Place[id]
			moved = append(moved, MovedItem{
				ID: id, Name: w.Items[id].Name, Label: w.Label(id), FolderID: strPtr(p.FolderID),
				Position: p.Position, PathText: JoinPath(w.Path(p.FolderID)),
			})
		}
		return Result{Moved: moved}, renames, nil
	})
}

// Reorder fixe l'ordre des enfants d'un dossier.
func (s *Service) Reorder(ctx context.Context, ws string, kind Kind, parentID string, folderOrder, itemOrder []string) (*Result, error) {
	return s.write(ctx, ws, kind, func(w *State) (Result, []Rename, error) {
		if len(folderOrder)+len(itemOrder) == 0 {
			return Result{}, nil, invalid("folder_ids ou item_ids est requis")
		}
		if err := w.Reorder(parentID, folderOrder, itemOrder); err != nil {
			return Result{}, nil, err
		}
		return Result{}, nil, nil
	})
}

// EnsurePath cree un chemin de dossiers `A / B / C` (niveaux manquants compris). Idempotent.
func (s *Service) EnsurePath(ctx context.Context, ws string, kind Kind, path string) (*Result, error) {
	return s.write(ctx, ws, kind, func(w *State) (Result, []Rename, error) {
		id, err := w.EnsurePath(path)
		if err != nil {
			return Result{}, nil, err
		}
		return Result{Folder: folderViewOf(w, id)}, nil, nil
	})
}

// MoveItemsToPath : comme MoveItems, la destination etant un chemin texte (cree au besoin).
func (s *Service) MoveItemsToPath(ctx context.Context, ws string, kind Kind, ids []string, path string, at Position, label string) (*Result, error) {
	return s.write(ctx, ws, kind, func(w *State) (Result, []Rename, error) {
		folderID, err := w.EnsurePath(path)
		if err != nil {
			return Result{}, nil, err
		}
		renames, err := w.MoveItems(ids, folderID, at, label)
		if err != nil {
			return Result{}, nil, err
		}
		var moved []MovedItem
		for _, id := range ids {
			p := w.Place[id]
			moved = append(moved, MovedItem{
				ID: id, Name: w.Items[id].Name, Label: w.Label(id), FolderID: strPtr(p.FolderID),
				Position: p.Position, PathText: JoinPath(w.Path(p.FolderID)),
			})
		}
		return Result{Moved: moved}, renames, nil
	})
}
