package domain

import (
	"context"
	"fmt"
	"strings"
)

// Veridian fork, lot 4 (08/10/2026) : API dédiée de l'usage d'un profil d'envoi.
//
// Avant ce lot la console écrivait l'usage par updateIntegration (pause) et
// workspaces.update (pool, profil transactionnel), deux écritures de tout un
// réglage de workspace, sans règle côté serveur sur la forme. Désormais trois
// opérations nommées, validées ici, qui modifient les settings d'un seul coup :
//
//   - setUsage : commercial (rotation) | transactional (le profil réservé) | unassigned ;
//   - pause / resume (commercial seulement : un mail transactionnel part toujours).
//
// Les fonctions de ce fichier sont pures (aucun I/O) : le service les applique à
// un workspace relu, valide, puis écrit une seule fois.

// Valeurs de l'usage demandé. Les mêmes que VeridianProfileUsage* (usage lu).
const (
	VeridianUsageRequestCommercial    = VeridianProfileUsageCommercial
	VeridianUsageRequestTransactional = VeridianProfileUsageTransactional
	VeridianUsageRequestUnassigned    = VeridianProfileUsageUnassigned
)

// VeridianReservedTransactionalProfileID rend l'identifiant du profil
// transactionnel RÉSERVÉ : le profil qu'aucun mail commercial ne doit jamais
// emprunter. Vide quand il n'y en a pas, auquel cas tout reste exactement comme
// avant le lot 4 (comportement natif de l'amont). Ne sont pas réservés :
//   - un transactional_email_provider_id qui ne désigne aucun profil email ;
//   - un profil à la fois dans le pool commercial explicite et transactionnel
//     (violation de l'exclusivité, signalée par VeridianUsageConflicts : on ne
//     change pas le comportement d'un workspace qui l'a déjà) ;
//   - le singleton historique partagé : pool explicite vide et
//     marketing_email_provider_id == transactional_email_provider_id (l'amont
//     utilise un seul profil pour les deux, rien à séparer).
func (w *Workspace) VeridianReservedTransactionalProfileID() string {
	if w == nil {
		return ""
	}
	id := strings.TrimSpace(w.Settings.TransactionalEmailProviderID)
	if id == "" {
		return ""
	}
	integration := w.GetIntegrationByID(id)
	if integration == nil || integration.Type != IntegrationTypeEmail || integration.EmailProvider.Kind == "" {
		return ""
	}
	pool := w.Settings.VeridianMarketingEmailProviderIDs
	for _, p := range pool {
		if strings.TrimSpace(p) == id {
			return ""
		}
	}
	if len(pool) == 0 && strings.TrimSpace(w.Settings.MarketingEmailProviderID) == id {
		return ""
	}
	return id
}

// --- contrats de l'API ---

// VeridianSetUsageRequest : POST /api/veridian/emailProfiles.setUsage.
type VeridianSetUsageRequest struct {
	WorkspaceID   string `json:"workspace_id"`
	IntegrationID string `json:"integration_id"`
	Usage         string `json:"usage"`
}

func (r *VeridianSetUsageRequest) Validate() error {
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return fmt.Errorf("workspace_id is required")
	}
	if strings.TrimSpace(r.IntegrationID) == "" {
		return fmt.Errorf("integration_id is required")
	}
	switch r.Usage {
	case VeridianUsageRequestCommercial, VeridianUsageRequestTransactional, VeridianUsageRequestUnassigned:
		return nil
	}
	return fmt.Errorf("usage must be one of: commercial, transactional, unassigned")
}

// VeridianSetUsageResult : l'état du profil et du workspace après l'opération.
type VeridianSetUsageResult struct {
	IntegrationID              string   `json:"integration_id"`
	Usage                      string   `json:"usage"`
	InRotation                 bool     `json:"in_rotation"`
	Paused                     bool     `json:"paused"`
	Rotation                   []string `json:"rotation"`
	TransactionalIntegrationID string   `json:"transactional_integration_id"`
	// PreviousTransactionalIntegrationID : l'ancien profil transactionnel, devenu
	// hors service quand un autre prend la place (vide sinon).
	PreviousTransactionalIntegrationID string `json:"previous_transactional_integration_id,omitempty"`
	// ClearedPause : la pause du profil a été levée parce qu'il devient
	// transactionnel (un profil transactionnel n'a pas de pause).
	ClearedPause bool `json:"cleared_pause,omitempty"`
}

// VeridianPauseRequest : POST /api/veridian/emailProfiles.pause|resume.
type VeridianPauseRequest struct {
	WorkspaceID   string `json:"workspace_id"`
	IntegrationID string `json:"integration_id"`
}

func (r *VeridianPauseRequest) Validate() error {
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return fmt.Errorf("workspace_id is required")
	}
	if strings.TrimSpace(r.IntegrationID) == "" {
		return fmt.Errorf("integration_id is required")
	}
	return nil
}

type VeridianPauseResult struct {
	IntegrationID string `json:"integration_id"`
	Paused        bool   `json:"paused"`
}

// VeridianEmailProfileAdminService regroupe les opérations nommées sur l'usage
// d'un profil. Droit requis : écriture sur le workspace (propriétaire, ou clé
// d'API scopée avec workspace:write).
type VeridianEmailProfileAdminService interface {
	SetUsage(ctx context.Context, req VeridianSetUsageRequest) (*VeridianSetUsageResult, error)
	Pause(ctx context.Context, req VeridianPauseRequest) (*VeridianPauseResult, error)
	Resume(ctx context.Context, req VeridianPauseRequest) (*VeridianPauseResult, error)
}

// --- opérations pures sur les settings ---

func (w *Workspace) veridianEmailProfileOrError(integrationID string) (*Integration, error) {
	integration := w.GetIntegrationByID(integrationID)
	if integration == nil || integration.Type != IntegrationTypeEmail || integration.EmailProvider.Kind == "" {
		return nil, NewValidationError(fmt.Sprintf("email profile not found: %s", integrationID))
	}
	return integration, nil
}

// veridianCleanPool rend le pool explicite sans identifiant vide ni doublon.
func veridianCleanPool(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// VeridianApplyUsage change l'usage d'un profil dans les settings du workspace
// (modification en mémoire, rien n'est écrit). Règles :
//
//   - commercial : le profil doit être vérifié (sauf le singleton historique déjà
//     actif) ; il entre dans la rotation (une rotation née du singleton garde le
//     singleton comme premier membre) ; s'il était le profil transactionnel, il ne
//     l'est plus ;
//   - transactional : le profil sort de la rotation, devient LE profil
//     transactionnel ; l'ancien passe hors service ; sa pause éventuelle est levée ;
//   - unassigned : hors rotation, hors transactionnel.
//
// Refus (ValidationError) : profil introuvable, profil non vérifié pour le
// commercial, et toute opération qui viderait la rotation commerciale d'un
// workspace qui en a une (la prospection s'arrêterait en silence : on ajoute un
// autre profil avant d'en retirer le dernier). marketing_email_provider_id reste
// toujours un membre de la rotation quand elle existe (l'amont s'en sert comme
// profil par défaut).
func (w *Workspace) VeridianApplyUsage(integrationID, usage string) (*VeridianSetUsageResult, error) {
	integrationID = strings.TrimSpace(integrationID)
	integration, err := w.veridianEmailProfileOrError(integrationID)
	if err != nil {
		return nil, err
	}
	result := &VeridianSetUsageResult{IntegrationID: integrationID}

	effectiveBefore := w.VeridianMarketingEmailProfiles()
	wasCommercial := false
	for _, p := range effectiveBefore {
		if p.IntegrationID == integrationID {
			wasCommercial = true
		}
	}
	// Pool explicite de départ ; un pool vide + singleton devient explicite quand
	// on lui ajoute un membre, en gardant le singleton comme premier membre.
	pool := veridianCleanPool(w.Settings.VeridianMarketingEmailProviderIDs)
	singleton := strings.TrimSpace(w.Settings.MarketingEmailProviderID)
	currentT := strings.TrimSpace(w.Settings.TransactionalEmailProviderID)

	removeFromPool := func(ids []string, id string) []string {
		out := make([]string, 0, len(ids))
		for _, p := range ids {
			if p != id {
				out = append(out, p)
			}
		}
		return out
	}

	switch usage {
	case VeridianUsageRequestCommercial:
		if integration.EmailProvider.VeridianTransportVerifiedAt == nil && singleton != integrationID {
			return nil, NewValidationError(fmt.Sprintf("email profile transport is not verified: %s (send a test first)", integrationID))
		}
		if len(pool) == 0 && singleton != "" && singleton != integrationID && singleton != currentT {
			if s := w.GetIntegrationByID(singleton); s != nil && s.Type == IntegrationTypeEmail && s.EmailProvider.Kind != "" {
				pool = append(pool, singleton)
			}
		}
		found := false
		for _, p := range pool {
			if p == integrationID {
				found = true
			}
		}
		if !found {
			pool = append(pool, integrationID)
		}
		if currentT == integrationID {
			w.Settings.TransactionalEmailProviderID = ""
		}
	case VeridianUsageRequestTransactional:
		if wasCommercial && len(effectiveBefore) == 1 {
			return nil, NewValidationError("this would empty the commercial rotation: add another commercial profile first")
		}
		pool = removeFromPool(pool, integrationID)
		if currentT != "" && currentT != integrationID {
			result.PreviousTransactionalIntegrationID = currentT
		}
		w.Settings.TransactionalEmailProviderID = integrationID
		if integration.EmailProvider.VeridianPaused {
			w.setVeridianPaused(integrationID, false)
			result.ClearedPause = true
		}
	case VeridianUsageRequestUnassigned:
		if wasCommercial && len(effectiveBefore) == 1 {
			return nil, NewValidationError("this would empty the commercial rotation: add another commercial profile first")
		}
		pool = removeFromPool(pool, integrationID)
		if currentT == integrationID {
			w.Settings.TransactionalEmailProviderID = ""
		}
	default:
		return nil, NewValidationError("usage must be one of: commercial, transactional, unassigned")
	}

	// Un singleton qui désigne ce profil ne peut plus rester le profil marketing
	// quand il quitte le commercial ; il passe au premier membre de la rotation.
	if len(pool) > 0 {
		w.Settings.VeridianMarketingEmailProviderIDs = pool
		member := false
		for _, p := range pool {
			if p == singleton {
				member = true
			}
		}
		if !member {
			w.Settings.MarketingEmailProviderID = pool[0]
		}
	} else {
		w.Settings.VeridianMarketingEmailProviderIDs = nil
		if singleton == integrationID && usage != VeridianUsageRequestCommercial {
			w.Settings.MarketingEmailProviderID = ""
		}
	}

	if err := w.ValidateVeridianMarketingEmailProfiles(); err != nil {
		return nil, NewValidationError(err.Error())
	}
	if err := w.ValidateVeridianUsageExclusivity(); err != nil {
		return nil, err
	}

	result.Usage = w.VeridianProfileUsageOf(integrationID)
	for _, p := range w.VeridianMarketingEmailProfiles() {
		result.Rotation = append(result.Rotation, p.IntegrationID)
		if p.IntegrationID == integrationID {
			result.InRotation = true
		}
	}
	if result.Rotation == nil {
		result.Rotation = []string{}
	}
	result.TransactionalIntegrationID = strings.TrimSpace(w.Settings.TransactionalEmailProviderID)
	if refreshed := w.GetIntegrationByID(integrationID); refreshed != nil {
		result.Paused = refreshed.EmailProvider.VeridianPaused
	}
	return result, nil
}

func (w *Workspace) setVeridianPaused(integrationID string, paused bool) {
	for i := range w.Integrations {
		if w.Integrations[i].ID == integrationID {
			w.Integrations[i].EmailProvider.VeridianPaused = paused
		}
	}
}

// VeridianApplyPause pose ou lève la pause d'un profil. Un profil transactionnel
// ne se met pas en pause : un mail transactionnel part toujours (un mot de passe
// oublié ne doit jamais attendre), la pause ne servirait qu'à faire croire
// l'inverse. Pour l'arrêter, on le met hors service.
func (w *Workspace) VeridianApplyPause(integrationID string, paused bool) (*VeridianPauseResult, error) {
	integrationID = strings.TrimSpace(integrationID)
	if _, err := w.veridianEmailProfileOrError(integrationID); err != nil {
		return nil, err
	}
	if paused && w.VeridianProfileUsageOf(integrationID) == VeridianProfileUsageTransactional {
		return nil, NewValidationError("a transactional profile cannot be paused: a transactional mail always leaves; make the profile unassigned to stop it")
	}
	w.setVeridianPaused(integrationID, paused)
	return &VeridianPauseResult{IntegrationID: integrationID, Paused: paused}, nil
}
