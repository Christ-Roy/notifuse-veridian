package mxgate

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/internal/veridian/mailbox"
)

// WorkspaceLister liste les espaces.
type WorkspaceLister interface {
	List(ctx context.Context) ([]*domain.Workspace, error)
}

const directoryTTL = time.Minute

// Profiles est l'annuaire : adresse de profil -> boîte, pour les domaines ouverts seulement. Relu au plus une fois par minute.
type Profiles struct {
	WS      WorkspaceLister
	Domains []string
	Now     func() time.Time

	mu     sync.Mutex
	loaded time.Time
	byAddr map[string][]*Target
	first  map[string]*Target // domaine -> boîte qui reçoit postmaster@ et abuse@
}

// NewProfiles construit l'annuaire pour les domaines ouverts.
func NewProfiles(ws WorkspaceLister, domains []string) *Profiles {
	p := &Profiles{WS: ws, Now: time.Now}
	for _, d := range domains {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			p.Domains = append(p.Domains, d)
		}
	}
	return p
}

func domainOf(a string) string {
	if i := strings.LastIndexByte(a, '@'); i >= 0 {
		return a[i+1:]
	}
	return ""
}

func (p *Profiles) open(a string) bool {
	d := domainOf(a)
	for _, x := range p.Domains {
		if x == d {
			return true
		}
	}
	return false
}

func (p *Profiles) load(ctx context.Context) error {
	if !p.loaded.IsZero() && p.Now().Sub(p.loaded) < directoryTTL {
		return nil
	}
	l, err := p.WS.List(ctx)
	if err != nil {
		return err
	}
	by, first := map[string][]*Target{}, map[string]*Target{}
	for _, w := range l {
		if w == nil {
			continue
		}
		owners := mailbox.OwnersOf(w)
		for a, profiles := range owners {
			if !p.open(a) {
				continue
			}
			t := &Target{WorkspaceID: w.ID, ProfileID: profiles[0], Address: a, Owners: owners}
			by[a] = append(by[a], t)
			d := domainOf(a)
			if f := first[d]; f == nil || a < f.Address {
				first[d] = t
			}
		}
	}
	p.byAddr, p.first, p.loaded = by, first, p.Now()
	return nil
}

// Lookup implémente Directory.
func (p *Profiles) Lookup(ctx context.Context, address string) (*Target, error) {
	a := strings.ToLower(strings.Trim(strings.TrimSpace(address), "<>"))
	if !p.open(a) {
		return nil, ErrUnknown
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(ctx); err != nil {
		return nil, err
	}
	switch l := p.byAddr[a]; {
	case len(l) == 1:
		return l[0], nil
	case len(l) > 1:
		return nil, ErrAmbiguous
	}
	if local := a[:strings.LastIndexByte(a, '@')]; local == "postmaster" || local == "abuse" {
		if t := p.first[domainOf(a)]; t != nil {
			return t, nil
		}
	}
	return nil, ErrUnknown
}
