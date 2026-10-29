package folders

// DDL de la migration V65. Additif et idempotent : tables neuves seulement, aucune table
// upstream n'est touchee. Les contraintes sont DEFERRABLE : une operation qui deplace
// plusieurs dossiers a la fois (suppression avec remontee du contenu, par exemple) passe
// par des etats intermediaires que seul l'etat final doit rendre coherents.
//
// parent_key et name_key sont des colonnes generees : l'unicite des freres ignore la casse
// et traite la racine (parent NULL) comme un parent a part entiere. Une CONTRAINTE
// d'unicite (differable) ne peut pas porter d'expression, d'ou ces deux colonnes.
var SchemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS veridian_folders (
		id         VARCHAR(32) PRIMARY KEY,
		kind       VARCHAR(16) NOT NULL,
		parent_id  VARCHAR(32),
		name       VARCHAR(255) NOT NULL,
		position   INTEGER NOT NULL DEFAULT 0,
		parent_key VARCHAR(32) GENERATED ALWAYS AS (COALESCE(parent_id, '')) STORED,
		name_key   VARCHAR(255) GENERATED ALWAYS AS (lower(name)) STORED,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		CONSTRAINT veridian_folders_kind_chk CHECK (kind IN ('segments', 'lists', 'templates')),
		CONSTRAINT veridian_folders_parent_fk FOREIGN KEY (parent_id) REFERENCES veridian_folders(id) DEFERRABLE INITIALLY DEFERRED,
		CONSTRAINT veridian_folders_sibling_uq UNIQUE (kind, parent_key, name_key) DEFERRABLE INITIALLY DEFERRED
	)`,
	`CREATE INDEX IF NOT EXISTS idx_veridian_folders_parent ON veridian_folders(kind, parent_id, position)`,
	`CREATE TABLE IF NOT EXISTS veridian_folder_items (
		kind       VARCHAR(16) NOT NULL,
		item_id    VARCHAR(255) NOT NULL,
		folder_id  VARCHAR(32),
		position   INTEGER NOT NULL DEFAULT 0,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (kind, item_id),
		CONSTRAINT veridian_folder_items_kind_chk CHECK (kind IN ('segments', 'lists', 'templates')),
		CONSTRAINT veridian_folder_items_folder_fk FOREIGN KEY (folder_id) REFERENCES veridian_folders(id) DEFERRABLE INITIALLY DEFERRED
	)`,
	`CREATE INDEX IF NOT EXISTS idx_veridian_folder_items_folder ON veridian_folder_items(folder_id, position)`,
}
