package http

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io/fs"
	"sync"
)

// === Veridian patch — mission "API & agents" (2026-10-03) ===
//
// Contenu distribué à l'agent (SKILL.md + AGENTS.md) : le savoir-faire
// Veridian sur l'emailing en masse, écrit pour un agent IA, SANS rien
// d'interne à Veridian (pas de nom de client, pas d'infra, pas de secret).
// Embarqué dans le binaire (go:embed) : servi identiquement en dev/staging/
// prod, pas de dépendance à un chemin filesystem qui pourrait manquer dans
// l'image.
//
//go:embed agentskill/SKILL.md agentskill/AGENTS.md
var agentSkillFS embed.FS

var (
	agentSkillTarballOnce sync.Once
	agentSkillTarballData []byte
	agentSkillTarballErr  error
)

// buildAgentSkillTarball construit un .tar.gz contenant le skill distribué,
// sous la forme attendue par le script d'installation :
//
//	notifuse/SKILL.md
//	notifuse/AGENTS.md
//
// Construit une seule fois (sync.Once) et mis en cache en mémoire — le
// contenu est statique (embarqué au build), aucune raison de refaire le
// tar+gzip à chaque requête GET /agent/skill.tar.gz.
func buildAgentSkillTarball() ([]byte, error) {
	agentSkillTarballOnce.Do(func() {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)

		err := fs.WalkDir(agentSkillFS, "agentskill", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			data, err := fs.ReadFile(agentSkillFS, path)
			if err != nil {
				return fmt.Errorf("read embedded %s: %w", path, err)
			}
			// agentskill/SKILL.md -> notifuse/SKILL.md (le script d'installation
			// dépose ce dossier tel quel dans ~/.claude/skills/notifuse/).
			name := "notifuse/" + path[len("agentskill/"):]
			hdr := &tar.Header{
				Name: name,
				Mode: 0644,
				Size: int64(len(data)),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return fmt.Errorf("write tar header %s: %w", name, err)
			}
			if _, err := tw.Write(data); err != nil {
				return fmt.Errorf("write tar body %s: %w", name, err)
			}
			return nil
		})
		if err != nil {
			agentSkillTarballErr = err
			return
		}
		if err := tw.Close(); err != nil {
			agentSkillTarballErr = fmt.Errorf("close tar writer: %w", err)
			return
		}
		if err := gz.Close(); err != nil {
			agentSkillTarballErr = fmt.Errorf("close gzip writer: %w", err)
			return
		}
		agentSkillTarballData = buf.Bytes()
	})
	return agentSkillTarballData, agentSkillTarballErr
}
