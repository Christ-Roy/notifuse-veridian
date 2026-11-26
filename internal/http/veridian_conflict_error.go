package http

import (
	"errors"
	"net/http"

	"github.com/lib/pq"
)

// ErrCodeAlreadyExists : une création a heurté un identifiant (ou une autre clé unique) déjà pris dans l'espace.
const ErrCodeAlreadyExists = "already_exists"

// veridianUniqueViolation reconnaît une violation de contrainte unique PostgreSQL (23505) au fond d'une chaîne
// d'erreurs enveloppées en %w (dépôt → service → handler). Rend la table touchée, vide si le pilote ne la donne pas.
//
// Mesuré en prod le 26/11/2026 (VER-243, outil MCP lists_create) : créer une liste sur un id existant rendait un
// 500 « Failed to create list », indiscernable d'une panne. Toutes les créations (listes, segments, modèles,
// automations, diffusions…) passaient par le même 500 : la classe se corrige ici, une fois, à côté de la
// classification 401/403 (classifyAuthError), sans toucher aux handlers upstream.
func veridianUniqueViolation(err error) (table string, ok bool) {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return pqErr.Table, true
	}
	return "", false
}

// veridianWriteConflict écrit un 409 nommé si err est une violation d'unicité, et rend true ; sinon n'écrit rien.
// La forme porte à la fois "error" (lu par la console et les clients upstream) et "code" (lu par la CLI et le MCP).
func veridianWriteConflict(w http.ResponseWriter, err error) bool {
	table, ok := veridianUniqueViolation(err)
	if !ok {
		return false
	}
	var details map[string]interface{}
	if table != "" {
		details = map[string]interface{}{"resource": table}
	}
	WriteJSONErrorCode(w, ErrCodeAlreadyExists, "already exists: an object with this id already exists in this workspace", http.StatusConflict, details)
	return true
}
