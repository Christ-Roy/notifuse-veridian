package service

// === Veridian patch — 2026-06-18 — GC des bases workspace orphelines (staging) ===
//
// Voir todo/2026-06-17-orphan-workspaces-staging-db-starvation.md.
//
// Sur staging, le DROP upstream sans FORCE échouait sur la race de connexions du
// worker → les bases physiques notifuse_ws_* s'accumulent sans record `workspaces`
// correspondant (689 orphelines pour 110 records, observé 2026-06-18). Elles
// gonflent le disque dev-pub et ralentissent le scan de workspaces.
//
// Ce service liste les bases orphelines (pg_database moins les records workspaces)
// et les DROP avec FORCE, SÉQUENTIELLEMENT (jamais en rafale parallèle — un DROP
// DATABASE prend un lock exclusif, des appels concurrents sur des goroutines
// actives crashent le container : piège vécu include_orphans:true en batch).
//
// GARDE-FOUS :
//   - STAGING-ONLY (le handler refuse 503 hors staging) ;
//   - EXCLUSION de tout préfixe de safety (canary + clients réels), JAMAIS dropé ;
//   - cap dur par run (anti-runaway) ;
//   - best-effort par base (un DROP raté est compté en erreur, le GC continue).

import (
	"context"
	"errors"

	"github.com/Notifuse/notifuse/pkg/logger"
)

// errOrphanGCUnsupported : le WorkspaceRepository injecté n'expose pas la capacité
// de GC (cas d'un repo mock/alternatif sans les méthodes veridian_workspace_drop).
var errOrphanGCUnsupported = errors.New("workspace repository does not support orphan DB GC")

// veridianOrphanDBGCRepo est la capacité (optionnelle) du WorkspaceRepository dont
// le GC a besoin. Implémentée par *repository.workspaceRepository (méthodes dans
// veridian_workspace_drop.go). Détectée par type-assertion → pas d'import du
// package repository depuis service.
type veridianOrphanDBGCRepo interface {
	VeridianListOrphanWorkspaceDBs(ctx context.Context) ([]string, error)
	VeridianForceDropDatabaseByName(ctx context.Context, dbName string, log logger.Logger) error
	VeridianWorkspaceDBPrefix() string
}

// VeridianOrphanDBGCInput pilote un run de GC.
type VeridianOrphanDBGCInput struct {
	// DryRun : si true, on LISTE les orphelines droppables sans rien DROP.
	DryRun bool `json:"dry_run"`

	// SafetyPrefixes : préfixes de workspaceID JAMAIS dropés. Si vide, on utilise
	// defaultSafetyClientPrefixes (canary + clients réels) — même liste que le wipe.
	SafetyPrefixes []string `json:"safety_prefixes,omitempty"`

	// MaxDrops : cap dur de bases dropées par run (anti-runaway). 0 = défaut.
	MaxDrops int `json:"max_drops,omitempty"`
}

// VeridianOrphanDBGCResponse rend le bilan d'un run.
type VeridianOrphanDBGCResponse struct {
	TotalOrphans    int               `json:"total_orphans"`     // bases sans record (avant exclusion safety)
	Droppable       int               `json:"droppable"`         // orphelines hors safety, candidates
	Dropped         []string          `json:"dropped"`           // bases effectivement dropées
	SkippedSafety   []string          `json:"skipped_safety"`    // exclues car préfixe de safety
	SkippedCap      int               `json:"skipped_cap"`       // au-delà du MaxDrops
	Errors          map[string]string `json:"errors"`            // base → message d'erreur du DROP
	DryRun          bool              `json:"dry_run"`
}

// veridianOrphanDBGCDefaultMaxDrops borne un run pour ne pas saturer le DB en une
// fois. 1000 = assez pour vider les 689 orphelines staging en un run, mais cappé.
const veridianOrphanDBGCDefaultMaxDrops = 1000

// VeridianGCOrphanWorkspaceDBs liste les bases orphelines et les DROP avec FORCE,
// SÉQUENTIELLEMENT. Exclut tout préfixe de safety. Best-effort par base.
//
// Le repo DOIT exposer la capacité veridianOrphanDBGCRepo (sinon erreur claire).
func (s *veridianService) VeridianGCOrphanWorkspaceDBs(ctx context.Context, input VeridianOrphanDBGCInput) (*VeridianOrphanDBGCResponse, error) {
	gcRepo, ok := s.workspaceRepo.(veridianOrphanDBGCRepo)
	if !ok {
		return nil, errOrphanGCUnsupported
	}

	prefix := gcRepo.VeridianWorkspaceDBPrefix()

	orphans, err := gcRepo.VeridianListOrphanWorkspaceDBs(ctx)
	if err != nil {
		return nil, err
	}

	safety := input.SafetyPrefixes
	if len(safety) == 0 {
		safety = defaultSafetyClientPrefixes
	}

	maxDrops := input.MaxDrops
	if maxDrops <= 0 {
		maxDrops = veridianOrphanDBGCDefaultMaxDrops
	}

	resp := &VeridianOrphanDBGCResponse{
		TotalOrphans:  len(orphans),
		Dropped:       []string{},
		SkippedSafety: []string{},
		Errors:        map[string]string{},
		DryRun:        input.DryRun,
	}

	for _, dbName := range orphans {
		// Dériver le workspaceID (best-effort, underscores conservés) pour le
		// matching de safety. On matche le préfixe DE SAFETY sur l'ID dérivé.
		wsID := veridianTrimDBPrefix(prefix, dbName)
		if veridianHasAnySafetyPrefix(wsID, safety) {
			resp.SkippedSafety = append(resp.SkippedSafety, dbName)
			continue
		}
		resp.Droppable++

		if resp.DryRun {
			continue
		}

		if len(resp.Dropped) >= maxDrops {
			resp.SkippedCap++
			continue
		}

		// DROP FORCE SÉQUENTIEL (une base à la fois). Best-effort : un échec est
		// enregistré et on continue (ne crashe pas le run).
		if dropErr := gcRepo.VeridianForceDropDatabaseByName(ctx, dbName, s.logger); dropErr != nil {
			resp.Errors[dbName] = dropErr.Error()
			continue
		}
		resp.Dropped = append(resp.Dropped, dbName)
	}

	if s.logger != nil {
		s.logger.WithFields(map[string]interface{}{
			"total_orphans":  resp.TotalOrphans,
			"droppable":      resp.Droppable,
			"dropped":        len(resp.Dropped),
			"skipped_safety": len(resp.SkippedSafety),
			"skipped_cap":    resp.SkippedCap,
			"errors":         len(resp.Errors),
			"dry_run":        resp.DryRun,
		}).Info("veridian: orphan workspace DB GC completed")
	}

	return resp, nil
}

// veridianTrimDBPrefix extrait l'ID workspace (best-effort) d'un nom de base.
func veridianTrimDBPrefix(prefix, dbName string) string {
	full := prefix + "_ws_"
	if len(dbName) > len(full) && dbName[:len(full)] == full {
		return dbName[len(full):]
	}
	return dbName
}

// veridianHasAnySafetyPrefix retourne true si wsID commence par l'un des préfixes
// de safety. ⚠️ La conversion "-"→"_" du nom de base rend les underscores du wsID
// ambigus : on matche donc le préfixe de safety à la fois tel quel ET avec ses
// tirets convertis en underscores (un préfixe "canary" reste "canary" : pas de
// tiret, pas d'ambiguïté ; un préfixe "real-client" matchera "real_client").
func veridianHasAnySafetyPrefix(wsID string, safety []string) bool {
	for _, sp := range safety {
		if sp == "" {
			continue
		}
		if veridianHasPrefix(wsID, sp) || veridianHasPrefix(wsID, veridianHyphensToUnderscores(sp)) {
			return true
		}
	}
	return false
}

func veridianHasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func veridianHyphensToUnderscores(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] == '-' {
			b[i] = '_'
		}
	}
	return string(b)
}
