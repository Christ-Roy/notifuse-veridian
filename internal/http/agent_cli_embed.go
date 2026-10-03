package http

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"sync"
)

// === Veridian patch -- mission "CLI distribue" (2026-10-03) ===
//
// Le CLI utilisateur (notifuse + son module partage notifuse_common.py,
// skill notifuse-cli) est embarque dans le binaire de l'app, exactement
// comme le skill AGENTS.md/SKILL.md (cf agent_assets.go) : servi
// identiquement en dev/staging/prod, aucune dependance a un chemin
// filesystem ni a une variable d'env qu'on pourrait oublier de poser au
// deploiement (AGENT_CLI_BINARY_PATH, avant ce correctif, restait vide en
// prod -- /agent/notifuse y repondait 503 en permanence).
//
// Les deux fichiers sont copies TELS QUELS depuis la source de verite
// (~/.claude/skills/notifuse-cli/bin/) par scripts/veridian/sync-agent-cli.sh,
// jamais edites a la main ici -- scripts/ci/check-agent-cli-sync.sh verifie
// au push que cette copie n'a pas divergé du manifeste sha256 qui
// l'accompagne.
//
// notifuse-admin (super cle Hub) n'est PAS embarque : seul le binaire USER,
// scope a NOTIFUSE_API_KEY, quitte le serveur.
//
//go:embed agentcli/notifuse agentcli/notifuse_common.py
var agentCLIFS embed.FS

var (
	agentCLITarballOnce sync.Once
	agentCLITarballData []byte
	agentCLITarballErr  error
)

// buildAgentCLITarball construit un .tar.gz contenant les deux fichiers du
// CLI utilisateur, A PLAT (pas de sous-dossier) : le script d'installation
// les extrait directement dans ~/.local/bin/, et notifuse (le shim de 31
// lignes) fait sys.path.insert(0, dirname(realpath(__file__))) pour
// trouver notifuse_common.py juste a cote de lui.
func buildAgentCLITarball() ([]byte, error) {
	agentCLITarballOnce.Do(func() {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)

		files := []struct {
			embeddedPath string
			tarName      string
			mode         int64
		}{
			{"agentcli/notifuse", "notifuse", 0755},
			{"agentcli/notifuse_common.py", "notifuse_common.py", 0644},
		}

		for _, f := range files {
			data, err := agentCLIFS.ReadFile(f.embeddedPath)
			if err != nil {
				agentCLITarballErr = fmt.Errorf("read embedded %s: %w", f.embeddedPath, err)
				return
			}
			hdr := &tar.Header{
				Name: f.tarName,
				Mode: f.mode,
				Size: int64(len(data)),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				agentCLITarballErr = fmt.Errorf("write tar header %s: %w", f.tarName, err)
				return
			}
			if _, err := tw.Write(data); err != nil {
				agentCLITarballErr = fmt.Errorf("write tar body %s: %w", f.tarName, err)
				return
			}
		}

		if err := tw.Close(); err != nil {
			agentCLITarballErr = fmt.Errorf("close tar writer: %w", err)
			return
		}
		if err := gz.Close(); err != nil {
			agentCLITarballErr = fmt.Errorf("close gzip writer: %w", err)
			return
		}
		agentCLITarballData = buf.Bytes()
	})
	return agentCLITarballData, agentCLITarballErr
}
