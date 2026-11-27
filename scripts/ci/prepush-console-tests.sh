#!/usr/bin/env bash
# prepush-console-tests.sh : les tests vitest de la console LIÉS aux fichiers que le push modifie, avant qu'il parte (pre-push).
# Avant le 27/11/2026, aucun vitest au pre-push : un test console cassé n'était vu qu'en CI, 20 à 30 min plus tard
# (job « Console : tests unitaires »). Ici : `vitest run` sur les tests liés aux fichiers console/src modifiés entre l'amont et HEAD
# (le test lui-même s'il est modifié, et tout test qui importe un fichier modifié). La CI rejoue la suite entière.
# Lancé par scripts/ci/lourd-ou-local.sh : lourd sur le bastion (npm ci sur le nœud), sur place ailleurs, jamais sauté.
# Codes : 0 vert (ou rien à tester), 1 rouge.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1
BASE="$(git rev-parse '@{upstream}' 2>/dev/null || git rev-parse origin/veridian 2>/dev/null)"
[ -n "$BASE" ] || { echo "prepush-console-tests : amont introuvable, rien de comparé" >&2; exit 1; }
# Seulement les fichiers encore présents (une suppression n'a pas de test lié à jouer).
FICHIERS="$(git diff --name-only --diff-filter=d "$BASE"...HEAD -- 'console/src/*.ts' 'console/src/*.tsx' | sed 's#^console/##')"
[ -n "$FICHIERS" ] || { echo "prepush-console-tests : aucun fichier console/src dans le push"; exit 0; }
# Tests à jouer : ceux du push, plus tout test qui importe un fichier modifié (import dont le chemin finit par son nom).
# Pas `vitest related` : il parcourt le graphe de TOUS les tests sans le plugin Lingui et bute sur les .po (mesuré le 27/11).
TESTS="$(cd console && {
  for f in $FICHIERS; do
    case "$f" in *.test.ts|*.test.tsx|*.spec.ts|*.spec.tsx) echo "$f"; continue ;; esac
    nom="$(basename "$f")"; nom="${nom%.*}"
    grep -rlE --include='*.test.ts' --include='*.test.tsx' --include='*.spec.ts' --include='*.spec.tsx' \
      "from ['\"][^'\"]*/${nom}['\"]" src 2>/dev/null
  done; } | sort -u | tr '\n' ' ')"
[ -n "${TESTS// /}" ] || { echo "prepush-console-tests : aucun test lié à $(echo $FICHIERS)"; exit 0; }
echo "prepush-console-tests : vitest run $TESTS"
"$ROOT/scripts/ci/lourd-ou-local.sh" "set -o pipefail; cd console && npm ci --no-audit --no-fund >/dev/null 2>&1 && npx vitest run $TESTS 2>&1 | tail -25"
rc=$?
[ "$rc" = 0 ] || { echo "prepush-console-tests : ROUGE (code $rc)" >&2; exit 1; }
echo "prepush-console-tests : vert"
