package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// OverviewReader : la vérité des profils d'envoi (capacité par classe). Même service que
// emailProfiles.overview : aucun second calcul de plafond.
type OverviewReader interface {
	GetEmailProfilesOverview(ctx context.Context, workspaceID string) (*domain.VeridianEmailProfilesOverview, error)
}

// SetOverview branche la lecture de la capacité des profils.
func (s *Service) SetOverview(o OverviewReader) { s.overview = o }

// StockRequest : stock.overview. Les champs non vides de Override prennent le pas sur la définition enregistrée
// (lecture seule : rien n'est écrit).
type StockRequest struct {
	WorkspaceID string
	Days        int
	Override    StockConfig
}

// loadStockConfig lit la définition enregistrée et applique la surcharge de la requête. Rend aussi l'origine des listes.
func (s *Service) loadStockConfig(ctx context.Context, workspaceID string, over StockConfig) (StockConfig, string, error) {
	var cfg StockConfig
	raw, err := s.store.GetSetting(ctx, workspaceID, StockKey)
	if err != nil {
		return cfg, "", fmt.Errorf("lecture de la définition du stock: %w", err)
	}
	source := "none"
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return cfg, "", fmt.Errorf("définition du stock illisible: %w", err)
		}
		source = "config"
	}
	if over.EnrolledListID != "" || over.ReserveListID != "" {
		source = "request"
	}
	merge := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	merge(&cfg.EnrolledListID, over.EnrolledListID)
	merge(&cfg.ReserveListID, over.ReserveListID)
	merge(&cfg.EnrollAutomationID, over.EnrollAutomationID)
	merge(&cfg.ClassField, over.ClassField)
	merge(&cfg.ExcludeEmailRegex, over.ExcludeEmailRegex)
	if len(over.RequireNonEmpty) > 0 {
		cfg.RequireNonEmpty = over.RequireNonEmpty
	}
	if len(over.ExcludePrefixes) > 0 {
		cfg.ExcludePrefixes = over.ExcludePrefixes
	}
	if over.TargetDays > 0 {
		cfg.TargetDays = over.TargetDays
	}
	if err := cfg.Validate(); err != nil {
		return cfg, "", err
	}
	return cfg, source, nil
}

// replyRateFunc donne le taux de réponse lissé d'une classe (mesure du workspace, sinon du catalogue).
func (s *Service) replyRateFunc(ctx context.Context, workspaceID string) func(string) float64 {
	obs := s.workspaceEngagement(ctx, workspaceID)
	cat := domain.VeridianClassPresets()
	prior := Smooth(0, 0, cat.ReplyPrior, cat.ReplySmoothingWeight)
	return func(class string) float64 {
		p, ok := domain.VeridianClassPresetFor(class)
		if !ok {
			return prior
		}
		return BuildObserved(p, cat, obs[class]).ReplyRateSmoothed
	}
}

// stockInputs lit tout ce que le tableau de stock et la suggestion partagent : définition, comptes, capacité.
type stockInputs struct {
	cfg                     StockConfig
	source                  string
	enrolled, reserve       map[string]ListCounts
	capacity                map[string]Capacity
	enrolledIDs, reserveIDs []string
}

func (s *Service) readStock(ctx context.Context, workspaceID string, over StockConfig, days int) (*stockInputs, error) {
	if s.overview == nil {
		return nil, fmt.Errorf("stock: lecture des profils d'envoi non branchée")
	}
	if days > 0 {
		over.TargetDays = days
	}
	cfg, source, err := s.loadStockConfig(ctx, workspaceID, over)
	if err != nil {
		return nil, err
	}
	cfg = cfg.WithDefaults()
	in := &stockInputs{cfg: cfg, source: source}
	if cfg.EnrolledListID != "" {
		in.enrolledIDs = []string{cfg.EnrolledListID}
	} else {
		ids, err := s.store.ActiveAutomationLists(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		in.enrolledIDs = ids
		if len(ids) > 0 {
			in.source = "automations"
		}
	}
	if cfg.ReserveListID != "" {
		in.reserveIDs = []string{cfg.ReserveListID}
	}
	all := append(append([]string{}, in.enrolledIDs...), in.reserveIDs...)
	counts, err := s.store.CountStock(ctx, workspaceID, cfg, all)
	if err != nil {
		return nil, err
	}
	in.enrolled, in.reserve = map[string]ListCounts{}, map[string]ListCounts{}
	add := func(dst map[string]ListCounts, list string) {
		for class, c := range counts[list] {
			cur := dst[class]
			cur.Total += c.Total
			cur.Unsent += c.Unsent
			dst[class] = cur
		}
	}
	for _, id := range in.enrolledIDs {
		add(in.enrolled, id)
	}
	for _, id := range in.reserveIDs {
		add(in.reserve, id)
	}
	ov, err := s.overview.GetEmailProfilesOverview(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("capacité des profils: %w", err)
	}
	in.capacity = CapacityByClass(ov)
	return in, nil
}

var stockPerms = []permNeed{
	need(domain.PermissionResourceContacts, domain.PermissionTypeRead),
	need(domain.PermissionResourceMessageHistory, domain.PermissionTypeRead),
}

// StockOverview : le stock par classe, les jours de couverture, le besoin de réapprovisionnement.
func (s *Service) StockOverview(ctx context.Context, req StockRequest) (*StockOverview, error) {
	ctx, _, err := s.authorize(ctx, req.WorkspaceID, stockPerms...)
	if err != nil {
		return nil, err
	}
	if req.Days < 0 || req.Days > 60 {
		return nil, invalid("days doit être entre 1 et 60")
	}
	in, err := s.readStock(ctx, req.WorkspaceID, req.Override, req.Days)
	if err != nil {
		return nil, err
	}
	rows := BuildStock(in.cfg, in.enrolled, in.reserve, in.capacity, s.replyRateFunc(ctx, req.WorkspaceID))
	out := &StockOverview{WorkspaceID: req.WorkspaceID, GeneratedAt: s.now().UTC().Format(time.RFC3339), TargetDays: in.cfg.TargetDays,
		Config: in.cfg, ConfigSource: in.source, Classes: rows, Totals: Totals(rows), Notes: stockNotes(in)}
	if out.Classes == nil {
		out.Classes = []StockRow{}
	}
	return out, nil
}

func stockNotes(in *stockInputs) []string {
	notes := []string{
		"inscrits et réserve : contacts des listes, filtres du script stock-par-classe.py (classe = " + in.cfg.ClassField + "). days_of_coverage = prospectables / capacité du jour, comme le script ; la capacité du jour baisse avec l'heure et s'annule hors fenêtre d'envoi.",
		"unsent = contacts jamais contactés ; days_unsent = unsent / capacité nominale (une journée d'ouverture entière) : la couverture à planifier. restock_need = contacts à aller chercher pour tenir " + fmt.Sprint(in.cfg.TargetDays) + " jours.",
	}
	if len(in.enrolledIDs) == 0 {
		notes = append(notes, "aucune liste d'inscrits : poser enrolled_list_id (stock.config.set) ou passer enrolled_list, sinon seules les listes des automations actives comptent.")
	}
	if len(in.reserveIDs) == 0 {
		notes = append(notes, "aucune liste de réserve définie : la réserve vaut 0.")
	}
	return notes
}

// StockConfigView : la définition effective du stock.
type StockConfigView struct {
	WorkspaceID string      `json:"workspace_id"`
	Config      StockConfig `json:"config"`
	Source      string      `json:"source"`
}

// GetStockConfig rend la définition enregistrée (avec les défauts posés).
func (s *Service) GetStockConfig(ctx context.Context, workspaceID string) (*StockConfigView, error) {
	ctx, _, err := s.authorize(ctx, workspaceID, need(domain.PermissionResourceWorkspace, domain.PermissionTypeRead))
	if err != nil {
		return nil, err
	}
	cfg, source, err := s.loadStockConfig(ctx, workspaceID, StockConfig{})
	if err != nil {
		return nil, err
	}
	return &StockConfigView{WorkspaceID: workspaceID, Config: cfg.WithDefaults(), Source: source}, nil
}

// SetStockConfig enregistre la définition du stock (remplace l'ancienne).
func (s *Service) SetStockConfig(ctx context.Context, workspaceID string, cfg StockConfig) (*StockConfigView, error) {
	ctx, user, err := s.authorize(ctx, workspaceID, need(domain.PermissionResourceWorkspace, domain.PermissionTypeWrite))
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	for _, l := range []struct{ field, id string }{{"enrolled_list_id", cfg.EnrolledListID}, {"reserve_list_id", cfg.ReserveListID}} {
		if strings.TrimSpace(l.id) != l.id {
			return nil, invalid("%s contient des espaces", l.field)
		}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetSetting(ctx, workspaceID, StockKey, raw, actor(user)); err != nil {
		return nil, err
	}
	s.log.WithField("workspace_id", workspaceID).WithField("by", actor(user)).Info("policy: définition du stock enregistrée")
	return &StockConfigView{WorkspaceID: workspaceID, Config: cfg.WithDefaults(), Source: "config"}, nil
}

// SuggestRequest : policy.suggest-enrollment.
type SuggestRequest struct {
	WorkspaceID   string
	Days          int
	IncludeEmails bool
	// Limit : adresses au plus par classe quand IncludeEmails (défaut 200, max 2000).
	Limit    int
	Override StockConfig
}

const (
	defaultSuggestEmails = 200
	maxSuggestEmails     = 2000
)

// SuggestEnrollment calcule, par classe, combien de contacts de la réserve inscrire pour tenir Days jours de couverture
// sans dépasser les plafonds, et combien aller chercher faute de réserve. Il n'inscrit PERSONNE : le résultat dit
// Applied=false, aucune route du module n'écrit dans une automation.
func (s *Service) SuggestEnrollment(ctx context.Context, req SuggestRequest) (*Suggestion, error) {
	ctx, _, err := s.authorize(ctx, req.WorkspaceID, stockPerms...)
	if err != nil {
		return nil, err
	}
	if req.Days < 0 || req.Days > 60 {
		return nil, invalid("days doit être entre 1 et 60")
	}
	if req.Limit < 0 || req.Limit > maxSuggestEmails {
		return nil, invalid("limit doit être entre 1 et %d", maxSuggestEmails)
	}
	in, err := s.readStock(ctx, req.WorkspaceID, req.Override, req.Days)
	if err != nil {
		return nil, err
	}
	days := in.cfg.TargetDays
	now := s.now()
	followups, err := s.store.FollowupsDue(ctx, req.WorkspaceID, in.cfg, in.enrolledIDs, horizon(now, days))
	if err != nil {
		return nil, err
	}
	perClass := 0
	if req.IncludeEmails {
		perClass = req.Limit
		if perClass == 0 {
			perClass = defaultSuggestEmails
		}
	}
	cands, err := s.store.ReserveCandidates(ctx, req.WorkspaceID, in.cfg, in.enrolledIDs, perClass)
	if err != nil {
		return nil, err
	}
	// La réserve de la suggestion est la réserve ÉLIGIBLE (ni désabonnée, ni rejetée, pas déjà inscrite) : le nombre suggéré
	// est exactement le nombre d'adresses qu'on peut donner.
	eligible := map[string]ListCounts{}
	for class, c := range cands {
		eligible[class] = ListCounts{Total: c.Total, Unsent: c.Total}
	}
	rows, totals, restock := BuildSuggestion(days, in.enrolled, eligible, in.capacity, followups, s.replyRateFunc(ctx, req.WorkspaceID))
	if req.IncludeEmails {
		for i := range rows {
			if c, ok := cands[rows[i].Class]; ok && rows[i].SuggestEnroll > 0 {
				n := rows[i].SuggestEnroll
				if n > len(c.Emails) {
					n = len(c.Emails)
				}
				rows[i].Emails = c.Emails[:n]
			}
		}
	}
	out := &Suggestion{WorkspaceID: req.WorkspaceID, GeneratedAt: now.UTC().Format(time.RFC3339), Days: days, EnrollAutomationID: in.cfg.EnrollAutomationID,
		Classes: rows, Totals: totals, RestockPriority: restock, Applied: false, Notes: suggestNotes(in, req.IncludeEmails, days)}
	if out.Classes == nil {
		out.Classes = []SuggestRow{}
	}
	if out.RestockPriority == nil {
		out.RestockPriority = []RestockItem{}
	}
	return out, nil
}

func suggestNotes(in *stockInputs, withEmails bool, days int) []string {
	n := []string{
		fmt.Sprintf("capacité d'une classe = plafond de classe après ralentissement du fusible, borné par la journée entière du profil ; horizon = %d jours d'envoi. Les relances de contacts déjà en séquence qui échoient dans l'horizon passent avant les nouveaux contacts.", days),
		"Rien n'a été inscrit : Applied vaut toujours false. Pour appliquer, inscrire les adresses suggérées (automations.enroll) ; l'ordre des adresses est la priorité (custom_string_2, P1 d'abord) puis le score (custom_number_1).",
	}
	if in.cfg.ReserveListID == "" {
		n = append(n, "aucune liste de réserve définie (stock.config) : rien à suggérer, tout le manque est à chasser.")
	}
	if in.cfg.EnrollAutomationID == "" {
		n = append(n, "aucune automation d'inscription définie : seuls les contacts déjà dans la liste des inscrits sont exclus des suggestions.")
	}
	if !withEmails {
		n = append(n, "adresses non demandées (include_emails).")
	}
	return n
}
