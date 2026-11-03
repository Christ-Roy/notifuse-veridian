#!/bin/sh
# cli-coverage-check.sh : couverture CLI embarquée (fiche 76, Constitution CI).
#   - chaque route /api/* du routeur Go a une commande dans internal/http/agentcli ;
#   - la CLI embarquée correspond à son MANIFEST, et à la source de vérité si elle est
#     présente (NOTIFUSE_CLI_SOURCE, défaut ~/.claude/skills/notifuse-cli/bin) ;
#   - la CLI s'exécute avec les seuls fichiers distribués.
# Trois états : 0 = VERT, 1 = ROUGE (bloque), 2 = NON ABOUTI (prévient sans bloquer).
#   scripts/veridian/cli-coverage-check.sh --selftest   éprouve l'extraction sur un routeur fabriqué
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
SRC="${NOTIFUSE_CLI_SOURCE:-$HOME/.claude/skills/notifuse-cli/bin}"
if ! command -v python3 >/dev/null 2>&1; then
  echo "NON ABOUTI : python3 absent sur cette machine" >&2
  exit 2
fi
if [ "${1:-}" = "--selftest" ]; then
  exec python3 "$HERE/cli_coverage_check.py" --selftest
fi
exec python3 "$HERE/cli_coverage_check.py" "$ROOT" --source "$SRC"
