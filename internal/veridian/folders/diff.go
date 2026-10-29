package folders

import (
	"sort"
)

// Diff : ce qu'il faut ecrire en base pour passer de l'etat `before` a l'etat `after`.
type Diff struct {
	FolderUpserts []Folder
	FolderDeletes []string
	PlaceUpserts  []Placement
	PlaceDeletes  []string
	Renames       []Rename
}

// Empty : rien a ecrire.
func (d Diff) Empty() bool {
	return len(d.FolderUpserts)+len(d.FolderDeletes)+len(d.PlaceUpserts)+len(d.PlaceDeletes)+len(d.Renames) == 0
}

// Between calcule le diff minimal (les lignes inchangees ne sont pas reecrites).
func Between(before, after *State, renames []Rename) Diff {
	d := Diff{Renames: renames}
	for id, f := range after.Folders {
		b, ok := before.Folders[id]
		if !ok || b.ParentID != f.ParentID || b.Name != f.Name || b.Position != f.Position {
			d.FolderUpserts = append(d.FolderUpserts, *f)
		}
	}
	for id := range before.Folders {
		if _, ok := after.Folders[id]; !ok {
			d.FolderDeletes = append(d.FolderDeletes, id)
		}
	}
	for id, p := range after.Place {
		b, ok := before.Place[id]
		if !ok || b.FolderID != p.FolderID || b.Position != p.Position {
			d.PlaceUpserts = append(d.PlaceUpserts, *p)
		}
	}
	for id := range before.Place {
		if _, ok := after.Place[id]; !ok {
			d.PlaceDeletes = append(d.PlaceDeletes, id)
		}
	}
	// Ordre deterministe : parents avant enfants a l'insertion, enfants avant parents a la suppression.
	sort.Slice(d.FolderUpserts, func(i, j int) bool {
		a, b := d.FolderUpserts[i], d.FolderUpserts[j]
		if da, db := after.Depth(a.ID), after.Depth(b.ID); da != db {
			return da < db
		}
		return a.ID < b.ID
	})
	sort.Slice(d.FolderDeletes, func(i, j int) bool {
		a, b := d.FolderDeletes[i], d.FolderDeletes[j]
		if da, db := before.Depth(a), before.Depth(b); da != db {
			return da > db
		}
		return a < b
	})
	sort.Slice(d.PlaceUpserts, func(i, j int) bool { return d.PlaceUpserts[i].ItemID < d.PlaceUpserts[j].ItemID })
	sort.Strings(d.PlaceDeletes)
	return d
}
