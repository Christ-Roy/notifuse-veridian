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
//
// Attrape-tout (26/11/2026, aucune adresse d'un domaine d'envoi ne doit rebondir) : une adresse du domaine qui n'est le
// profil de personne (contact@, postmaster@, une faute de frappe) va dans la boîte du PROPRIÉTAIRE du domaine. Le
// propriétaire se déclare (VERIDIAN_MX_DOMAINS=domaine=espace) ; sans déclaration, un domaine dont tous les profils sont
// dans un seul espace appartient à cet espace ; un domaine partagé par plusieurs espaces sans déclaration ne reçoit que
// ses adresses exactes. Jamais deviné : le 26/11, l'ancien repli postmaster@ (plus petite adresse, tous espaces
// confondus) tombait dans l'espace d'un client qui redirige son courrier vers des adresses à lui.
type Profiles struct {
	WS      WorkspaceLister
	Domains []string
	Owners  map[string]string // domaine -> espace propriétaire déclaré
	Now     func() time.Time

	mu       sync.Mutex
	loaded   time.Time
	byAddr   map[string][]*Target
	catchAll map[string]*Target // domaine -> boîte qui reçoit les adresses sans profil (postmaster@ et abuse@ compris)
}

// NewProfiles construit l'annuaire pour les domaines ouverts : « domaine » ou « domaine=espace propriétaire ».
func NewProfiles(ws WorkspaceLister, domains []string) *Profiles {
	p := &Profiles{WS: ws, Now: time.Now, Owners: map[string]string{}}
	for _, d := range domains {
		d, owner, _ := strings.Cut(d, "=")
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			p.Domains = append(p.Domains, d)
			if owner = strings.TrimSpace(owner); owner != "" {
				p.Owners[d] = owner
			}
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
	by := map[string][]*Target{}
	first := map[string]map[string]*Target{} // domaine -> espace -> sa boîte de plus petite adresse sur ce domaine
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
			if first[d] == nil {
				first[d] = map[string]*Target{}
			}
			if f := first[d][w.ID]; f == nil || a < f.Address {
				first[d][w.ID] = t
			}
		}
	}
	catchAll := map[string]*Target{}
	for d, perWS := range first {
		if owner, declared := p.Owners[d]; declared {
			if t := perWS[owner]; t != nil {
				catchAll[d] = t
			}
			continue
		}
		if len(perWS) == 1 {
			for _, t := range perWS {
				catchAll[d] = t
			}
		}
	}
	p.byAddr, p.catchAll, p.loaded = by, catchAll, p.Now()
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
	if t := p.catchAll[domainOf(a)]; t != nil {
		return t, nil
	}
	return nil, ErrUnknown
}
