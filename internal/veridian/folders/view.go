package folders

// FolderView : un dossier tel que le rendent l'API et la CLI.
type FolderView struct {
	ID          string   `json:"id"`
	ParentID    *string  `json:"parent_id"`
	Name        string   `json:"name"`
	Position    int      `json:"position"`
	Depth       int      `json:"depth"`
	Path        []string `json:"path"`
	PathText    string   `json:"path_text"`
	Count       int      `json:"count"`        // objets, sous-dossiers compris
	DirectItems int      `json:"direct_items"` // objets directement dedans
	Subfolders  int      `json:"subfolders"`
}

// ItemView : un objet range (ou a la racine).
type ItemView struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`  // nom stocke (chemin complet de compatibilite)
	Label    string  `json:"label"` // nom court, sans le chemin
	FolderID *string `json:"folder_id"`
	Position *int    `json:"position"` // null : jamais ordonne (racine, ordre natif de la liste)
	PathText string  `json:"path_text"`
}

// View est la lecture complete de l'arbre d'un genre.
type View struct {
	Kind    Kind         `json:"kind"`
	Folders []FolderView `json:"folders"` // ordre d'affichage : parcours en profondeur
	Items   []ItemView   `json:"items"`   // ordre d'affichage, objets sans rang en dernier
	Adopt   *AdoptReport `json:"adopt,omitempty"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// View produit la vue ordonnee. Les compteurs sont calcules ici, une seule fois, par un
// seul code : la console les relit, elle ne les recompte pas.
func (s *State) View() View {
	v := View{Kind: s.Kind, Folders: []FolderView{}, Items: []ItemView{}}
	counts, direct := map[string]int{}, map[string]int{}
	for id, p := range s.Place {
		if _, live := s.Items[id]; !live {
			continue
		}
		direct[p.FolderID]++
		for _, fid := range s.PathIDs(p.FolderID) {
			counts[fid]++
		}
	}
	var walk func(parent string)
	addItem := func(id string) {
		it := s.Items[id]
		p, placed := s.Place[id]
		iv := ItemView{ID: id, Name: it.Name, Label: s.Label(id)}
		if placed {
			iv.FolderID = strPtr(p.FolderID)
			pos := p.Position
			iv.Position = &pos
			iv.PathText = JoinPath(s.Path(p.FolderID))
		}
		v.Items = append(v.Items, iv)
	}
	walk = func(parent string) {
		for _, f := range s.Children(parent) {
			path := s.Path(f.ID)
			fv := FolderView{
				ID: f.ID, ParentID: strPtr(f.ParentID), Name: f.Name, Position: f.Position,
				Depth: len(path), Path: path, PathText: JoinPath(path),
				Count: counts[f.ID], Subfolders: len(s.Children(f.ID)),
			}
			fv.DirectItems = direct[f.ID]
			v.Folders = append(v.Folders, fv)
			walk(f.ID)
			for _, id := range s.ItemsIn(f.ID) {
				addItem(id)
			}
		}
	}
	walk("")
	for _, id := range s.ItemsIn("") {
		addItem(id)
	}
	for _, id := range s.Unplaced() {
		addItem(id)
	}
	return v
}
