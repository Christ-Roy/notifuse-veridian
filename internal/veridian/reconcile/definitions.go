package reconcile

// Definition : la définition écrite d'une grandeur comptée. Il n'y en a qu'une par
// grandeur ; toute surface (tableau de bord, fiche séquence, API, base) doit rendre le
// nombre que donne cette définition. Les contrôles du réconciliateur la rejouent.
type Definition struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Definition string `json:"definition"`
	Source     string `json:"source"`
}

// Definitions : le registre. Une modification de comptage commence ici.
var definitions = []Definition{
	{
		Key:        "inscrits",
		Label:      "Inscrits d'une séquence",
		Definition: "Nombre de lignes de contact_automations de la séquence, tous statuts confondus (un contact inscrit deux fois dans la même séquence compte deux fois).",
		Source:     "contact_automations, séquence non supprimée",
	},
	{
		Key:        "envoyes_etape",
		Label:      "Envoyés à une étape",
		Definition: "Contacts distincts de la séquence à qui un mail d'une étape donnée OU d'une étape suivante est réellement parti (ligne message_history avec sent_at et sans failed_at). Un mail mis en file qui n'est jamais parti (contact sorti avant l'envoi) ne compte pas.",
		Source:     "message_history par automation_id et modèle de l'étape",
	},
	{
		Key:        "reponses_humaines",
		Label:      "Réponses humaines",
		Definition: "Contacts distincts de la séquence qui ont une ligne veridian_contact_reply de type humain. Les réponses automatiques et les défis anti-spam ne comptent jamais.",
		Source:     "veridian_contact_reply, rattachée par appartenance à la séquence",
	},
	{
		Key:        "sorties_reponse",
		Label:      "Sorties pour réponse",
		Definition: "Lignes de contact_automations sorties dont la raison est de la famille « a répondu » (replied*, contact_replied). Un contact sorti pour réponse doit avoir une réponse humaine enregistrée.",
		Source:     "contact_automations.exit_reason, classé par VeridianClassifyExitReason",
	},
	{
		Key:        "stock_restant",
		Label:      "Stock restant",
		Definition: "Personnes distinctes actives dans au moins une liste non supprimée, à qui aucun message commercial n'est jamais parti, hors listes d'exclusion (nom contenant « exclu ») et hors personnes présentes dans l'une d'elles.",
		Source:     "contact_lists et message_history (hors transactionnel)",
	},
	{
		Key:        "compteurs_stockes",
		Label:      "Compteurs stockés d'une séquence",
		Definition: "automations.stats (inscrits, terminés, sortis, échoués) doit égaler le décompte des lignes de contact_automations par statut.",
		Source:     "automations.stats et contact_automations",
	},
}

// Definitions rend une copie du registre.
func Definitions() []Definition {
	out := make([]Definition, len(definitions))
	copy(out, definitions)
	return out
}
