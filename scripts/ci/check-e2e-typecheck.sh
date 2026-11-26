#!/usr/bin/env bash
# check-e2e-typecheck.sh : typecheck TS des E2E (tests/e2e-veridian) + tests de l'aide de signature Hub.
# Lance par .husky/pre-push quand le push touche tests/e2e-veridian. Passe par `lourd` (jamais en local sur le bastion).
#   exit 0 = vert ; exit 1 = rouge ; exit 2 = non abouti (lourd absent ou noeud injoignable). Aucune tache planifiee.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 2
L="$(command -v lourd 2>/dev/null || echo "$HOME/bin/lourd")"
[ -x "$L" ] || { echo "check-e2e-typecheck : NON ABOUTI (lourd introuvable)" >&2; exit 2; }
out="$("$L" sh -c 'cd tests/e2e-veridian && npm ci --no-audit --no-fund >/dev/null 2>&1 && npx tsc --noEmit -p . && node --experimental-strip-types --test hub-signature.test.mjs prod-residue.test.mjs 2>&1 | tail -15' 2>&1)"
rc=$?
echo "$out" | tail -20 >&2
[ "$rc" = "255" ] && { echo "check-e2e-typecheck : NON ABOUTI (noeud injoignable)" >&2; exit 2; }
[ "$rc" = "0" ] && echo "$out" | grep -q "^# fail 0" || { echo "check-e2e-typecheck : ROUGE" >&2; exit 1; }
echo "check-e2e-typecheck : vert"
