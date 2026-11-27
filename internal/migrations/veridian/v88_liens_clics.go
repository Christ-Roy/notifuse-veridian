package veridian

// V88 (27/11/2026, reprise amont 35.0, « per-link click breakdown ») : détail des clics par lien d'un message.
// Expand seulement : une colonne JSONB nullable, sans défaut ni réécriture de table ; les lignes existantes
// lisent NULL jusqu'à leur premier clic. L'image précédente ignore la colonne. Le clic s'écrit par un UPDATE
// atomique (jsonb_set), jamais par lecture puis écriture, car Mail tourne en deux exemplaires.
func init() {
	Register(Spec{
		Number: 88, Lot: "Liens", Name: "liens_clics",
		Workspace: []string{
			`ALTER TABLE message_history ADD COLUMN IF NOT EXISTS clicked_links JSONB`,
		},
	})
}
