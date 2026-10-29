package folders

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Erreurs typees : le handler les traduit en 404 / 409 / 422.
var (
	ErrNotFound = errors.New("introuvable")
	ErrConflict = errors.New("conflit")
	ErrInvalid  = errors.New("requete invalide")
)

func notFound(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrNotFound, fmt.Sprintf(format, a...))
}
func conflict(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrConflict, fmt.Sprintf(format, a...))
}
func invalid(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// State est l'arbre d'UN genre d'objet, en memoire. Toutes les operations metier sont des
// methodes pures de State ; le stockage charge un State, applique une operation, puis
// ecrit le Diff entre l'etat d'avant et l'etat d'apres.
type State struct {
	Kind    Kind
	Folders map[string]*Folder
	Place   map[string]*Placement // lignes veridian_folder_items, par objet
	Items   map[string]Item       // objets vivants de la table upstream

	NewID func() string
}

// DefaultNewID : 16 caracteres hexadecimaux.
func DefaultNewID() string { return strings.ReplaceAll(uuid.NewString(), "-", "")[:16] }

// NewState construit un etat vide.
func NewState(kind Kind) *State {
	return &State{Kind: kind, Folders: map[string]*Folder{}, Place: map[string]*Placement{}, Items: map[string]Item{}, NewID: DefaultNewID}
}

// Clone copie profondement l'etat (les operations travaillent sur une copie).
func (s *State) Clone() *State {
	c := NewState(s.Kind)
	c.NewID = s.NewID
	for k, f := range s.Folders {
		cp := *f
		c.Folders[k] = &cp
	}
	for k, p := range s.Place {
		cp := *p
		c.Place[k] = &cp
	}
	for k, it := range s.Items {
		c.Items[k] = it
	}
	return c
}

// PathIDs : identifiants de la racine jusqu'au dossier inclus. Vide pour la racine. Une
// boucle (donnee corrompue) interrompt le parcours.
func (s *State) PathIDs(folderID string) []string {
	var rev []string
	seen := map[string]bool{}
	for id := folderID; id != "" && !seen[id]; {
		f, ok := s.Folders[id]
		if !ok {
			break
		}
		seen[id] = true
		rev = append(rev, id)
		id = f.ParentID
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// Path : noms de la racine jusqu'au dossier inclus.
func (s *State) Path(folderID string) []string {
	ids := s.PathIDs(folderID)
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = s.Folders[id].Name
	}
	return out
}

// Depth : 0 pour la racine, 1 pour un dossier de premier niveau.
func (s *State) Depth(folderID string) int { return len(s.PathIDs(folderID)) }

// ItemPath : chemin (noms de dossiers) de l'objet ; vide a la racine.
func (s *State) ItemPath(itemID string) []string {
	if p, ok := s.Place[itemID]; ok {
		return s.Path(p.FolderID)
	}
	return nil
}

// IsDescendant : `id` est `ancestor` ou se trouve dessous.
func (s *State) IsDescendant(id, ancestor string) bool {
	for _, x := range s.PathIDs(id) {
		if x == ancestor {
			return true
		}
	}
	return false
}

// SubfolderHeight : nombre de niveaux du sous-arbre enracine en `id` (lui compris).
func (s *State) SubfolderHeight(id string) int {
	best := 0
	for _, c := range s.Children(id) {
		if h := s.SubfolderHeight(c.ID); h > best {
			best = h
		}
	}
	return best + 1
}

// Children : sous-dossiers de `parentID` ("" = racine) dans l'ordre d'affichage.
func (s *State) Children(parentID string) []*Folder {
	var out []*Folder
	for _, f := range s.Folders {
		if f.ParentID == parentID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		if c := NaturalCompare(a.Name, b.Name); c != 0 {
			return c < 0
		}
		return a.ID < b.ID
	})
	return out
}

// Label : libelle court d'un objet dans sa position actuelle.
func (s *State) Label(itemID string) string {
	it := s.Items[itemID]
	return LabelFor(it.Name, s.ItemPath(itemID))
}

// ItemsIn : objets ranges (avec une ligne) dans `folderID` ("" = racine), dans l'ordre.
func (s *State) ItemsIn(folderID string) []string {
	var out []string
	for id, p := range s.Place {
		if p.FolderID == folderID {
			if _, live := s.Items[id]; live {
				out = append(out, id)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := s.Place[out[i]], s.Place[out[j]]
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		if c := NaturalCompare(s.Label(out[i]), s.Label(out[j])); c != 0 {
			return c < 0
		}
		return out[i] < out[j]
	})
	return out
}

// Unplaced : objets vivants sans ligne (racine, sans rang), ordre naturel du nom.
func (s *State) Unplaced() []string {
	var out []string
	for id := range s.Items {
		if _, ok := s.Place[id]; !ok {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if c := NaturalCompare(s.Items[out[i]].Name, s.Items[out[j]].Name); c != 0 {
			return c < 0
		}
		return out[i] < out[j]
	})
	return out
}

// renumberFolders pose 0..n-1 sur la liste ordonnee de freres.
func (s *State) renumberFolders(ids []string) {
	for i, id := range ids {
		s.Folders[id].Position = i
	}
}

func (s *State) renumberItems(folderID string, ids []string) {
	for i, id := range ids {
		p := s.Place[id]
		if p == nil {
			p = &Placement{ItemID: id}
			s.Place[id] = p
		}
		p.FolderID = folderID
		p.Position = i
	}
}

// findChild retrouve un sous-dossier par nom, sans tenir compte de la casse.
func (s *State) findChild(parentID, name string) *Folder {
	for _, f := range s.Children(parentID) {
		if strings.EqualFold(f.Name, name) {
			return f
		}
	}
	return nil
}

// insertAt insere `id` dans `ids` (deja prive de id) : a l'indice pos, avant `before`, ou en queue.
func insertAt(ids []string, id string, pos *int, before string) ([]string, error) {
	idx := len(ids)
	switch {
	case before != "":
		idx = -1
		for i, x := range ids {
			if x == before {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, notFound("repere before_id %q introuvable parmi les freres", before)
		}
	case pos != nil:
		if *pos < 0 {
			return nil, invalid("position negative")
		}
		if *pos < idx {
			idx = *pos
		}
	}
	out := make([]string, 0, len(ids)+1)
	out = append(out, ids[:idx]...)
	out = append(out, id)
	out = append(out, ids[idx:]...)
	return out, nil
}

func without(ids []string, drop ...string) []string {
	skip := map[string]bool{}
	for _, d := range drop {
		skip[d] = true
	}
	out := make([]string, 0, len(ids))
	for _, x := range ids {
		if !skip[x] {
			out = append(out, x)
		}
	}
	return out
}

func folderIDs(fs []*Folder) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.ID
	}
	return out
}
