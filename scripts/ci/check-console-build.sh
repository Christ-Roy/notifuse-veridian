#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────
# check-console-build.sh — garde-fou « la console boote »
#
# Pourquoi ce script existe (incident 2026-05-22) :
#   Le code-splitting Vite (manualChunks) a été mal configuré : React et
#   TanStack/Antd se sont retrouvés dans des chunks SÉPARÉS. Rollup ne
#   garantit pas l'ordre d'évaluation entre chunks frères → le chunk
#   `tanstack` s'exécutait avant que React soit défini → crash au boot
#   « Cannot read properties of undefined (reading createContext) ».
#   La console est restée morte EN PROD, et les 225 tests unitaires
#   (Vitest/jsdom) ne l'ont PAS vu : ils montent les composants depuis le
#   code source, jamais depuis le build découpé en chunks.
#
# Ce que ce script vérifie — analyse STATIQUE du dist/, zéro navigateur,
# rapide (≈ durée du build) :
#   1. Le build réussit.
#   2. index.html existe et référence ≥1 chunk JS.
#   3. Tous les chunks JS/CSS référencés par index.html existent sur disque.
#   4. Aucun chunk JS vide (un chunk vide = manualChunks mal découpé).
#   5. INTÉGRITÉ REACT : si manualChunks a créé un chunk « react-vendor »,
#      AUCUN autre chunk vendor (antd, tanstack, ant-design…) ne doit
#      exister séparément — tout ce qui appelle createContext au
#      top-level doit être dans le MÊME chunk que React. C'est LE check
#      qui aurait bloqué l'incident.
#
# Exit 0 = build sain. Exit ≠ 0 = build cassé, push/CI doivent bloquer.
#
# Usage :  scripts/ci/check-console-build.sh
#          SKIP_BUILD=1 scripts/ci/check-console-build.sh   (réutilise dist/)
# ─────────────────────────────────────────────────────────────────────────
set -euo pipefail

RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; BLUE=$'\033[0;34m'; NC=$'\033[0m'

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONSOLE_DIR="$REPO_ROOT/console"
DIST_DIR="$CONSOLE_DIR/dist"

fail() { echo "${RED}✗ $1${NC}"; exit 1; }
ok()   { echo "${GREEN}✓ $1${NC}"; }

echo "${BLUE}── check-console-build : garde-fou « la console boote » ──${NC}"

[ -d "$CONSOLE_DIR" ] || fail "console/ introuvable ($CONSOLE_DIR)"

# ── 1. Build ─────────────────────────────────────────────────────────────
if [ "${SKIP_BUILD:-0}" = "1" ] && [ -d "$DIST_DIR" ]; then
  echo "  ${YELLOW}SKIP_BUILD=1 → réutilise le dist/ existant${NC}"
else
  echo "  build de la console (npm run build)…"
  if ! ( cd "$CONSOLE_DIR" && npm run build >/tmp/console-build.log 2>&1 ); then
    echo "${RED}--- npm run build a échoué : ---${NC}"
    tail -30 /tmp/console-build.log
    fail "le build de la console échoue"
  fi
  ok "build réussi"
fi

[ -d "$DIST_DIR" ]               || fail "dist/ absent après build"
INDEX="$DIST_DIR/index.html"
[ -f "$INDEX" ]                  || fail "dist/index.html absent"

# ── 2. index.html référence des chunks ───────────────────────────────────
REFERENCED_JS=$(grep -oE 'assets/[A-Za-z0-9._-]+\.js' "$INDEX" | sort -u || true)
[ -n "$REFERENCED_JS" ]          || fail "dist/index.html ne référence aucun chunk JS"
JS_COUNT=$(echo "$REFERENCED_JS" | wc -l | tr -d ' ')
ok "index.html référence $JS_COUNT chunk(s) JS"

# ── 3. Tous les assets référencés existent ───────────────────────────────
MISSING=0
for asset in $(grep -oE 'assets/[A-Za-z0-9._-]+\.(js|css)' "$INDEX" | sort -u); do
  if [ ! -f "$DIST_DIR/$asset" ]; then
    echo "${RED}  ✗ asset référencé mais absent du disque : $asset${NC}"
    MISSING=1
  fi
done
[ "$MISSING" = "0" ]             || fail "des assets référencés par index.html sont absents"
ok "tous les assets référencés existent sur disque"

# ── 4. Aucun chunk JS vide ───────────────────────────────────────────────
EMPTY=0
for js in "$DIST_DIR"/assets/*.js; do
  [ -f "$js" ] || continue
  # un chunk de < 30 octets est forcément vide/dégénéré
  if [ "$(wc -c < "$js")" -lt 30 ]; then
    echo "${RED}  ✗ chunk JS quasi vide : $(basename "$js") ($(wc -c < "$js") octets)${NC}"
    EMPTY=1
  fi
done
[ "$EMPTY" = "0" ]               || fail "un ou plusieurs chunks JS sont vides (manualChunks mal découpé ?)"
ok "aucun chunk JS vide"

# ── 5. Intégrité du chunk React (LE check de l'incident) ─────────────────
# Règle : si un chunk « react-vendor » existe, alors React ET tout
# l'écosystème qui appelle React.createContext au top-level (antd,
# tanstack, ant-design, fortawesome) doit y être fusionné. Un chunk
# vendor séparé pour l'un d'eux = bug d'ordre d'évaluation potentiel.
REACT_VENDOR=$(ls "$DIST_DIR"/assets/react-vendor-*.js 2>/dev/null | head -1 || true)
if [ -n "$REACT_VENDOR" ]; then
  STRAY=""
  for forbidden in tanstack antd ant-design fortawesome react-dom; do
    HIT=$(ls "$DIST_DIR"/assets/${forbidden}-*.js 2>/dev/null | head -1 || true)
    [ -n "$HIT" ] && STRAY="$STRAY $(basename "$HIT")"
  done
  if [ -n "$STRAY" ]; then
    echo "${RED}  ✗ chunk(s) vendor séparé(s) de react-vendor :$STRAY${NC}"
    echo "${YELLOW}    Risque : Rollup ne garantit pas l'ordre entre chunks frères.${NC}"
    echo "${YELLOW}    Un chunk qui appelle React.createContext peut s'exécuter${NC}"
    echo "${YELLOW}    avant React → crash boot « createContext of undefined ».${NC}"
    echo "${YELLOW}    Fix : fusionner ces libs dans react-vendor (cf. vite.config.ts).${NC}"
    fail "intégrité du chunk React violée — la console risque de ne pas booter"
  fi
  ok "intégrité React OK : react-vendor atomique, aucun vendor React fragmenté"
else
  # Pas de manualChunks react-vendor : soit bundle monolithique (pas de
  # risque d'ordre), soit autre stratégie — on ne bloque pas.
  echo "  ${YELLOW}(pas de chunk react-vendor — check d'intégrité React non applicable)${NC}"
fi

echo "${GREEN}── check-console-build : la console est buildable et structurellement saine ──${NC}"
exit 0
