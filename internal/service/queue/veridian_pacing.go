package queue

import (
	"context"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork : ÉTALEMENT des envois commerciaux d'un profil sur sa fenêtre d'envoi (27/11/2026).
//
// Demande de Robert : « on devrait prendre la fenêtre de temps pour espacer le plus possible les mails ». Mesuré le
// même jour sur l'espace de prospection greenbeamcraft : 87 envois entre 07:00 et 10:41 UTC, écart médian 70 s, 42
// envois à moins d'une minute du précédent, pour une fenêtre de 8 h à 19 h. Cause : les débits sont posés PAR CLASSE
// de fournisseur destinataire ; vingt-cinq classes qui avancent chacune à son rythme font, additionnées, plus d'un envoi
// par minute pour le profil, et le plafond du jour se vide le matin. Aucune porte ne répartissait le reste du jour.
//
// Règle : après un envoi commercial d'un profil, le suivant attend
//
//	intervalle = minutes d'ouverture restantes de la fenêtre / (envois utiles restants + 1)
//
// dispersé de ±30 % (même amplitude et même cascade que le jitter du débit de classe, fiche 25 ; *0 = sans
// dispersion). « Envois utiles restants » et « minutes restantes » viennent du plan du profil (fiche 60 : somme des
// classes bornée par la chauffe, les plafonds et le stock dû), le calcul que montre l'écran Profils d'envoi : aucun
// second calcul de capacité ici. Le plan est relu toutes les 2 minutes par espace ; entre deux lectures, les envois
// de ce processus sont décomptés. L'intervalle est recalculé à chaque examen : si le stock grossit ou si la journée
// a pris du retard, l'attente raccourcit d'elle-même.
//
// Ne s'applique qu'aux envois commerciaux (séquences, campagnes) d'un profil qui a une fenêtre d'envoi et dont la
// capacité du jour est bornée et connue. Sans planificateur branché, plan illisible, fenêtre fermée ou plus rien
// d'utile à envoyer : la porte laisse passer (les autres portes, plafonds compris, tiennent toujours). Coupe-circuit :
// VERIDIAN_PACING=off.
//
// État en mémoire, par processus : après un redémarrage, le premier envoi d'un profil part sans attendre, puis
// l'étalement reprend. Deux allocations simultanées (déploiement à canari) peuvent doubler le rythme quelques minutes.

// VeridianPaceInput : ce que le plan du jour dit d'un profil.
type VeridianPaceInput struct {
	// Remaining : envois commerciaux utiles restants aujourd'hui (capacité utile restante). Known faux = illimité
	// ou non mesuré : pas d'étalement.
	Remaining int
	Known     bool
	// MinutesLeft : minutes d'ouverture restantes de la fenêtre du profil, au moment du plan.
	MinutesLeft float64
}

// PacingPlanner rend le plan du jour de chaque profil d'un espace (clé : id d'intégration).
type PacingPlanner interface {
	ProfilePace(ctx context.Context, workspaceID string) (map[string]VeridianPaceInput, error)
}

const (
	veridianPacingPlanTTL     = 2 * time.Minute
	veridianPacingPlanTimeout = 20 * time.Second
	// Borne haute d'un intervalle : au-delà, le plan est sans doute faux (une poignée d'envois pour une journée) et
	// la fenêtre se fermerait sur une file en attente. Le dernier envoi utile reste ainsi dans la journée.
	veridianPacingMaxInterval = 90 * time.Minute
)

type veridianPacingPlan struct {
	at       time.Time
	profiles map[string]VeridianPaceInput
	ok       bool
}

type veridianPacingSlot struct {
	last   time.Time
	factor float64 // dispersion tirée au dernier envoi, gardée jusqu'au suivant (pas de retirage à chaque examen)
}

type veridianPacer struct {
	planner PacingPlanner
	now     func() time.Time
	rng     func() float64

	mu        sync.Mutex
	plans     map[string]veridianPacingPlan // espace -> plan
	slots     map[string]veridianPacingSlot // espace|profil -> dernier envoi
	sincePlan map[string]int                // espace|profil -> envois de ce processus depuis le plan
}

var veridianPacerHolder atomic.Pointer[veridianPacer]

// SetVeridianPacingPlanner branche (ou retire, avec nil) l'étalement.
func SetVeridianPacingPlanner(p PacingPlanner) {
	if p == nil {
		veridianPacerHolder.Store(nil)
		return
	}
	veridianPacerHolder.Store(newVeridianPacer(p, time.Now, rand.Float64))
}

func newVeridianPacer(p PacingPlanner, now func() time.Time, rng func() float64) *veridianPacer {
	return &veridianPacer{planner: p, now: now, rng: rng,
		plans: map[string]veridianPacingPlan{}, slots: map[string]veridianPacingSlot{}, sincePlan: map[string]int{}}
}

func veridianPacingDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VERIDIAN_PACING"))) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}

// veridianPacingApplies : envoi commercial d'un profil qui a une fenêtre.
func veridianPacingApplies(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) bool {
	return veridianRelayIsCommercial(entry) && veridianResolveSendingWindow(workspace, provider, entry) != nil
}

func veridianPacingKey(workspaceID, integrationID string) string {
	return workspaceID + "|" + integrationID
}

// plan rend le plan de l'espace, relu au-delà du TTL. Appelé sous p.mu. Un échec de lecture est gardé TTL lui aussi
// (pas une lecture par examen d'entrée) et laisse passer.
func (p *veridianPacer) plan(workspaceID string, now time.Time) veridianPacingPlan {
	if pl, ok := p.plans[workspaceID]; ok && now.Sub(pl.at) < veridianPacingPlanTTL {
		return pl
	}
	ctx, cancel := context.WithTimeout(context.Background(), veridianPacingPlanTimeout)
	defer cancel()
	profiles, err := p.planner.ProfilePace(ctx, workspaceID)
	pl := veridianPacingPlan{at: now, profiles: profiles, ok: err == nil}
	p.plans[workspaceID] = pl
	for k := range p.sincePlan {
		if strings.HasPrefix(k, workspaceID+"|") {
			delete(p.sincePlan, k)
		}
	}
	return pl
}

// interval : l'intervalle courant du profil, 0 = pas d'étalement. Appelé sous p.mu.
func (p *veridianPacer) interval(workspaceID, integrationID string, now time.Time) (time.Duration, int, float64) {
	pl := p.plan(workspaceID, now)
	if !pl.ok {
		return 0, 0, 0
	}
	in, found := pl.profiles[integrationID]
	if !found || !in.Known {
		return 0, 0, 0
	}
	remaining := in.Remaining - p.sincePlan[veridianPacingKey(workspaceID, integrationID)]
	minutes := in.MinutesLeft - now.Sub(pl.at).Minutes()
	return veridianPacingInterval(remaining, minutes), remaining, minutes
}

// veridianPacingInterval : minutes restantes / (envois restants + 1), borné. PUR.
func veridianPacingInterval(remaining int, minutesLeft float64) time.Duration {
	if remaining <= 0 || minutesLeft <= 0 {
		return 0
	}
	d := time.Duration(minutesLeft / float64(remaining+1) * float64(time.Minute))
	if d > veridianPacingMaxInterval {
		d = veridianPacingMaxInterval
	}
	return d
}

// verdict : le créneau du profil est-il libre ? Lecture seule (rien n'est réservé).
func (p *veridianPacer) verdict(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry, integrationID string) veridianGateVerdict {
	if veridianPacingDisabled() {
		return veridianSkippedVerdict(domain.VeridianGatePacing, "VERIDIAN_PACING=off")
	}
	if workspace == nil || !veridianPacingApplies(workspace, provider, entry) {
		return veridianSkippedVerdict(domain.VeridianGatePacing, "not a commercial send with a sending window")
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	iv, remaining, minutes := p.interval(workspace.ID, integrationID, now)
	if iv <= 0 {
		return veridianPassVerdict(domain.VeridianGatePacing, remaining, nil, "", "no spreading (plan unreadable, unbounded or nothing left)")
	}
	slot, seen := p.slots[veridianPacingKey(workspace.ID, integrationID)]
	limit := iv.Round(time.Second).String()
	if !seen || slot.last.IsZero() {
		return veridianPassVerdict(domain.VeridianGatePacing, "first send of this process", limit, "", "")
	}
	next := slot.last.Add(time.Duration(float64(iv) * slot.factor))
	if !now.Before(next) {
		return veridianPassVerdict(domain.VeridianGatePacing, now.Sub(slot.last).Round(time.Second).String(), limit, "", "")
	}
	wait := next.Sub(now)
	if wait < time.Second {
		wait = time.Second
	}
	return veridianGateVerdict{
		Gate: domain.VeridianGatePacing, Verdict: domain.VeridianVerdictBlock,
		Value: now.Sub(slot.last).Round(time.Second).String(), Limit: limit,
		Detail: veridianPacingDetail(remaining, minutes),
		Delay:  wait, Reason: domain.VeridianReasonPacing,
	}
}

func veridianPacingDetail(remaining int, minutes float64) string {
	return strconv.Itoa(remaining) + " sends left over " + strconv.Itoa(int(minutes)) + " window minutes"
}

// take réserve le créneau : à appeler quand toutes les portes du candidat sont ouvertes. Faux si un autre examen l'a
// pris entre la lecture et la réservation (le candidat est alors bloqué comme par la porte).
func (p *veridianPacer) take(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry, integrationID string) bool {
	if veridianPacingDisabled() || workspace == nil || !veridianPacingApplies(workspace, provider, entry) {
		return true
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	key := veridianPacingKey(workspace.ID, integrationID)
	iv, _, _ := p.interval(workspace.ID, integrationID, now)
	if slot, seen := p.slots[key]; iv > 0 && seen && !slot.last.IsZero() && now.Before(slot.last.Add(time.Duration(float64(iv)*slot.factor))) {
		return false
	}
	factor := 1.0
	if pct := veridianResolveJitterPct(workspace, provider, entry); pct > 0 && p.rng != nil {
		factor = 1 + (p.rng()*2-1)*pct
	}
	p.slots[key] = veridianPacingSlot{last: now, factor: factor}
	p.sincePlan[key]++
	return true
}

// veridianPacingVerdict / veridianPacingTake : points d'entrée des portes (no-op sans planificateur).
func veridianPacingVerdict(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry, integrationID string) veridianGateVerdict {
	p := veridianPacerHolder.Load()
	if p == nil {
		return veridianSkippedVerdict(domain.VeridianGatePacing, "pacing not wired")
	}
	return p.verdict(workspace, provider, entry, integrationID)
}

func veridianPacingTake(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry, integrationID string) bool {
	p := veridianPacerHolder.Load()
	if p == nil {
		return true
	}
	return p.take(workspace, provider, entry, integrationID)
}
