package folders

import (
	"fmt"
	"sort"
	"unicode/utf8"
)

// AdoptReport rend compte d'une adoption.
type AdoptReport struct {
	Adopted        int      `json:"adopted"`         // objets ranges d'apres leur nom
	FoldersCreated int      `json:"folders_created"` // dossiers crees pour eux
	Pruned         int      `json:"pruned"`          // lignes d'appartenance orphelines supprimees
	Warnings       []string `json:"warnings,omitempty"`
}

// Changed dit si l'adoption a modifie l'etat.
func (r AdoptReport) Changed() bool { return r.Adopted+r.FoldersCreated+r.Pruned > 0 }

// Adopt convertit la convention de nom `A / B / C` en arbre natif, sans perte :
//   - le NOM de l'objet n'est jamais modifie (les clients qui le lisent encore voient la meme chose) ;
//   - un objet dont le nom porte un chemin et qui n'est pas range sous ce chemin est range sous
//     ce chemin (dossiers crees au besoin) ; un objet deja range sous le bon chemin est laisse ;
//   - un objet dont le nom n'a pas de separateur n'est jamais deplace ;
//   - les nouveaux dossiers et objets sont ranges apres l'existant, en ordre naturel du nom, ce
//     qui reproduit l'ordre d'affichage de l'ancienne console ;
//   - les lignes d'appartenance dont l'objet n'existe plus sont retirees.
//
// Idempotent : une seconde passe ne change rien. C'est le pas de la migration V65 et le
// garde-fou qui range aussi un client qui cree encore ses objets a l'ancienne.
func (s *State) Adopt() AdoptReport {
	var rep AdoptReport
	for id := range s.Place {
		if _, live := s.Items[id]; !live {
			delete(s.Place, id)
			rep.Pruned++
		}
	}

	ids := make([]string, 0, len(s.Items))
	for id := range s.Items {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	newFolders := map[string][]*Folder{} // parent -> dossiers crees
	newItems := map[string][]string{}    // dossier -> objets ranges
	isNewFolder := map[string]bool{}
	isNewItem := map[string]bool{}

	for _, id := range ids {
		name := s.Items[id].Name
		parts := ParsePath(name)
		if len(parts) < 2 {
			continue
		}
		folderPath := parts[:len(parts)-1]
		if p, ok := s.Place[id]; ok && samePath(s.Path(p.FolderID), folderPath) {
			continue
		}
		if len(folderPath) > MaxDepth {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s : chemin de %d niveaux (maximum %d), laisse a la racine", name, len(folderPath), MaxDepth))
			continue
		}
		tooLong := false
		for _, part := range folderPath {
			if utf8.RuneCountInString(part) > MaxNameLen {
				tooLong = true
			}
		}
		if tooLong {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s : un nom de dossier depasse %d caracteres, laisse a la racine", name, MaxNameLen))
			continue
		}
		parent, ok := "", true
		var created []*Folder
		for _, part := range folderPath {
			f := s.findChild(parent, part)
			if f == nil {
				if len(s.Folders) >= MaxFoldersPerKind {
					rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s : plus de %d dossiers, laisse a la racine", name, MaxFoldersPerKind))
					ok = false
					break
				}
				f = &Folder{ID: s.NewID(), Kind: s.Kind, ParentID: parent, Name: part, Position: -1}
				s.Folders[f.ID] = f
				created = append(created, f)
			}
			parent = f.ID
		}
		if !ok {
			for _, f := range created {
				delete(s.Folders, f.ID)
			}
			continue
		}
		for _, f := range created {
			newFolders[f.ParentID] = append(newFolders[f.ParentID], f)
			isNewFolder[f.ID] = true
			rep.FoldersCreated++
		}
		s.Place[id] = &Placement{ItemID: id, FolderID: parent, Position: -1}
		newItems[parent] = append(newItems[parent], id)
		isNewItem[id] = true
		rep.Adopted++
	}

	// Rangs : apres l'existant, en ordre naturel (c'etait l'ordre de l'ancienne console).
	for parent, list := range newFolders {
		next := 0
		for _, f := range s.Children(parent) {
			if !isNewFolder[f.ID] && f.Position >= next {
				next = f.Position + 1
			}
		}
		sort.Slice(list, func(i, j int) bool {
			if c := NaturalCompare(list[i].Name, list[j].Name); c != 0 {
				return c < 0
			}
			return list[i].ID < list[j].ID
		})
		for _, f := range list {
			f.Position = next
			next++
		}
	}
	for folder, list := range newItems {
		next := 0
		for id, p := range s.Place {
			if p.FolderID == folder && !isNewItem[id] && p.Position >= next {
				next = p.Position + 1
			}
		}
		sort.Slice(list, func(i, j int) bool {
			li, lj := s.Label(list[i]), s.Label(list[j])
			if c := NaturalCompare(li, lj); c != 0 {
				return c < 0
			}
			return list[i] < list[j]
		})
		for _, id := range list {
			s.Place[id].Position = next
			next++
		}
	}
	return rep
}
