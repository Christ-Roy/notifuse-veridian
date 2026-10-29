package policy

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

type noopLog struct{}

func (noopLog) Debug(string)                                      {}
func (noopLog) Info(string)                                       {}
func (noopLog) Warn(string)                                       {}
func (noopLog) Error(string)                                      {}
func (noopLog) Fatal(string)                                      {}
func (n noopLog) WithField(string, interface{}) logger.Logger     { return n }
func (n noopLog) WithFields(map[string]interface{}) logger.Logger { return n }

// fakeStore : dépôt en mémoire.
type fakeStore struct {
	mu        sync.Mutex
	overrides map[string]Override
	prios     map[string]PrioritySource
	sources   map[string]string // "type:id" -> nom
	settings  map[string][]byte
	pending   map[string]int // "type:id" -> mails en attente (priorité par défaut)
	failList  error
	reads     int
	// stock : comptes par liste puis par classe ; stockCalls : définitions reçues ; autoLists : listes des automations actives.
	stock      map[string]map[string]ListCounts
	stockCalls []StockConfig
	autoLists  []string
	// followups : relances à venir par classe ; candidates : réserve éligible par classe ; candidateCalls : perClass reçus.
	followups      map[string]int
	candidates     map[string]Candidates
	candidateCalls []int
}

func newFakeStore() *fakeStore {
	return &fakeStore{overrides: map[string]Override{}, prios: map[string]PrioritySource{}, sources: map[string]string{}, settings: map[string][]byte{}, pending: map[string]int{}}
}

func (f *fakeStore) ListOverrides(context.Context, string) ([]Override, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.failList != nil {
		return nil, f.failList
	}
	var out []Override
	for _, o := range f.overrides {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })
	return out, nil
}

func (f *fakeStore) UpsertOverride(_ context.Context, _ string, o Override) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.overrides[o.Class]
	cur.Class = o.Class
	if o.DailyCap > 0 {
		cur.DailyCap = o.DailyCap
	}
	if o.RatePerMin > 0 {
		cur.RatePerMin = o.RatePerMin
	}
	if o.WarmupShare > 0 {
		cur.WarmupShare = o.WarmupShare
	}
	if o.SlowdownThreshold > 0 {
		cur.SlowdownThreshold = o.SlowdownThreshold
	}
	cur.UpdatedBy, cur.UpdatedAt = o.UpdatedBy, time.Now()
	f.overrides[o.Class] = cur
	return nil
}

func (f *fakeStore) ClearOverride(_ context.Context, _, class string, rules []string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.overrides[class]
	if !ok {
		return false, nil
	}
	if len(rules) == 0 {
		rules = AllRules
	}
	for _, r := range rules {
		switch r {
		case RuleDailyCap:
			cur.DailyCap = 0
		case RuleRatePerMin:
			cur.RatePerMin = 0
		case RuleWarmupShare:
			cur.WarmupShare = 0
		case RuleSlowdownThreshold:
			cur.SlowdownThreshold = 0
		}
	}
	if cur.DailyCap == 0 && cur.RatePerMin == 0 && cur.WarmupShare == 0 && cur.SlowdownThreshold == 0 {
		delete(f.overrides, class)
	} else {
		f.overrides[class] = cur
	}
	return true, nil
}

func (f *fakeStore) ListPriorities(context.Context, string) (map[string]PrioritySource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failList != nil {
		return nil, f.failList
	}
	out := map[string]PrioritySource{}
	for k, v := range f.prios {
		out[k] = v
	}
	return out, nil
}

func (f *fakeStore) ListSources(context.Context, string) ([]PrioritySource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []PrioritySource
	for k, name := range f.sources {
		parts := strings.SplitN(k, ":", 2)
		p := PrioritySource{SourceType: parts[0], SourceID: parts[1], Name: name, Status: "draft", Priority: 5, Pending: f.pending[k]}
		if e, ok := f.prios[k]; ok {
			p.Priority, p.Explicit = e.Priority, true
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	return out, nil
}

func (f *fakeStore) SourceName(_ context.Context, _, st, id string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.sources[st+":"+id]
	return n, ok, nil
}

func (f *fakeStore) SetPriority(_ context.Context, _, st, id string, p int, by string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prios[st+":"+id] = PrioritySource{SourceType: st, SourceID: id, Priority: p, Explicit: true, UpdatedBy: by}
	return int64(f.pending[st+":"+id]), nil
}

func (f *fakeStore) ClearPriority(_ context.Context, _, st, id string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.prios, st+":"+id)
	return int64(f.pending[st+":"+id]), nil
}

func (f *fakeStore) GetSetting(_ context.Context, _, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings[key], nil
}

func (f *fakeStore) SetSetting(_ context.Context, _, key string, v []byte, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings[key] = v
	return nil
}

func (f *fakeStore) CountStock(_ context.Context, _ string, cfg StockConfig, listIDs []string) (map[string]map[string]ListCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stockCalls = append(f.stockCalls, cfg)
	out := map[string]map[string]ListCounts{}
	for _, id := range listIDs {
		if c, ok := f.stock[id]; ok {
			out[id] = c
		}
	}
	return out, nil
}

func (f *fakeStore) FollowupsDue(context.Context, string, StockConfig, []string, time.Time) (map[string]int, error) {
	return f.followups, nil
}

func (f *fakeStore) ReserveCandidates(_ context.Context, _ string, _ StockConfig, _ []string, perClass int) (map[string]Candidates, error) {
	f.candidateCalls = append(f.candidateCalls, perClass)
	out := map[string]Candidates{}
	for class, c := range f.candidates {
		cc := Candidates{Total: c.Total}
		if perClass > 0 {
			n := perClass
			if n > len(c.Emails) {
				n = len(c.Emails)
			}
			cc.Emails = c.Emails[:n]
		}
		out[class] = cc
	}
	return out, nil
}

func (f *fakeStore) ActiveAutomationLists(context.Context, string) ([]string, error) {
	return f.autoLists, nil
}

var errBoom = errors.New("base indisponible")

// fakeAuth : authentification de test ; perms vide = propriétaire.
type fakeAuth struct {
	err   error
	perms domain.UserPermissions
	owner bool
	email string
	calls int
}

func (a *fakeAuth) AuthenticateUserForWorkspace(ctx context.Context, _ string) (context.Context, *domain.User, *domain.UserWorkspace, error) {
	a.calls++
	if a.err != nil {
		return ctx, nil, nil, a.err
	}
	role := "member"
	if a.owner {
		role = "owner"
	}
	email := a.email
	if email == "" {
		email = "agent@example.test"
	}
	return ctx, &domain.User{Email: email}, &domain.UserWorkspace{Role: role, Permissions: a.perms}, nil
}
