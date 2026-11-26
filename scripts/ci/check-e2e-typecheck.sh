#!/usr/bin/env bash
# check-e2e-typecheck.sh : typecheck TS des E2E (tests/e2e-veridian) + tests de l'aide de signature Hub.
# Lance par .husky/pre-push quand le push touche tests/e2e-veridian, par scripts/ci/lourd-ou-local.sh : lourd sur le bastion,
# sur place ailleurs, et relancé ici en nice 19 si lourd n'a rien fait tourner (jamais sauté). Aucune tache planifiee.
#   exit 0 = vert ; autre = rouge.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1
"$ROOT/scripts/ci/lourd-ou-local.sh" 'set -o pipefail; cd tests/e2e-veridian && npm ci --no-audit --no-fund >/dev/null 2>&1 && npx tsc --noEmit -p . && node --experimental-strip-types --test hub-signature.test.mjs prod-residue.test.mjs 2>&1 | tail -15'
rc=$?
[ "$rc" = 0 ] || { echo "check-e2e-typecheck : ROUGE (code $rc)" >&2; exit 1; }
echo "check-e2e-typecheck : vert"
