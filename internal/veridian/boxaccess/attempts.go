package boxaccess

import (
	"sort"
	"sync"
	"time"
)

// Essais de connexion reçus, pour l'écran des accès : « on n'a rien reçu de vous » dit plus qu'un mot de passe faux (le client n'a jamais atteint la
// porte : STARTTLS sur 443, autre port). En mémoire, bornés, jamais le mot de passe ; remis à zéro au redémarrage, la date le dit.
const (
	attemptsPerBox = 20
	attemptsBoxes  = 2000
)

// Attempt : un essai.
type Attempt struct {
	At      time.Time `json:"at"`
	Via     string    `json:"via"` // imap, smtp
	Address string    `json:"address"`
	OK      bool      `json:"ok"`
	Reason  string    `json:"reason,omitempty"` // bad_credentials, rate_limited, closed
}

// Attempts garde les derniers essais par adresse.
type Attempts struct {
	mu    sync.Mutex
	since time.Time
	boxes map[string][]Attempt
}

// NewAttempts construit un journal vide.
func NewAttempts() *Attempts {
	return &Attempts{since: time.Now().UTC(), boxes: map[string][]Attempt{}}
}

// Journal : celui du processus, écrit par les portes IMAP et SMTP.
var Journal = NewAttempts()

// Since : depuis quand le journal regarde.
func (a *Attempts) Since() time.Time { return a.since }

// Add note un essai. Au-delà de la borne, l'adresse la plus anciennement vue s'efface.
func (a *Attempts) Add(via, address string, ok bool, reason string) {
	address = NormalizeAddress(address)
	if address == "" || len(address) > 255 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, known := a.boxes[address]; !known && len(a.boxes) >= attemptsBoxes {
		oldest, at := "", time.Time{}
		for k, l := range a.boxes {
			if t := l[len(l)-1].At; oldest == "" || t.Before(at) {
				oldest, at = k, t
			}
		}
		delete(a.boxes, oldest)
	}
	l := append(a.boxes[address], Attempt{At: time.Now().UTC(), Via: via, Address: address, OK: ok, Reason: reason})
	if len(l) > attemptsPerBox {
		l = append([]Attempt(nil), l[len(l)-attemptsPerBox:]...)
	}
	a.boxes[address] = l
}

// Of rend les essais de ces adresses, du plus récent au plus ancien.
func (a *Attempts) Of(addresses []string, limit int) []Attempt {
	a.mu.Lock()
	defer a.mu.Unlock()
	out, seen := []Attempt{}, map[string]bool{}
	for _, x := range addresses {
		if x = NormalizeAddress(x); !seen[x] {
			seen[x] = true
			out = append(out, a.boxes[x]...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
