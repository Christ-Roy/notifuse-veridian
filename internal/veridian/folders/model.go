// Package folders : dossiers natifs (lot L2, migration V65) pour les segments, les listes
// et les modeles. Un dossier est une ligne d'un arbre (parent, ordre, nom) propre a un
// genre d'objet ; l'appartenance d'un objet a un dossier est une ligne de
// veridian_folder_items. Aucune table upstream n'est modifiee.
//
// Le NOM stocke de l'objet (segments.name, lists.name, templates.name) reste le « chemin
// complet » `A / B / C` : le serveur le tient a jour a chaque deplacement (nom de
// compatibilite), si bien que les clients qui lisent encore le nom restent justes. La
// verite est l'arbre ; le nom en est le reflet lisible.
//
// Le coeur (ce fichier, path.go, state.go, ops.go) est pur : aucune E/S, entierement
// testable sans base. store.go charge l'etat, applique le diff dans une transaction.
package folders

import "time"

// Kind est le genre d'objet range : un arbre par genre.
type Kind string

const (
	KindSegments  Kind = "segments"
	KindLists     Kind = "lists"
	KindTemplates Kind = "templates"
)

// Kinds liste les genres geres, dans l'ordre d'affichage.
var Kinds = []Kind{KindSegments, KindLists, KindTemplates}

// Valid dit si le genre est connu.
func (k Kind) Valid() bool {
	for _, c := range Kinds {
		if k == c {
			return true
		}
	}
	return false
}

const (
	// Separator est la convention historique de nom, « espace slash espace ».
	Separator = " / "
	// MaxDepth : profondeur maximale de l'arbre (dossiers imbriques).
	MaxDepth = 8
	// MaxNameLen : longueur maximale d'un nom de dossier ou d'un libelle (colonne VARCHAR(255)).
	MaxNameLen = 255
	// MaxFoldersPerKind borne la taille d'un arbre (garde-fou contre un agent emballe).
	MaxFoldersPerKind = 2000
)

// Folder est une ligne de veridian_folders.
type Folder struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	ParentID  string    `json:"parent_id"` // "" = racine
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

// Placement est une ligne de veridian_folder_items : l'objet ItemID est dans FolderID
// (vide = racine, avec un rang explicite) a la place Position.
type Placement struct {
	ItemID   string
	FolderID string
	Position int
}

// Item est un objet vivant lu dans la table upstream (segment, liste, modele).
type Item struct {
	ID   string
	Name string
}
