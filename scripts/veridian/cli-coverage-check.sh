#!/bin/sh
# cli-coverage-check.sh : couverture CLI embarquée (fiche 76, Constitution CI).
#   - chaque route /api/* du routeur Go a une commande dans internal/http/agentcli ;
#   - la CLI embarquée correspond à son MANIFEST, et à la source de vérité si elle est
#     présente (NOTIFUSE_CLI_SOURCE, défaut ~/.claude/skills/notifuse-cli/bin) ;
#   - la CLI s'exécute avec les seuls fichiers distribués ;
#   - l'OpenAPI se regroupe sans --force et openapi.json versionné en est le bundle (openapi_bundle_check.py).
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
# Le contrat d'API publié suit le routeur : l'OpenAPI se regroupe SANS --force et openapi.json en est le reflet
# (openapi_bundle_check.py, 35 références cassées relevées le 26/11/2026). Les deux contrôles tournent ; le pire l'emporte
# (1 ROUGE > 2 NON ABOUTI > 0 VERT).
pire() { if [ "$1" = 1 ] || [ "$2" = 1 ]; then echo 1; elif [ "$1" = 2 ] || [ "$2" = 2 ]; then echo 2; else echo 0; fi; }
if [ "${1:-}" = "--selftest" ]; then
  python3 "$HERE/cli_coverage_check.py" --selftest; a=$?
  python3 "$HERE/openapi_bundle_check.py" "$ROOT" --selftest; b=$?
  exit "$(pire "$a" "$b")"
fi
python3 "$HERE/cli_coverage_check.py" "$ROOT" --source "$SRC"; a=$?
python3 "$HERE/openapi_bundle_check.py" "$ROOT"; b=$?
exit "$(pire "$a" "$b")"
