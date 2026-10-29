package folders

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Rename : nouveau nom de compatibilite d'un objet (`A / B / C`) a ecrire dans la table upstream.
type Rename struct {
	ItemID string `json:"id"`
	Name   string `json:"name"`
}

// Position decrit ou inserer parmi les freres : un indice (0 = en tete) ou un repere
// « avant cet identifiant ». Vide = en queue.
type Position struct {
	Index  *int
	Before string
}

// rewriteNames recalcule le nom de compatibilite des objets dont le chemin a change
// (`before` = etat avant l'operation) et de ceux listes dans `labels` (nouveau libelle
// demande). Met a jour s.Items et rend la liste des ecritures upstream.
func (s *State) rewriteNames(before *State, labels map[string]string) ([]Rename, error) {
	ids := make([]string, 0, len(s.Items))
	for id := range s.Items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []Rename
	for _, id := range ids {
		oldPath, newPath := before.ItemPath(id), s.ItemPath(id)
		label, forced := labels[id]
		if !forced && strings.Join(oldPath, "\x00") == strings.Join(newPath, "\x00") {
			continue
		}
		if !forced {
			label = LabelFor(before.Items[id].Name, oldPath)
		}
		name := CompatName(newPath, label)
		if utf8.RuneCountInString(name) > MaxNameLen {
			return nil, invalid("le nom complet « %s » depasse %d caracteres", name, MaxNameLen)
		}
		if name != s.Items[id].Name {
			s.Items[id] = Item{ID: id, Name: name}
			out = append(out, Rename{ItemID: id, Name: name})
		}
	}
	return out, nil
}

func (s *State) checkParent(parentID string) error {
	if parentID == "" {
		return nil
	}
	if _, ok := s.Folders[parentID]; !ok {
		return notFound("dossier parent %q", parentID)
	}
	return nil
}

// CreateFolder cree un dossier sous `parentID` ("" = racine).
func (s *State) CreateFolder(name, parentID string, at Position) (*Folder, error) {
	if err := ValidateName("dossier", name); err != nil {
		return nil, invalid("%v", err)
	}
	if err := s.checkParent(parentID); err != nil {
		return nil, err
	}
	if s.Depth(parentID)+1 > MaxDepth {
		return nil, invalid("profondeur maximale de %d niveaux atteinte", MaxDepth)
	}
	if len(s.Folders) >= MaxFoldersPerKind {
		return nil, invalid("limite de %d dossiers atteinte", MaxFoldersPerKind)
	}
	if s.findChild(parentID, name) != nil {
		return nil, conflict("un dossier « %s » existe deja a cet endroit", name)
	}
	f := &Folder{ID: s.NewID(), Kind: s.Kind, ParentID: parentID, Name: name}
	siblings := folderIDs(s.Children(parentID))
	ordered, err := insertAt(siblings, f.ID, at.Index, at.Before)
	if err != nil {
		return nil, err
	}
	s.Folders[f.ID] = f
	s.renumberFolders(ordered)
	return f, nil
}

// RenameFolder renomme un dossier ; les objets qu'il contient voient leur nom de compatibilite suivre.
func (s *State) RenameFolder(id, name string) ([]Rename, error) {
	f, ok := s.Folders[id]
	if !ok {
		return nil, notFound("dossier %q", id)
	}
	if err := ValidateName("dossier", name); err != nil {
		return nil, invalid("%v", err)
	}
	if other := s.findChild(f.ParentID, name); other != nil && other.ID != id {
		return nil, conflict("un dossier « %s » existe deja a cet endroit", name)
	}
	before := s.Clone()
	f.Name = name
	return s.rewriteNames(before, nil)
}

// MoveFolder deplace un dossier (avec tout son contenu) sous `parentID`.
func (s *State) MoveFolder(id, parentID string, at Position) ([]Rename, error) {
	f, ok := s.Folders[id]
	if !ok {
		return nil, notFound("dossier %q", id)
	}
	if err := s.checkParent(parentID); err != nil {
		return nil, err
	}
	if parentID != "" && s.IsDescendant(parentID, id) {
		return nil, invalid("un dossier ne peut pas etre deplace dans lui-meme ni dans un de ses sous-dossiers")
	}
	if s.Depth(parentID)+s.SubfolderHeight(id) > MaxDepth {
		return nil, invalid("profondeur maximale de %d niveaux depassee", MaxDepth)
	}
	if other := s.findChild(parentID, f.Name); other != nil && other.ID != id {
		return nil, conflict("un dossier « %s » existe deja dans la destination", f.Name)
	}
	before := s.Clone()
	oldParent := f.ParentID
	siblings := without(folderIDs(s.Children(parentID)), id)
	ordered, err := insertAt(siblings, id, at.Index, at.Before)
	if err != nil {
		return nil, err
	}
	f.ParentID = parentID
	s.renumberFolders(ordered)
	if oldParent != parentID {
		s.renumberFolders(folderIDs(s.Children(oldParent)))
	}
	return s.rewriteNames(before, nil)
}

// DeleteFolder supprime un dossier. Jamais un objet : si le dossier n'est pas vide, `toParent`
// remonte son contenu (sous-dossiers et objets) d'un cran ; sinon refus.
func (s *State) DeleteFolder(id string, toParent bool) ([]Rename, error) {
	f, ok := s.Folders[id]
	if !ok {
		return nil, notFound("dossier %q", id)
	}
	subs := s.Children(id)
	items := s.ItemsIn(id)
	if (len(subs) > 0 || len(items) > 0) && !toParent {
		return nil, conflict("le dossier « %s » n'est pas vide (%d sous-dossier(s), %d element(s)) : videz-le, ou supprimez-le avec contents=to_parent pour remonter son contenu d'un cran", f.Name, len(subs), len(items))
	}
	for _, sub := range subs {
		if other := s.findChild(f.ParentID, sub.Name); other != nil && other.ID != id {
			return nil, conflict("remonter « %s » est impossible : un dossier du meme nom existe deja dans « %s »", sub.Name, JoinPath(s.Path(f.ParentID)))
		}
	}
	before := s.Clone()
	parent := f.ParentID
	// Sous-dossiers et objets prennent la place du dossier, a la suite des freres existants.
	folderOrder := folderIDs(s.Children(parent))
	var merged []string
	for _, x := range folderOrder {
		if x == id {
			for _, sub := range subs {
				merged = append(merged, sub.ID)
			}
			continue
		}
		merged = append(merged, x)
	}
	for _, sub := range subs {
		s.Folders[sub.ID].ParentID = parent
	}
	delete(s.Folders, id)
	s.renumberFolders(merged)
	if len(items) > 0 {
		ordered := append(s.ItemsIn(parent), items...)
		s.renumberItems(parent, ordered)
	}
	return s.rewriteNames(before, nil)
}

// MoveItems range des objets dans `folderID` ("" = racine). `label` (si non vide, un seul objet)
// renomme aussi le libelle court.
func (s *State) MoveItems(ids []string, folderID string, at Position, label string) ([]Rename, error) {
	if len(ids) == 0 {
		return nil, invalid("aucun identifiant a deplacer")
	}
	if err := s.checkParent(folderID); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := s.Items[id]; !ok {
			return nil, notFound("%s %q", s.Kind, id)
		}
		if seen[id] {
			return nil, invalid("identifiant %q en double", id)
		}
		seen[id] = true
	}
	labels := map[string]string{}
	if label != "" {
		if len(ids) != 1 {
			return nil, invalid("name ne peut accompagner qu'un seul identifiant")
		}
		if err := ValidateName("libelle", label); err != nil {
			return nil, invalid("%v", err)
		}
		labels[ids[0]] = label
	}
	before := s.Clone()
	oldFolders := map[string]bool{}
	for _, id := range ids {
		if p, ok := s.Place[id]; ok {
			oldFolders[p.FolderID] = true
		}
	}
	siblings := without(s.ItemsIn(folderID), ids...)
	// Insertion groupee : le premier a la position demandee, les suivants a sa suite.
	ordered, err := insertAt(siblings, ids[0], at.Index, at.Before)
	if err != nil {
		return nil, err
	}
	if len(ids) > 1 {
		pos := indexOf(ordered, ids[0])
		rest := append([]string{}, ordered[pos+1:]...)
		ordered = append(append(ordered[:pos+1:pos+1], ids[1:]...), rest...)
	}
	s.renumberItems(folderID, ordered)
	for old := range oldFolders {
		if old != folderID {
			s.renumberItems(old, s.ItemsIn(old))
		}
	}
	return s.rewriteNames(before, labels)
}

// Reorder fixe l'ordre des enfants de `parentID` : les identifiants listes passent en tete,
// dans cet ordre ; les autres suivent dans leur ordre actuel.
func (s *State) Reorder(parentID string, folderOrder, itemOrder []string) error {
	if err := s.checkParent(parentID); err != nil {
		return err
	}
	if len(folderOrder) > 0 {
		cur := folderIDs(s.Children(parentID))
		head, err := checkSubset("dossier", folderOrder, cur)
		if err != nil {
			return err
		}
		s.renumberFolders(append(head, without(cur, head...)...))
	}
	if len(itemOrder) > 0 {
		cur := s.ItemsIn(parentID)
		head, err := checkSubset(string(s.Kind), itemOrder, cur)
		if err != nil {
			return err
		}
		s.renumberItems(parentID, append(head, without(cur, head...)...))
	}
	return nil
}

func checkSubset(what string, want, have []string) ([]string, error) {
	in := map[string]bool{}
	for _, h := range have {
		in[h] = true
	}
	seen := map[string]bool{}
	for _, w := range want {
		if !in[w] {
			return nil, invalid("%s %q n'est pas dans ce dossier", what, w)
		}
		if seen[w] {
			return nil, invalid("%s %q en double", what, w)
		}
		seen[w] = true
	}
	return append([]string{}, want...), nil
}

func indexOf(ids []string, id string) int {
	for i, x := range ids {
		if x == id {
			return i
		}
	}
	return -1
}

// FindPath retrouve un dossier par son chemin texte (`A / B`), sans tenir compte de la casse.
func (s *State) FindPath(path string) (string, error) {
	parts := ParsePath(path)
	parent := ""
	for _, part := range parts {
		f := s.findChild(parent, part)
		if f == nil {
			return "", notFound("dossier « %s »", path)
		}
		parent = f.ID
	}
	return parent, nil
}

// EnsurePath cree les niveaux manquants d'un chemin `A / B / C` (comme mkdir -p) et rend
// l'identifiant du dernier. Idempotent.
func (s *State) EnsurePath(path string) (string, error) {
	parts := ParsePath(path)
	parent := ""
	for _, part := range parts {
		if f := s.findChild(parent, part); f != nil {
			parent = f.ID
			continue
		}
		f, err := s.CreateFolder(part, parent, Position{})
		if err != nil {
			return "", err
		}
		parent = f.ID
	}
	return parent, nil
}
