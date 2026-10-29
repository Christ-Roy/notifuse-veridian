package folders

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixtureSegment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type goldenNode struct {
	Depth int    `json:"depth"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	ID    string `json:"id"`
}

func seqID() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("f%03d", n) }
}

func stateOf(kind Kind, items ...Item) *State {
	st := NewState(kind)
	st.NewID = seqID()
	for _, it := range items {
		st.Items[it.ID] = it
	}
	return st
}

func animalsdState(t *testing.T) (*State, []fixtureSegment) {
	var segs []fixtureSegment
	readJSON(t, "animalsd_segments.json", &segs)
	require.NotEmpty(t, segs)
	st := NewState(KindSegments)
	st.NewID = seqID()
	for _, s := range segs {
		st.Items[s.ID] = Item{ID: s.ID, Name: s.Name}
	}
	return st, segs
}

// Jeu reel : les segments d'animalsd, ranges a l'ancienne par convention de nom. La conversion
// doit (1) ne modifier aucun nom, (2) ne perdre ni dupliquer aucun objet, (3) rendre l'arbre
// que la console construisait a partir des noms : memes dossiers, memes libelles, meme ordre.
func TestAdopt_JeuReelAnimalsd(t *testing.T) {
	st, segs := animalsdState(t)
	origNames := map[string]string{}
	for id, it := range st.Items {
		origNames[id] = it.Name
	}

	rep := st.Adopt()
	assert.Equal(t, len(segs), rep.Adopted)
	assert.Empty(t, rep.Warnings)
	assert.Equal(t, 0, rep.Pruned)

	for id, name := range origNames {
		assert.Equal(t, name, st.Items[id].Name, "le nom de %s ne doit pas changer", id)
	}

	// Aucun objet perdu ni double.
	v := st.View()
	require.Len(t, v.Items, len(segs))
	seen := map[string]bool{}
	for _, it := range v.Items {
		assert.False(t, seen[it.ID], "doublon %s", it.ID)
		seen[it.ID] = true
		assert.NotContains(t, it.Label, " / ", "aucun separateur ne subsiste dans un libelle (%s)", it.Label)
		// Le nom reconstruit depuis l'arbre est le nom d'origine.
		path := st.ItemPath(it.ID)
		assert.Equal(t, origNames[it.ID], CompatName(path, it.Label), it.ID)
	}
	for _, f := range v.Folders {
		assert.NotContains(t, f.Name, " / ")
	}

	// Meme arbre, meme ordre que la console (golden genere par le TypeScript).
	var golden []goldenNode
	readJSON(t, "animalsd_tree_golden.json", &golden)
	var got []goldenNode
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, f := range st.Children(parent) {
			got = append(got, goldenNode{Depth: depth, Kind: "folder", Label: f.Name})
			walk(f.ID, depth+1)
			for _, id := range st.ItemsIn(f.ID) {
				got = append(got, goldenNode{Depth: depth + 1, Kind: "item", Label: st.Label(id), ID: id})
			}
		}
	}
	walk("", 0)
	for _, id := range st.ItemsIn("") {
		got = append(got, goldenNode{Depth: 0, Kind: "item", Label: st.Label(id), ID: id})
	}
	require.Len(t, got, len(golden))
	for i := range golden {
		assert.Equal(t, golden[i].Kind, got[i].Kind, "rang %d", i)
		assert.Equal(t, golden[i].Depth, got[i].Depth, "rang %d", i)
		assert.Equal(t, golden[i].Label, got[i].Label, "rang %d", i)
		if golden[i].Kind == "item" {
			assert.Equal(t, golden[i].ID, got[i].ID, "rang %d", i)
		}
	}

	// Compteurs : 6 dossiers de premier niveau, 34 objets.
	top := 0
	total := 0
	for _, f := range v.Folders {
		if f.ParentID == nil {
			top++
			total += f.Count
		}
	}
	assert.Equal(t, 6, top)
	assert.Equal(t, len(segs), total)
}

func TestAdopt_Idempotent(t *testing.T) {
	st, _ := animalsdState(t)
	st.Adopt()
	before := st.Clone()
	rep := st.Adopt()
	assert.False(t, rep.Changed(), "la seconde passe ne change rien")
	assert.Equal(t, before.Folders, st.Folders)
	assert.Equal(t, before.Place, st.Place)
}

func TestAdopt_NeDeplacePasUnObjetSansSeparateur(t *testing.T) {
	st := stateOf(KindLists, Item{"a", "Seule"}, Item{"b", "X / Y"})
	st.Adopt()
	_, placed := st.Place["a"]
	assert.False(t, placed, "un nom sans separateur reste a la racine, sans ligne")
	require.Contains(t, st.Place, "b")
	assert.Equal(t, []string{"X"}, st.ItemPath("b"))
}

func TestAdopt_RangeUnClientALAncienne(t *testing.T) {
	// Un dossier existe, range a la main ; un client cree ensuite « Cibles / Neuf » par le nom.
	st := stateOf(KindSegments, Item{"s1", "Cibles / Ancien"}, Item{"s2", "Cibles / Neuf"})
	st.Adopt()
	f := st.Children("")[0]
	st.Place["s1"].Position = 0
	st.Place["s2"].FolderID = "" // derive : range a la racine par erreur
	st.Place["s2"].Position = 0
	rep := st.Adopt()
	assert.Equal(t, 1, rep.Adopted)
	assert.Equal(t, 0, rep.FoldersCreated, "le dossier existant est reutilise")
	assert.Equal(t, f.ID, st.Place["s2"].FolderID)
	assert.Equal(t, 1, st.Place["s2"].Position, "range apres l'existant")
}

func TestAdopt_CasseIgnoreePourReutiliserUnDossier(t *testing.T) {
	st := stateOf(KindSegments, Item{"a", "Cibles / A"}, Item{"b", "cibles / B"})
	st.Adopt()
	assert.Len(t, st.Folders, 1, "l'unicite des freres ignore la casse")
	assert.Equal(t, st.Place["a"].FolderID, st.Place["b"].FolderID)
}

func TestAdopt_PurgeLesAppartenancesOrphelines(t *testing.T) {
	st := stateOf(KindSegments, Item{"vivant", "Vivant"})
	st.Folders["d"] = &Folder{ID: "d", Kind: KindSegments, Name: "D"}
	st.Place["vivant"] = &Placement{ItemID: "vivant", FolderID: "d"}
	st.Place["mort"] = &Placement{ItemID: "mort", FolderID: "d"}
	rep := st.Adopt()
	assert.Equal(t, 1, rep.Pruned)
	assert.NotContains(t, st.Place, "mort")
	assert.Contains(t, st.Place, "vivant")
}

func TestAdopt_BornesProfondeur(t *testing.T) {
	deep := "a / b / c / d / e / f / g / h / i / x" // 9 niveaux de dossiers
	st := stateOf(KindSegments, Item{"deep", deep})
	rep := st.Adopt()
	assert.Len(t, rep.Warnings, 1)
	assert.Empty(t, st.Folders, "rien n'est cree pour un chemin trop profond")
	assert.NotContains(t, st.Place, "deep")
}

func TestAdopt_OrdreNaturelEtStable(t *testing.T) {
	items := []Item{{"1", "D / P10"}, {"2", "D / P2"}, {"3", "D / P1"}, {"4", "D2 / x"}, {"5", "D10 / x"}}
	st := stateOf(KindSegments, items...)
	st.Adopt()
	var order []string
	for _, f := range st.Children("") {
		order = append(order, f.Name)
	}
	assert.Equal(t, []string{"D", "D2", "D10"}, order)
	for i, f := range st.Children("") {
		assert.Equal(t, i, f.Position, "les rangs partent de 0, sans trou")
	}
	var labels []string
	for _, id := range st.ItemsIn(st.Children("")[0].ID) {
		labels = append(labels, st.Label(id))
	}
	assert.Equal(t, []string{"P1", "P2", "P10"}, labels)
	for i, id := range st.ItemsIn(st.Children("")[0].ID) {
		assert.Equal(t, i, st.Place[id].Position)
	}
	ids := []string{}
	for id := range st.Items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	assert.Len(t, ids, 5)
}
