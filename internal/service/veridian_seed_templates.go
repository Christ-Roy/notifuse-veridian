package service

// === Veridian patch — 2026-05-23 ===
// Seed automatique du template transactionnel "invitation-prospection" dans
// chaque workspace fraîchement provisionné. Couvre le ticket cross-app
// 2026-05-23-import-template-invitation-prospection.
//
// Pourquoi un seed inconditionnel : tous les workspaces Notifuse sont des
// tenants Veridian. Le template est minuscule (~3kB MJML) et l'opération est
// idempotente (skip si déjà présent). Le coût d'avoir le template dispo sur
// tous les workspaces (même non-Prospection) est négligeable comparé au
// bénéfice de ne pas avoir à coordonner un seed conditionnel via le Hub.
//
// Best-effort : si le seed échoue (templateService non câblé, DB momentanée,
// validation MJML qui change), Provision() log un warning mais ne bloque pas
// — le template peut toujours être créé manuellement via l'UI ou un futur
// endpoint admin dédié.

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/notifuse_mjml"
)

// invitationProspectionMJML est le template MJML source aligné sur
// veridian-prospection/templates/notifuse/invitation-prospection.mjml.
// Embed via go:embed → un seul artefact à maintenir côté Notifuse, ne dérive
// pas du fichier upstream Prospection (qui sert de référence visuelle).
//
//go:embed veridian_seed_invitation_prospection.mjml
var invitationProspectionMJML string

const (
	// SeedInvitationProspectionTemplateID est l'ID du template + de la
	// notification transactionnelle créés au provisioning. Doit matcher
	// l'ID utilisé côté veridian-prospection/src/lib/notifuse/client.ts
	// → sendInvitationEmail() (notification.id = "invitation-prospection").
	SeedInvitationProspectionTemplateID = "invitation-prospection"
	seedInvitationProspectionName       = "Invitation Prospection"
	seedInvitationProspectionSubject    = "{{ inviter_email }} vous invite sur {{ workspace_name }}"
)

// ConfigureSeedTemplatesSupport injecte les services template +
// transactional dans veridianService. Pattern setter post-construction
// (identique à ConfigureAPIKeyGraceSupport), pour ne pas casser la signature
// NewVeridianService et les mocks de tests existants.
//
// Si l'un des deux est nil, le seed sera silencieusement skippé au Provision().
func ConfigureSeedTemplatesSupport(
	svc domain.VeridianService,
	templateService domain.TemplateService,
	txService *TransactionalNotificationService,
) error {
	impl, ok := svc.(*veridianService)
	if !ok {
		return errors.New("svc is not a *veridianService — seed templates unsupported on this implementation")
	}
	impl.templateService = templateService
	impl.transactionalNotificationService = txService
	return nil
}

// seedInvitationProspectionTemplate crée (idempotent) le template + la
// notification transactionnelle invitation-prospection dans le workspace
// donné. Appelé en fin de Provision(). Best-effort : log warning sur échec,
// jamais bloquant.
//
// Ordre d'exécution :
//  1. Skip si templateService ou transactionalNotificationService non câblé
//     (mode test ou config minimaliste).
//  2. ctxAsRoot pour bypasser auth — Provision est déjà sous HMAC Hub, on
//     ne re-vérifie pas un user humain ici.
//  3. Check si le template existe déjà via GetTemplateByID. Si oui → skip.
//  4. Sinon CreateTemplate avec EditorMode=code + MjmlSource.
//  5. Check si la notification existe déjà via GetNotification. Si oui → done.
//  6. Sinon CreateNotification qui référence le template.
func (s *veridianService) seedInvitationProspectionTemplate(ctx context.Context, workspaceID string) {
	if s.templateService == nil || s.transactionalNotificationService == nil {
		// Mode test ou self-hosted minimaliste : pas de seed.
		return
	}

	rootCtx, sessionID, _, err := s.ctxAsRoot(ctx)
	if err != nil {
		s.logWarnSeed(workspaceID, err, "ctxAsRoot failed")
		return
	}
	defer s.cleanupSession(ctx, sessionID)

	// Étape 1 — template MJML
	existing, getErr := s.templateService.GetTemplateByID(rootCtx, workspaceID, SeedInvitationProspectionTemplateID, 0)
	if getErr == nil && existing != nil && existing.DeletedAt == nil {
		// Déjà présent et non soft-deleted → on garde le template existant
		// (le client a peut-être customisé son contenu). On poursuit sur la
		// notification au cas où elle manquerait.
	} else {
		mjml := invitationProspectionMJML
		tmpl := &domain.Template{
			ID:       SeedInvitationProspectionTemplateID,
			Name:     seedInvitationProspectionName,
			Channel:  domain.ChannelEmail,
			Category: string(domain.TemplateCategoryTransactional),
			Email: &domain.EmailTemplate{
				EditorMode:      domain.EditorModeCode,
				MjmlSource:      &mjml,
				Subject:         seedInvitationProspectionSubject,
				CompiledPreview: mjml,
				// VisualEditorTree non requis en code mode mais le champ
				// JSON exige un root mjml valide pour Unmarshal. On fournit
				// un arbre minimal vide qui passera Validate() en mode code
				// (la validation skip VisualEditorTree quand EditorMode=code).
				VisualEditorTree: emptyMJMLRoot(),
			},
			TestData: domain.MapOfAny{
				"inviter_email":   "boss@acme.com",
				"workspace_name":  "Team Sales",
				"invite_url":      "https://prospection.app.veridian.site/invite/example",
				"expires_at":      "2026-12-31T00:00:00.000Z",
				"unsubscribe_url": "https://prospection.app.veridian.site/unsubscribe/example",
			},
		}
		if err := s.templateService.CreateTemplate(rootCtx, workspaceID, tmpl); err != nil {
			if !isDuplicateErr(err) {
				s.logWarnSeed(workspaceID, err, "CreateTemplate failed")
				return
			}
			// Race : un autre Provision concurrent l'a créé entre Get et
			// Create → on continue sur la notification.
		}
	}

	// Étape 2 — transactional notification (référence le template)
	existingNotif, getNotifErr := s.transactionalNotificationService.GetNotification(rootCtx, workspaceID, SeedInvitationProspectionTemplateID)
	if getNotifErr == nil && existingNotif != nil && existingNotif.DeletedAt == nil {
		// Déjà présent → done.
		return
	}

	params := domain.TransactionalNotificationCreateParams{
		ID:          SeedInvitationProspectionTemplateID,
		Name:        seedInvitationProspectionName,
		Description: "Invitation cross-app envoyée par Veridian Prospection à un nouvel utilisateur. Variables Liquid : inviter_email, workspace_name, invite_url, expires_at.",
		Channels: domain.ChannelTemplates{
			domain.TransactionalChannelEmail: domain.ChannelTemplate{
				TemplateID: SeedInvitationProspectionTemplateID,
			},
		},
		TrackingSettings: notifuse_mjml.TrackingSettings{
			EnableTracking: true,
		},
		Metadata: domain.MapOfAny{
			"source":   "veridian-seed",
			"category": "cross-app-invitation",
		},
	}
	if _, err := s.transactionalNotificationService.CreateNotification(rootCtx, workspaceID, params); err != nil {
		if !isDuplicateErr(err) {
			s.logWarnSeed(workspaceID, err, "CreateNotification failed")
		}
	}
}

// emptyMJMLRoot retourne un arbre MJML minimal (mjml root + head + body)
// valide pour passer le Unmarshal JSON. Utilisé en code mode où la
// validation skip le VisualEditorTree, mais le champ doit toujours
// désérialiser à un block de type "mjml" pour la persistance.
func emptyMJMLRoot() notifuse_mjml.EmailBlock {
	headBase := notifuse_mjml.NewBaseBlock("head", notifuse_mjml.MJMLComponentMjHead)
	head := &notifuse_mjml.MJHeadBlock{BaseBlock: headBase}

	bodyBase := notifuse_mjml.NewBaseBlock("body", notifuse_mjml.MJMLComponentMjBody)
	body := &notifuse_mjml.MJBodyBlock{BaseBlock: bodyBase}

	rootBase := notifuse_mjml.NewBaseBlock("mjml-root", notifuse_mjml.MJMLComponentMjml)
	rootBase.Children = []notifuse_mjml.EmailBlock{head, body}
	return &notifuse_mjml.MJMLBlock{BaseBlock: rootBase}
}

// isDuplicateErr détecte une erreur de violation d'unique constraint
// pour le seed. Le repo Postgres retourne typiquement un wrap autour de
// `pq: duplicate key value violates unique constraint`. On reste tolérant
// — toute erreur qui contient "duplicate" ou "already" est considérée OK
// pour le seed idempotent.
func isDuplicateErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "already exists")
}

func (s *veridianService) logWarnSeed(workspaceID string, err error, msg string) {
	if s.logger == nil {
		return
	}
	s.logger.WithFields(map[string]interface{}{
		"workspace_id": workspaceID,
		"template_id":  SeedInvitationProspectionTemplateID,
		"error":        fmt.Sprintf("%v", err),
	}).Warn("veridian seed: " + msg)
}
