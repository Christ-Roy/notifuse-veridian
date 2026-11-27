package reservoir

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"

	"github.com/lib/pq"

	"github.com/Notifuse/notifuse/internal/domain"
)

// ProspectionLabels : le nom des champs que la prospection Veridian remplit à l'import (27/11/2026). Sans eux, la fiche d'un
// contact affichait « Custom String 1 à 5 » et « Custom Number 1 » : Robert, depuis la boîte d'Ilum Photos, ne pouvait pas lire
// la fiche d'un prospect. C'est le contrat que lisent déjà le stock par classe (policy/stock.go : custom_string_2 = priorité,
// custom_number_1 = score) et l'activation (store.go : le meilleur score d'abord), et celui des exports (skill email-marketing).
var ProspectionLabels = map[string]string{
	"custom_string_1": "Activité",
	"custom_string_2": "Priorité",
	"custom_string_3": "Département",
	"custom_string_4": "Entreprise",
	"custom_string_5": "Messagerie",
	"custom_number_1": "Score",
}

// Labeler nomme les champs qu'un import vient de remplir, sans jamais renommer un champ que l'espace a déjà nommé.
type Labeler interface {
	EnsureLabels(ctx context.Context, workspaceID string, labels map[string]string) error
}

// SQLLabeler écrit dans la base système, en une instruction : les libellés déjà posés gagnent (fusion jsonb « défauts || existants »),
// et un réglage enregistré au même moment par la console n'est pas écrasé, puisque seule la clé custom_field_labels est touchée.
// Rien n'est écrit quand tous les champs sont déjà nommés (la date de mise à jour de l'espace ne bouge pas à chaque import).
// Des réglages valant le JSON null (pas le NULL SQL) sont traités comme vides : jsonb_set échouait dessus (vu au test le 27/11).
type SQLLabeler struct{ DB *sql.DB }

func (l SQLLabeler) EnsureLabels(ctx context.Context, workspaceID string, labels map[string]string) error {
	if len(labels) == 0 {
		return nil
	}
	raw, err := json.Marshal(labels)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	_, err = l.DB.ExecContext(ctx, `
		WITH cur AS (
			SELECT id,
			       CASE WHEN jsonb_typeof(settings) = 'object' THEN settings ELSE '{}'::jsonb END AS s,
			       CASE WHEN jsonb_typeof(settings->'custom_field_labels') = 'object' THEN settings->'custom_field_labels' ELSE '{}'::jsonb END AS l
			FROM workspaces WHERE id = $1)
		UPDATE workspaces w
		SET settings = jsonb_set(cur.s, '{custom_field_labels}', $2::jsonb || cur.l), updated_at = NOW()
		FROM cur
		WHERE w.id = cur.id AND NOT (cur.l ?& $3)`,
		workspaceID, string(raw), pq.Array(keys))
	return err
}

var priorityFormat = regexp.MustCompile(`^P[1-4]$`)

// isProspectionExport : l'import porte la signature de l'export de prospection, une priorité P1 à P4 (custom_string_2) ET une
// classe de messagerie connue (custom_string_5) sur au moins un contact, et aucune valeur hors de ces formats. Mesuré le 27/11 en
// prod : les espaces emploient ces champs dans des sens propres (ville en custom_string_2 chez un client, espèce d'animal en
// custom_string_1 chez un autre). Hors de cette signature, nommer « Priorité » ou « Activité » tromperait : rien n'est nommé.
func isProspectionExport(contacts []*domain.Contact) bool {
	both := false
	for _, c := range contacts {
		p := c.CustomString2 != nil && !c.CustomString2.IsNull && c.CustomString2.String != ""
		k := c.CustomString5 != nil && !c.CustomString5.IsNull && c.CustomString5.String != ""
		if (p && !priorityFormat.MatchString(c.CustomString2.String)) || (k && !domain.IsValidProviderClass(c.CustomString5.String)) {
			return false
		}
		both = both || (p && k)
	}
	return both
}

// LabelsFor : les libellés du contrat pour les seuls champs que ces contacts remplissent, quand l'import est un export de
// prospection (isProspectionExport). Un import qui n'écrit que l'adresse ne nomme rien.
func LabelsFor(contacts []*domain.Contact) map[string]string {
	if !isProspectionExport(contacts) {
		return map[string]string{}
	}
	used := map[string]bool{}
	for _, c := range contacts {
		for key, set := range map[string]bool{
			"custom_string_1": c.CustomString1 != nil && !c.CustomString1.IsNull,
			"custom_string_2": c.CustomString2 != nil && !c.CustomString2.IsNull,
			"custom_string_3": c.CustomString3 != nil && !c.CustomString3.IsNull,
			"custom_string_4": c.CustomString4 != nil && !c.CustomString4.IsNull,
			"custom_string_5": c.CustomString5 != nil && !c.CustomString5.IsNull,
			"custom_number_1": c.CustomNumber1 != nil && !c.CustomNumber1.IsNull,
		} {
			if set {
				used[key] = true
			}
		}
	}
	out := map[string]string{}
	for key := range used {
		out[key] = ProspectionLabels[key]
	}
	return out
}
