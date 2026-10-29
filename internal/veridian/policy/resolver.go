package policy

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Resolver donne au worker et aux enqueues la politique effective et les priorités d'un
// workspace. Il implémente domain.VeridianPolicyProvider et
// domain.VeridianSendPriorityProvider. Les lectures en base sont mises en cache (TTL court)
// et ne bloquent jamais longtemps : base illisible = dernière valeur connue, sinon presets
// et priorité 5. Le cache est invalidé tout de suite sur les écritures du même processus.
type Resolver struct {
	store Store
	log   logger.Logger
	ttl   time.Duration
	// retry : délai avant de réessayer après un échec de lecture.
	retry   time.Duration
	timeout time.Duration
	now     func() time.Time

	mu   sync.RWMutex
	pol  map[string]polEntry
	prio map[string]prioEntry
	sf   singleflight.Group
}

type polEntry struct {
	snap *domain.VeridianPolicySnapshot
	at   time.Time // fin de validité
}

type prioEntry struct {
	byKey map[string]int
	at    time.Time
}

// NewResolver crée le résolveur. ttl <= 0 : 30 secondes.
func NewResolver(store Store, log logger.Logger, ttl time.Duration) *Resolver {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Resolver{store: store, log: log, ttl: ttl, retry: 5 * time.Second, timeout: 2 * time.Second, now: time.Now,
		pol: map[string]polEntry{}, prio: map[string]prioEntry{}}
}

var (
	_ domain.VeridianPolicyProvider       = (*Resolver)(nil)
	_ domain.VeridianSendPriorityProvider = (*Resolver)(nil)
)

// Invalidate oublie le cache d'un workspace (appelé après chaque écriture).
func (r *Resolver) Invalidate(workspaceID string) {
	r.mu.Lock()
	delete(r.pol, workspaceID)
	delete(r.prio, workspaceID)
	r.mu.Unlock()
}

// BuildSnapshot applique des surcharges au catalogue : une copie, jamais l'original.
func BuildSnapshot(overrides []Override) *domain.VeridianPolicySnapshot {
	base := domain.VeridianPresetSnapshot()
	s := &domain.VeridianPolicySnapshot{
		Classes:    make(map[string]domain.VeridianClassPolicy, len(base.Classes)),
		Caps:       make(map[string]int, len(base.Caps)),
		Rates:      make(map[string]float64, len(base.Rates)),
		Overridden: map[string]domain.VeridianPolicyOverridden{},
	}
	for k, v := range base.Classes {
		s.Classes[k] = v
	}
	for k, v := range base.Caps {
		s.Caps[k] = v
	}
	for k, v := range base.Rates {
		s.Rates[k] = v
	}
	for _, o := range overrides {
		if !domain.IsValidProviderClass(o.Class) {
			continue // ligne orpheline : jamais appliquée
		}
		p := s.Classes[o.Class]
		var ov domain.VeridianPolicyOverridden
		if o.DailyCap > 0 {
			p.DailyCap, ov.DailyCap = o.DailyCap, true
			s.Caps[o.Class] = o.DailyCap
		}
		if o.RatePerMin > 0 {
			p.RatePerMin, ov.RatePerMin = o.RatePerMin, true
			s.Rates[o.Class] = o.RatePerMin
		}
		if o.WarmupShare > 0 {
			p.WarmupShare, ov.WarmupShare = o.WarmupShare, true
		}
		if o.SlowdownThreshold > 0 {
			p.SlowdownThreshold, ov.SlowdownThreshold = o.SlowdownThreshold, true
		}
		s.Classes[o.Class] = p
		if ov.Any() {
			s.Overridden[o.Class] = ov
		}
	}
	return s
}

// PolicyFor : voir domain.VeridianPolicyProvider. Ne rend jamais nil.
func (r *Resolver) PolicyFor(workspaceID string) *domain.VeridianPolicySnapshot {
	now := r.now()
	r.mu.RLock()
	e, ok := r.pol[workspaceID]
	r.mu.RUnlock()
	if ok && now.Before(e.at) {
		return e.snap
	}
	v, _, _ := r.sf.Do("pol:"+workspaceID, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
		defer cancel()
		ov, err := r.store.ListOverrides(ctx, workspaceID)
		if err != nil {
			// Table absente (avant V66), base indisponible : on garde la dernière valeur connue, sinon
			// les presets, et on réessaie dans quelques secondes.
			r.log.WithField("workspace_id", workspaceID).WithField("error", err.Error()).Warn("policy: surcharges illisibles, presets ou dernière valeur utilisés")
			snap := domain.VeridianPresetSnapshot()
			if ok {
				snap = e.snap
			}
			r.mu.Lock()
			r.pol[workspaceID] = polEntry{snap: snap, at: r.now().Add(r.retry)}
			r.mu.Unlock()
			return snap, nil
		}
		snap := BuildSnapshot(ov)
		r.mu.Lock()
		r.pol[workspaceID] = polEntry{snap: snap, at: r.now().Add(r.ttl)}
		r.mu.Unlock()
		return snap, nil
	})
	return v.(*domain.VeridianPolicySnapshot)
}

// SendPriorityFor : voir domain.VeridianSendPriorityProvider. 0 = aucune priorité posée.
func (r *Resolver) SendPriorityFor(workspaceID string, sourceType domain.EmailQueueSourceType, sourceID string) int {
	now := r.now()
	r.mu.RLock()
	e, ok := r.prio[workspaceID]
	r.mu.RUnlock()
	if !ok || !now.Before(e.at) {
		v, _, _ := r.sf.Do("prio:"+workspaceID, func() (any, error) {
			ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
			defer cancel()
			rows, err := r.store.ListPriorities(ctx, workspaceID)
			if err != nil {
				r.log.WithField("workspace_id", workspaceID).WithField("error", err.Error()).Warn("policy: priorités illisibles, défaut 5 ou dernière valeur utilisés")
				next := prioEntry{byKey: map[string]int{}, at: r.now().Add(r.retry)}
				if ok {
					next.byKey = e.byKey
				}
				r.mu.Lock()
				r.prio[workspaceID] = next
				r.mu.Unlock()
				return next, nil
			}
			m := make(map[string]int, len(rows))
			for k, p := range rows {
				m[k] = p.Priority
			}
			next := prioEntry{byKey: m, at: r.now().Add(r.ttl)}
			r.mu.Lock()
			r.prio[workspaceID] = next
			r.mu.Unlock()
			return next, nil
		})
		e = v.(prioEntry)
	}
	return e.byKey[string(sourceType)+":"+sourceID]
}
