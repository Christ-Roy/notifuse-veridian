#!/usr/bin/env bash
# check-i18n-extracted.sh : les catalogues .po sont-ils a jour des t`...` du code ?
# Meme controle que l'etape 6 de check-console-build.sh (CI), lancable en local AVANT le push
# (hook .husky/pre-push, ou a la main). Ne cree aucune tache planifiee.
#   exit 0 = a jour ; exit 1 = catalogues obsoletes (les .po sont mis a jour sur place :
#   committe-les) ; exit 2 = non abouti (extract impossible : node_modules absent...).
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT/console" || exit 2
[ -d node_modules ] || { echo "check-i18n-extracted : NON ABOUTI (console/node_modules absent, npm ci)" >&2; exit 2; }
before="$(cd "$ROOT" && git diff -- console/src/i18n/locales | md5sum)"
npx lingui extract >/tmp/lingui-extract-local.log 2>&1 || { echo "check-i18n-extracted : NON ABOUTI (lingui extract a echoue, voir /tmp/lingui-extract-local.log)" >&2; exit 2; }
after="$(cd "$ROOT" && git diff -- console/src/i18n/locales | md5sum)"
if [ "$before" != "$after" ]; then
  echo "check-i18n-extracted : catalogues .po obsoletes, ils viennent d'etre mis a jour." >&2
  echo "  Traduis les msgstr vides de fr.po, puis : git add console/src/i18n/locales && git commit" >&2
  exit 1
fi
echo "check-i18n-extracted : catalogues a jour"
