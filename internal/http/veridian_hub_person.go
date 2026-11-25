package http

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Notifuse/notifuse/internal/service"
)

// Désigner une personne du Hub (CONTRAT-HUB 5.19.7, fiche 89) : `hub_user_id` obligatoire, l'adresse n'est qu'informative.

const (
	// ErrCodeHubUserConflict : l'adresse reçue est déjà portée ici par un autre compte, rien n'est écrit.
	ErrCodeHubUserConflict = "hub_user_conflict"
	// ErrCodeNotAMember : transfer-owner vers une personne qui n'est pas membre.
	ErrCodeNotAMember = "not_a_member"
	// ErrCodeAdminWithdrawn : admin retiré de l espace, rien d automatique ne le ramène (fiche 95).
	ErrCodeAdminWithdrawn = "admin_withdrawn"
	// ErrCodeOwnerExists : attach-owner sans transfert explicite alors qu un autre propriétaire humain existe.
	ErrCodeOwnerExists = "owner_exists"
)

// hubPerson est le bloc commun des corps signés du Hub. `email` est le nom du contrat ; les deux autres sont les anciens noms, encore acceptés.
type hubPerson struct {
	HubUserID    string `json:"hub_user_id"`
	Email        string `json:"email,omitempty"`
	UserEmail    string `json:"user_email,omitempty"`
	HubUserEmail string `json:"hub_user_email,omitempty"`
	EmailProven  bool   `json:"email_proven,omitempty"`
}

func (p hubPerson) address() string {
	for _, e := range []string{p.Email, p.UserEmail, p.HubUserEmail} {
		if e != "" {
			return e
		}
	}
	return ""
}

// requireHubUserID écrit le 400 d'un hub_user_id absent ou mal formé.
func requireHubUserID(w http.ResponseWriter, field, value string) bool {
	if value == "" {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, field+" is required", http.StatusBadRequest, map[string]interface{}{"missing": []string{field}})
		return false
	}
	if _, err := uuid.Parse(value); err != nil || len(value) != 36 {
		WriteJSONErrorCode(w, ErrCodeInvalidPayload, field+" must be a UUID", http.StatusBadRequest, map[string]interface{}{"field": field})
		return false
	}
	return true
}

// writeHubPersonError écrit les refus propres à la personne. false : l'erreur est d'une autre nature.
func writeHubPersonError(w http.ResponseWriter, err error, tenantID, hubUserID string) bool {
	details := map[string]interface{}{"tenant_id": tenantID, "hub_user_id": hubUserID}
	switch {
	case errors.Is(err, service.ErrUserNotInApp):
		WriteJSONErrorCode(w, ErrCodeUserNotInApp, "user_not_in_app", http.StatusNotFound, details)
	case errors.Is(err, service.ErrHubUserIDConflict):
		WriteJSONErrorCode(w, ErrCodeHubUserConflict, "this address belongs to another account here", http.StatusConflict, details)
	case errors.Is(err, service.ErrOwnerNotMember):
		WriteJSONErrorCode(w, ErrCodeNotAMember, "the new owner must already be a member", http.StatusConflict, details)
	case errors.Is(err, service.ErrAdminWithdrawn):
		WriteJSONErrorCode(w, ErrCodeAdminWithdrawn, "admin_withdrawn", http.StatusConflict, details)
	case errors.Is(err, service.ErrOwnerExists):
		WriteJSONErrorCode(w, ErrCodeOwnerExists, "another human owner holds this workspace; send transfer:true to take ownership from them", http.StatusConflict, details)
	default:
		return false
	}
	return true
}
