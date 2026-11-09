package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/Notifuse/notifuse/internal/domain"
)

// tool : un outil MCP = une route de l'API Notifuse. Le droit exige est celui de l'upstream (ressource +
// lecture/ecriture) : la liste est filtree avec le meme calcul, l'appel est refuse en amont par la route.
type tool struct {
	Name        string
	Description string // une ligne
	Resource    domain.PermissionResource
	Write       bool // ecriture (readOnlyHint faux)
	Destructive bool // efface ou ENVOIE un mail (destructiveHint vrai)
	Method      string
	Path        string
	Props       map[string]any // schema JSON des arguments
	Required    []string
	Paginated   bool
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
func obj(desc string) map[string]any { return map[string]any{"type": "object", "description": desc} }

var pageProps = map[string]any{
	"limit":  num("nombre de lignes (defaut 25, max 100)"),
	"offset": num("decalage de pagination"),
	"cursor": str("curseur de la page suivante, tel que rendu par l'appel precedent"),
}

func withPage(p map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range pageProps {
		out[k] = v
	}
	for k, v := range p {
		out[k] = v
	}
	return out
}

// catalog : les outils utiles, pas toute l'API. Tout envoi de mail est destructif (garde de test).
var catalog = []tool{
	{Name: "contacts_list", Description: "Liste les contacts de l'espace (filtre email, liste).", Resource: domain.PermissionResourceContacts,
		Method: http.MethodGet, Path: "/api/contacts.list", Paginated: true,
		Props: withPage(map[string]any{"email": str("filtre par adresse"), "list_id": str("filtre par liste")})},
	{Name: "contacts_get_by_email", Description: "Lit un contact par son adresse.", Resource: domain.PermissionResourceContacts,
		Method: http.MethodGet, Path: "/api/contacts.getByEmail", Props: map[string]any{"email": str("adresse du contact")}, Required: []string{"email"}},
	{Name: "contacts_upsert", Description: "Cree ou met a jour un contact (email obligatoire).", Resource: domain.PermissionResourceContacts, Write: true,
		Method: http.MethodPost, Path: "/api/contacts.upsert", Props: map[string]any{"contact": obj("contact : email, first_name, last_name, champs libres")}, Required: []string{"contact"}},
	{Name: "contacts_delete", Description: "Supprime definitivement un contact par son adresse.", Resource: domain.PermissionResourceContacts, Write: true, Destructive: true,
		Method: http.MethodPost, Path: "/api/contacts.delete", Props: map[string]any{"email": str("adresse du contact")}, Required: []string{"email"}},
	{Name: "lists_list", Description: "Liste les listes de diffusion.", Resource: domain.PermissionResourceLists,
		Method: http.MethodGet, Path: "/api/lists.list", Props: map[string]any{}},
	{Name: "segments_list", Description: "Liste les segments de contacts.", Resource: domain.PermissionResourceContacts,
		Method: http.MethodGet, Path: "/api/segments.list", Props: map[string]any{}},
	{Name: "templates_list", Description: "Liste les modeles d'e-mail.", Resource: domain.PermissionResourceTemplates,
		Method: http.MethodGet, Path: "/api/templates.list", Props: map[string]any{}},
	{Name: "templates_get", Description: "Lit un modele d'e-mail par son id.", Resource: domain.PermissionResourceTemplates,
		Method: http.MethodGet, Path: "/api/templates.get", Props: map[string]any{"id": str("id du modele")}, Required: []string{"id"}},
	{Name: "broadcasts_list", Description: "Liste les campagnes (broadcasts).", Resource: domain.PermissionResourceBroadcasts,
		Method: http.MethodGet, Path: "/api/broadcasts.list", Paginated: true, Props: withPage(map[string]any{"status": str("filtre par statut")})},
	{Name: "broadcasts_get", Description: "Lit une campagne par son id.", Resource: domain.PermissionResourceBroadcasts,
		Method: http.MethodGet, Path: "/api/broadcasts.get", Props: map[string]any{"id": str("id de la campagne")}, Required: []string{"id"}},
	{Name: "transactional_list", Description: "Liste les notifications transactionnelles.", Resource: domain.PermissionResourceTransactional,
		Method: http.MethodGet, Path: "/api/transactional.list", Paginated: true, Props: withPage(nil)},
	{Name: "transactional_get", Description: "Lit une notification transactionnelle par son id.", Resource: domain.PermissionResourceTransactional,
		Method: http.MethodGet, Path: "/api/transactional.get", Props: map[string]any{"id": str("id de la notification")}, Required: []string{"id"}},
	{Name: "transactional_send", Description: "ENVOIE un e-mail transactionnel reel a un contact.", Resource: domain.PermissionResourceTransactional, Write: true, Destructive: true,
		Method: http.MethodPost, Path: "/api/transactional.send", Props: map[string]any{"notification": obj("id (de la notification), contact {email}, data (variables)")}, Required: []string{"notification"}},
	{Name: "messages_list", Description: "Journal des messages envoyes (statut, ouverture, rebond).", Resource: domain.PermissionResourceMessageHistory,
		Method: http.MethodGet, Path: "/api/messages.list", Paginated: true, Props: withPage(map[string]any{"contact_email": str("filtre par destinataire"), "channel": str("email")})},
}

const (
	defaultLimit = 25
	maxLimit     = 100
)

func findTool(name string) (*tool, bool) {
	for i := range catalog {
		if catalog[i].Name == name {
			return &catalog[i], true
		}
	}
	return nil, false
}

// allowed : l'utilisateur a-t-il le droit exige sur cet espace (meme calcul que l'upstream) ?
func (t *tool) allowed(uw *domain.UserWorkspace) bool {
	need := domain.PermissionTypeRead
	if t.Write {
		need = domain.PermissionTypeWrite
	}
	return uw.HasPermission(t.Resource, need)
}

func (t *tool) descriptor() map[string]any {
	props := t.Props
	if props == nil {
		props = map[string]any{}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(t.Required) > 0 {
		schema["required"] = t.Required
	}
	return map[string]any{
		"name":        t.Name,
		"description": t.Description,
		"inputSchema": schema,
		"annotations": map[string]any{"readOnlyHint": !t.Write, "destructiveHint": t.Destructive},
	}
}

// request construit l'appel interne. L'espace est pose ICI, apres les arguments : une IA ne choisit jamais
// l'espace (un workspace_id fourni est ignore faute de figurer au schema).
func (t *tool) request(args map[string]any, workspaceID string) (query url.Values, body []byte, err error) {
	for _, r := range t.Required {
		if v, ok := args[r]; !ok || v == nil || v == "" {
			return nil, nil, fmt.Errorf("argument requis manquant : %s", r)
		}
	}
	picked := map[string]any{}
	for k := range t.Props {
		if v, ok := args[k]; ok && v != nil {
			picked[k] = v
		}
	}
	if t.Paginated {
		picked["limit"] = clampLimit(picked["limit"])
	}
	if t.Method == http.MethodPost {
		picked["workspace_id"] = workspaceID
		body, err = json.Marshal(picked)
		return nil, body, err
	}
	query = url.Values{}
	keys := make([]string, 0, len(picked))
	for k := range picked {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := picked[k].(type) {
		case string:
			query.Set(k, v)
		case float64:
			query.Set(k, strconv.FormatFloat(v, 'f', -1, 64))
		case int:
			query.Set(k, strconv.Itoa(v))
		case bool:
			query.Set(k, strconv.FormatBool(v))
		default:
			b, _ := json.Marshal(v)
			query.Set(k, string(b))
		}
	}
	query.Set("workspace_id", workspaceID)
	return query, nil, nil
}

func clampLimit(v any) int {
	n := defaultLimit
	switch x := v.(type) {
	case float64:
		n = int(x)
	case int:
		n = x
	}
	if n < 1 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}
