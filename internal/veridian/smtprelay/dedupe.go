package smtprelay

import (
	"sync"
	"time"
)

// dedupePruneAt : au-dela de ce nombre d'entrees, Remember purge les expirees.
const dedupePruneAt = 4096

// Dedupe memorise, dans le processus, les Message-ID deja relayes pendant une
// fenetre (idempotence : un retry client ne double pas le mail). Perdu au
// redemarrage : limite assumee en phase 1 (fiche 71).
type Dedupe struct {
	mu   sync.Mutex
	ttl  time.Duration
	seen map[string]time.Time
}

// NewDedupe construit une memoire de fenetre ttl.
func NewDedupe(ttl time.Duration) *Dedupe {
	return &Dedupe{ttl: ttl, seen: make(map[string]time.Time)}
}

// Seen dit si key a ete memorisee moins de ttl avant now.
func (d *Dedupe) Seen(key string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	at, ok := d.seen[key]
	return ok && now.Sub(at) < d.ttl
}

// Remember memorise key a partir de now, et purge les entrees expirees une fois
// le seuil atteint.
func (d *Dedupe) Remember(key string, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen[key] = now
	if len(d.seen) > dedupePruneAt {
		for k, at := range d.seen {
			if now.Sub(at) >= d.ttl {
				delete(d.seen, k)
			}
		}
	}
}
