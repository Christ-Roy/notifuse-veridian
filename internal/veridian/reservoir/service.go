package reservoir

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Authenticator : droits de l'appelant sur l'espace (service d'authentification upstream).
type Authenticator interface {
	AuthenticateUserForWorkspace(ctx context.Context, workspaceID string) (context.Context, *domain.User, *domain.UserWorkspace, error)
}

// Service : import groupé, état des réservoirs, activation.
type Service struct {
	Auth     Authenticator
	Store    *Store
	Contacts Contacts
	// Workspaces : lit le profil transactionnel réservé de l'espace.
	Workspaces WorkspaceReader
	Log        logger.Logger

	locks sync.Map // un import à la fois par espace : deux lots simultanés ne doivent pas se croire chacun le premier
}

// WorkspaceReader lit un espace.
type WorkspaceReader interface {
	GetByID(ctx context.Context, id string) (*domain.Workspace, error)
}

// refuseUncapped : un modèle « transactionnel » part par le profil réservé SANS aucune porte (plafond compris). Une séquence qui
// en contient n'est jamais un réservoir de prospection : l'alimenter en masse contournerait la limite quotidienne.
func (s *Service) refuseUncapped(ctx context.Context, wsID string, a Automation) error {
	w, err := s.Workspaces.GetByID(ctx, wsID)
	if err != nil {
		return err
	}
	if w.VeridianReservedTransactionalProfileID() == "" {
		return nil // sans profil réservé, ces mails suivent le chemin commercial et ses plafonds
	}
	tpl, err := s.Store.TransactionalTemplates(ctx, wsID, a.ID)
	if err != nil {
		return err
	}
	if len(tpl) > 0 {
		return invalid("rien n'a été écrit : la séquence « %s » envoie des modèles transactionnels (%s), qui partent hors plafond ; passe-les en catégorie marketing", a.Name, strings.Join(tpl, ", "))
	}
	return nil
}

type need struct {
	res  domain.PermissionResource
	kind domain.PermissionType
}

func (s *Service) authorize(ctx context.Context, ws string, needs ...need) (context.Context, error) {
	if strings.TrimSpace(ws) == "" {
		return ctx, invalid("workspace_id est requis")
	}
	ctx, _, uw, err := s.Auth.AuthenticateUserForWorkspace(ctx, ws)
	if err != nil {
		return ctx, fmt.Errorf("authentification : %w", err)
	}
	for _, n := range needs {
		if !uw.HasPermission(n.res, n.kind) {
			return ctx, domain.NewPermissionError(n.res, n.kind, fmt.Sprintf("droit %s:%s requis", n.res, n.kind))
		}
	}
	return ctx, nil
}

var (
	contactsRead    = need{domain.PermissionResourceContacts, domain.PermissionTypeRead}
	contactsWrite   = need{domain.PermissionResourceContacts, domain.PermissionTypeWrite}
	listsRead       = need{domain.PermissionResourceLists, domain.PermissionTypeRead}
	listsWrite      = need{domain.PermissionResourceLists, domain.PermissionTypeWrite}
	automationsRead = need{domain.PermissionResourceAutomations, domain.PermissionTypeRead}
	automationsWr   = need{domain.PermissionResourceAutomations, domain.PermissionTypeWrite}
	historyRead     = need{domain.PermissionResourceMessageHistory, domain.PermissionTypeRead}
)

// IntakeRequest : POST /api/veridian/contacts.intake.
type IntakeRequest struct {
	WorkspaceID string            `json:"workspace_id"`
	Contacts    []json.RawMessage `json:"contacts"`
	ListIDs     []string          `json:"list_ids"`
	Dedupe      []string          `json:"dedupe"`
	Existing    string            `json:"existing"`
	DryRun      bool              `json:"dry_run"`
	// AllowTriggers : accepter qu'une séquence active démarre toute seule sur ces contacts.
	AllowTriggers bool `json:"allow_triggers"`
}

// IntakeResult : ce qui a été écrit (ou le serait, en simulation).
type IntakeResult struct {
	DryRun     bool           `json:"dry_run"`
	Received   int            `json:"received"`
	Created    int            `json:"created"`
	Updated    int            `json:"updated"`
	Existing   int            `json:"existing"`
	Subscribed int            `json:"subscribed"`
	Skipped    map[string]int `json:"skipped"`
	// Details : les premiers contacts écartés, avec la raison.
	Details []Detail `json:"details,omitempty"`
	// Triggers : séquences actives que cet import démarre (création de contact, inscription aux listes visées).
	Triggers []Automation `json:"triggers,omitempty"`
}

// TriggerError : l'import démarrerait une séquence, l'appelant ne l'a pas accepté. Rien n'a été écrit.
type TriggerError struct{ Triggers []Automation }

func (e *TriggerError) Error() string {
	names := make([]string, len(e.Triggers))
	for i, t := range e.Triggers {
		names[i] = fmt.Sprintf("« %s » (%s)", t.Name, t.ID)
	}
	return "rien n'a été écrit : cet import démarrerait la ou les séquences actives " + strings.Join(names, ", ") +
		". Vise une liste de réserve puis active par lots (reservoir.activate), ou repasse avec allow_triggers=true"
}

func cleanIDs(ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Intake importe un lot de contacts.
func (s *Service) Intake(ctx context.Context, req IntakeRequest) (*IntakeResult, error) {
	needs := []need{contactsWrite}
	lists := cleanIDs(req.ListIDs)
	if len(lists) > 0 {
		needs = append(needs, listsWrite)
	}
	ctx, err := s.authorize(ctx, req.WorkspaceID, needs...)
	if err != nil {
		return nil, err
	}
	dd, err := ParseDedupe(req.Dedupe)
	if err != nil {
		return nil, err
	}
	mode := req.Existing
	switch mode {
	case "":
		mode = ExistingAttach
	case ExistingAttach, ExistingUpdate, ExistingSkip:
	default:
		return nil, invalid("existing vaut attach, update ou skip (reçu %q)", mode)
	}
	switch {
	case len(req.Contacts) == 0:
		return nil, invalid("contacts est requis (au moins un)")
	case len(req.Contacts) > MaxBatch:
		return nil, invalid("%d contacts : %d au plus par appel", len(req.Contacts), MaxBatch)
	case len(lists) > maxLists:
		return nil, invalid("%d listes au plus par appel", maxLists)
	}
	if len(lists) > 0 {
		miss, err := s.Store.MissingLists(ctx, req.WorkspaceID, lists)
		if err != nil {
			return nil, err
		}
		if len(miss) > 0 {
			return nil, invalid("liste inconnue : %s", strings.Join(miss, ", "))
		}
	}

	var rows []Row
	var bad []Detail
	for i, raw := range req.Contacts {
		r, reason := ParseRow(i, raw)
		if reason != "" {
			bad = append(bad, Detail{Index: i, Email: r.Email, Reason: reason})
			continue
		}
		rows = append(rows, r)
	}

	mu, _ := s.locks.LoadOrStore(req.WorkspaceID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	known := Known{}
	if len(rows) > 0 {
		if known, err = s.Store.Known(ctx, req.WorkspaceID, rows, dd); err != nil {
			return nil, err
		}
	}
	plan := Build(rows, bad, known, dd)
	res := &IntakeResult{DryRun: req.DryRun, Received: len(req.Contacts), Skipped: plan.Skipped, Details: plan.Details}
	touchesLists := len(lists) > 0 && (len(plan.New) > 0 || (mode != ExistingSkip && len(plan.Existing) > 0))
	if len(plan.New) > 0 || touchesLists {
		target := lists
		if !touchesLists {
			target = nil
		}
		if res.Triggers, err = s.Store.LiveTriggers(ctx, req.WorkspaceID, target, len(plan.New) > 0); err != nil {
			return nil, err
		}
	}
	if req.DryRun {
		res.Created = len(plan.New)
		res.Existing = len(plan.Existing)
		if mode == ExistingUpdate {
			res.Updated, res.Existing = res.Existing, 0
		}
		return res, nil
	}
	if len(res.Triggers) > 0 && !req.AllowTriggers {
		return nil, &TriggerError{Triggers: res.Triggers}
	}
	for _, t := range res.Triggers {
		if err := s.refuseUncapped(ctx, req.WorkspaceID, t); err != nil {
			return nil, err
		}
	}

	var write []*domain.Contact
	var attach []string
	build := func(r Row, st Stored) {
		c, err := r.Contact(st.JSON1)
		if err != nil {
			plan.skip(r, ReasonBadContact, "")
			return
		}
		c.CreatedAt = st.Created // zéro : date du jour ; mise à jour : date d'origine gardée
		write = append(write, c)
	}
	for _, r := range plan.New {
		build(r, Stored{})
	}
	switch mode {
	case ExistingUpdate:
		emails := make([]string, len(plan.Existing))
		for i, r := range plan.Existing {
			emails[i] = r.Email
		}
		stored, err := s.Store.Existing(ctx, req.WorkspaceID, emails)
		if err != nil {
			return nil, err
		}
		for _, r := range plan.Existing {
			build(r, stored[r.Email])
		}
	case ExistingAttach:
		for _, r := range plan.Existing {
			attach = append(attach, r.Email)
		}
		res.Existing = len(plan.Existing)
	default:
		res.Existing = len(plan.Existing)
	}
	for start := 0; start < len(write); start += domain.BulkImportChunkSize {
		end := min(start+domain.BulkImportChunkSize, len(write))
		done, err := s.Contacts.BulkUpsertContacts(ctx, req.WorkspaceID, write[start:end])
		if err != nil {
			// Le lot déjà écrit reste écrit : on le dit, l'import est rejouable (l'adresse dédoublonne).
			s.Log.WithField("workspace_id", req.WorkspaceID).WithField("error", err.Error()).Error("reservoir: écriture d'un lot refusée")
			for _, c := range write[start:end] {
				plan.skip(Row{Email: c.Email}, ReasonWriteFailed, "")
			}
			continue
		}
		for _, d := range done {
			attach = append(attach, d.Email)
			if d.IsNew {
				res.Created++
			} else {
				res.Updated++
			}
		}
	}
	if res.Subscribed, err = s.Store.Subscribe(ctx, req.WorkspaceID, attach, lists); err != nil {
		return nil, fmt.Errorf("contacts écrits (%d créés), inscription aux listes en échec : %w", res.Created, err)
	}
	res.Details = plan.Details
	s.Log.WithFields(map[string]interface{}{"workspace_id": req.WorkspaceID, "received": res.Received, "created": res.Created, "updated": res.Updated,
		"existing": res.Existing, "subscribed": res.Subscribed, "lists": len(lists)}).Info("reservoir: import")
	return res, nil
}

// ListState : une liste vue comme réservoir.
type ListState struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Pool
}

// Summary rend l'état des listes (toutes, ou celles demandées) : combien il en reste à activer, et pourquoi pas les autres.
func (s *Service) Summary(ctx context.Context, ws string, listIDs []string, automationID string) ([]ListState, error) {
	ctx, err := s.authorize(ctx, ws, contactsRead, listsRead, automationsRead, historyRead)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.Lists(ctx, ws)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range cleanIDs(listIDs) {
		want[id] = true
	}
	out := []ListState{}
	for _, l := range all {
		if len(want) > 0 && !want[l.ID] {
			continue
		}
		if len(out) == maxLists {
			break
		}
		p, err := s.Store.Inspect(ctx, ws, Source{ListID: l.ID}, automationID)
		if err != nil {
			return nil, err
		}
		out = append(out, ListState{ID: l.ID, Name: l.Name, Pool: p})
	}
	return out, nil
}

// ActivateRequest : POST /api/veridian/reservoir.activate.
type ActivateRequest struct {
	WorkspaceID  string   `json:"workspace_id"`
	AutomationID string   `json:"automation_id"`
	ListID       string   `json:"list_id"`
	SegmentID    string   `json:"segment_id"`
	Emails       []string `json:"emails"`
	Limit        int      `json:"limit"`
	// IncludeContacted : reprendre aussi des contacts qui ont déjà reçu un mail commercial.
	IncludeContacted bool `json:"include_contacted"`
	DryRun           bool `json:"dry_run"`
	// BatchID : nom du lot, choisi par l'appelant. Rejouer le même rend le résultat d'origine : sans lui, rejouer une
	// activation par liste ou segment ferait entrer le lot SUIVANT.
	BatchID string `json:"batch_id"`
}

// ActivateResult : qui est entré dans la séquence, et l'état de la source avant.
type ActivateResult struct {
	DryRun       bool     `json:"dry_run"`
	Replayed     bool     `json:"replayed,omitempty"` // lot déjà passé : rien de neuf
	AutomationID string   `json:"automation_id"`
	Selected     int      `json:"selected"`
	Enrolled     int      `json:"enrolled"`
	Remaining    int      `json:"remaining"`
	Source       Pool     `json:"source"`
	Emails       []string `json:"emails,omitempty"` // les premiers
	Note         string   `json:"note"`
}

const activateNote = "les plafonds d'envoi restent ceux des profils : activer un lot ne fait pas partir plus de mails par jour, le surplus attend en file"

// Activate fait entrer un lot de contacts dans une séquence active.
func (s *Service) Activate(ctx context.Context, req ActivateRequest) (*ActivateResult, error) {
	ctx, err := s.authorize(ctx, req.WorkspaceID, automationsWr, contactsRead, listsWrite, historyRead)
	if err != nil {
		return nil, err
	}
	src := Source{ListID: strings.TrimSpace(req.ListID), SegmentID: strings.TrimSpace(req.SegmentID)}
	for _, e := range req.Emails {
		src.Emails = append(src.Emails, domain.NormalizeEmail(e))
	}
	n := 0
	for _, on := range []bool{src.ListID != "", src.SegmentID != "", len(src.Emails) > 0} {
		if on {
			n++
		}
	}
	switch {
	case req.AutomationID == "":
		return nil, invalid("automation_id est requis")
	case n != 1:
		return nil, invalid("donne une seule source : list_id, segment_id ou emails")
	case len(src.Emails) > MaxBatch:
		return nil, invalid("%d adresses au plus par appel", MaxBatch)
	case len(req.BatchID) > 100:
		return nil, invalid("batch_id : 100 caractères au plus")
	case req.Limit < 0 || req.Limit > MaxBatch:
		return nil, invalid("limit est compris entre 1 et %d", MaxBatch)
	}
	limit := req.Limit
	if limit == 0 {
		limit = 100
		if len(src.Emails) > 0 {
			limit = MaxBatch
		}
	}
	a, err := s.Store.Automation(ctx, req.WorkspaceID, req.AutomationID)
	if err != nil {
		return nil, err
	}
	switch {
	case a == nil:
		return nil, invalid("séquence inconnue : %s", req.AutomationID)
	case a.status != "live":
		return nil, invalid("la séquence « %s » n'est pas active (%s) : rien n'a été inscrit", a.Name, a.status)
	case a.root == "":
		return nil, invalid("la séquence « %s » n'a pas de premier nœud", a.Name)
	}
	if err := s.refuseUncapped(ctx, req.WorkspaceID, *a); err != nil {
		return nil, err
	}
	if a.list != "" {
		// L'inscription à la liste de la séquence ne doit pas en démarrer une AUTRE.
		others, err := s.Store.LiveTriggers(ctx, req.WorkspaceID, []string{a.list}, false)
		if err != nil {
			return nil, err
		}
		for _, o := range others {
			if o.ID != a.ID {
				return nil, invalid("rien n'a été inscrit : la liste %s de cette séquence démarre aussi « %s » (%s)", a.list, o.Name, o.ID)
			}
		}
	}
	res := &ActivateResult{DryRun: req.DryRun, AutomationID: a.ID, Note: activateNote}
	if res.Source, err = s.Store.Inspect(ctx, req.WorkspaceID, src, a.ID); err != nil {
		return nil, err
	}
	pickBatch := func(q querier) ([]string, error) {
		return pickFrom(ctx, q, src, a.ID, limit, req.IncludeContacted)
	}
	open := res.Source.Available
	if req.IncludeContacted {
		open += res.Source.Contacted
	}
	var emails []string
	if req.DryRun {
		if emails, err = s.Store.Pick(ctx, req.WorkspaceID, src, a.ID, limit, req.IncludeContacted); err != nil {
			return nil, err
		}
		res.Selected = len(emails)
	} else {
		if emails, res.Selected, res.Replayed, err = s.Store.Activate(ctx, req.WorkspaceID, a, req.BatchID, pickBatch); err != nil {
			return nil, err
		}
		res.Enrolled = len(emails)
		s.Log.WithFields(map[string]interface{}{"workspace_id": req.WorkspaceID, "automation_id": a.ID, "selected": res.Selected, "enrolled": res.Enrolled, "replayed": res.Replayed}).Info("reservoir: activation")
		if !res.Replayed {
			open -= res.Enrolled
		}
	}
	res.Remaining = open
	if len(emails) > maxDetails {
		emails = emails[:maxDetails]
	}
	res.Emails = emails
	return res, nil
}
