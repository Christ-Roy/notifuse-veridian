package queue

import (
	"sort"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork (fiche 62, lot 1 « pourquoi ça n'envoie pas », 10/10/2026).
//
// Chaque porte de la cascade (exclusion, réputation, débit de classe, plafond
// journalier, plafond par adresse, fenêtre) gagne une variante « Verdict » qui rend
// ce qu'elle a RÉELLEMENT utilisé : valeur observée, limite appliquée, délai,
// verdict. La fonction historique `(délai, bool)` devient un simple wrapper de la
// variante : UNE seule implémentation par porte (le défaut de la fiche 53 était d'en
// avoir deux). Un test de parité rejoue l'ancienne boucle de sélection et exige les
// mêmes gagnants.

// veridianGateVerdict est le résultat structuré d'une porte.
type veridianGateVerdict struct {
	Gate    string        // domain.VeridianGate*
	Verdict string        // domain.VeridianVerdict*
	Value   interface{}   // valeur observée
	Limit   interface{}   // limite appliquée
	Name    string        // sous-porte (warmup, provider_class, per_recipient, per_sender...)
	Detail  string        // précision libre et courte
	Delay   time.Duration // délai de re-planification demandé (blocage seulement)
	Reason  string        // code de raison (blocage seulement)
	Class   string        // porte d'exclusion : classe exclue
}

// Blocked : la porte refuse l'envoi maintenant.
func (v veridianGateVerdict) Blocked() bool { return v.Verdict == domain.VeridianVerdictBlock }

// record convertit le verdict en ligne de trace.
func (v veridianGateVerdict) record() domain.VeridianGateRecord {
	return domain.VeridianGateRecord{
		Gate:    v.Gate,
		Verdict: v.Verdict,
		Value:   v.Value,
		Limit:   v.Limit,
		Name:    v.Name,
		DelayS:  int(v.Delay / time.Second),
		Detail:  v.Detail,
	}
}

func veridianPassVerdict(gate string, value, limit interface{}, name, detail string) veridianGateVerdict {
	return veridianGateVerdict{Gate: gate, Verdict: domain.VeridianVerdictPass, Value: value, Limit: limit, Name: name, Detail: detail}
}

func veridianSkippedVerdict(gate, detail string) veridianGateVerdict {
	return veridianGateVerdict{Gate: gate, Verdict: domain.VeridianVerdictSkipped, Detail: detail}
}

// veridianCanonicalGateOrder : ordre d'AFFICHAGE des portes dans la trace (celui de la
// cascade documentée), indépendant de l'ordre d'évaluation.
var veridianCanonicalGateOrder = []string{
	domain.VeridianGateExcluded,
	domain.VeridianGateReputation,
	domain.VeridianGateClassRate,
	domain.VeridianGateDailyCap,
	domain.VeridianGateSenderCap,
	domain.VeridianGateWindow,
}

// veridianSortedKeys rend les clés d'un ensemble en ordre stable (trace lisible).
func veridianSortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k, ok := range set {
		if ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// veridianCandidateEvaluation est l'examen COMPLET d'un candidat : toutes les portes
// sont évaluées (sans court-circuit) pour expliquer la décision.
type veridianCandidateEvaluation struct {
	Candidate     veridianFailoverCandidate
	Outcome       string // selected | blocked | excluded | paused | circuit_open | skipped
	Gates         []veridianGateVerdict
	ExcludedClass string
	// BlockedDelay : un candidat bloqué ne peut pas envoyer avant que TOUTES ses
	// portes bloquantes se rouvrent : c'est le plus long de leurs délais.
	BlockedDelay time.Duration
	// Dominant : la porte bloquante au délai le plus long (la VRAIE raison).
	Dominant *veridianGateVerdict
}

// veridianDominantBlock choisit la porte bloquante au délai le plus long. À égalité,
// la plus tardive de l'ordre canonique l'emporte (fenêtre avant plafond avant
// débit) : la plus structurelle est la plus parlante.
func veridianDominantBlock(gates []veridianGateVerdict) (*veridianGateVerdict, time.Duration) {
	var best *veridianGateVerdict
	var longest time.Duration
	for i := range gates {
		g := gates[i]
		if !g.Blocked() {
			continue
		}
		if best == nil || g.Delay >= longest {
			gg := g
			best, longest = &gg, g.Delay
		}
	}
	return best, longest
}

// trace convertit l'examen en ligne de trace pour le journal.
func (e veridianCandidateEvaluation) trace(profileName string) domain.VeridianCandidateTrace {
	byGate := make(map[string]veridianGateVerdict, len(e.Gates))
	for _, g := range e.Gates {
		byGate[g.Gate] = g
	}
	gates := make([]domain.VeridianGateRecord, 0, len(e.Gates))
	for _, id := range veridianCanonicalGateOrder {
		if g, ok := byGate[id]; ok {
			gates = append(gates, g.record())
		}
	}
	return domain.VeridianCandidateTrace{
		Profile:     e.Candidate.IntegrationID,
		ProfileName: profileName,
		From:        e.Candidate.FromAddress,
		Outcome:     e.Outcome,
		Gates:       gates,
	}
}
