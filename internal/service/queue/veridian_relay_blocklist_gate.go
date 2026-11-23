package queue

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian VER-225 (23/11/2026) — porte « relais hors liste noire ».
//
// Le relais nord (51.210.7.44) a été listé Spamhaus ZEN du 26/10 au 06/11, très probablement à
// cause du volume : les envois partaient quand même et se faisaient refuser. Avant d'envoyer du
// COMMERCIAL (séquence, campagne) par un profil SMTP, la porte vérifie que l'IP de son relais n'est
// pas listée, par une requête directe aux serveurs faisant autorité de Spamhaus avec les témoins
// 127.0.0.2 (doit être listé) et 127.0.0.1 (doit être absent). Le vérificateur vit dans
// internal/veridian/relayguard ; sans vérificateur branché, la porte passe (assemblage de test).
//
//   - listée : l'entrée attend, le profil est suspendu (veridian_paused) et l'alerte part, une fois ;
//   - non aboutie (témoins faux, serveurs muets, résolveur refusé) : RIEN ne part, nouvel essai
//     dans relayUnverifiedRetry ;
//   - propre : le verdict vaut relayCleanTTL. La première entrée de chaque ouverture de fenêtre
//     déclenche donc une vérification (la nuit dépasse le délai), puis une par heure.
//
// Le transactionnel n'est jamais retenu (un mot de passe oublié ne doit pas attendre).

const (
	relayCleanTTL        = time.Hour
	relayUnverifiedRetry = 5 * time.Minute
)

// RelayState : résultat d'une vérification de relais.
type RelayState int

const (
	RelayUnverified RelayState = iota // mesure non aboutie : rien ne part
	RelayClean                        // aucune IP du relais n'est listée
	RelayListed                       // au moins une IP est listée
)

// RelayCheck : le verdict d'une vérification, avec ce qu'elle a mesuré.
type RelayCheck struct {
	State  RelayState
	IPs    []string
	Detail string
	// Warning : propre pour l'envoi, mais à signaler (IP en PBL seulement : liste des plages
	// « grand public », qui ne vise pas un relais légitime ; méthode de l'infra, 23/11).
	Warning string
}

// RelayChecker vérifie l'hôte SMTP d'un relais (résolution de ses IP et requête ZEN).
type RelayChecker interface {
	CheckRelay(ctx context.Context, host string) RelayCheck
}

// RelayListedHandler est appelé UNE fois quand un relais passe en liste noire : suspension du
// profil et alerte (internal/veridian/relayguard).
type RelayListedHandler func(workspaceID, integrationID, host string, c RelayCheck)

type relayGuardHolder struct {
	checker  RelayChecker
	onListed RelayListedHandler
	now      func() time.Time
	mu       sync.Mutex
	cache    map[string]relayCacheEntry // hôte -> dernier verdict
	alerted  map[string]bool            // workspace|profil déjà suspendu et signalé
}

type relayCacheEntry struct {
	check RelayCheck
	until time.Time
}

var relayGuard atomic.Pointer[relayGuardHolder]

// SetVeridianRelayChecker branche (ou retire, avec nil) la vérification des relais.
func SetVeridianRelayChecker(c RelayChecker, onListed RelayListedHandler) {
	if c == nil {
		relayGuard.Store(nil)
		return
	}
	relayGuard.Store(&relayGuardHolder{checker: c, onListed: onListed, now: time.Now,
		cache: map[string]relayCacheEntry{}, alerted: map[string]bool{}})
}

// veridianRelayIsCommercial : seules les séquences et les campagnes passent par la porte.
func veridianRelayIsCommercial(entry *domain.EmailQueueEntry) bool {
	return entry != nil && (entry.SourceType == domain.EmailQueueSourceAutomation || entry.SourceType == domain.EmailQueueSourceBroadcast)
}

// veridianRelayBlocklistVerdict : la porte pour un candidat.
func (w *EmailQueueWorker) veridianRelayBlocklistVerdict(workspace *domain.Workspace, cand veridianFailoverCandidate, entry *domain.EmailQueueEntry) veridianGateVerdict {
	h := relayGuard.Load()
	if h == nil || h.checker == nil {
		return veridianSkippedVerdict(domain.VeridianGateRelay, "relay guard not wired")
	}
	if !veridianRelayIsCommercial(entry) {
		return veridianSkippedVerdict(domain.VeridianGateRelay, "transactional")
	}
	p := cand.Provider
	if p == nil || p.Kind != domain.EmailProviderKindSMTP || p.SMTP == nil || strings.TrimSpace(p.SMTP.Host) == "" {
		return veridianSkippedVerdict(domain.VeridianGateRelay, "not an SMTP relay")
	}
	host := strings.ToLower(strings.TrimSpace(p.SMTP.Host))
	ctx := w.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	c, fresh := h.lookup(ctx, host)
	if fresh {
		w.veridianLogRelayCheck(workspace, cand.IntegrationID, host, c)
	}
	limit := "zen.spamhaus.org"
	switch c.State {
	case RelayClean:
		return veridianPassVerdict(domain.VeridianGateRelay, strings.Join(c.IPs, ","), limit, "", "")
	case RelayListed:
		wsID := ""
		if workspace != nil {
			wsID = workspace.ID
		}
		h.signalListed(wsID, cand.IntegrationID, host, c)
		return veridianGateVerdict{Gate: domain.VeridianGateRelay, Verdict: domain.VeridianVerdictBlock,
			Value: strings.Join(c.IPs, ","), Limit: limit, Delay: relayCleanTTL,
			Reason: domain.VeridianReasonRelayBlocklisted, Detail: veridianTrim(c.Detail, 120)}
	default:
		return veridianGateVerdict{Gate: domain.VeridianGateRelay, Verdict: domain.VeridianVerdictBlock,
			Value: host, Limit: limit, Delay: relayUnverifiedRetry,
			Reason: domain.VeridianReasonRelayUnverified, Detail: veridianTrim(c.Detail, 120)}
	}
}

// lookup rend le verdict en cache s'il vaut encore, sinon vérifie (une vérification à la fois).
func (h *relayGuardHolder) lookup(ctx context.Context, host string) (RelayCheck, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if e, ok := h.cache[host]; ok && now.Before(e.until) {
		return e.check, false
	}
	c := h.checker.CheckRelay(ctx, host)
	ttl := relayUnverifiedRetry
	if c.State == RelayClean || c.State == RelayListed {
		ttl = relayCleanTTL
	}
	h.cache[host] = relayCacheEntry{check: c, until: now.Add(ttl)}
	return c, true
}

// signalListed suspend et alerte une seule fois par profil (la pause réveille la suite).
func (h *relayGuardHolder) signalListed(ws, integrationID, host string, c RelayCheck) {
	key := ws + "|" + integrationID
	h.mu.Lock()
	first := !h.alerted[key]
	h.alerted[key] = true
	h.mu.Unlock()
	if first && h.onListed != nil {
		h.onListed(ws, integrationID, host, c)
	}
}

func veridianTrim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// veridianLogRelayCheck journalise une mesure fraîche qui mérite d'être vue (champs stables pour
// une règle d'alerte Loki : service notifuse-zen, gravite, impact). Le relais listé est signalé à
// part, au niveau critique, par le suspendeur (internal/veridian/relayguard).
func (w *EmailQueueWorker) veridianLogRelayCheck(workspace *domain.Workspace, integrationID, host string, c RelayCheck) {
	if w.logger == nil {
		return
	}
	wsID := ""
	if workspace != nil {
		wsID = workspace.ID
	}
	f := map[string]interface{}{"service": "notifuse-zen", "workspace_id": wsID, "integration_id": integrationID,
		"relay_host": host, "relay_ips": strings.Join(c.IPs, ","), "detail": c.Detail}
	switch {
	case c.State == RelayUnverified:
		f["gravite"], f["impact"] = "erreur", "interne"
		w.logger.WithFields(f).Error(RelayUnverifiedMessage)
	case c.State == RelayClean && c.Warning != "":
		f["gravite"], f["impact"], f["warning"] = "avertissement", "interne", c.Warning
		w.logger.WithFields(f).Warn(RelayWarningMessage)
	}
}

// Messages stables (règles d'alerte).
const (
	RelayUnverifiedMessage = "VeridianRelayGuard: relay blocklist check not completed, commercial sending held"
	RelayWarningMessage    = "VeridianRelayGuard: relay IP listed on Spamhaus PBL only (warning, sending continues)"
)
