package queue

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
)

// Veridian fork — lot 2 « vérité d'un profil » (08/10/2026).
//
// VeridianEffectivePlan est LA fonction qui dit ce que le worker appliquera à un
// profil d'envoi maintenant : plafond du jour et porte qui le fixe, ce qu'il
// reste, état du fusible de réputation par couple (domaine émetteur x classe),
// fenêtre d'envoi, classes exclues, pause. Elle est appelée par l'API
// emailProfiles.overview (console, CLI), et elle ne recalcule RIEN elle-même :
//
//   - les plafonds viennent de veridianResolveCapLimits, la résolution que lisent
//     aussi la porte de plafond journalier et la réservation atomique du worker ;
//   - le débit par classe vient de veridianEffectiveClassRate (porte de débit) ;
//   - la fenêtre vient de veridianResolveSendingWindow (porte de fenêtre) ;
//   - le facteur et l'arrêt d'un couple viennent de VeridianComputeReputationStatus
//     (qui rejoue veridianEvaluateCouple, le calcul du fusible) ;
//   - la pause est le test de veridianSelectSendableIntegration.
//
// Le test veridian_effective_plan_test.go rejoue les décisions réelles du worker
// (veridianSelectSendableIntegration + réservation) sur les mêmes compteurs et
// exige l'égalité avec ce que dit le plan. Aucun I/O ici : les compteurs du jour
// et l'état de réputation sont passés en entrée (VeridianPlanInput).

// VeridianPlanInput regroupe tout ce que le calcul lit.
type VeridianPlanInput struct {
	Workspace     *domain.Workspace
	IntegrationID string
	Now           time.Time
	Observed      domain.VeridianPlanObserved
	// Reputation : état du fusible par domaine émetteur (VeridianComputeReputationStatus).
	// Absent pour un domaine = aucun envoi observé, couples sains.
	Reputation map[string]VeridianReputationStatus
}

// VeridianEffectivePlan calcule la vérité d'un profil. Un profil introuvable ou
// qui n'est pas une intégration email rend un plan non applicable.
func VeridianEffectivePlan(in VeridianPlanInput) domain.VeridianEffectivePlan {
	now := in.Now.UTC()
	plan := domain.VeridianEffectivePlan{
		Date:                 now.Format("2006-01-02"),
		Gates:                []domain.VeridianPlanGate{},
		ExcludedClasses:      []string{},
		SenderDomains:        []string{},
		Classes:              []domain.VeridianPlanClass{},
		BlockedBy:            []string{},
		DomainSlowdownFactor: 1,
		Mode:                 domain.VeridianProfileUsageUnassigned,
		ClassCapsSource:      "none",
		ClassRatesSource:     "none",
		Window:               domain.VeridianPlanWindow{Source: "none", OpenNow: true},
	}
	ws := in.Workspace
	integration := ws.GetIntegrationByID(in.IntegrationID)
	if integration == nil || integration.Type != domain.IntegrationTypeEmail {
		return plan
	}
	provider := &integration.EmailProvider
	plan.Mode = ws.VeridianProfileUsageOf(in.IntegrationID)
	plan.Paused = provider.VeridianPaused
	plan.NativeRatePerMin = provider.VeridianEffectiveRateLimit()
	plan.ProfileDailyCap = provider.VeridianEffectiveProfileDailyCap()

	senders, domains := veridianPlanSenders(provider)
	plan.SenderDomains = domains
	primaryDomain := ""
	if sender := provider.GetSender(""); sender != nil {
		primaryDomain = veridianEmailDomain(sender.Email)
	}
	if primaryDomain == "" && len(domains) > 0 {
		primaryDomain = domains[0]
	}

	senderSentTotal := 0
	for _, email := range senders {
		senderSentTotal += in.Observed.SenderSent[email]
	}
	plan.SentToday = in.Observed.ProfileAccepted
	plan.ReservedToday = in.Observed.ProfileReserved

	if plan.Mode != domain.VeridianProfileUsageCommercial {
		// Transactionnel (ou non assigné) : le worker ne passe aucune porte
		// commerciale pour ce profil. On ne montre que son volume du jour.
		if senderSentTotal > plan.SentToday {
			plan.SentToday = senderSentTotal
		}
		plan.Applicable = false
		veridianPlanNoGates(&plan)
		return plan
	}
	plan.Applicable = true

	entry := &domain.EmailQueueEntry{}

	// --- fusible de réputation (domaine émetteur principal) ---
	rep, haveRep := in.Reputation[primaryDomain]
	if haveRep {
		plan.Complaints7d = rep.Complaints7d
		plan.ReputationAlert = rep.Alert
		if rep.DomainFactor > 1 {
			plan.DomainSlowdownFactor = rep.DomainFactor
		}
	}
	classStatus := map[string]domain.VeridianReputationClassStatus{}
	if haveRep {
		for _, c := range rep.Classes {
			classStatus[c.Class] = c
		}
	}
	factorOf := func(class string) int {
		if cs, ok := classStatus[class]; ok && cs.Factor > 1 {
			return cs.Factor
		}
		return plan.DomainSlowdownFactor
	}

	// --- tables par classe et leurs origines ---
	rates := veridianResolveProviderClassRates(ws, provider, entry)
	switch {
	case len(provider.VeridianProviderClassRates) > 0:
		plan.ClassRatesSource = "profile"
	case len(ws.Settings.VeridianProviderClassRates) > 0:
		plan.ClassRatesSource = "workspace"
	}
	switch {
	case len(provider.VeridianProviderClassDailyCap) > 0:
		plan.ClassCapsSource = "profile"
	case len(ws.Settings.VeridianProviderClassDailyCap) > 0:
		plan.ClassCapsSource = "workspace"
	}
	excluded := domain.VeridianResolveExcludedClasses(ws, provider, entry)
	for class := range excluded {
		plan.ExcludedClasses = append(plan.ExcludedClasses, class)
	}
	sort.Strings(plan.ExcludedClasses)

	// --- plafonds hors classe (identiques pour toutes les classes) ---
	base := veridianResolveCapLimits(ws, provider, entry, nil, nil, now)
	plan.PerRecipientDailyCap = base.PerRecipient
	plan.PerSenderDailyCap = base.PerSender

	step, of := domain.VeridianWarmupStep(provider.VeridianWarmupStartedAt, provider.VeridianWarmupSchedule, provider.VeridianWarmupStepDays, now)
	plan.Warmup = domain.VeridianPlanWarmup{Active: base.Warmup > 0, Day: step, Of: of, CapToday: base.Warmup, Started: provider.VeridianWarmupStartedAt}

	// --- fenêtre d'envoi ---
	if window := veridianResolveSendingWindow(ws, provider, entry); window != nil {
		plan.Window.Configured = true
		plan.Window.Days = window.Days
		plan.Window.StartHour, plan.Window.EndHour = window.StartHour, window.EndHour
		plan.Window.Timezone = window.Timezone
		if plan.Window.Timezone == "" {
			plan.Window.Timezone = ws.Settings.Timezone
		}
		plan.Window.Source = "workspace"
		if provider.VeridianSendingWindow.IsValid() {
			plan.Window.Source = "profile"
		}
		plan.Window.OpenNow = window.IsWithinWindow(now, ws.Settings.Timezone)
		if !plan.Window.OpenNow {
			next := window.NextOpening(now, ws.Settings.Timezone).UTC()
			plan.Window.NextOpenAt = &next
		}
	}

	// --- classes ---
	domainSent := in.Observed.DomainSent[primaryDomain]
	if r := in.Observed.DomainReserved[primaryDomain]; r > domainSent {
		domainSent = r // compteur atomique de chauffe, plus haut si résultat SMTP ambigu
	}
	profileUsed := in.Observed.ProfileReserved
	if in.Observed.ProfileAccepted > profileUsed {
		profileUsed = in.Observed.ProfileAccepted
	}
	perSenderUsed, perSenderRemaining := 0, 0
	if base.PerSender > 0 {
		for _, email := range senders {
			sent := in.Observed.SenderSent[email]
			perSenderUsed += sent
			if left := base.PerSender - sent; left > 0 {
				perSenderRemaining += left
			}
		}
	}

	profileBlocks := []string{}
	if plan.Paused {
		profileBlocks = append(profileBlocks, domain.VeridianPlanBlockPaused)
	}
	if !veridianPlanInRotation(ws, in.IntegrationID) {
		if provider.VeridianTransportVerifiedAt == nil {
			profileBlocks = append(profileBlocks, domain.VeridianPlanBlockUnverified)
		} else {
			profileBlocks = append(profileBlocks, domain.VeridianPlanBlockNotInRotation)
		}
	}
	if plan.Window.Configured && !plan.Window.OpenNow {
		profileBlocks = append(profileBlocks, domain.VeridianPlanBlockWindowClosed)
	}
	plan.BlockedBy = append(plan.BlockedBy, profileBlocks...)

	allClassesCapped := true
	classCapTotal, classUsedTotal, classRemainingTotal := 0, 0, 0
	for _, class := range domain.VeridianAllProviderClasses() {
		lim := veridianResolveCapLimits(ws, provider, entry, func() string { return class }, factorOf, now)
		pc := domain.VeridianPlanClass{
			Class:          class,
			Excluded:       excluded[class],
			RateConfigured: rates[class],
			RatePerMin:     veridianEffectiveClassRate(rates, class, factorOf(class)),
			Factor:         factorOf(class),
		}
		if cs, ok := classStatus[class]; ok {
			pc.Reason, pc.Stopped = cs.Reason, cs.Stopped
		} else if pc.Factor > 1 {
			pc.Reason = "complaint"
		}
		classSent := in.Observed.DomainClassSent[primaryDomain][class]
		if r := in.Observed.DomainClassReserved[primaryDomain][class]; r > classSent {
			classSent = r
		}
		pc.SentToday = classSent

		var remaining *int
		lower := func(v int) {
			if v < 0 {
				v = 0
			}
			if remaining == nil || v < *remaining {
				remaining = &v
			}
		}
		if lim.ClassBase > 0 {
			capEff, capCfg := lim.ClassCap, lim.ClassBase
			pc.DailyCap, pc.DailyCapConfigured = &capEff, &capCfg
			lower(capEff - classSent)
			classCapTotal += capEff
			classUsedTotal += classSent
			if left := capEff - classSent; left > 0 {
				classRemainingTotal += left
			}
		} else {
			allClassesCapped = false
		}
		if lim.Warmup > 0 {
			lower(lim.Warmup - domainSent)
		}
		if lim.Profile > 0 {
			lower(lim.Profile - profileUsed)
		}
		if base.PerSender > 0 && len(senders) > 0 {
			lower(perSenderRemaining)
		}
		pc.Remaining = remaining

		// Raison de blocage dans l'ordre des portes du worker.
		blocked := ""
		switch {
		case len(profileBlocks) > 0:
			blocked = profileBlocks[0]
		case pc.Excluded:
			blocked = domain.VeridianPlanBlockExcludedClass
		case pc.Stopped:
			blocked = domain.VeridianPlanBlockReputationStopped
		case lim.Warmup > 0 && domainSent >= lim.Warmup:
			blocked = domain.VeridianPlanBlockWarmup
		case lim.ClassBase > 0 && classSent >= lim.ClassCap:
			blocked = domain.VeridianPlanBlockClassCap
		case base.PerSender > 0 && len(senders) > 0 && perSenderRemaining <= 0:
			blocked = domain.VeridianPlanBlockPerSender
		case lim.Profile > 0 && profileUsed >= lim.Profile:
			blocked = domain.VeridianPlanBlockProfileCap
		}
		pc.BlockedBy = blocked
		pc.SendableNow = blocked == ""
		if pc.SendableNow {
			plan.SendableNow = true
		}
		plan.Classes = append(plan.Classes, pc)
	}

	// --- portes de plafond du jour (profil entier) ---
	addGate := func(name string, capacity, used, remaining int, detail string) {
		if remaining < 0 {
			remaining = 0
		}
		plan.Gates = append(plan.Gates, domain.VeridianPlanGate{Name: name, Cap: capacity, Used: used, Remaining: remaining, Detail: detail})
	}
	if base.Warmup > 0 {
		addGate(domain.VeridianPlanGateWarmup, base.Warmup, domainSent, base.Warmup-domainSent, "domaine "+primaryDomain+", toutes classes")
	}
	if base.Profile > 0 {
		addGate(domain.VeridianPlanGateProfileCap, base.Profile, profileUsed, base.Profile-profileUsed, "profil entier")
	}
	if base.PerSender > 0 && len(senders) > 0 {
		addGate(domain.VeridianPlanGatePerSender, base.PerSender*len(senders), perSenderUsed, perSenderRemaining,
			"par adresse émettrice, "+strconv.Itoa(len(senders))+" adresse(s)")
	}
	if allClassesCapped && classCapTotal > 0 {
		addGate(domain.VeridianPlanGateClassCap, classCapTotal, classUsedTotal, classRemainingTotal, "somme des plafonds des classes")
	}

	plan.LimitingGate = domain.VeridianPlanGateNone
	plan.RemainingGate = domain.VeridianPlanGateNone
	for i := range plan.Gates {
		g := plan.Gates[i]
		if plan.DailyCapToday == nil || g.Cap < *plan.DailyCapToday {
			v := g.Cap
			plan.DailyCapToday = &v
			plan.LimitingGate = g.Name
			plan.LimitingDetail = g.Detail
		}
		if plan.RemainingToday == nil || g.Remaining < *plan.RemainingToday {
			v := g.Remaining
			plan.RemainingToday = &v
			plan.RemainingGate = g.Name
		}
	}
	return plan
}

// veridianPlanNoGates fixe les valeurs « aucune porte » d'un plan non applicable.
func veridianPlanNoGates(p *domain.VeridianEffectivePlan) {
	p.LimitingGate = domain.VeridianPlanGateNone
	p.RemainingGate = domain.VeridianPlanGateNone
}

// veridianPlanSenders rend les adresses émettrices (minuscules, sans doublon) et
// leurs domaines, dans l'ordre de déclaration.
func veridianPlanSenders(provider *domain.EmailProvider) (emails []string, domains []string) {
	seenEmail := map[string]bool{}
	seenDomain := map[string]bool{}
	for _, s := range provider.Senders {
		email := strings.ToLower(strings.TrimSpace(s.Email))
		if email == "" || seenEmail[email] {
			continue
		}
		seenEmail[email] = true
		emails = append(emails, email)
		if d := veridianEmailDomain(email); d != "" && !seenDomain[d] {
			seenDomain[d] = true
			domains = append(domains, d)
		}
	}
	return emails, domains
}

// veridianPlanInRotation : le profil est-il dans le pool résolu du worker ?
func veridianPlanInRotation(ws *domain.Workspace, integrationID string) bool {
	for _, p := range ws.VeridianMarketingEmailProfiles() {
		if p.IntegrationID == integrationID {
			return true
		}
	}
	return false
}
