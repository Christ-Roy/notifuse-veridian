package broadcast

import (
	"context"
	"sync"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Veridian — résolution du pixel d'ouverture par classe AVEC fallback workspace.
//
// Problème corrigé (audit hardening 2026-06-13) : VeridianResolveOpenPixel
// accepte un *Workspace pour le fallback "broadcast.metadata > workspace
// settings > défaut tunnel" (cf. domain/veridian_open_pixel.go + UI Settings →
// Cold outreach qui persiste workspace.Settings.VeridianOpenPixelByClass via le
// fix bcc23764). Mais les message senders n'ont pas le workspace en main : ils
// ne reçoivent qu'un workspaceID, et les 3 call-sites passaient workspace=nil.
// Conséquence : la config pixel posée au NIVEAU WORKSPACE (chemin UI principal)
// était persistée mais JAMAIS appliquée à l'envoi — seul le défaut tunnel ou la
// config répétée sur le broadcast jouaient. Asymétrie avec le throttle, qui lit
// bien le workspace au gate worker.
//
// Ce resolver câble le fallback proprement : DI optionnelle (setter, pas de
// changement de signature constructeur → zéro casse des tests existants), un
// seul GetByID par batch (cache mémoïsé sur l'instance de résolution), nil-safe
// (sans repo injecté, comportement strictement identique à l'avant-fix : le
// workspace reste nil et le fallback workspace est simplement inactif).

// veridianWorkspacePixelResolver mémoïse le workspace pour un batch d'envoi afin
// de ne le charger qu'une fois (zéro I/O par recipient). Sans workspaceRepo
// injecté, Resolve passe workspace=nil — comportement upstream préservé.
type veridianWorkspacePixelResolver struct {
	repo   domain.WorkspaceRepository
	logger logger.Logger

	mu        sync.Mutex
	cached    *domain.Workspace
	fetchedID string
	fetched   bool
}

// newVeridianWorkspacePixelResolver crée un resolver. repo peut être nil : dans
// ce cas le fallback workspace est inactif (le pixel suit broadcast + défaut).
func newVeridianWorkspacePixelResolver(repo domain.WorkspaceRepository, log logger.Logger) *veridianWorkspacePixelResolver {
	return &veridianWorkspacePixelResolver{repo: repo, logger: log}
}

// workspace retourne le workspace mémoïsé pour cet ID, ou nil si le repo n'est
// pas injecté / le fetch échoue. Un échec de fetch ne bloque JAMAIS l'envoi :
// on retombe sur la résolution sans fallback workspace (défaut tunnel). Le
// résultat (y compris nil) est mis en cache pour ne tenter le fetch qu'une fois.
func (r *veridianWorkspacePixelResolver) workspace(ctx context.Context, workspaceID string) *domain.Workspace {
	if r == nil || r.repo == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fetched && r.fetchedID == workspaceID {
		return r.cached
	}

	ws, err := r.repo.GetByID(ctx, workspaceID)
	if err != nil {
		// Best-effort : on log en debug et on dégrade vers nil (pas de fallback
		// workspace). Surtout pas d'échec d'envoi pour un lookup de config pixel.
		if r.logger != nil {
			r.logger.WithFields(map[string]interface{}{
				"workspace_id": workspaceID,
				"error":        err.Error(),
			}).Debug("Veridian pixel resolver: workspace fetch failed, falling back to broadcast/default policy")
		}
		ws = nil
	}

	r.cached = ws
	r.fetchedID = workspaceID
	r.fetched = true
	return ws
}

// resolveOpenPixel calcule le flag pixel effectif en injectant le workspace
// mémoïsé. Wrapper fin autour de domain.VeridianResolveOpenPixel pour centraliser
// le fallback workspace côté senders.
func (r *veridianWorkspacePixelResolver) resolveOpenPixel(
	ctx context.Context,
	workspaceID string,
	contact *domain.Contact,
	email string,
	broadcast *domain.Broadcast,
) *bool {
	ws := r.workspace(ctx, workspaceID)
	return domain.VeridianResolveOpenPixel(contact, email, broadcast, ws)
}
