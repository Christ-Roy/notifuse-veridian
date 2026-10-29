package folders

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustCreate(t *testing.T, st *State, name, parent string) string {
	t.Helper()
	f, err := st.CreateFolder(name, parent, Position{})
	require.NoError(t, err)
	return f.ID
}

func names(st *State, parent string) []string {
	var out []string
	for _, f := range st.Children(parent) {
		out = append(out, f.Name)
	}
	return out
}

func TestCreateFolder_RegleEtOrdre(t *testing.T) {
	st := stateOf(KindSegments)
	a := mustCreate(t, st, "A", "")
	mustCreate(t, st, "B", "")
	zero := 0
	_, err := st.CreateFolder("C", "", Position{Index: &zero})
	require.NoError(t, err)
	assert.Equal(t, []string{"C", "A", "B"}, names(st, ""))
	_, err = st.CreateFolder("D", "", Position{Before: a})
	require.NoError(t, err)
	assert.Equal(t, []string{"C", "D", "A", "B"}, names(st, ""))

	_, err = st.CreateFolder("a", "", Position{})
	assert.ErrorIs(t, err, ErrConflict, "doublon insensible a la casse")
	_, err = st.CreateFolder("X / Y", "", Position{})
	assert.ErrorIs(t, err, ErrInvalid, "separateur interdit")
	_, err = st.CreateFolder("Z", "inconnu", Position{})
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = st.CreateFolder("Z", "", Position{Before: "inconnu"})
	assert.ErrorIs(t, err, ErrNotFound)

	// Meme nom sous un autre parent : permis.
	_, err = st.CreateFolder("A", a, Position{})
	assert.NoError(t, err)
}

func TestCreateFolder_ProfondeurEtLimite(t *testing.T) {
	st := stateOf(KindSegments)
	parent := ""
	for i := 0; i < MaxDepth; i++ {
		parent = mustCreate(t, st, string(rune('a'+i)), parent)
	}
	_, err := st.CreateFolder("trop", parent, Position{})
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestMoveItems_ReecritLeNomDeCompatibilite(t *testing.T) {
	st := stateOf(KindSegments, Item{"s1", "Vieux / P1"}, Item{"s2", "Libre"})
	st.Adopt()
	cibles := mustCreate(t, st, "Cibles", "")

	renames, err := st.MoveItems([]string{"s1"}, cibles, Position{}, "")
	require.NoError(t, err)
	assert.Equal(t, []Rename{{"s1", "Cibles / P1"}}, renames)
	assert.Equal(t, "Cibles / P1", st.Items["s1"].Name)
	assert.Equal(t, "P1", st.Label("s1"))

	// Un objet de la racine entre dans un dossier : son nom devient le chemin complet.
	renames, err = st.MoveItems([]string{"s2"}, cibles, Position{}, "")
	require.NoError(t, err)
	assert.Equal(t, []Rename{{"s2", "Cibles / Libre"}}, renames)

	// Retour a la racine : le nom redevient le libelle seul.
	renames, err = st.MoveItems([]string{"s1"}, "", Position{}, "")
	require.NoError(t, err)
	assert.Equal(t, []Rename{{"s1", "P1"}}, renames)
	assert.Equal(t, "P1", st.Items["s1"].Name)

	// Ne pas deplacer est sans effet d'ecriture.
	renames, err = st.MoveItems([]string{"s2"}, cibles, Position{}, "")
	require.NoError(t, err)
	assert.Empty(t, renames)
}

func TestMoveItems_LibelleEtErreurs(t *testing.T) {
	st := stateOf(KindLists, Item{"l1", "Ancien"}, Item{"l2", "Autre"})
	d := mustCreate(t, st, "D", "")
	renames, err := st.MoveItems([]string{"l1"}, d, Position{}, "Nouveau")
	require.NoError(t, err)
	assert.Equal(t, []Rename{{"l1", "D / Nouveau"}}, renames)

	_, err = st.MoveItems([]string{"l1", "l2"}, d, Position{}, "Nom")
	assert.ErrorIs(t, err, ErrInvalid, "un libelle ne va qu'avec un seul objet")
	_, err = st.MoveItems([]string{"l1"}, d, Position{}, "A / B")
	assert.ErrorIs(t, err, ErrInvalid, "pas de separateur dans un libelle")
	_, err = st.MoveItems([]string{"inconnu"}, d, Position{}, "")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = st.MoveItems([]string{"l1", "l1"}, d, Position{}, "")
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = st.MoveItems([]string{"l1"}, "nulle-part", Position{}, "")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = st.MoveItems(nil, d, Position{}, "")
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestMoveItems_Ordre(t *testing.T) {
	st := stateOf(KindSegments, Item{"a", "a"}, Item{"b", "b"}, Item{"c", "c"}, Item{"d", "d"})
	d := mustCreate(t, st, "D", "")
	_, err := st.MoveItems([]string{"a", "b", "c"}, d, Position{}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, st.ItemsIn(d))
	zero := 0
	_, err = st.MoveItems([]string{"c"}, d, Position{Index: &zero}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"c", "a", "b"}, st.ItemsIn(d))
	_, err = st.MoveItems([]string{"d"}, d, Position{Before: "a"}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"c", "d", "a", "b"}, st.ItemsIn(d))
	// Groupe : conserve l'ordre donne, insere en bloc.
	_, err = st.MoveItems([]string{"b", "a"}, d, Position{Index: &zero}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a", "c", "d"}, st.ItemsIn(d))
}

func TestRenameFolder_SuitLesNomsDeToutLeSousArbre(t *testing.T) {
	st := stateOf(KindSegments, Item{"s1", "Cibles / Animaleries / P1"}, Item{"s2", "Cibles / Tous"})
	st.Adopt()
	cibles := st.Children("")[0].ID
	renames, err := st.RenameFolder(cibles, "Cibles 2")
	require.NoError(t, err)
	assert.ElementsMatch(t, []Rename{{"s1", "Cibles 2 / Animaleries / P1"}, {"s2", "Cibles 2 / Tous"}}, renames)
	assert.Equal(t, "P1", st.Label("s1"), "le libelle ne bouge pas")

	_, err = st.RenameFolder(cibles, "Mauvais / Nom")
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = st.RenameFolder("inconnu", "X")
	assert.ErrorIs(t, err, ErrNotFound)
	other := mustCreate(t, st, "Autre", "")
	_, err = st.RenameFolder(other, "cibles 2")
	assert.ErrorIs(t, err, ErrConflict)
	// Renommer en changeant seulement la casse est permis.
	_, err = st.RenameFolder(other, "AUTRE")
	assert.NoError(t, err)
}

func TestMoveFolder(t *testing.T) {
	st := stateOf(KindSegments, Item{"s1", "A / B / x"})
	st.Adopt()
	a := st.Children("")[0].ID
	b := st.Children(a)[0].ID
	c := mustCreate(t, st, "C", "")

	renames, err := st.MoveFolder(b, c, Position{})
	require.NoError(t, err)
	assert.Equal(t, []Rename{{"s1", "C / B / x"}}, renames)
	assert.Equal(t, []string{"C", "B"}, st.Path(b))

	_, err = st.MoveFolder(c, b, Position{})
	assert.ErrorIs(t, err, ErrInvalid, "pas dans un descendant")
	_, err = st.MoveFolder(c, c, Position{})
	assert.ErrorIs(t, err, ErrInvalid, "pas dans lui-meme")

	// Conflit de nom a destination.
	mustCreate(t, st, "B", "")
	_, err = st.MoveFolder(b, "", Position{})
	assert.ErrorIs(t, err, ErrConflict)

	// Retour a la racine une fois le conflit leve.
	root := st.Children("")
	for _, f := range root {
		if f.Name == "B" {
			_, derr := st.DeleteFolder(f.ID, false)
			require.NoError(t, derr)
		}
	}
	renames, err = st.MoveFolder(b, "", Position{})
	require.NoError(t, err)
	assert.Equal(t, []Rename{{"s1", "B / x"}}, renames)
}

func TestMoveFolder_Profondeur(t *testing.T) {
	st := stateOf(KindSegments)
	chain := ""
	for i := 0; i < MaxDepth-1; i++ {
		chain = mustCreate(t, st, string(rune('a'+i)), chain)
	}
	sub := mustCreate(t, st, "x", "")
	mustCreate(t, st, "y", sub)
	_, err := st.MoveFolder(sub, chain, Position{})
	assert.ErrorIs(t, err, ErrInvalid, "le sous-arbre depasserait la profondeur maximale")
}

func TestDeleteFolder(t *testing.T) {
	st := stateOf(KindSegments, Item{"s1", "A / B / x"}, Item{"s2", "A / y"})
	st.Adopt()
	a := st.Children("")[0].ID

	_, err := st.DeleteFolder(a, false)
	assert.ErrorIs(t, err, ErrConflict, "refus si non vide")
	assert.Contains(t, st.Folders, a, "rien n'est supprime en cas de refus")

	renames, err := st.DeleteFolder(a, true)
	require.NoError(t, err)
	assert.NotContains(t, st.Folders, a)
	assert.ElementsMatch(t, []Rename{{"s1", "B / x"}, {"s2", "y"}}, renames)
	assert.Equal(t, []string{"B"}, names(st, ""))
	assert.Contains(t, st.Items, "s1", "jamais un objet supprime")
	assert.Contains(t, st.Items, "s2")

	empty := mustCreate(t, st, "Vide", "")
	_, err = st.DeleteFolder(empty, false)
	assert.NoError(t, err)
	_, err = st.DeleteFolder("inconnu", false)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestDeleteFolder_RemonteSansCollision(t *testing.T) {
	st := stateOf(KindSegments, Item{"s", "A / B / x"}, Item{"t", "B / y"})
	st.Adopt()
	var a string
	for _, f := range st.Children("") {
		if f.Name == "A" {
			a = f.ID
		}
	}
	_, err := st.DeleteFolder(a, true)
	assert.ErrorIs(t, err, ErrConflict, "B existe deja a la racine : la remontee est refusee, rien ne change")
	assert.Contains(t, st.Folders, a)
}

func TestReorder(t *testing.T) {
	st := stateOf(KindSegments, Item{"i1", "i1"}, Item{"i2", "i2"})
	a, b, c := mustCreate(t, st, "A", ""), mustCreate(t, st, "B", ""), mustCreate(t, st, "C", "")
	require.NoError(t, st.Reorder("", []string{c, a}, nil))
	assert.Equal(t, []string{"C", "A", "B"}, names(st, ""), "les non listes suivent")
	_ = b
	_, err := st.MoveItems([]string{"i1", "i2"}, "", Position{}, "")
	require.NoError(t, err)
	require.NoError(t, st.Reorder("", nil, []string{"i2", "i1"}))
	assert.Equal(t, []string{"i2", "i1"}, st.ItemsIn(""))
	assert.ErrorIs(t, st.Reorder("", []string{"inconnu"}, nil), ErrInvalid)
	assert.ErrorIs(t, st.Reorder("", []string{a, a}, nil), ErrInvalid)
	assert.ErrorIs(t, st.Reorder("nope", []string{a}, nil), ErrNotFound)
}

func TestEnsurePathEtFindPath(t *testing.T) {
	st := stateOf(KindSegments)
	id, err := st.EnsurePath("A / B / C")
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B", "C"}, st.Path(id))
	again, err := st.EnsurePath("a / b / c")
	require.NoError(t, err)
	assert.Equal(t, id, again, "idempotent, casse ignoree")
	assert.Len(t, st.Folders, 3)
	found, err := st.FindPath("A / B")
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B"}, st.Path(found))
	_, err = st.FindPath("A / Z")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestNomTropLongRefuse(t *testing.T) {
	long := ""
	for i := 0; i < 24; i++ {
		long += "0123456789"
	}
	st := stateOf(KindSegments, Item{"s", long})
	d := mustCreate(t, st, long, "")
	_, err := st.MoveItems([]string{"s"}, d, Position{}, "")
	assert.ErrorIs(t, err, ErrInvalid, "le nom complet depasserait 255 caracteres")
	assert.Equal(t, long, st.Items["s"].Name)
}

func TestDiff_Minimal(t *testing.T) {
	st := stateOf(KindSegments, Item{"s1", "A / x"}, Item{"s2", "A / y"})
	st.Adopt()
	before := st.Clone()
	assert.True(t, Between(before, st, nil).Empty())
	a := st.Children("")[0].ID
	_, err := st.RenameFolder(a, "AA")
	require.NoError(t, err)
	d := Between(before, st, nil)
	assert.Len(t, d.FolderUpserts, 1)
	assert.Empty(t, d.PlaceUpserts, "les appartenances ne bougent pas")
	assert.Empty(t, d.FolderDeletes)
}

func TestDiff_OrdreParentsPuisEnfants(t *testing.T) {
	st := stateOf(KindSegments, Item{"s", "A / B / C / x"})
	empty := stateOf(KindSegments, Item{"s", "A / B / C / x"})
	st.Adopt()
	d := Between(empty, st, nil)
	require.Len(t, d.FolderUpserts, 3)
	assert.Equal(t, "A", d.FolderUpserts[0].Name)
	assert.Equal(t, "C", d.FolderUpserts[2].Name)
	back := Between(st, empty, nil)
	require.Len(t, back.FolderDeletes, 3)
	assert.Equal(t, st.Folders[back.FolderDeletes[0]].Name, "C", "enfants avant parents")
}

func TestView_Compteurs(t *testing.T) {
	st := stateOf(KindSegments, Item{"s1", "A / B / x"}, Item{"s2", "A / y"}, Item{"s3", "libre"})
	st.Adopt()
	empty := mustCreate(t, st, "Vide", "")
	v := st.View()
	by := map[string]FolderView{}
	for _, f := range v.Folders {
		by[f.PathText] = f
	}
	assert.Equal(t, 2, by["A"].Count)
	assert.Equal(t, 1, by["A"].DirectItems)
	assert.Equal(t, 1, by["A"].Subfolders)
	assert.Equal(t, 1, by["A / B"].Count)
	assert.Equal(t, 0, by["Vide"].Count)
	_ = empty
	require.Len(t, v.Items, 3)
	assert.Equal(t, "s3", v.Items[2].ID)
	assert.Nil(t, v.Items[2].Position)
	assert.Nil(t, v.Items[2].FolderID)
}
