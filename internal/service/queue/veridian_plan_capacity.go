package queue

import (
	"math"
	"sort"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork, lot 6 (10/10/2026) : CAPACITÉ DU JOUR d'un profil.
//
// Demande de Robert : la capacité maximale d'envoi d'un profil n'est plus un plafond
// plat « pas très smart » mais la somme de ce que chaque classe de destinataire peut
// absorber. Pour une classe :
//
//	capacité restante = min( plafond de classe - envoyés (après le facteur du fusible),
//	                         débit effectif x minutes d'ouverture restantes de la fenêtre )
//
// 0 si la classe est exclue, arrêtée par le fusible, ou si la fenêtre est close. Le débit
// effectif est veridianEffectiveClassRate (déjà divisé par le facteur du fusible) ; le
// plafond est veridianResolveCapLimits.ClassCap (idem) : AUCUN second calcul, ce sont les
// valeurs que lisent les portes du worker. Le jitter disperse seulement les re-contrôles
// autour de 60/débit (moyenne inchangée, veridian_jitter.go) : il ne change pas le débit
// moyen, donc pas la capacité. Le jeton de départ du limiteur (burst 1) peut ajouter UN
// envoi par classe et par journée : la capacité est donnée à un envoi près, par le bas.
//
// La somme est ensuite bornée par les portes de PROFIL qui existent dans le worker :
// la chauffe (plafond total du domaine), le plafond par expéditeur et, s'il est posé, le
// plafond plat du profil (désormais facultatif). Tout est calculé ICI, depuis les mêmes
// résolutions que le worker ; le test de parité rejoue le worker.
//
// « Utile » : la capacité qui peut réellement servir = envoyés + min(reste, stock dû
// aujourd'hui dans la classe). Le stock (file + séquences) est partagé par le pool : la
// part de chaque profil est calculée par VeridianAllocateStock.

type veridianBound struct {
	gate      string
	remaining int
}

type veridianCapacityInput struct {
	Classes          []domain.VeridianPlanClass
	FullRemaining    []*int
	FullLimiter      []string
	Sent             int
	Bounds           []veridianBound
	FullBounds       []veridianBound
	FlatCap          int
	MinutesLeft      float64
	MinutesFull      float64
	WindowConfigured bool
	StockShare       map[string]int
}

// veridianRateCapacity : envois qu'un débit/minute laisse passer sur `minutes` minutes
// (par le bas, à un envoi près).
func veridianRateCapacity(ratePerMin, minutes float64) int {
	if ratePerMin <= 0 || minutes <= 0 {
		return 0
	}
	return int(math.Floor(ratePerMin*minutes + 1e-9))
}

// veridianClassCapacityRemaining : ce qu'une classe peut encore absorber. nil = aucune
// limite de volume (ni plafond de classe ni débit).
func veridianClassCapacityRemaining(pc domain.VeridianPlanClass, lim veridianCapLimits, classSent int, minutes float64, windowClosed bool) (*int, string) {
	zero := 0
	switch {
	case pc.Excluded:
		return &zero, domain.VeridianCapacityLimiterExcluded
	case pc.Stopped:
		return &zero, domain.VeridianCapacityLimiterStopped
	case windowClosed:
		return &zero, domain.VeridianCapacityLimiterWindow
	}
	var out *int
	limiter := domain.VeridianCapacityLimiterNone
	if lim.ClassBase > 0 {
		v := lim.ClassCap - classSent
		if v < 0 {
			v = 0
		}
		out, limiter = &v, domain.VeridianCapacityLimiterClassCap
	}
	if pc.RatePerMin > 0 {
		v := veridianRateCapacity(pc.RatePerMin, minutes)
		if out == nil || v < *out {
			out, limiter = &v, domain.VeridianCapacityLimiterRate
		}
	}
	return out, limiter
}

func veridianMinBound(bounds []veridianBound) (*int, string) {
	var out *int
	gate := domain.VeridianPlanGateNone
	for _, b := range bounds {
		v := b.remaining
		if v < 0 {
			v = 0
		}
		if out == nil || v < *out {
			out, gate = &v, b.gate
		}
	}
	return out, gate
}

func veridianMinPtr(a, b *int) *int {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case *b < *a:
		return b
	}
	return a
}

// veridianSumRemaining additionne ; nil dès qu'une classe est sans limite.
func veridianSumRemaining(values []*int) *int {
	total := 0
	for _, v := range values {
		if v == nil {
			return nil
		}
		total += *v
	}
	return &total
}

func veridianAddPtr(base int, extra *int) *int {
	if extra == nil {
		return nil
	}
	v := base + *extra
	return &v
}

// veridianClassesLimit dit si ce sont les débits ou les plafonds de classe qui bornent
// la somme, et la classe qui pèse le plus.
func veridianClassesLimit(values []*int, limiters []string, classes []domain.VeridianPlanClass) (string, string) {
	rate, caps, best, bestClass := 0, 0, -1, ""
	for i, v := range values {
		if v == nil || *v <= 0 {
			continue
		}
		switch limiters[i] {
		case domain.VeridianCapacityLimiterRate:
			rate += *v
		case domain.VeridianCapacityLimiterClassCap:
			caps += *v
		}
		if *v > best {
			best, bestClass = *v, classes[i].Class
		}
	}
	switch {
	case best < 0:
		return domain.VeridianCapacityLimitBlocked, ""
	case rate >= caps && rate > 0:
		return domain.VeridianCapacityLimitClassRates, bestClass
	case caps > 0:
		return domain.VeridianCapacityLimitClassCaps, bestClass
	}
	return domain.VeridianCapacityLimitNone, bestClass
}

// veridianBuildCapacity assemble la capacité du profil et complète chaque classe
// (capacités configurée et utile, stock, limite). Les classes sont modifiées en place.
func veridianBuildCapacity(in veridianCapacityInput) domain.VeridianPlanCapacity {
	c := domain.VeridianPlanCapacity{
		Applicable: true, Sent: in.Sent, FlatCap: in.FlatCap,
		StockKnown:        in.StockShare != nil,
		WindowMinutesLeft: int(math.Round(in.MinutesLeft)),
		WindowMinutesFull: int(math.Round(in.MinutesFull)),
		Limiting:          domain.VeridianCapacityLimitNone,
		ProfileBoundGate:  domain.VeridianPlanGateNone,
		FullDayLimiting:   domain.VeridianCapacityLimitNone,
	}
	remaining := make([]*int, len(in.Classes))
	useful := make([]*int, len(in.Classes))
	limiters := make([]string, len(in.Classes))
	for i := range in.Classes {
		pc := &in.Classes[i]
		remaining[i] = pc.CapacityRemaining
		limiters[i] = pc.CapacityLimiter
		useful[i] = remaining[i]
		if in.StockShare != nil {
			k := in.StockShare[pc.Class]
			pc.Stock = &k
			if remaining[i] == nil || k < *remaining[i] {
				v := k
				useful[i] = &v
			}
		}
		pc.CapacityConfigured = veridianAddPtr(pc.SentToday, remaining[i])
		pc.CapacityUseful = veridianAddPtr(pc.SentToday, useful[i])
	}

	sumR := veridianSumRemaining(remaining)
	sumU := veridianSumRemaining(useful)
	bound, gate := veridianMinBound(in.Bounds)
	c.SumRemaining = sumR
	c.ProfileBound, c.ProfileBoundGate = bound, gate

	c.RemainingConfigured = veridianMinPtr(sumR, bound)
	c.RemainingUseful = veridianMinPtr(sumU, bound)
	c.Configured = veridianAddPtr(in.Sent, c.RemainingConfigured)
	c.Useful = veridianAddPtr(in.Sent, c.RemainingUseful)
	if c.RemainingConfigured != nil && c.RemainingUseful != nil {
		v := *c.RemainingConfigured - *c.RemainingUseful
		c.Unusable = &v
	}

	// Ce qui borne la capacité configurée : une porte de profil si elle est sous la somme
	// des classes, sinon la fenêtre close, sinon les débits ou les plafonds de classe.
	switch {
	case bound != nil && (sumR == nil || *bound <= *sumR):
		c.Limiting = gate
	case in.WindowConfigured && in.MinutesLeft <= 0:
		c.Limiting = domain.VeridianCapacityLimitWindow
	case sumR != nil:
		c.Limiting, c.LimitingClass = veridianClassesLimit(remaining, limiters, in.Classes)
	}

	if in.StockShare != nil {
		worst, worstClass := 0, ""
		for i := range in.Classes {
			if remaining[i] == nil || useful[i] == nil {
				continue
			}
			if gap := *remaining[i] - *useful[i]; gap > worst {
				worst, worstClass = gap, in.Classes[i].Class
			}
		}
		c.StockLimitingClass = worstClass
	}

	// Journée d'ouverture type : mêmes classes et mêmes portes, compteurs à zéro.
	sumF := veridianSumRemaining(in.FullRemaining)
	boundF, gateF := veridianMinBound(in.FullBounds)
	c.FullDayConfigured = veridianMinPtr(sumF, boundF)
	switch {
	case boundF != nil && (sumF == nil || *boundF <= *sumF):
		c.FullDayLimiting = gateF
	case sumF != nil:
		c.FullDayLimiting, _ = veridianClassesLimit(in.FullRemaining, in.FullLimiter, in.Classes)
	}
	return c
}

// VeridianAllocateStock partage le stock du pool (classe -> nombre de contacts dus
// aujourd'hui) entre les profils qui l'envoient. Le pool est partagé : un contact n'est
// attaché à aucun profil avant l'envoi (veridian_pool_failover.go). La part d'un profil
// pour une classe est proportionnelle à ce qu'il peut absorber dans cette classe
// (absorb), au plus grand reste : la somme des parts d'une classe égale son stock, et un
// profil qui peut absorber plus que sa part voit son utile plafonné par elle. absorb :
// profil -> classe -> capacité restante (nil = sans limite, pesée comme le stock entier).
func VeridianAllocateStock(stock map[string]int, absorb map[string]map[string]*int) map[string]map[string]int {
	out := map[string]map[string]int{}
	ids := make([]string, 0, len(absorb))
	for id := range absorb {
		ids = append(ids, id)
		out[id] = map[string]int{}
	}
	sort.Strings(ids)
	for _, class := range domain.VeridianAllProviderClasses() {
		total := stock[class]
		weights := make([]int, len(ids))
		sum := 0
		for i, id := range ids {
			w := 0
			if v, ok := absorb[id][class]; ok {
				if v == nil {
					w = total
				} else {
					w = *v
				}
			}
			weights[i] = w
			sum += w
		}
		if total <= 0 || sum <= 0 {
			for _, id := range ids {
				out[id][class] = 0
			}
			continue
		}
		type share struct {
			idx, whole, rest int
		}
		shares := make([]share, len(ids))
		given := 0
		for i := range ids {
			num := total * weights[i]
			shares[i] = share{idx: i, whole: num / sum, rest: num % sum}
			given += shares[i].whole
		}
		left := total - given
		order := append([]share(nil), shares...)
		sort.SliceStable(order, func(a, b int) bool { return order[a].rest > order[b].rest })
		for k := 0; k < len(order) && left > 0; k++ {
			if weights[order[k].idx] > 0 {
				shares[order[k].idx].whole++
				left--
			}
		}
		for i, id := range ids {
			out[id][class] = shares[i].whole
		}
	}
	return out
}
