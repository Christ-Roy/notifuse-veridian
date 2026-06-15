package broadcast

import (
	"context"
	"strconv"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Veridian — DÉDUPLICATION anti-hash identique À L'ENQUEUE (cold outbound). C'est
// ICI que la VARIÉTÉ est garantie : le sender a le template BRUT en main et peut
// re-spinner. Le worker n'est qu'un filet (il ne peut pas re-varier un payload
// figé, cf. veridian_content_hash_gate.go).
//
// Principe : étant donné le rendu final initial (subject + body, déjà Liquid +
// spintax avec seed=email), on calcule son hash et on vérifie via le repo
// message_history s'il a DÉJÀ été envoyé vers la MÊME classe de provider
// destinataire dans la fenêtre glissante. Si oui (collision), on tente une
// RE-SPIN : ré-appliquer spintax avec un seed PERTURBÉ (email+":r1", ":r2", …),
// ce qui — pour un template qui a ≥2 variantes — produit un rendu différent donc
// un hash neuf. On boucle jusqu'à N tentatives bornées. Si après N le hash reste
// connu (template SANS spintax = 1 seule variante possible), on laisse passer le
// rendu initial + on logge un warning (variété insuffisante) : JAMAIS de perte
// de mail, jamais de blocage dur.
//
// Le respin du contenu est délégué au sender via un callback (le dedup ne sait
// pas compiler du MJML ; le sender, si). Le dedup orchestre la BOUCLE
// anti-collision + le hash + le lookup repo ; le sender fournit le moteur de
// rendu par seed.
//
// DI optionnelle (nil = no-op strict, comportement upstream) : un dedup nil ou
// un anti-hash résolu désactivé renvoie le rendu initial inchangé sans aucun I/O.

// veridianDefaultMaxRespin borne le nombre de re-spin. 3 suffit à faire bouger un
// template avec ≥2 groupes spintax ; au-delà, le template est trop pauvre, c'est
// au linter de le signaler (on n'insiste pas indéfiniment).
const veridianDefaultMaxRespin = 3

// veridianContentDedupResult porte le rendu retenu (éventuellement re-spinné) et
// son hash, à poser sur le payload.
type veridianContentDedupResult struct {
	Subject     string
	Body        string
	ContentHash string // vide si anti-hash inactif sur cet envoi
}

// veridianContentDedupParams agrège tout ce dont la résolution a besoin. Le
// callback Respin(seed) renvoie le (subject, body) rendus pour un seed donné
// (Liquid déjà appliqué, spintax appliqué avec CE seed) — c'est le sender qui le
// fournit, il a le template brut.
type veridianContentDedupParams struct {
	WorkspaceID    string
	Email          string
	Contact        *domain.Contact
	Broadcast      *domain.Broadcast
	Provider       *domain.EmailProvider
	Workspace      *domain.Workspace
	InitialSubject string
	InitialBody    string
	// Respin re-rend (subject, body) pour un seed perturbé. Si nil, la re-spin est
	// impossible (on ne fait que détecter, sans varier).
	Respin func(seed string) (subject, body string)
}

// veridianContentDedup orchestre l'anti-hash à l'enqueue. Stateless (toute la
// config vient des params) ; il porte juste un logger. nil-safe.
type veridianContentDedup struct {
	repo   domain.MessageHistoryRepository
	logger logger.Logger
}

// newVeridianContentDedup crée le dédupliqueur. repo peut être nil → Resolve
// devient un no-op (renvoie le rendu initial sans hash). C'est le repo
// message_history (déjà disponible sur les senders) qui sert le lookup fenêtre.
func newVeridianContentDedup(repo domain.MessageHistoryRepository, log logger.Logger) *veridianContentDedup {
	return &veridianContentDedup{repo: repo, logger: log}
}

// Resolve calcule le hash du rendu initial, détecte une collision par classe
// dans la fenêtre, et tente jusqu'à N re-spin pour la lever. Retourne le rendu
// retenu + son hash. NON-RÉGRESSION : dedup nil, repo nil, ou anti-hash résolu
// désactivé → renvoie le rendu initial avec hash vide, ZÉRO I/O. Best-effort :
// une erreur de lecture repo ne bloque jamais l'enqueue (on garde le rendu
// courant + son hash, le filet worker re-constatera si besoin).
func (d *veridianContentDedup) Resolve(ctx context.Context, p veridianContentDedupParams) veridianContentDedupResult {
	initial := veridianContentDedupResult{Subject: p.InitialSubject, Body: p.InitialBody}
	if d == nil || d.repo == nil {
		return initial
	}

	// Anti-hash actif uniquement en CONTEXTE COLD (le hash n'a de sens que pour le
	// cold outbound). Hors cold, no-op strict.
	if !domain.VeridianIsColdContext(p.Contact, p.Broadcast, p.Workspace) {
		return initial
	}
	if !d.resolveEnabled(p) {
		return initial
	}

	window := d.resolveWindow(p)
	since := veridianContentDedupSince(window)
	class := domain.ClassifyProviderClass(p.Email)
	if c := domain.VeridianContactProviderClass(p.Contact); c != "" {
		class = c
	}
	domains, exclude := domain.VeridianDomainsForClass(class)

	subject, body := p.InitialSubject, p.InitialBody
	hash := domain.VeridianContentHash(subject, body)

	// Boucle anti-collision : tant que ce hash existe déjà vers cette classe dans
	// la fenêtre ET qu'on a encore des tentatives, re-spinner avec un seed perturbé.
	for attempt := 1; attempt <= veridianDefaultMaxRespin; attempt++ {
		exists, err := d.repo.ExistsContentHashSince(ctx, p.WorkspaceID, hash, domains, exclude, since)
		if err != nil {
			// Best-effort : on n'insiste pas, on garde le rendu courant + son hash.
			if d.logger != nil {
				d.logger.WithFields(map[string]interface{}{
					"workspace_id": p.WorkspaceID,
					"error":        err.Error(),
				}).Warn("Anti-hash dedup lookup failed at enqueue, keeping current render (best-effort)")
			}
			return veridianContentDedupResult{Subject: subject, Body: body, ContentHash: hash}
		}
		if !exists {
			// Hash neuf pour cette classe : on retient ce rendu.
			return veridianContentDedupResult{Subject: subject, Body: body, ContentHash: hash}
		}
		// Collision : tenter une re-spin si possible.
		if p.Respin == nil {
			break
		}
		seed := veridianRespinSeed(p.Email, attempt)
		rs, rb := p.Respin(seed)
		newHash := domain.VeridianContentHash(rs, rb)
		if newHash == hash {
			// La re-spin n'a pas changé le rendu (template sans spintax / variante
			// déjà épuisée) : inutile d'insister, on sort.
			break
		}
		subject, body, hash = rs, rb, newHash
	}

	// Collision résiduelle après N tentatives (ou re-spin impossible / template
	// sans variété) : on ENVOIE quand même (pas de perte de mail), en posant le
	// hash courant + un warning exploitable.
	if d.logger != nil {
		d.logger.WithFields(map[string]interface{}{
			"workspace_id":   p.WorkspaceID,
			"provider_class": class,
			"recipient":      p.Email,
			"content_hash":   hash,
		}).Warn("Anti-hash: identical content already sent to this provider class in window; template lacks spintax variety, sending anyway")
	}
	return veridianContentDedupResult{Subject: subject, Body: body, ContentHash: hash}
}

// resolveEnabled résout si l'anti-hash est actif via la cascade
// broadcast → infra → workspace → défaut cold (ON). On est déjà en contexte cold
// quand on arrive ici (Resolve l'a vérifié), donc le défaut est true.
func (d *veridianContentDedup) resolveEnabled(p veridianContentDedupParams) bool {
	metaEnabled, metaOK := false, false
	if p.Broadcast != nil {
		metaEnabled, metaOK = domain.VeridianAntiHashEnabledFromMetadata(p.Broadcast.Metadata)
	}
	var infra, workspace *bool
	if p.Provider != nil {
		infra = p.Provider.VeridianAntiHashEnabled
	}
	if p.Workspace != nil {
		workspace = p.Workspace.Settings.VeridianAntiHashEnabled
	}
	return domain.VeridianAntiHashEnabledFor(metaEnabled, metaOK, infra, workspace, true)
}

// resolveWindow résout la fenêtre glissante via la cascade broadcast → infra →
// workspace → défaut (72h).
func (d *veridianContentDedup) resolveWindow(p veridianContentDedupParams) time.Duration {
	metaHours := 0
	if p.Broadcast != nil {
		metaHours = domain.VeridianAntiHashWindowHoursFromMetadata(p.Broadcast.Metadata)
	}
	infraHours := 0
	if p.Provider != nil {
		infraHours = p.Provider.VeridianAntiHashWindowHours
	}
	workspaceHours := 0
	if p.Workspace != nil {
		workspaceHours = p.Workspace.Settings.VeridianAntiHashWindowHours
	}
	return domain.VeridianAntiHashWindow(metaHours, infraHours, workspaceHours)
}

// veridianContentDedupSince retourne l'instant de début de la fenêtre glissante.
func veridianContentDedupSince(window time.Duration) time.Time {
	return time.Now().Add(-window)
}

// veridianRespinSeed perturbe le seed de spintax pour la re-spin n° attempt :
// email + ":r" + attempt. ResolveSpintax étant déterministe sur le seed, un seed
// différent → une variante (potentiellement) différente. Le suffixe ":rN" reste
// stable pour un même (email, attempt) → re-render reproductible.
func veridianRespinSeed(email string, attempt int) string {
	return email + ":r" + strconv.Itoa(attempt)
}
