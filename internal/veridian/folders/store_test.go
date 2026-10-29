package folders

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Notifuse/notifuse/internal/veridian/folders/foldertest"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Vrai PostgreSQL jetable (VERIDIAN_TEST_POSTGRES_DSN) : le SQL (colonnes generees, contraintes
// differees, verrou, renommage dans les tables upstream) ne se prouve pas en mock. Chaque test
// a son schema.
func testDB(t *testing.T) *sql.DB { return foldertest.NewDB(t) }

func seedAnimalsd(t *testing.T, db *sql.DB) []fixtureSegment {
	var segs []fixtureSegment
	readJSON(t, "animalsd_segments.json", &segs)
	for _, s := range segs {
		_, err := db.Exec(`INSERT INTO segments (id, name) VALUES ($1, $2)`, s.ID, s.Name)
		require.NoError(t, err)
	}
	return segs
}

func names3(t *testing.T, db *sql.DB, table string) map[string]string {
	rows, err := db.Query(`SELECT id, name FROM ` + table + ` ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		require.NoError(t, rows.Scan(&id, &name))
		out[id] = name
	}
	return out
}

func TestMigration_ConvertitLesNomsExistantsSansRienPerdre(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	segs := seedAnimalsd(t, db)
	_, err := db.Exec(`INSERT INTO lists (id, name) VALUES ('l1','1 Prospection / Séquence froide'), ('l2','Libre'), ('l3','9 Technique / Mort')`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE lists SET deleted_at = NOW() WHERE id = 'l3'`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO templates (id, name, version) VALUES ('t1','Ancienne version',1), ('t1','Mails / Bienvenue',2), ('t2','Mails / Mort',1)`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE templates SET deleted_at = NOW() WHERE id = 't2'`)
	require.NoError(t, err)

	before := names3(t, db, "segments")
	listsBefore := names3(t, db, "lists")

	require.NoError(t, EnsureSchema(ctx, db))
	require.NoError(t, EnsureSchema(ctx, db), "schema idempotent")
	reports, err := MigrateWorkspace(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, len(segs), reports[KindSegments].Adopted)
	assert.Equal(t, 1, reports[KindLists].Adopted, "la liste supprimee n'est pas adoptee")
	assert.Equal(t, 1, reports[KindTemplates].Adopted, "seule la derniere version vivante compte")

	// Aucun nom modifie dans les tables upstream.
	assert.Equal(t, before, names3(t, db, "segments"))
	assert.Equal(t, listsBefore, names3(t, db, "lists"))

	var nSeg, nFold int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_folder_items WHERE kind='segments' AND folder_id IS NOT NULL`).Scan(&nSeg))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_folders WHERE kind='segments'`).Scan(&nFold))
	assert.Equal(t, len(segs), nSeg)
	// 6 dossiers de tete, 6 familles sous « 2 Cibles », et le faux dossier « Champs corrompus (cart_ » que
	// l'ancienne convention fabriquait deja (un « / » accidentel dans le nom d'un segment).
	assert.Equal(t, 13, nFold)

	// Seconde passe : aucun changement.
	reports, err = MigrateWorkspace(ctx, db)
	require.NoError(t, err)
	for kind, r := range reports {
		assert.False(t, r.Changed(), "%s : la migration rejouee ne change rien", kind)
	}
}

func fakeService(db *sql.DB, calls *[]string) *Service {
	return &Service{
		DB: func(ctx context.Context, ws string) (*sql.DB, error) { return db, nil },
		Authz: func(ctx context.Context, ws string, kind Kind, write bool) error {
			if calls != nil {
				*calls = append(*calls, fmt.Sprintf("%s:%s:%v", ws, kind, write))
			}
			return nil
		},
	}
}

func TestService_DeplacementsEcrivantLeNomDeCompatibilite(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	require.NoError(t, EnsureSchema(ctx, db))
	seedAnimalsd(t, db)
	svc := fakeService(db, nil)

	view, err := svc.View(ctx, "ws", KindSegments)
	require.NoError(t, err)
	require.NotNil(t, view.Adopt, "la lecture range ce qui ne l'etait pas")
	assert.Equal(t, 34, view.Adopt.Adopted)

	var prio string
	for _, f := range view.Folders {
		if f.PathText == "1 Priorité" {
			prio = f.ID
		}
	}
	require.NotEmpty(t, prio)

	// Un nouveau dossier racine, dans lequel on range P1 : le nom stocke suit.
	res, err := svc.CreateFolder(ctx, "ws", KindSegments, "Archive", "", Position{})
	require.NoError(t, err)
	res, err = svc.MoveItems(ctx, "ws", KindSegments, []string{"a_priorite_p1"}, res.Folder.ID, Position{}, "")
	require.NoError(t, err)
	assert.Equal(t, "Archive / P1 · à contacter en premier", res.Moved[0].Name)
	assert.Equal(t, "Archive / P1 · à contacter en premier", names3(t, db, "segments")["a_priorite_p1"])
	assert.Equal(t, "1 Priorité / P2 · à contacter ensuite", names3(t, db, "segments")["a_priorite_p2"], "les autres ne bougent pas")

	// Renommer un dossier reecrit tous les noms de son sous-arbre, et seulement eux.
	var cibles string
	for _, f := range view.Folders {
		if f.PathText == "2 Cibles" {
			cibles = f.ID
		}
	}
	res, err = svc.RenameFolder(ctx, "ws", cibles, "Cibles")
	require.NoError(t, err)
	assert.Len(t, res.Renamed, 12)
	assert.Equal(t, "Cibles / Animaleries indépendantes / P1", names3(t, db, "segments")["animaleriesindepp1"])
	assert.Equal(t, "3 Zones / Proche (≤ 100 km de Faramans)", names3(t, db, "segments")["d_zone_proche"])

	// Deplacer le dossier sous Archive.
	res, err = svc.MoveFolder(ctx, "ws", cibles, mustFolder(t, db, "Archive"), Position{})
	require.NoError(t, err)
	assert.Equal(t, "Archive / Cibles / Animaleries indépendantes / P1", names3(t, db, "segments")["animaleriesindepp1"])

	// Le nom lu par un client a l'ancienne est reparsable en le meme chemin que l'arbre.
	v2, err := svc.View(ctx, "ws", KindSegments)
	require.NoError(t, err)
	assert.Nil(t, v2.Adopt, "rien a adopter : les noms et l'arbre concordent")
	for _, it := range v2.Items {
		if it.FolderID == nil {
			continue
		}
		assert.Equal(t, it.PathText+Separator+it.Label, it.Name, it.ID)
	}

	// Supprimer un dossier non vide est refuse ; to_parent remonte.
	_, err = svc.DeleteFolder(ctx, "ws", cibles, false)
	assert.ErrorIs(t, err, ErrConflict)
	_, err = svc.DeleteFolder(ctx, "ws", cibles, true)
	require.NoError(t, err)
	assert.Equal(t, "Archive / Animaleries indépendantes / P1", names3(t, db, "segments")["animaleriesindepp1"])
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM segments`).Scan(&n))
	assert.Equal(t, 34, n, "aucun segment supprime")
}

func mustFolder(t *testing.T, db *sql.DB, name string) string {
	var id string
	require.NoError(t, db.QueryRow(`SELECT id FROM veridian_folders WHERE name = $1`, name).Scan(&id))
	return id
}

func TestService_RemonteeAvecHomonymeDifferePAsUneViolation(t *testing.T) {
	// A / A / x : supprimer le A exterieur remonte le A interieur sous le meme nom, au meme
	// endroit : l'etat intermediaire viole l'unicite, l'etat final non (contrainte differee).
	db := testDB(t)
	ctx := context.Background()
	require.NoError(t, EnsureSchema(ctx, db))
	_, err := db.Exec(`INSERT INTO lists (id, name) VALUES ('x', 'A / A / x')`)
	require.NoError(t, err)
	svc := fakeService(db, nil)
	view, err := svc.View(ctx, "ws", KindLists)
	require.NoError(t, err)
	var outer string
	for _, f := range view.Folders {
		if f.Depth == 1 {
			outer = f.ID
		}
	}
	_, err = svc.DeleteFolder(ctx, "ws", outer, true)
	// Le dossier remonte porte le meme nom que celui qu'on supprime : c'est accepte (il prend sa place).
	require.NoError(t, err)
	assert.Equal(t, "A / x", names3(t, db, "lists")["x"])
}

func TestService_ConcurrenceDeuxCreationsDuMemeNom(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	require.NoError(t, EnsureSchema(ctx, db))
	svc := fakeService(db, nil)
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.CreateFolder(ctx, "ws", KindTemplates, "Meme", "", Position{})
		}(i)
	}
	wg.Wait()
	ok, conflicts := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			ok++
		case strings.Contains(e.Error(), "conflit"):
			conflicts++
		default:
			t.Fatalf("erreur inattendue : %v", e)
		}
	}
	assert.Equal(t, 1, ok)
	assert.Equal(t, 5, conflicts)
}

func TestService_ModeleRenommeSeulementSaDerniereVersion(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	require.NoError(t, EnsureSchema(ctx, db))
	_, err := db.Exec(`INSERT INTO templates (id, name, version) VALUES ('t1','v1 nom',1), ('t1','v2 nom',2)`)
	require.NoError(t, err)
	svc := fakeService(db, nil)
	f, err := svc.CreateFolder(ctx, "ws", KindTemplates, "Mails", "", Position{})
	require.NoError(t, err)
	_, err = svc.MoveItems(ctx, "ws", KindTemplates, []string{"t1"}, f.Folder.ID, Position{}, "")
	require.NoError(t, err)
	var v1, v2 string
	require.NoError(t, db.QueryRow(`SELECT name FROM templates WHERE id='t1' AND version=1`).Scan(&v1))
	require.NoError(t, db.QueryRow(`SELECT name FROM templates WHERE id='t1' AND version=2`).Scan(&v2))
	assert.Equal(t, "v1 nom", v1)
	assert.Equal(t, "Mails / v2 nom", v2)
}

func TestService_DroitsEtLectureSansDroitDEcriture(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	require.NoError(t, EnsureSchema(ctx, db))
	_, err := db.Exec(`INSERT INTO lists (id, name) VALUES ('x', 'A / x')`)
	require.NoError(t, err)

	var calls []string
	ro := fakeService(db, &calls)
	ro.Authz = func(ctx context.Context, ws string, kind Kind, write bool) error {
		calls = append(calls, fmt.Sprintf("%s:%v", kind, write))
		if write {
			return fmt.Errorf("lecture seule")
		}
		return nil
	}
	v, err := ro.View(ctx, "ws", KindLists)
	require.NoError(t, err)
	require.Len(t, v.Folders, 1, "la vue est calculee")
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_folders`).Scan(&n))
	assert.Equal(t, 0, n, "sans droit d'ecriture rien n'est persiste")

	_, err = ro.CreateFolder(ctx, "ws", KindLists, "Nope", "", Position{})
	assert.EqualError(t, err, "lecture seule")
	assert.Contains(t, calls, "lists:true")

	_, err = ro.View(ctx, "", KindLists)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = ro.View(ctx, "ws", Kind("autre"))
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestService_AppartenanceOrphelineEstPurgee(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	require.NoError(t, EnsureSchema(ctx, db))
	_, err := db.Exec(`INSERT INTO segments (id, name) VALUES ('s','S')`)
	require.NoError(t, err)
	svc := fakeService(db, nil)
	f, err := svc.CreateFolder(ctx, "ws", KindSegments, "D", "", Position{})
	require.NoError(t, err)
	_, err = svc.MoveItems(ctx, "ws", KindSegments, []string{"s"}, f.Folder.ID, Position{}, "")
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE segments SET status='deleted' WHERE id='s'`)
	require.NoError(t, err)
	v, err := svc.View(ctx, "ws", KindSegments)
	require.NoError(t, err)
	assert.Empty(t, v.Items)
	assert.Equal(t, 0, v.Folders[0].Count)
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM veridian_folder_items`).Scan(&n))
	assert.Equal(t, 0, n)
}

func TestTranslateDBError(t *testing.T) {
	assert.ErrorIs(t, translateDBError(&pq.Error{Code: "23505"}), ErrConflict, "unicite -> conflit, pas une panne")
	assert.ErrorIs(t, translateDBError(&pq.Error{Code: "23503"}), ErrConflict)
	other := &pq.Error{Code: "08006"}
	assert.Equal(t, other, translateDBError(other))
	assert.NoError(t, translateDBError(nil))
}
