package domain

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

//go:generate mockgen -destination mocks/mock_veridian_prospection_stats_service.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianProspectionStatsService
//go:generate mockgen -destination mocks/mock_veridian_prospection_stats_repository.go -package mocks github.com/Notifuse/notifuse/internal/domain VeridianProspectionStatsRepository

// Veridian fork, lot 5 (08/10/2026) : agrégats du tableau de bord de prospection
// qui n'existaient nulle part.
//
// Ce que le tableau de bord lit ailleurs, SANS second calcul :
//   - envois par jour et par heure, par profil, rejets, désinscriptions, plaintes :
//     le moteur analytics (schéma message_history, dimension veridian_profile_id) ;
//   - plafonds du jour, réputation par fournisseur destinataire : emailProfiles.overview ;
//   - réponses globales : messages.replyStats.
//
// Ce qui manquait, et que ce fichier pose : les réponses humaines et automatiques
// PAR SÉQUENCE et PAR LISTE (segment), l'avancement de chaque séquence étape par
// étape (combien à J0, J+4, J+10, combien sortis et pourquoi), et le stock restant
// par liste. La donnée « a répondu » vit dans veridian_contact_reply (un contact =
// une ligne) : elle est rattachée à une séquence ou à une liste par l'appartenance
// du contact (contact_automations, contact_lists). Un contact inscrit dans deux
// séquences compte dans chacune : les lignes ne se somment pas, le total global
// reste celui de messages.replyStats.

// Classes de raison de sortie d'une séquence.
const (
	VeridianExitClassReplied      = "replied"
	VeridianExitClassRejected     = "rejected"
	VeridianExitClassUnsubscribed = "unsubscribed"
	VeridianExitClassExcluded     = "excluded"
	VeridianExitClassOther        = "other"
)

// VeridianClassifyExitReason range le texte libre contact_automations.exit_reason
// dans une des classes du tableau de bord. « excluded_provider_class:ionos » et
// « pre-filtered recipient: ... » sont des exclusions volontaires du tunnel cold.
func VeridianClassifyExitReason(reason string) string {
	r := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.HasPrefix(r, "replied"):
		return VeridianExitClassReplied
	case strings.HasPrefix(r, "bounce"):
		return VeridianExitClassRejected
	case strings.HasPrefix(r, "unsubscribe"):
		return VeridianExitClassUnsubscribed
	case strings.HasPrefix(r, "excluded_provider_class"), strings.HasPrefix(r, "pre-filtered"):
		return VeridianExitClassExcluded
	default:
		return VeridianExitClassOther
	}
}

// --- Contrat de sortie (GET|POST /api/veridian/prospection.stats) ---

// VeridianProspectionStage : une étape d'envoi d'une séquence (un mail, ou les
// deux variantes d'un test A/B qui se rejoignent à la même étape).
type VeridianProspectionStage struct {
	Stage   int      `json:"stage"`
	Label   string   `json:"label"` // J0, J+4, J+10 : délai cumulé depuis l'entrée
	NodeIDs []string `json:"node_ids"`
	// Sent : contacts à qui ce mail est parti (et qui peuvent être plus loin).
	Sent int `json:"sent"`
	// Queued : le mail est en file d'envoi, pas encore parti.
	Queued int `json:"queued"`
	// Waiting : le contact a reçu le mail précédent et attend l'échéance de celui-ci.
	Waiting int `json:"waiting"`
	// ExitedAfter : sortis de la séquence après avoir reçu ce mail (n'iront pas plus loin).
	ExitedAfter int `json:"exited_after"`
}

// VeridianProspectionExits : sorties de séquence, par raison.
type VeridianProspectionExits struct {
	Replied      int `json:"replied"`
	Rejected     int `json:"rejected"`
	Unsubscribed int `json:"unsubscribed"`
	Excluded     int `json:"excluded"`
	Other        int `json:"other"`
	Total        int `json:"total"`
	// BeforeFirstMail : sortis sans avoir reçu aucun mail (exclusion, pré-filtre...).
	BeforeFirstMail int `json:"before_first_mail"`
}

type VeridianSequenceStats struct {
	AutomationID string                     `json:"automation_id"`
	Name         string                     `json:"name"`
	Status       string                     `json:"status"`
	ListID       string                     `json:"list_id"`
	Enrolled     int                        `json:"enrolled"`
	Completed    int                        `json:"completed"`
	Failed       int                        `json:"failed"`
	Stages       []VeridianProspectionStage `json:"stages"`
	Exits        VeridianProspectionExits   `json:"exits"`
	// Réponses de la fenêtre demandée, des contacts inscrits dans cette séquence.
	RepliesHuman int `json:"replies_human"`
	RepliesAuto  int `json:"replies_auto"`
	// SentContacts : contacts distincts de la séquence à qui un mail est parti dans
	// la fenêtre. Dénominateur du taux (même esprit que le taux global : réponses
	// de la période sur envois de la période).
	SentContacts   int      `json:"sent_contacts"`
	ReplyRateHuman *float64 `json:"reply_rate_human"`
}

type VeridianSegmentStats struct {
	ListID         string   `json:"list_id"`
	Name           string   `json:"name"`
	SequenceIDs    []string `json:"sequence_ids"`
	Active         int      `json:"active"`
	Bounced        int      `json:"bounced"`
	Unsubscribed   int      `json:"unsubscribed"`
	Complained     int      `json:"complained"`
	NeverContacted int      `json:"never_contacted"` // stock restant : actifs sans aucun mail parti
	RepliesHuman   int      `json:"replies_human"`
	RepliesAuto    int      `json:"replies_auto"`
	SentContacts   int      `json:"sent_contacts"`
	ReplyRateHuman *float64 `json:"reply_rate_human"`
}

type VeridianProspectionStats struct {
	GeneratedAt time.Time               `json:"generated_at"`
	Since       *time.Time              `json:"since"`
	Until       *time.Time              `json:"until"`
	Sequences   []VeridianSequenceStats `json:"sequences"`
	Segments    []VeridianSegmentStats  `json:"segments"`
	// Totals : sommes sans doublon des lignes qui peuvent se sommer (stock restant,
	// contacts en file J0). Les réponses ne se somment pas (voir l'en-tête).
	Totals VeridianProspectionTotals `json:"totals"`
}

type VeridianProspectionTotals struct {
	StockRemaining int `json:"stock_remaining"`
	QueuedFirst    int `json:"queued_first_mail"`
}

type VeridianProspectionStatsRequest struct {
	WorkspaceID string    `json:"workspace_id"`
	Since       time.Time `json:"-"`
	Until       time.Time `json:"-"`
}

type VeridianProspectionStatsService interface {
	GetProspectionStats(ctx context.Context, req *VeridianProspectionStatsRequest) (*VeridianProspectionStats, error)
}

// --- Lecture brute (repository) ---

type VeridianProspectionAutomation struct {
	ID         string
	Name       string
	Status     string
	ListID     string
	RootNodeID string
	Nodes      []AutomationNode
}

// VeridianProspectionProgressRow : nombre de contact_automations d'une séquence vivante
// pour un état donné. LastEmailNodeID = dernier nœud email dont l'exécution est
// terminée (le mail est mis en file à ce moment, il peut ne pas être encore parti).
type VeridianProspectionProgressRow struct {
	AutomationID    string
	Status          string
	ExitReason      string
	CurrentNodeID   string
	LastEmailNodeID string
	Count           int
}

type VeridianProspectionKeyCount struct {
	Key   string
	Count int
}

type VeridianProspectionReplyRow struct {
	Key   string // identifiant de la séquence ou de la liste
	Human int
	Auto  int
}

type VeridianProspectionListRow struct {
	ID             string
	Name           string
	Active         int
	Bounced        int
	Unsubscribed   int
	Complained     int
	NeverContacted int
}

type VeridianProspectionRaw struct {
	Automations       []VeridianProspectionAutomation
	Progress          []VeridianProspectionProgressRow
	AutomationReplies []VeridianProspectionReplyRow
	AutomationSent    []VeridianProspectionKeyCount
	Lists             []VeridianProspectionListRow
	ListReplies       []VeridianProspectionReplyRow
	ListSent          []VeridianProspectionKeyCount
}

type VeridianProspectionStatsRepository interface {
	// GetProspectionRaw lit, en lecture seule, les comptes bruts de la fenêtre
	// [since, until[ (zéro = pas de borne). Aucune écriture.
	GetProspectionRaw(ctx context.Context, workspaceID string, since, until time.Time) (*VeridianProspectionRaw, error)
}

// --- Construction (fonction pure, sans I/O) ---

type veridianNodeStages struct {
	stages    []VeridianProspectionStage
	nodeStage map[string]int
}

// veridianSequenceStages parcourt le graphe depuis la racine et numérote les étapes
// d'envoi : un nœud email ouvre l'étape (nombre de mails déjà croisés + 1), les deux
// variantes d'un test A/B qui rejoignent le même délai tombent sur la même étape. Le
// libellé vient du délai cumulé (J0, J+4, J+10), jamais d'un nom saisi à la main.
func veridianSequenceStages(a VeridianProspectionAutomation) veridianNodeStages {
	byID := map[string]AutomationNode{}
	for _, n := range a.Nodes {
		byID[n.ID] = n
	}
	root := a.RootNodeID
	if _, ok := byID[root]; !ok && len(a.Nodes) > 0 {
		root = a.Nodes[0].ID
	}
	type cursor struct {
		id     string
		emails int
		delay  float64 // minutes cumulées
	}
	out := veridianNodeStages{nodeStage: map[string]int{}}
	stageIdx := map[int]*VeridianProspectionStage{}
	visited := map[string]bool{}
	queue := []cursor{{id: root}}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		n, ok := byID[c.id]
		if !ok || visited[c.id] {
			continue
		}
		visited[c.id] = true
		emails, delay := c.emails, c.delay
		switch n.Type {
		case NodeTypeEmail:
			emails++
			out.nodeStage[n.ID] = emails
			st := stageIdx[emails]
			if st == nil {
				st = &VeridianProspectionStage{Stage: emails, Label: veridianStageLabel(delay), NodeIDs: []string{}}
				stageIdx[emails] = st
			}
			st.NodeIDs = append(st.NodeIDs, n.ID)
		case NodeTypeDelay:
			delay += veridianDelayMinutes(n.Config)
		}
		for _, next := range veridianNodeChildren(n) {
			queue = append(queue, cursor{id: next, emails: emails, delay: delay})
		}
	}
	for _, st := range stageIdx {
		sort.Strings(st.NodeIDs)
		out.stages = append(out.stages, *st)
	}
	sort.Slice(out.stages, func(i, j int) bool { return out.stages[i].Stage < out.stages[j].Stage })
	return out
}

func veridianNodeChildren(n AutomationNode) []string {
	var next []string
	if n.NextNodeID != nil && *n.NextNodeID != "" {
		next = append(next, *n.NextNodeID)
	}
	if variants, ok := n.Config["variants"].([]interface{}); ok {
		for _, v := range variants {
			if m, ok := v.(map[string]interface{}); ok {
				if id, ok := m["next_node_id"].(string); ok && id != "" {
					next = append(next, id)
				}
			}
		}
	}
	return next
}

func veridianDelayMinutes(cfg map[string]interface{}) float64 {
	d, _ := cfg["duration"].(float64)
	if d <= 0 {
		return 0
	}
	switch unit, _ := cfg["unit"].(string); unit {
	case "minutes":
		return d
	case "hours":
		return d * 60
	default: // days
		return d * 1440
	}
}

func veridianStageLabel(delayMinutes float64) string {
	if delayMinutes <= 0 {
		return "J0"
	}
	if int(delayMinutes)%1440 == 0 {
		return fmt.Sprintf("J+%d", int(delayMinutes)/1440)
	}
	return fmt.Sprintf("+%dh", int(delayMinutes/60))
}

func veridianReplyRate(human, sent int) *float64 {
	if sent <= 0 {
		return nil
	}
	rate := float64(human) / float64(sent)
	return &rate
}

// VeridianBuildProspectionStats assemble la réponse. Pure : testée sans base.
func VeridianBuildProspectionStats(raw *VeridianProspectionRaw, since, until, now time.Time) *VeridianProspectionStats {
	out := &VeridianProspectionStats{
		GeneratedAt: now.UTC(),
		Sequences:   []VeridianSequenceStats{},
		Segments:    []VeridianSegmentStats{},
	}
	if !since.IsZero() {
		s := since.UTC()
		out.Since = &s
	}
	if !until.IsZero() {
		u := until.UTC()
		out.Until = &u
	}
	if raw == nil {
		return out
	}

	replies := map[string]VeridianProspectionReplyRow{}
	for _, r := range raw.AutomationReplies {
		replies[r.Key] = r
	}
	sent := map[string]int{}
	for _, r := range raw.AutomationSent {
		sent[r.Key] = r.Count
	}

	stagesByAutomation := map[string]veridianNodeStages{}
	seqIndex := map[string]int{}
	listSequences := map[string][]string{}
	for _, a := range raw.Automations {
		ns := veridianSequenceStages(a)
		stagesByAutomation[a.ID] = ns
		st := make([]VeridianProspectionStage, len(ns.stages))
		copy(st, ns.stages)
		if st == nil {
			st = []VeridianProspectionStage{}
		}
		seq := VeridianSequenceStats{
			AutomationID: a.ID, Name: a.Name, Status: a.Status, ListID: a.ListID, Stages: st,
			RepliesHuman: replies[a.ID].Human, RepliesAuto: replies[a.ID].Auto, SentContacts: sent[a.ID],
		}
		seq.ReplyRateHuman = veridianReplyRate(seq.RepliesHuman, seq.SentContacts)
		seqIndex[a.ID] = len(out.Sequences)
		out.Sequences = append(out.Sequences, seq)
		if a.ListID != "" {
			listSequences[a.ListID] = append(listSequences[a.ListID], a.ID)
		}
	}

	for _, row := range raw.Progress {
		idx, ok := seqIndex[row.AutomationID]
		if !ok {
			continue
		}
		seq := &out.Sequences[idx]
		ns := stagesByAutomation[row.AutomationID]
		seq.Enrolled += row.Count

		// Etape du dernier mail PARTI. « completed » est posé quand le mail est mis en
		// file : tant que le contact est « en cours d'envoi » (sending), ou qu'il est sorti
		// (exclusion, pré-filtre) sur le nœud de ce même mail, ce mail n'est PAS parti et
		// l'étape de ce nœud n'est pas atteinte.
		sentStage := ns.nodeStage[row.LastEmailNodeID]
		queuedStage := 0
		if cs := ns.nodeStage[row.CurrentNodeID]; cs > 0 {
			sending := row.Status == string(ContactAutomationStatusSending)
			exitedOnQueuedMail := row.Status == string(ContactAutomationStatusExited) && row.CurrentNodeID == row.LastEmailNodeID
			if sending || exitedOnQueuedMail {
				sentStage = cs - 1
				if sending {
					queuedStage = cs
				}
			}
		}
		for s := 1; s <= sentStage && s <= len(seq.Stages); s++ {
			seq.Stages[s-1].Sent += row.Count
		}
		if queuedStage > 0 && queuedStage <= len(seq.Stages) {
			seq.Stages[queuedStage-1].Queued += row.Count
		}

		switch row.Status {
		case string(ContactAutomationStatusActive):
			if sentStage < len(seq.Stages) {
				seq.Stages[sentStage].Waiting += row.Count // l'étape suivante est l'indice sentStage
			}
		case string(ContactAutomationStatusCompleted):
			seq.Completed += row.Count
		case string(ContactAutomationStatusFailed):
			seq.Failed += row.Count
		case string(ContactAutomationStatusExited):
			seq.Exits.Total += row.Count
			switch VeridianClassifyExitReason(row.ExitReason) {
			case VeridianExitClassReplied:
				seq.Exits.Replied += row.Count
			case VeridianExitClassRejected:
				seq.Exits.Rejected += row.Count
			case VeridianExitClassUnsubscribed:
				seq.Exits.Unsubscribed += row.Count
			case VeridianExitClassExcluded:
				seq.Exits.Excluded += row.Count
			default:
				seq.Exits.Other += row.Count
			}
			if sentStage >= 1 && sentStage <= len(seq.Stages) {
				seq.Stages[sentStage-1].ExitedAfter += row.Count
			} else {
				seq.Exits.BeforeFirstMail += row.Count
			}
		}
		if queuedStage == 1 {
			out.Totals.QueuedFirst += row.Count
		}
	}

	listReplies := map[string]VeridianProspectionReplyRow{}
	for _, r := range raw.ListReplies {
		listReplies[r.Key] = r
	}
	listSent := map[string]int{}
	for _, r := range raw.ListSent {
		listSent[r.Key] = r.Count
	}
	for _, l := range raw.Lists {
		seqs := listSequences[l.ID]
		// Une liste sans contact actif et sans séquence est une liste de suppression
		// (désinscrits, rejets) : pas un segment de prospection.
		if l.Active == 0 && len(seqs) == 0 {
			continue
		}
		if seqs == nil {
			seqs = []string{}
		}
		seg := VeridianSegmentStats{
			ListID: l.ID, Name: l.Name, SequenceIDs: seqs,
			Active: l.Active, Bounced: l.Bounced, Unsubscribed: l.Unsubscribed, Complained: l.Complained,
			NeverContacted: l.NeverContacted,
			RepliesHuman:   listReplies[l.ID].Human, RepliesAuto: listReplies[l.ID].Auto, SentContacts: listSent[l.ID],
		}
		seg.ReplyRateHuman = veridianReplyRate(seg.RepliesHuman, seg.SentContacts)
		out.Segments = append(out.Segments, seg)
		out.Totals.StockRemaining += l.NeverContacted
	}
	return out
}
