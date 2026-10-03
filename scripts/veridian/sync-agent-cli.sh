#!/usr/bin/env sh
# sync-agent-cli.sh -- copie versionnee du CLI utilisateur distribue aux
# clients (/agent/notifuse, mission "CLI distribue" 2026-10-03).
#
# Source de verite : ~/.claude/skills/notifuse-cli/bin/{notifuse,notifuse_common.py}
# sur le poste de l'operateur (bastion), PAS ce depot. Ce script copie les
# DEUX fichiers tels quels dans internal/http/agentcli/ (go:embed les sert en
# tar.gz, cf internal/http/agent_cli_embed.go) et ecrit un manifeste sha256
# pour que la CI puisse detecter une divergence (edition a la main du
# fichier embarque sans repasser par ce script).
#
# notifuse-admin (super cle Hub) n'est JAMAIS copie ici : seul le binaire
# USER scope (notifuse + son module partage notifuse_common.py) est distribue.
#
# Usage : ./scripts/veridian/sync-agent-cli.sh [chemin/vers/skills/notifuse-cli/bin]
# Par defaut : ~/.claude/skills/notifuse-cli/bin (poste bastion standard).

set -eu

SRC_DIR="${1:-$HOME/.claude/skills/notifuse-cli/bin}"
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DEST_DIR="$REPO_ROOT/internal/http/agentcli"

for f in notifuse notifuse_common.py; do
  if [ ! -f "$SRC_DIR/$f" ]; then
    echo "ERROR: $SRC_DIR/$f introuvable -- source de verite absente, rien copie." >&2
    exit 2
  fi
done

mkdir -p "$DEST_DIR"
cp "$SRC_DIR/notifuse" "$DEST_DIR/notifuse"
cp "$SRC_DIR/notifuse_common.py" "$DEST_DIR/notifuse_common.py"
chmod 0755 "$DEST_DIR/notifuse"
chmod 0644 "$DEST_DIR/notifuse_common.py"

( cd "$DEST_DIR" && sha256sum notifuse notifuse_common.py > MANIFEST.sha256 )

echo "OK: $DEST_DIR synchronise depuis $SRC_DIR" >&2
echo "  $(wc -l < "$DEST_DIR/notifuse") lignes notifuse, $(wc -l < "$DEST_DIR/notifuse_common.py") lignes notifuse_common.py" >&2
cat "$DEST_DIR/MANIFEST.sha256" >&2
