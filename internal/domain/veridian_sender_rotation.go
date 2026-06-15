package domain

import (
	"sort"
	"sync"
)

// Veridian fork — ROTATION DES SENDERS (multi-SMTP) cold outbound.
//
// Une infra d'envoi (EmailProvider) porte plusieurs senders (les 3 boîtes
// agences-veridian.fr p.ex.). Upstream, le sender est figé : GetSender retourne
// celui du template (SenderID) sinon le premier IsDefault → tous les mails
// partent du même expéditeur. En cold, on veut au contraire RÉPARTIR la charge
// sur tous les senders pour :
//   - aligner la capacité d'envoi globale sur le NOMBRE de senders (3 boîtes à
//     X/min ≈ 3·X/min agrégé) — cf. VeridianEffectiveRateLimit ;
//   - préserver la réputation IP/domaine en ne martelant pas le même couple
//     (sender → classe de provider destinataire). Le round-robin est keyé PAR
//     CLASSE : chaque classe (google/microsoft/…) a son propre curseur, donc les
//     envois vers gmail tournent sur S1→S2→S3→S1 indépendamment de ceux vers
//     outlook. On évite ainsi qu'un sender soit sur-représenté auprès d'un
//     provider donné.
//
// Le sélecteur est :
//   - DÉTERMINISTE-testable : pour une séquence d'appels donnée et un ordre de
//     senders stable, la suite des senders choisis est reproductible (curseur
//     entier incrémental modulo N) ;
//   - THREAD-SAFE : enqueue concurrent multi-goroutine (batch broadcast) ;
//   - état MINIMAL en mémoire (best-effort) : les curseurs vivent dans le
//     process d'enqueue. Un redémarrage repart de zéro — sans impact (la
//     répartition reste équilibrée sur la durée).
//
// OPT-IN strict : la rotation ne s'active que si l'infra a STRICTEMENT plus d'un
// sender ET qu'on est en contexte cold (rotation demandée par l'appelant, qui
// l'active sur les chemins broadcast cold). Sinon, comportement upstream
// (GetSender figé) strictement inchangé. La rotation respecte par ailleurs le
// SenderID explicite d'un template : voir VeridianSelectSender ci-dessous.

// VeridianSenderRotator répartit les senders d'une infra en round-robin par
// classe de provider destinataire. Sûr en concurrence. Le zéro-value est prêt à
// l'emploi ; préférer NewVeridianSenderRotator pour la lisibilité.
type VeridianSenderRotator struct {
	mu sync.Mutex
	// cursors[integrationID|class] = prochain index à servir pour ce couple.
	cursors map[string]int
}

// NewVeridianSenderRotator construit un rotator vide.
func NewVeridianSenderRotator() *VeridianSenderRotator {
	return &VeridianSenderRotator{cursors: make(map[string]int)}
}

func veridianSenderRotationKey(integrationID, class string) string {
	return integrationID + "|" + class
}

// next renvoie l'index round-robin suivant pour (integrationID, class) sur n
// senders, et avance le curseur. n doit être > 0.
func (r *VeridianSenderRotator) next(integrationID, class string, n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cursors == nil {
		r.cursors = make(map[string]int)
	}
	key := veridianSenderRotationKey(integrationID, class)
	idx := r.cursors[key] % n
	r.cursors[key] = (r.cursors[key] + 1) % n
	return idx
}

// veridianEligibleSenders retourne les senders éligibles à la rotation, dans un
// ordre STABLE (tri par ID) pour un round-robin déterministe quel que soit
// l'ordre de persistance JSON. Tous les senders valides (email non vide)
// participent.
func veridianEligibleSenders(senders []EmailSender) []EmailSender {
	out := make([]EmailSender, 0, len(senders))
	for _, s := range senders {
		if s.Email != "" {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// VeridianSelectSender choisit le sender à utiliser pour un envoi cold, en
// round-robin par classe de provider destinataire.
//
// Précédence (la rotation NE casse PAS le contrat upstream) :
//  1. Si templateSenderID est non vide ET correspond à un sender de l'infra →
//     ce sender est RETOURNÉ TEL QUEL (un template qui force explicitement son
//     expéditeur garde la main ; la rotation ne le contredit pas).
//  2. Sinon, si l'infra a > 1 sender → round-robin par classe (rotator).
//  3. Sinon (un seul sender, ou aucun éligible) → fallback GetSender upstream
//     (sender IsDefault).
//
// rotator peut être nil (contexte non-cold / rotation désactivée) → on retombe
// directement sur GetSender, comportement upstream strictement inchangé.
// Retourne nil si l'infra n'a aucun sender utilisable (l'appelant gère l'erreur
// comme avec GetSender).
func (e *EmailProvider) VeridianSelectSender(rotator *VeridianSenderRotator, integrationID, templateSenderID, recipientClass string) *EmailSender {
	// 1. SenderID explicite du template : respecté tel quel (pas de rotation).
	if templateSenderID != "" {
		if s := e.GetSender(templateSenderID); s != nil && s.ID == templateSenderID {
			return s
		}
	}

	// 2. Round-robin par classe si rotation active et plusieurs senders.
	if rotator != nil {
		eligible := veridianEligibleSenders(e.Senders)
		if len(eligible) > 1 {
			idx := rotator.next(integrationID, recipientClass, len(eligible))
			return &eligible[idx]
		}
	}

	// 3. Fallback upstream (sender par défaut ou template par défaut).
	return e.GetSender(templateSenderID)
}

// VeridianIsColdContext détecte si l'envoi se fait dans le tunnel cold outreach,
// à partir des MÊMES signaux que le resolver pixel (un seul suffit) : tag contact
// custom_string_5, OU config cold posée sur le broadcast (rates/caps/window/pixel
// dans metadata), OU config cold au niveau workspace (settings). Hors contexte
// cold → la rotation des senders reste DÉSACTIVÉE (comportement upstream :
// sender figé par GetSender). Centralise la détection pour éviter de la dupliquer
// entre le resolver pixel et la rotation des senders.
func VeridianIsColdContext(contact *Contact, broadcast *Broadcast, workspace *Workspace) bool {
	if VeridianContactProviderClass(contact) != "" {
		return true
	}
	if broadcast != nil {
		md := broadcast.Metadata
		if len(VeridianProviderClassRatesFromMetadata(md)) > 0 ||
			len(VeridianProviderClassDailyCapFromMetadata(md)) > 0 ||
			VeridianPerRecipientDailyCapFromMetadata(md) > 0 ||
			VeridianSendingWindowFromMetadata(md) != nil ||
			len(VeridianOpenPixelByClassFromMetadata(md)) > 0 {
			return true
		}
	}
	if workspace != nil {
		s := workspace.Settings
		if len(s.VeridianProviderClassRates) > 0 ||
			len(s.VeridianProviderClassDailyCap) > 0 ||
			s.VeridianPerRecipientDailyCap > 0 ||
			s.VeridianSendingWindow.IsValid() ||
			len(s.VeridianOpenPixelByClass) > 0 {
			return true
		}
	}
	return false
}

// VeridianActiveSenderCount retourne le nombre de senders utilisables (email non
// vide) de l'infra. Sert à aligner la capacité d'envoi globale sur le nombre de
// boîtes disponibles (cf. VeridianEffectiveRateLimit).
func (e *EmailProvider) VeridianActiveSenderCount() int {
	n := 0
	for _, s := range e.Senders {
		if s.Email != "" {
			n++
		}
	}
	return n
}

// VeridianEffectiveRateLimit aligne le débit global de l'infra sur son nombre de
// senders : un débit RateLimitPerMinute configuré PAR SENDER devient
// RateLimitPerMinute · N au niveau infra, puisque N boîtes distinctes envoient
// en parallèle vers des destinataires différents (la pression par sender reste
// celle configurée). Avec 0 ou 1 sender, retourne RateLimitPerMinute inchangé
// (non-régression). N'altère PAS RateLimitPerMinute stocké : c'est un calcul à
// la lecture, l'appelant l'utilise comme plafond agrégé sans toucher la config.
func (e *EmailProvider) VeridianEffectiveRateLimit() int {
	n := e.VeridianActiveSenderCount()
	if n <= 1 {
		return e.RateLimitPerMinute
	}
	return e.RateLimitPerMinute * n
}
