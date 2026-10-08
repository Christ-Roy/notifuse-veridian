package queue

import (
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork (correctif 2026-10-05, incident robertbrunon 05/10) — FAILOVER
// de pool à l'ENVOI.
//
// Constat : le workspace robertbrunon porte deux intégrations SMTP dans son
// pool de rotation (veridian_marketing_email_provider_ids), mais l'intégration
// d'un message est figée à la MISE EN FILE (nœud email de l'automation /
// veridianResolveEmailProfile pour les broadcasts) et plus jamais reconsidérée.
// Quand cette intégration atteint un plafond (chauffe, profil, classe, débit)
// ou est indisponible (circuit breaker ouvert), le worker reportait l'entrée
// au lendemain SANS jamais regarder si une autre intégration du pool avait de
// la marge pour CE destinataire et SA classe — 3 777 messages reportés alors
// qu'une seconde infra, vérifiée, n'envoyait rien. Le pool ne servait à rien.
//
// Correctif : à l'ENVOI (processEntry), le worker construit la liste ordonnée
// des intégrations du pool éligibles pour cette entrée (veridianBuildFailover
// Candidates) et essaie chacune, dans l'ordre, avec EXACTEMENT les mêmes
// gates qu'avant (exclusion de classe, réputation, débit par classe, plafond
// journalier, plafond émetteur, fenêtre d'envoi) — seulement rejoués pour
// CHAQUE candidat plutôt que pour la seule intégration assignée. La première
// intégration qui passe tous les gates gagne : l'entrée est réécrite
// (IntegrationID, From/FromName — donc Message-ID et DKIM, qui dérivent tous
// deux du domaine d'envoi via le relais de CETTE intégration) puis la réserve
// atomique de quota (veridianReserveDailyQuota, inchangée) et l'envoi SMTP
// continuent normalement. Report au lendemain UNIQUEMENT si AUCUN membre du
// pool n'a de marge (cf. veridianSelectSendableIntegration).
//
// Règles préservées à l'identique : le plafond PAR DESTINATAIRE reste
// workspace-global (veridianDailyCapGate le vérifie pour chaque candidat avec
// le même COUNT, donc la même réponse) ; une exclusion de classe n'autorise le
// passage que si AU MOINS UN candidat ne l'exclut pas (permanent seulement si
// TOUS excluent) ; la sortie sur réponse et l'anti-empreinte restent des
// contrôles recipient-level, non touchés par le choix d'intégration.
//
// Continuité de séquence (automations) : une relance (J+4, J+10...) garde de
// préférence le même expéditeur que le premier envoi (J0) à ce contact dans
// la même automation — cf. veridianSequenceAnchor. Elle ne bascule de domaine
// que si ce premier expéditeur est resté silencieux plus de 48h
// (veridianSequenceAnchorUnavailableAfter) : un plafond atteint AUJOURD'HUI ne
// suffit pas à changer de domaine pour une relance, l'entrée est simplement
// reportée (cf. veridianFailoverCandidateIDs, anchorFound && anchorAvailable
// → liste à un seul élément, pas de repli).

// veridianSequenceAnchorUnavailableAfter borne le silence toléré d'un
// expéditeur de séquence avant qu'une relance soit autorisée à changer de
// domaine. 48h : largement plus qu'un simple plafond journalier (qui se lève
// au prochain minuit), mais assez court pour ne pas laisser une séquence
// bloquée des jours sur une infra réellement morte.
const veridianSequenceAnchorUnavailableAfter = 48 * time.Hour

// veridianSequenceAnchorLookupLimit borne l'historique du contact scanné pour
// retrouver le premier envoi de l'automation courante. Une séquence cold a au
// plus quelques étages (J0/J4/J10...) : 100 est une marge large pour un coût
// de requête borné.
const veridianSequenceAnchorLookupLimit = 100

// veridianFailoverCandidate est un membre du pool prêt à être essayé pour
// cette entrée : son intégration, son EmailProvider, et l'expéditeur par
// défaut de cette infra (From réécrit si ce candidat gagne). La rotation
// d'expéditeurs CONTACT PAR CONTACT à l'intérieur d'une même intégration
// (veridianResolveSender, package broadcast) reste inchangée : ce fork ne
// bascule qu'ENTRE intégrations, avec le sender par défaut de la cible.
type veridianFailoverCandidate struct {
	IntegrationID string
	Provider      *domain.EmailProvider
	FromAddress   string
	FromName      string
}

// veridianCandidateFromIntegration construit un candidat depuis une
// intégration résolue (pool ou intégration assignée d'origine).
//
// fallbackFrom/fallbackName, quand non vides, priment sur le sender par
// défaut de l'infra : c'est le cas de l'intégration ASSIGNÉE à l'enqueue,
// dont Payload.FromAddress porte déjà le sender réellement résolu à ce
// moment-là (rotation d'expéditeurs contact par contact pour les broadcasts,
// cf. veridianResolveSender dans le package broadcast — ce fork ne doit pas
// l'écraser par un autre sender "par défaut" de la MÊME infra). Un sibling du
// pool, lui, n'a jamais été résolu : on prend son sender par défaut
// (fallback vide). Sans sender exploitable d'aucune façon (infra mal
// configurée), FromAddress reste vide : le candidat est alors ignoré par
// veridianSelectSendableIntegration (jamais envoyé sans adresse d'expédition
// connue, non-régression).
func veridianCandidateFromIntegration(integration *domain.Integration, fallbackFrom, fallbackName string) veridianFailoverCandidate {
	if integration == nil {
		return veridianFailoverCandidate{}
	}
	cand := veridianFailoverCandidate{
		IntegrationID: integration.ID,
		Provider:      &integration.EmailProvider,
	}
	if fallbackFrom != "" {
		cand.FromAddress, cand.FromName = fallbackFrom, fallbackName
		return cand
	}
	if sender := integration.EmailProvider.GetSender(""); sender != nil {
		cand.FromAddress = sender.Email
		cand.FromName = sender.Name
	}
	return cand
}

// veridianFailoverCandidateIDs ordonne les IDs d'intégration du pool pour UNE
// tentative d'envoi. Fonction PURE (zéro I/O), directement testable — cf.
// veridian_pool_failover_test.go pour les deux cas de continuité de séquence
// exigés par la mission 2026-10-05.
//
//   - anchorFound && anchorAvailable : la continuité prime sur le débit — un
//     seul candidat (l'ancre), jamais de repli. Un plafond atteint aujourd'hui
//     reporte l'entrée, il ne fait pas changer de domaine.
//   - anchorFound && !anchorAvailable : l'ancre est restée silencieuse plus de
//     48h, le failover s'ouvre, mais l'ancre morte reste essayée EN DERNIER
//     (au cas où c'est la seule option).
//   - !anchorFound (premier envoi d'une séquence, ou broadcast) : le pool dans
//     son ordre configuré, l'intégration assignée à l'enqueue en premier (le
//     chemin actuel reste le défaut, le failover n'intervient que si elle n'a
//     pas de marge).
func veridianFailoverCandidateIDs(poolIDs []string, assignedID, anchorID string, anchorFound, anchorAvailable bool) []string {
	if anchorFound && anchorAvailable {
		return []string{anchorID}
	}

	exclude := ""
	if anchorFound && !anchorAvailable {
		exclude = anchorID
	}

	ordered := make([]string, 0, len(poolIDs)+1)
	seen := make(map[string]bool, len(poolIDs)+1)
	if assignedID != "" && assignedID != exclude {
		ordered = append(ordered, assignedID)
		seen[assignedID] = true
	}
	for _, id := range poolIDs {
		if id == "" || id == exclude || seen[id] {
			continue
		}
		ordered = append(ordered, id)
		seen[id] = true
	}
	if exclude != "" {
		ordered = append(ordered, exclude) // dernier recours
	}
	return ordered
}

// veridianPoolProfileIDs extrait les IDs du pool résolu (ordre déclaré,
// doublons/IDs invalides déjà filtrés par VeridianMarketingEmailProfiles).
func veridianPoolProfileIDs(profiles []domain.VeridianEmailProfile) []string {
	ids := make([]string, 0, len(profiles))
	for _, p := range profiles {
		ids = append(ids, p.IntegrationID)
	}
	return ids
}

// veridianSequenceAnchor retrouve l'intégration et le domaine émetteur du
// PREMIER envoi réussi de l'automation courante vers ce contact — le J0
// qu'une relance (J+4, J+10...) doit préférer. found=false pour un broadcast,
// pour le premier envoi lui-même (rien d'antérieur), ou sur toute erreur de
// lecture (best-effort : ne bloque jamais un envoi).
func (w *EmailQueueWorker) veridianSequenceAnchor(workspace *domain.Workspace, entry *domain.EmailQueueEntry) (integrationID, senderDomain string, found bool) {
	if entry == nil || workspace == nil {
		return "", "", false
	}
	if entry.SourceType != domain.EmailQueueSourceAutomation || entry.SourceID == "" || entry.ContactEmail == "" {
		return "", "", false
	}
	if w.messageHistoryRepo == nil {
		return "", "", false
	}

	history, _, err := w.messageHistoryRepo.GetByContact(w.ctx, workspace.ID, workspace.Settings.SecretKey, entry.ContactEmail, veridianSequenceAnchorLookupLimit, 0)
	if err != nil || len(history) == 0 {
		return "", "", false
	}

	var earliest *domain.MessageHistory
	for _, m := range history {
		if m == nil || m.AutomationID == nil || *m.AutomationID != entry.SourceID {
			continue
		}
		if m.SentAt == nil || m.VeridianProfileID == "" || m.ID == entry.MessageID {
			continue
		}
		if earliest == nil || m.SentAt.Before(*earliest.SentAt) {
			earliest = m
		}
	}
	if earliest == nil {
		return "", "", false
	}
	return earliest.VeridianProfileID, veridianEmailDomain(earliest.VeridianSenderEmail), true
}

// veridianAnchorAvailable répond à la question mission : le premier
// expéditeur de la séquence est-il "indisponible depuis plus de 2 jours" ?
// Disponible = au moins un envoi réussi depuis ce domaine dans la fenêtre.
// Domaine non résolvable ou erreur de lecture → disponible par défaut (on ne
// force JAMAIS un changement de domaine sur une mesure ratée).
func (w *EmailQueueWorker) veridianAnchorAvailable(workspaceID, senderDomain string, now time.Time) bool {
	if senderDomain == "" || w.messageHistoryRepo == nil {
		return true
	}
	since := now.Add(-veridianSequenceAnchorUnavailableAfter)
	count, err := w.messageHistoryRepo.CountSentSinceForSenderDomain(w.ctx, workspaceID, senderDomain, since)
	if err != nil {
		return true
	}
	return count > 0
}

// veridianBuildFailoverCandidates résout, pour cette entrée, la liste ordonnée
// des candidats à essayer (cf. veridianFailoverCandidateIDs). Sans pool
// explicite ou singleton configuré pour cette intégration (VeridianMarketing
// EmailProfiles vide), ne renvoie QUE l'intégration assignée à l'origine :
// non-régression stricte pour tout workspace sans rotation.
func (w *EmailQueueWorker) veridianBuildFailoverCandidates(
	workspace *domain.Workspace,
	entry *domain.EmailQueueEntry,
	assigned *domain.Integration,
) []veridianFailoverCandidate {
	assignedFrom, assignedName := entry.Payload.FromAddress, entry.Payload.FromName

	profiles := workspace.VeridianMarketingEmailProfiles()
	if len(profiles) == 0 {
		// Lot 4 : si l'integration assignee est le profil transactionnel reserve, la
		// liste est vide (l'entree attend un profil commercial, elle ne part pas par lui).
		return veridianDropReservedFromCandidates(workspace,
			[]veridianFailoverCandidate{veridianCandidateFromIntegration(assigned, assignedFrom, assignedName)})
	}

	byID := make(map[string]domain.VeridianEmailProfile, len(profiles))
	for _, p := range profiles {
		byID[p.IntegrationID] = p
	}

	anchorID, anchorDomain, anchorFound := w.veridianSequenceAnchor(workspace, entry)
	// Lot 4 : une ancre qui est le profil transactionnel reserve n'ancre rien, la
	// rotation commerciale ne l'emprunte jamais.
	if reserved := workspace.VeridianReservedTransactionalProfileID(); anchorFound && reserved != "" && anchorID == reserved {
		anchorFound = false
	}
	anchorAvailable := anchorFound && w.veridianAnchorAvailable(workspace.ID, anchorDomain, time.Now())
	// Lot 2 : une ancre en pause n'est plus disponible, la relance bascule tout de
	// suite sur un autre profil (une pause est une décision de l'opérateur, pas
	// un plafond du jour).
	if anchorFound && anchorAvailable {
		if anchorIntegration := workspace.GetIntegrationByID(anchorID); anchorIntegration != nil && anchorIntegration.EmailProvider.VeridianPaused {
			anchorAvailable = false
		}
	}

	orderedIDs := veridianFailoverCandidateIDs(veridianPoolProfileIDs(profiles), entry.IntegrationID, anchorID, anchorFound, anchorAvailable)

	candidates := make([]veridianFailoverCandidate, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		fallbackFrom, fallbackName := "", ""
		if id == entry.IntegrationID {
			// The originally assigned integration already has a resolved
			// sender on the payload (possibly contact-level rotation within
			// that infra) — reuse it instead of re-deriving a fresh default.
			fallbackFrom, fallbackName = assignedFrom, assignedName
		}
		if profile, ok := byID[id]; ok {
			cand := veridianFailoverCandidate{IntegrationID: profile.IntegrationID, Provider: profile.Provider}
			if fallbackFrom != "" {
				cand.FromAddress, cand.FromName = fallbackFrom, fallbackName
			} else {
				cand.FromAddress = veridianSenderFromProvider(profile.Provider)
				cand.FromName = veridianSenderNameFromProvider(profile.Provider)
			}
			candidates = append(candidates, cand)
			continue
		}
		// L'ancre (ou l'intégration assignée) est sortie du pool explicite
		// (supprimée, non vérifiée...) : on la résout quand même directement
		// pour lui laisser sa chance avant de retomber sur le pool.
		if integ := workspace.GetIntegrationByID(id); integ != nil && integ.Type == domain.IntegrationTypeEmail && integ.EmailProvider.Kind != "" {
			candidates = append(candidates, veridianCandidateFromIntegration(integ, fallbackFrom, fallbackName))
		}
	}
	return veridianDropReservedFromCandidates(workspace, candidates)
}

func veridianSenderFromProvider(provider *domain.EmailProvider) string {
	if provider == nil {
		return ""
	}
	if sender := provider.GetSender(""); sender != nil {
		return sender.Email
	}
	return ""
}

func veridianSenderNameFromProvider(provider *domain.EmailProvider) string {
	if provider == nil {
		return ""
	}
	if sender := provider.GetSender(""); sender != nil {
		return sender.Name
	}
	return ""
}

// veridianSelectionResult est le verdict de veridianSelectSendableIntegration.
//   - Candidate non-nil : intégration gagnante, entry.Payload.FromAddress/
//     FromName ont déjà été réécrits sur cette valeur (il reste à committer
//     IntegrationID/ProviderKind côté appelant).
//   - Candidate nil, Permanent true : TOUS les candidats joignables excluent
//     la classe de ce destinataire (politique, jamais retenté).
//   - Candidate nil, Permanent false : aucun candidat n'a de marge maintenant
//     ; RetryDelay est le délai le plus court observé (recheck le plus tôt
//     possible plutôt que d'attendre arbitrairement le pire délai du lot).
type veridianSelectionResult struct {
	Candidate     *veridianFailoverCandidate
	Permanent     bool
	ExcludedClass string
	RetryDelay    time.Duration
}

// veridianSelectSendableIntegration essaie, dans l'ordre de priorité résolu
// par veridianBuildFailoverCandidates, chaque candidat avec EXACTEMENT les
// gates déjà existants (exclusion de classe, réputation, débit par classe,
// plafond journalier, plafond émetteur, fenêtre d'envoi) — simplement rejoués
// par candidat au lieu d'une seule fois pour l'intégration assignée. La
// réserve atomique (veridianReserveDailyQuota) reste, elle, appelée UNE SEULE
// fois par l'appelant sur le gagnant : c'est l'autorité finale, ces gates ne
// sont qu'une présélection bon marché (même contrat que documenté dans
// veridian_daily_quota.go).
func (w *EmailQueueWorker) veridianSelectSendableIntegration(
	workspace *domain.Workspace,
	entry *domain.EmailQueueEntry,
	assignedIntegration *domain.Integration,
) veridianSelectionResult {
	candidates := w.veridianBuildFailoverCandidates(workspace, entry, assignedIntegration)

	savedFrom, savedName := entry.Payload.FromAddress, entry.Payload.FromName
	savedIntegrationID := entry.IntegrationID
	defer func() {
		entry.Payload.FromAddress, entry.Payload.FromName = savedFrom, savedName
		entry.IntegrationID = savedIntegrationID
	}()

	reachable := 0
	excludedCount := 0
	anyCircuitOpen := false
	excludedClass := ""
	minDelay := veridianDailyCapRecheckInterval

	for i := range candidates {
		cand := candidates[i]
		if cand.Provider == nil {
			continue
		}
		// Lot 2 (08/10) : profil en PAUSE (veridian_paused). Il ne reçoit rien ;
		// l'entrée bascule sur les autres membres du pool, ou attend (file
		// conservée) s'il n'en reste aucun. Ni « atteignable » (une pause n'est pas
		// une exclusion de classe), ni circuit ouvert. La levée de la pause réveille
		// la file (UpdateIntegration -> WakePendingByIntegration).
		if cand.Provider.VeridianPaused {
			continue
		}
		// Un FromAddress vide est une base de décision valable pour
		// l'intégration ASSIGNÉE à l'origine (comportement pré-fork : de
		// nombreux envois legacy/sans sender connu passaient déjà par les
		// gates avec un From vide, chacun best-effort sur ce cas). Ce n'est
		// PAS le cas pour un SIBLING du pool : y basculer sans adresse
		// d'expédition résolue produirait un From vide sur une infra qu'on
		// vient juste de choisir — jamais une base de décision.
		if cand.IntegrationID != entry.IntegrationID && cand.FromAddress == "" {
			continue
		}
		if w.circuitBreaker.IsOpen(cand.IntegrationID) {
			anyCircuitOpen = true
			continue
		}
		reachable++

		entry.Payload.FromAddress = cand.FromAddress
		entry.Payload.FromName = cand.FromName
		// Correctif 2026-10-06 : les gates qui tiennent un etat PAR INTEGRATION
		// (limiter de debit par classe, cle {integration, classe}) doivent voir
		// le CANDIDAT, pas l'integration assignee a l'enqueue. Sinon le seau de
		// nord est consomme puis relu pour relai, qui parait toujours bride :
		// sous debit etale nord n'atteint jamais son plafond, la bascule ne se
		// declenchait jamais et relai n'envoyait rien. Restaure par le defer ;
		// l'appelant committe IntegrationID sur le gagnant.
		entry.IntegrationID = cand.IntegrationID

		if class, excluded := w.veridianExcludedClassGate(workspace, cand.Provider, entry); excluded {
			excludedCount++
			excludedClass = class
			continue
		}
		if delay, frozen := w.veridianReputationGate(workspace, cand.Provider, entry); frozen {
			minDelay = veridianMinDuration(minDelay, delay)
			continue
		}
		if delay, throttled := w.veridianProviderClassGate(workspace, cand.Provider, entry); throttled {
			minDelay = veridianMinDuration(minDelay, delay)
			continue
		}
		if delay, capped := w.veridianDailyCapGate(workspace, cand.Provider, entry); capped {
			minDelay = veridianMinDuration(minDelay, delay)
			continue
		}
		if delay, capped := w.veridianPerSenderCapGate(workspace, cand.Provider, entry); capped {
			minDelay = veridianMinDuration(minDelay, delay)
			continue
		}
		if delay, closed := w.veridianSendingWindowGate(workspace, cand.Provider, entry); closed {
			minDelay = veridianMinDuration(minDelay, delay)
			continue
		}

		won := cand
		savedFrom, savedName = cand.FromAddress, cand.FromName // commit : le defer réappliquera ces valeurs (no-op)
		return veridianSelectionResult{Candidate: &won}
	}

	if reachable > 0 && excludedCount == reachable {
		return veridianSelectionResult{Permanent: true, ExcludedClass: excludedClass}
	}
	if reachable == 0 && anyCircuitOpen {
		minDelay = veridianMinDuration(minDelay, w.circuitBreaker.GetConfig().CooldownPeriod)
	}
	return veridianSelectionResult{RetryDelay: minDelay}
}

func veridianMinDuration(a, b time.Duration) time.Duration {
	if b < a {
		return b
	}
	return a
}
