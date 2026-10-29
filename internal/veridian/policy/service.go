package policy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// ValidationError : entrée refusée (HTTP 400).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// Authenticator est la part de domain.AuthService dont le module a besoin.
type Authenticator interface {
	AuthenticateUserForWorkspace(ctx context.Context, workspaceID string) (context.Context, *domain.User, *domain.UserWorkspace, error)
}

// WorkspaceReader est la part du dépôt de workspaces dont le module a besoin.
type WorkspaceReader interface {
	GetByID(ctx context.Context, id string) (*domain.Workspace, error)
}

// Service porte les cas d'usage du module. Chaque méthode authentifie l'appelant pour le
// workspace et vérifie ses permissions AVANT tout accès aux données.
type Service struct {
	auth       Authenticator
	workspaces WorkspaceReader
	store      Store
	engagement domain.VeridianEngagementByClassRepository
	resolver   *Resolver
	log        logger.Logger
	now        func() time.Time
}

// NewService crée le service. engagement peut être nil (mesures du catalogue seules).
func NewService(auth Authenticator, workspaces WorkspaceReader, store Store,
	engagement domain.VeridianEngagementByClassRepository, resolver *Resolver, log logger.Logger) *Service {
	return &Service{auth: auth, workspaces: workspaces, store: store, engagement: engagement, resolver: resolver, log: log, now: time.Now}
}

func (s *Service) authorize(ctx context.Context, workspaceID string, perms ...permNeed) (context.Context, *domain.User, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return ctx, nil, invalid("workspace_id est requis")
	}
	ctx, user, membership, err := s.auth.AuthenticateUserForWorkspace(ctx, workspaceID)
	if err != nil {
		return ctx, nil, fmt.Errorf("failed to authenticate user: %w", err)
	}
	for _, p := range perms {
		if !membership.HasPermission(p.resource, p.kind) {
			return ctx, nil, domain.NewPermissionError(p.resource, p.kind, fmt.Sprintf("Insufficient permissions: %s access to %s required", p.kind, p.resource))
		}
	}
	return ctx, user, nil
}

type permNeed struct {
	resource domain.PermissionResource
	kind     domain.PermissionType
}

func need(r domain.PermissionResource, k domain.PermissionType) permNeed { return permNeed{r, k} }

func actor(u *domain.User) string {
	if u == nil {
		return ""
	}
	return u.Email
}

// workspaceEngagement lit ce que le workspace a envoyé et reçu par classe (tout l'historique).
// Illisible : le catalogue sert de mesure, la vue le dit (basis).
func (s *Service) workspaceEngagement(ctx context.Context, workspaceID string) map[string]*domain.VeridianObservedEngagement {
	out := map[string]*domain.VeridianObservedEngagement{}
	if s.engagement == nil {
		return out
	}
	rows, err := s.engagement.GetEngagementByClass(ctx, workspaceID, time.Time{}, time.Time{})
	if err != nil {
		s.log.WithField("workspace_id", workspaceID).WithField("error", err.Error()).Warn("policy: engagement par classe illisible, mesures du catalogue utilisées")
		return out
	}
	for _, r := range rows {
		if r.Class == "" {
			continue
		}
		o := out[r.Class]
		if o == nil {
			o = &domain.VeridianObservedEngagement{}
			out[r.Class] = o
		}
		o.Sent += r.Sent
		o.Bounced += r.Bounced
		o.RepliedHuman += r.RepliedHuman
	}
	return out
}

// Get rend la politique effective du workspace, expliquée. class vide = toutes les classes.
func (s *Service) Get(ctx context.Context, workspaceID, class string) (*PolicyView, error) {
	ctx, _, err := s.authorize(ctx, workspaceID, need(domain.PermissionResourceWorkspace, domain.PermissionTypeRead))
	if err != nil {
		return nil, err
	}
	class = strings.TrimSpace(class)
	if class != "" && !domain.IsValidProviderClass(class) {
		return nil, invalid("classe inconnue %q", class)
	}
	return s.view(ctx, workspaceID, class)
}

func (s *Service) view(ctx context.Context, workspaceID, class string) (*PolicyView, error) {
	ws, err := s.workspaces.GetByID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("lecture du workspace: %w", err)
	}
	overrides, err := s.store.ListOverrides(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("lecture des surcharges: %w", err)
	}
	snap := BuildSnapshot(overrides)
	cat := domain.VeridianClassPresets()
	sh := ComputeShadows(ws)
	obs := s.workspaceEngagement(ctx, workspaceID)
	view := &PolicyView{WorkspaceID: workspaceID, CatalogAsOf: cat.AsOf, Source: cat.Source, Classes: []ClassView{}}
	for _, p := range cat.Classes {
		if class != "" && p.Class != class {
			continue
		}
		view.Classes = append(view.Classes, BuildView(p, cat, snap, sh, obs[p.Class]))
	}
	for _, o := range snap.Overridden {
		if o.Any() {
			view.Overrides++
		}
	}
	return view, nil
}

// SetRequest : surcharge d'une classe. Les valeurs à zéro sont laissées telles quelles.
type SetRequest struct {
	WorkspaceID       string  `json:"workspace_id"`
	Class             string  `json:"class"`
	DailyCap          int     `json:"daily_cap"`
	RatePerMin        float64 `json:"rate_per_min"`
	WarmupShare       float64 `json:"warmup_share"`
	SlowdownThreshold float64 `json:"slowdown_threshold"`
	Reason            string  `json:"reason"`
}

// SetResult : la classe après écriture, et les avertissements (valeur masquée par un profil).
type SetResult struct {
	Class    ClassView `json:"class"`
	Warnings []string  `json:"warnings"`
}

// Set pose une surcharge de politique pour une classe.
func (s *Service) Set(ctx context.Context, req SetRequest) (*SetResult, error) {
	ctx, user, err := s.authorize(ctx, req.WorkspaceID, need(domain.PermissionResourceWorkspace, domain.PermissionTypeWrite))
	if err != nil {
		return nil, err
	}
	req.Class = strings.TrimSpace(req.Class)
	if !domain.IsValidProviderClass(req.Class) {
		return nil, invalid("classe inconnue %q (voir policy.get pour la liste)", req.Class)
	}
	if req.DailyCap == 0 && req.RatePerMin == 0 && req.WarmupShare == 0 && req.SlowdownThreshold == 0 {
		return nil, invalid("rien à poser : donner au moins une des valeurs daily_cap, rate_per_min, warmup_share, slowdown_threshold")
	}
	if err := domain.VeridianValidateClassPolicyValues(req.DailyCap, req.RatePerMin, req.WarmupShare, req.SlowdownThreshold); err != nil {
		return nil, invalid("%s", err.Error())
	}
	if len(req.Reason) > 300 {
		return nil, invalid("reason est limité à 300 caractères")
	}
	if err := s.store.UpsertOverride(ctx, req.WorkspaceID, Override{Class: req.Class, DailyCap: req.DailyCap, RatePerMin: req.RatePerMin,
		WarmupShare: req.WarmupShare, SlowdownThreshold: req.SlowdownThreshold, Reason: strings.TrimSpace(req.Reason), UpdatedBy: actor(user)}); err != nil {
		return nil, err
	}
	s.resolver.Invalidate(req.WorkspaceID)
	s.log.WithField("workspace_id", req.WorkspaceID).WithField("class", req.Class).WithField("by", actor(user)).Info("policy: surcharge posée")
	return s.afterWrite(ctx, req.WorkspaceID, req.Class)
}

// Reset retire des surcharges (les règles nommées, toutes si la liste est vide) : la classe
// retrouve le preset du catalogue.
func (s *Service) Reset(ctx context.Context, workspaceID, class string, rules []string) (*SetResult, error) {
	ctx, user, err := s.authorize(ctx, workspaceID, need(domain.PermissionResourceWorkspace, domain.PermissionTypeWrite))
	if err != nil {
		return nil, err
	}
	class = strings.TrimSpace(class)
	if !domain.IsValidProviderClass(class) {
		return nil, invalid("classe inconnue %q", class)
	}
	for _, r := range rules {
		if _, ok := ruleColumn[r]; !ok {
			return nil, invalid("règle inconnue %q (attendu : %s)", r, strings.Join(AllRules, ", "))
		}
	}
	if _, err := s.store.ClearOverride(ctx, workspaceID, class, rules); err != nil {
		return nil, err
	}
	s.resolver.Invalidate(workspaceID)
	s.log.WithField("workspace_id", workspaceID).WithField("class", class).WithField("by", actor(user)).Info("policy: surcharge retirée")
	return s.afterWrite(ctx, workspaceID, class)
}

func (s *Service) afterWrite(ctx context.Context, workspaceID, class string) (*SetResult, error) {
	v, err := s.view(ctx, workspaceID, class)
	if err != nil {
		return nil, err
	}
	res := &SetResult{Class: v.Classes[0], Warnings: []string{}}
	if by := res.Class.DailyCap.ShadowedBy; len(by) > 0 && res.Class.DailyCap.Source == SourceWorkspaceOverride {
		res.Warnings = append(res.Warnings, "le plafond posé est masqué par : "+strings.Join(by, ", ")+" (leur valeur s'applique pour eux)")
	}
	if by := res.Class.RatePerMin.ShadowedBy; len(by) > 0 && res.Class.RatePerMin.Source == SourceWorkspaceOverride {
		res.Warnings = append(res.Warnings, "la cadence posée est masquée par : "+strings.Join(by, ", ")+" (leur valeur s'applique pour eux)")
	}
	return res, nil
}

// PriorityList : toutes les campagnes et automations avec leur priorité effective.
type PriorityList struct {
	WorkspaceID string           `json:"workspace_id"`
	Default     int              `json:"default_priority"`
	Min         int              `json:"min_priority"`
	Max         int              `json:"max_priority"`
	Sources     []PrioritySource `json:"sources"`
	Note        string           `json:"note"`
}

// PriorityNote : la limite à connaître, rappelée à chaque lecture et écriture.
const PriorityNote = "Plus le nombre est petit, plus la campagne passe tôt (2 avant 5, 5 avant 9 ; 1 est réservé au transactionnel de séquence). " +
	"La priorité ne change l'ordre que si la file est engorgée entre campagnes : quand le plafond d'envoi par expéditeur ou par fournisseur est la limite, elle ne fait pas partir plus de mails."

// ListPriorities rend les priorités. Lecture des campagnes ET des automations.
func (s *Service) ListPriorities(ctx context.Context, workspaceID string) (*PriorityList, error) {
	ctx, _, err := s.authorize(ctx, workspaceID,
		need(domain.PermissionResourceBroadcasts, domain.PermissionTypeRead),
		need(domain.PermissionResourceAutomations, domain.PermissionTypeRead))
	if err != nil {
		return nil, err
	}
	src, err := s.store.ListSources(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if src == nil {
		src = []PrioritySource{}
	}
	return &PriorityList{WorkspaceID: workspaceID, Default: domain.EmailQueuePriorityMarketing,
		Min: domain.VeridianMinMarketingPriority, Max: domain.VeridianMaxMarketingPriority, Sources: src, Note: PriorityNote}, nil
}

func (s *Service) priorityPerm(sourceType string) (permNeed, error) {
	switch sourceType {
	case SourceTypeBroadcast:
		return need(domain.PermissionResourceBroadcasts, domain.PermissionTypeWrite), nil
	case SourceTypeAutomation:
		return need(domain.PermissionResourceAutomations, domain.PermissionTypeWrite), nil
	}
	return permNeed{}, invalid("source_type doit être %q ou %q", SourceTypeBroadcast, SourceTypeAutomation)
}

// SetPriority pose la priorité d'une campagne ou automation et réécrit celle de ses mails
// en attente (les mails déjà partis ne bougent pas).
func (s *Service) SetPriority(ctx context.Context, workspaceID, sourceType, sourceID string, priority int) (*PriorityChange, error) {
	perm, err := s.priorityPerm(sourceType)
	if err != nil {
		return nil, err
	}
	ctx, user, err := s.authorize(ctx, workspaceID, perm)
	if err != nil {
		return nil, err
	}
	if err := domain.VeridianValidateMarketingPriority(priority); err != nil {
		return nil, invalid("%s", err.Error())
	}
	if err := s.requireSource(ctx, workspaceID, sourceType, sourceID); err != nil {
		return nil, err
	}
	n, err := s.store.SetPriority(ctx, workspaceID, sourceType, sourceID, priority, actor(user))
	if err != nil {
		return nil, err
	}
	s.resolver.Invalidate(workspaceID)
	s.log.WithField("workspace_id", workspaceID).WithField("source", sourceType+":"+sourceID).WithField("priority", priority).WithField("by", actor(user)).Info("policy: priorité posée")
	return &PriorityChange{SourceType: sourceType, SourceID: sourceID, Priority: priority, RequeuedPending: n, Note: PriorityNote}, nil
}

// ClearPriority retire la priorité posée : la source retrouve le défaut 5.
func (s *Service) ClearPriority(ctx context.Context, workspaceID, sourceType, sourceID string) (*PriorityChange, error) {
	perm, err := s.priorityPerm(sourceType)
	if err != nil {
		return nil, err
	}
	ctx, user, err := s.authorize(ctx, workspaceID, perm)
	if err != nil {
		return nil, err
	}
	if err := s.requireSource(ctx, workspaceID, sourceType, sourceID); err != nil {
		return nil, err
	}
	n, err := s.store.ClearPriority(ctx, workspaceID, sourceType, sourceID)
	if err != nil {
		return nil, err
	}
	s.resolver.Invalidate(workspaceID)
	s.log.WithField("workspace_id", workspaceID).WithField("source", sourceType+":"+sourceID).WithField("by", actor(user)).Info("policy: priorité retirée")
	return &PriorityChange{SourceType: sourceType, SourceID: sourceID, Priority: domain.EmailQueuePriorityMarketing, RequeuedPending: n, Note: PriorityNote}, nil
}

func (s *Service) requireSource(ctx context.Context, workspaceID, sourceType, sourceID string) error {
	if strings.TrimSpace(sourceID) == "" {
		return invalid("source_id est requis")
	}
	_, ok, err := s.store.SourceName(ctx, workspaceID, sourceType, sourceID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s %q: %w", sourceType, sourceID, ErrNotFound)
	}
	return nil
}
