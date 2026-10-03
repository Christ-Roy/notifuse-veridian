package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// === Veridian patch — mission "API & agents" (2026-10-03) ===
//
// AgentHandler sert les routes PUBLIQUES du parcours "Brancher mon agent" :
//   - GET  /agent/install.sh      script shell, pas d'auth (c'est le script
//     lui-même qui est public, pas le secret)
//   - POST /api/agent.exchangeToken  échange un jeton d'installation à usage
//     unique contre la vraie clé API — public
//     par construction (le jeton EST le secret,
//     comme un lien magique), mais consommé
//     atomiquement une seule fois
//   - GET  /agent/notifuse        binaire/script CLI utilisateur (build
//     séparé, skill notifuse-cli — 503 tant que
//     AgentCLIBinaryPath n'est pas configuré)
//   - GET  /agent/skill.tar.gz    skill + AGENTS.md distribués (embarqués au
//     build, cf. agent_assets.go)
//
// La mint du jeton (POST /api/workspaces.createAgentInstallToken) reste sur
// WorkspaceHandler (authentifiée, même pattern que createAPIKey).
type AgentHandler struct {
	workspaceService domain.WorkspaceServiceInterface
	logger           logger.Logger
	cliBinaryPath    string
	apiHost          string
}

// NewAgentHandler crée le handler. cliBinaryPath peut être vide (self-hosted
// sans CLI publié encore) : /agent/notifuse renvoie alors 503 au lieu de
// planter. apiHost est l'hôte (sans schéma) que le script d'installation
// doit appeler pour l'échange de jeton / le téléchargement du CLI et du
// skill — dérivé de config.APIEndpoint, PAS du header Host de la requête
// (un Host usurpé ne doit jamais se retrouver dans un script qu'on fait
// exécuter localement chez l'utilisateur).
func NewAgentHandler(workspaceService domain.WorkspaceServiceInterface, logger logger.Logger, cliBinaryPath string, apiHost string) *AgentHandler {
	return &AgentHandler{
		workspaceService: workspaceService,
		logger:           logger,
		cliBinaryPath:    cliBinaryPath,
		apiHost:          apiHost,
	}
}

// RegisterRoutes registers the public /agent/* routes and the install-token
// exchange endpoint. None of these go through requireAuth: the install
// script itself has no business being authenticated (it hasn't got a key
// yet), and the token exchange is authenticated BY the one-time token.
func (h *AgentHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("/agent/install.sh", http.HandlerFunc(h.handleInstallScript))
	mux.Handle("/agent/notifuse", http.HandlerFunc(h.handleCLIBinary))
	mux.Handle("/agent/skill.tar.gz", http.HandlerFunc(h.handleSkillTarball))
	mux.Handle("/api/agent.exchangeToken", http.HandlerFunc(h.handleExchangeToken))
}

// handleInstallScript serves the POSIX shell installer. Pure text template,
// no per-request computation: the script reads --token from its own argv,
// never from this handler.
func (h *AgentHandler) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	script := strings.ReplaceAll(agentInstallScript, "__NOTIFUSE_API_HOST__", h.apiHost)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(script))
}

// handleCLIBinary serves the user-scoped CLI (built separately, skill
// notifuse-cli — not this mission's to build, only to distribute). Returns
// 503 with a clear body when AgentCLIBinaryPath isn't configured, instead
// of a confusing 404 or a panic on a missing file.
func (h *AgentHandler) handleCLIBinary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.cliBinaryPath == "" {
		WriteJSONError(w, "agent CLI binary not yet published on this instance", http.StatusServiceUnavailable)
		return
	}
	f, err := os.Open(h.cliBinaryPath)
	if err != nil {
		if h.logger != nil {
			h.logger.WithField("path", h.cliBinaryPath).WithField("error", err.Error()).Error("agent CLI binary configured but unreadable")
		}
		WriteJSONError(w, "agent CLI binary not yet published on this instance", http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = f.Close() }()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="notifuse"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "notifuse", fileModTimeOrZero(h.cliBinaryPath), f)
}

func fileModTimeOrZero(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// handleSkillTarball serves the embedded skill (SKILL.md + AGENTS.md) as a
// pre-built .tar.gz (cached after the first build, see agent_assets.go).
func (h *AgentHandler) handleSkillTarball(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := buildAgentSkillTarball()
	if err != nil {
		if h.logger != nil {
			h.logger.WithField("error", err.Error()).Error("failed to build agent skill tarball")
		}
		WriteJSONError(w, "failed to build skill archive", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="notifuse-skill.tar.gz"`)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// ExchangeAgentInstallTokenRequest is the body of POST /api/agent.exchangeToken.
type ExchangeAgentInstallTokenRequest struct {
	Token string `json:"token"`
}

// handleExchangeToken consumes a one-time install token and returns the
// api_key it unlocks, as simple KEY=VALUE lines (NOT JSON) so the shell
// installer can parse the response with nothing but `read`/`IFS` — no jq
// dependency on the machine being provisioned. A reused or expired token is
// refused (410 Gone); an unknown token is refused (404) — see
// domain/agent_install.go for why these stay distinguishable at this layer
// (unlike the JWT-auth path, there is no "valid credential, wrong scope"
// case here: either the one-time secret matches a live row, or it doesn't).
func (h *AgentHandler) handleExchangeToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ExchangeAgentInstallTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		WriteJSONError(w, "Missing token", http.StatusBadRequest)
		return
	}

	creds, err := h.workspaceService.ExchangeAgentInstallToken(r.Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAgentInstallTokenNotFound):
			WriteJSONError(w, "install token not found", http.StatusNotFound)
		case errors.Is(err, domain.ErrAgentInstallTokenUsed):
			WriteJSONError(w, "install token already used", http.StatusGone)
		case errors.Is(err, domain.ErrAgentInstallTokenExpired):
			WriteJSONError(w, "install token expired", http.StatusGone)
		default:
			if h.logger != nil {
				h.logger.WithField("error", err.Error()).Error("failed to exchange agent install token")
			}
			WriteJSONError(w, "failed to exchange install token", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "NOTIFUSE_API_KEY=%s\nNOTIFUSE_API_URL=%s\nNOTIFUSE_WORKSPACE=%s\n",
		creds.APIKey, creds.APIURL, creds.WorkspaceID)
}

// agentInstallScript is the POSIX shell installer served at /agent/install.sh.
// Design goals, in order:
//  1. The api_key never appears in the install command the user copy-pastes,
//     nor in shell history: only the one-time --token does, and it is
//     burned server-side on first use.
//  2. Idempotent and safe to re-run (a second run with the SAME token simply
//     fails cleanly: "token already used" — expected, not a crash).
//  3. Writes the key to disk chmod 600 and never echoes it.
const agentInstallScript = `#!/bin/sh
# Notifuse agent installer — installs the user CLI, writes the API key to
# ~/.config/notifuse/env (chmod 600, never printed), and installs the
# "notifuse" skill (+ generic AGENTS.md) for any coding agent to pick up.
#
# Usage: curl -fsSL https://<notifuse-host>/agent/install.sh | sh -s -- --token <install_token>
#
# The --token is a ONE-TIME, short-lived (10 min) install token minted from
# the console's "API & agents" page. It is NOT the API key: the real key is
# fetched server-side via the token and never appears on this command line
# or in your shell history.

set -eu

TOKEN=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --token) TOKEN="$2"; shift 2 ;;
    --token=*) TOKEN="${1#--token=}"; shift ;;
    *) shift ;;
  esac
done

if [ -z "$TOKEN" ]; then
  echo "Usage: curl -fsSL https://<host>/agent/install.sh | sh -s -- --token <install_token>" >&2
  echo "Get a token from the workspace's 'API & agents' settings page." >&2
  exit 1
fi

# API_HOST is substituted server-side (from the app's own configured
# APIEndpoint, never from a client-controlled header) before this script is
# served — see internal/http/agent_handler.go handleInstallScript.
API_HOST="__NOTIFUSE_API_HOST__"

echo "Exchanging install token..." >&2
RESPONSE=$(curl -fsS -X POST "https://${API_HOST}/api/agent.exchangeToken" \
  -H "Content-Type: application/json" \
  -d "{\"token\":\"${TOKEN}\"}")

CONFIG_DIR="$HOME/.config/notifuse"
mkdir -p "$CONFIG_DIR"
ENV_FILE="$CONFIG_DIR/env"

# Parse the KEY=VALUE lines without eval (values are server-controlled
# JWT/URL/workspace-id, but we still never shell-eval untrusted output).
: > "$ENV_FILE"
chmod 600 "$ENV_FILE"
echo "$RESPONSE" | while IFS='=' read -r key value; do
  case "$key" in
    NOTIFUSE_API_KEY|NOTIFUSE_API_URL|NOTIFUSE_WORKSPACE)
      printf '%s=%s\n' "$key" "$value" >> "$ENV_FILE"
      ;;
  esac
done

if ! grep -q '^NOTIFUSE_API_KEY=' "$ENV_FILE"; then
  echo "Install failed: token exchange did not return an API key (already used or expired?)." >&2
  exit 1
fi

echo "Credentials written to $ENV_FILE (chmod 600). Never printed." >&2

echo "Installing notifuse CLI..." >&2
mkdir -p "$HOME/bin"
if curl -fsS "https://${API_HOST}/agent/notifuse" -o "$HOME/bin/notifuse" 2>/dev/null; then
  chmod +x "$HOME/bin/notifuse"
  echo "notifuse CLI installed to $HOME/bin/notifuse" >&2
else
  echo "notifuse CLI not available yet on this instance — skipping (credentials are installed, retry later for the CLI)." >&2
fi

echo "Installing notifuse skill..." >&2
SKILL_DIR="$HOME/.claude/skills"
mkdir -p "$SKILL_DIR"
TMP_TAR=$(mktemp)
curl -fsS "https://${API_HOST}/agent/skill.tar.gz" -o "$TMP_TAR"
tar -xzf "$TMP_TAR" -C "$SKILL_DIR"
rm -f "$TMP_TAR"
echo "Skill installed to $SKILL_DIR/notifuse/" >&2

echo "Done. Load ~/.config/notifuse/env (set -a; . ~/.config/notifuse/env; set +a) before using the CLI." >&2
`
