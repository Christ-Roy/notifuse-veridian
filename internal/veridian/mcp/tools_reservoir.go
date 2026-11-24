package mcp

import (
	"encoding/json"
	"net/http"

	"github.com/Notifuse/notifuse/internal/domain"
)

var (
	readContacts    = also{domain.PermissionResourceContacts, domain.PermissionTypeRead}
	readLists       = also{domain.PermissionResourceLists, domain.PermissionTypeRead}
	writeLists      = also{domain.PermissionResourceLists, domain.PermissionTypeWrite}
	readAutomations = also{domain.PermissionResourceAutomations, domain.PermissionTypeRead}
	readHistory     = also{domain.PermissionResourceMessageHistory, domain.PermissionTypeRead}
)

// Outils de prospection en volume (fiche 85) : alimenter des réservoirs, activer par lots, suivre. AUCUN ne relève un plafond,
// un débit ou une fenêtre d'envoi (garde de test) : la limite quotidienne se règle dans la console, pas depuis une IA.
var reservoirTools = []tool{
	{Name: "contacts_import", Description: "Importe jusqu'a 1000 contacts : dedoublonne par email, SIREN et domaine, ecarte desinscrits, rebonds et plaintes, inscrit aux listes. Commence par dry_run=true. Refuse de demarrer une sequence active sauf allow_triggers=true.",
		Resource: domain.PermissionResourceContacts, Write: true, Destructive: true, Method: http.MethodPost, Path: "/api/veridian/contacts.intake",
		Props: map[string]any{
			"contacts":       objs("1 a 1000 contacts : email obligatoire ; first_name, last_name, phone, job_title, custom_string_1 a 5... ; siren ou siret, website ou domaine servent au dedoublonnage ; tout autre champ (ville, naf, raison_sociale) est garde dans custom_json_1", 1000),
			"list_ids":       strs("listes ou inscrire les contacts (lists_list), de preference une liste de reserve"),
			"dedupe":         strs("cles de dedoublonnage : email, siren, domain (defaut : les trois)"),
			"existing":       str("adresse deja presente : attach (defaut, fiche intacte, ajoutee aux listes), update (fiche completee) ou skip"),
			"dry_run":        boolean("simulation : compte sans rien ecrire"),
			"allow_triggers": boolean("accepter qu'une sequence active demarre sur ces contacts, donc ENVOIE des mails"),
		}, Required: []string{"contacts"}},
	{Name: "contacts_count", Description: "Nombre de contacts de l'espace.", Resource: domain.PermissionResourceContacts,
		Method: http.MethodGet, Path: "/api/contacts.count", Props: map[string]any{}},
	{Name: "contacts_by_provider", Description: "Repartition des contacts par fournisseur de messagerie (Google, Microsoft, OVH...).", Resource: domain.PermissionResourceContacts,
		Method: http.MethodGet, Path: "/api/veridian/contacts.providerBreakdown", Props: map[string]any{"list_id": str("limiter a une liste")}},
	{Name: "lists_create", Description: "Cree une liste (par exemple une liste de reserve).", Resource: domain.PermissionResourceLists, Write: true,
		Method: http.MethodPost, Path: "/api/lists.create", Props: map[string]any{"id": str("identifiant, lettres et chiffres, 32 au plus"), "name": str("nom affiche"), "description": str("a quoi sert la liste"),
			"is_public": boolean("visible des contacts"), "is_double_optin": boolean("confirmation par mail avant inscription")}, Required: []string{"id", "name"}},
	{Name: "segment_contacts", Description: "Contacts d'un segment, pagines. Un segment se recalcule seul d'apres ses regles : on n'y ajoute personne a la main.", Resource: domain.PermissionResourceContacts,
		Method: http.MethodGet, Path: "/api/segments.contacts", Paginated: true, Props: withPage(map[string]any{"segment_id": str("id du segment (segments_list)")}), Required: []string{"segment_id"}},
	{Name: "segment_rebuild", Description: "Recalcule un segment maintenant (une sequence qui se declenche sur ce segment peut demarrer).", Resource: domain.PermissionResourceContacts, Write: true, Destructive: true,
		Method: http.MethodPost, Path: "/api/segments.rebuild", Props: map[string]any{"segment_id": str("id du segment")}, Required: []string{"segment_id"}},

	{Name: "reservoirs_status", Description: "Etat des reservoirs (listes) : total, activables, et pourquoi pas les autres (desinscrits, rebonds, plaintes, deja repondu, deja contactes, deja en sequence).", Resource: domain.PermissionResourceContacts,
		Also: []also{readLists, readAutomations, readHistory}, Method: http.MethodGet, Path: "/api/veridian/reservoir.summary", Props: map[string]any{"list_ids": str("listes, separees par des virgules (defaut : toutes, 20 au plus)"), "automation_id": str("sequence visee : ecarte ceux qui y sont deja passes")}},
	{Name: "reservoir_activate", Description: "Fait entrer un lot de contacts dans une sequence ACTIVE (donc ENVOIE, dans la limite quotidienne). Ecarte les bloques, ceux deja contactes ou deja en sequence. Commence par dry_run=true.",
		Resource: domain.PermissionResourceAutomations, Write: true, Destructive: true, Also: []also{readContacts, writeLists, readHistory}, Method: http.MethodPost, Path: "/api/veridian/reservoir.activate",
		Props: map[string]any{
			"automation_id": str("sequence (automations_list), statut live"),
			"list_id":       str("source : une liste de reserve"), "segment_id": str("source : un segment"), "emails": strs("source : des adresses (1000 au plus)"),
			"limit":             num("taille du lot (defaut 100, max 1000)"),
			"include_contacted": boolean("reprendre aussi ceux qui ont deja recu un mail commercial"),
			"batch_id":          str("nom du lot, a fournir TOUJOURS depuis un programme : rejouer le meme nom ne fait entrer personne de plus"),
			"dry_run":           boolean("simulation : choisit sans inscrire"),
		}, Required: []string{"automation_id"}},
	{Name: "stock_overview", Description: "Reserve et inscrits par fournisseur : jours de prospection restants, classes a reapprovisionner.", Resource: domain.PermissionResourceContacts,
		Also: []also{readHistory}, Method: http.MethodGet, Path: "/api/veridian/stock.overview", Props: map[string]any{"days": num("jours de couverture vises (1 a 60)")}},
	{Name: "stock_suggest", Description: "Combien de contacts de la reserve activer par fournisseur pour N jours sans depasser les plafonds. N'inscrit personne.", Resource: domain.PermissionResourceContacts,
		Also: []also{readHistory}, Method: http.MethodGet, Path: "/api/veridian/policy.suggest-enrollment", Props: map[string]any{"days": num("jours de couverture (1 a 60)")}},

	{Name: "automations_list", Description: "Liste les sequences (automatisations) : statut, declencheur, compteurs.", Resource: domain.PermissionResourceAutomations,
		Method: http.MethodGet, Path: "/api/automations.list", Paginated: true, Props: withPage(map[string]any{"status": str("draft, live ou paused")}), Compact: compactAutomations},
	{Name: "automations_get", Description: "Lit une sequence en entier (noeuds compris).", Resource: domain.PermissionResourceAutomations,
		Method: http.MethodGet, Path: "/api/automations.get", Props: map[string]any{"automation_id": str("id de la sequence")}, Required: []string{"automation_id"}},
	{Name: "automations_pause", Description: "Met une sequence en pause : plus rien ne part, les mails en file attendent.", Resource: domain.PermissionResourceAutomations, Write: true,
		Method: http.MethodPost, Path: "/api/automations.pause", Props: map[string]any{"automation_id": str("id de la sequence")}, Required: []string{"automation_id"}},
	{Name: "automations_activate", Description: "Active ou reprend une sequence : elle ENVOIE des mails reels.", Resource: domain.PermissionResourceAutomations, Write: true, Destructive: true,
		Method: http.MethodPost, Path: "/api/automations.activate", Props: map[string]any{"automation_id": str("id de la sequence")}, Required: []string{"automation_id"}},
	{Name: "automations_enrollments", Description: "Contacts passes par une sequence : statut, etape, raison de sortie.", Resource: domain.PermissionResourceAutomations,
		Method: http.MethodGet, Path: "/api/automations.enrollments", Paginated: true, Props: withPage(map[string]any{"automation_id": str("id de la sequence"), "status": str("active, completed, exited ou failed")}), Required: []string{"automation_id"}},
	{Name: "automations_exit_contact", Description: "Sort un contact d'une sequence : il ne recevra plus la suite.", Resource: domain.PermissionResourceAutomations, Write: true,
		Method: http.MethodPost, Path: "/api/automations.exitContact", Props: map[string]any{"automation_id": str("id de la sequence"), "email": str("adresse du contact"), "reason": str("raison (facultatif)")}, Required: []string{"automation_id", "email"}},

	{Name: "sending_quota", Description: "Quota d'envoi du jour par profil : plafond, envoyes, restants, ce qui limite, fenetre horaire, alertes.", Resource: domain.PermissionResourceMessageHistory,
		Method: http.MethodGet, Path: "/api/veridian/emailProfiles.overview", Props: map[string]any{}, Compact: compactQuota},
	{Name: "sending_stats", Description: "Statistiques de prospection par sequence et par etape : envoyes, en file, reponses, sorties.", Resource: domain.PermissionResourceMessageHistory,
		Also: []also{readContacts}, Method: http.MethodGet, Path: "/api/veridian/prospection.stats", Props: map[string]any{"since": str("depuis AAAA-MM-JJ"), "until": str("jusqu'a AAAA-MM-JJ")}},
	{Name: "sending_queue", Description: "Pourquoi des mails attendent : file d'envoi groupee par raison.", Resource: domain.PermissionResourceAutomations,
		Method: http.MethodGet, Path: "/api/veridian/queue.explain", Props: map[string]any{"group_by": str("reason, class, profile, automation"), "automation": str("limiter a une sequence")}},
	{Name: "replies_stats", Description: "Nombre de reponses de prospects.", Resource: domain.PermissionResourceMessageHistory,
		Method: http.MethodGet, Path: "/api/veridian/messages.replyStats", Props: map[string]any{}},
}

// pick garde les clés voulues d'un objet.
func pick(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			out[k] = v
		}
	}
	return out
}

// compactList réduit chaque objet du tableau field. Forme inattendue : la réponse d'origine, jamais une réponse vidée.
func compactList(raw []byte, field string, f func(map[string]any) map[string]any) []byte {
	var o map[string]any
	if json.Unmarshal(raw, &o) != nil {
		return raw
	}
	items, ok := o[field].([]any)
	if !ok {
		return raw
	}
	out := make([]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, f(m))
		}
	}
	o[field] = out
	b, err := json.Marshal(o)
	if err != nil {
		return raw
	}
	return b
}

func compactSegments(raw []byte) []byte {
	return compactList(raw, "segments", func(m map[string]any) map[string]any {
		return pick(m, "id", "name", "status", "version", "users_count", "folder_id")
	})
}

func compactAutomations(raw []byte) []byte {
	return compactList(raw, "automations", func(m map[string]any) map[string]any {
		out := pick(m, "id", "name", "status", "list_id", "trigger", "stats", "created_at", "updated_at")
		if n, ok := m["nodes"].([]any); ok {
			out["nodes_count"] = len(n)
		}
		return out
	})
}

func compactQuota(raw []byte) []byte {
	return compactList(raw, "profiles", func(m map[string]any) map[string]any {
		out := pick(m, "integration_id", "name", "usage", "in_rotation", "paused", "verified", "senders")
		plan, ok := m["plan"].(map[string]any)
		if !ok {
			return out
		}
		p := pick(plan, "date", "mode", "paused", "daily_cap_today", "sent_today", "reserved_today", "remaining_today", "remaining_gate", "limiting_gate", "limiting_detail",
			"warmup", "window", "excluded_classes", "complaints_7d", "reputation_alert")
		classes := []any{}
		list, _ := plan["classes"].([]any)
		for _, c := range list {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if sent, _ := cm["sent_today"].(float64); sent > 0 {
				classes = append(classes, pick(cm, "class", "daily_cap", "sent_today", "remaining", "blocked_by"))
			}
		}
		p["classes_sent_today"] = classes
		out["plan"] = p
		return out
	})
}
