#!/usr/bin/env bash
# check-i18n-extracted.sh : les catalogues .po sont-ils a jour des t`...` du code, et fr.po complet ?
# Meme controle que l'etape 6 de check-console-build.sh et les tests de completude (CI), lancable en local AVANT le push
# (hook .husky/pre-push, ou a la main). Ne cree aucune tache planifiee.
#   exit 0 = a jour ; exit 1 = catalogues obsoletes (les .po sont mis a jour sur place : committe-les) ou msgstr FR vides ;
#   exit 2 = non abouti (extract impossible).
#
# 27/11/2026 : sur le bastion, console/node_modules n'existe pas ; le controle sortait TOUJOURS « non abouti » et laissait
# passer, la CI refusait 20 min plus tard. Desormais, sans node_modules, l'extraction part par lourd (rise ou node02,
# scripts/ci/lourd-ou-local.sh) sur une copie de l'arbre de travail, et son diff des .po revient en patch applique ici.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 2
LOC=console/src/i18n/locales
before="$(git diff -- "$LOC" | md5sum)"

if [ -d console/node_modules ]; then
  (cd console && npx lingui extract >/tmp/lingui-extract-local.log 2>&1) \
    || { echo "check-i18n-extracted : NON ABOUTI (lingui extract a echoue, voir /tmp/lingui-extract-local.log)" >&2; exit 2; }
else
  OUT="$(mktemp)"; PATCH="$(mktemp)"; trap 'rm -f "$OUT" "$PATCH"' EXIT
  echo "check-i18n-extracted : console/node_modules absent ici, extraction par lourd" >&2
  "$ROOT/scripts/ci/lourd-ou-local.sh" 'cd console && npm ci --no-audit --no-fund >/dev/null 2>&1 && mkdir -p /tmp/i18n-orig && cp src/i18n/locales/*.po /tmp/i18n-orig/ && npx lingui extract >/tmp/i18n-extract.log 2>&1 || { tail -5 /tmp/i18n-extract.log >&2; exit 3; }; echo "===PATCH-I18N==="; for f in src/i18n/locales/*.po; do diff -u --label "a/console/$f" --label "b/console/$f" "/tmp/i18n-orig/$(basename "$f")" "$f"; done; echo "===FIN-I18N==="' >"$OUT"
  rc=$?
  grep -q '^===FIN-I18N===$' "$OUT" || { echo "check-i18n-extracted : NON ABOUTI (extraction distante sans resultat, code $rc)" >&2; exit 2; }
  sed -n '/^===PATCH-I18N===$/,/^===FIN-I18N===$/p' "$OUT" | sed '1d;$d' >"$PATCH"
  if [ -s "$PATCH" ]; then
    patch -p1 -s --no-backup-if-mismatch <"$PATCH" \
      || { echo "check-i18n-extracted : NON ABOUTI (le patch des .po ne s'applique pas sur l'arbre local)" >&2; exit 2; }
  fi
fi

after="$(git diff -- "$LOC" | md5sum)"
if [ "$before" != "$after" ]; then
  echo "check-i18n-extracted : catalogues .po obsoletes, ils viennent d'etre mis a jour." >&2
  echo "  Traduis les msgstr vides de fr.po, puis : git add $LOC && git commit" >&2
  exit 1
fi
# fr.po : aucun msgstr vide pour un msgid non vide (la CI le refuse par ses tests de completude).
vides="$(python3 - "$LOC/fr.po" <<'PY'
import re, sys
s = open(sys.argv[1], encoding="utf-8").read()
for bloc in s.split("\n\n"):
    if re.search(r'^#~', bloc, re.M):
        continue
    m = re.search(r'^msgid "(.+)"\nmsgstr ""\s*$', bloc, re.M)
    if m and not re.search(r'^msgstr ""\n"', bloc, re.M):
        print(m.group(1))
PY
)"
if [ -n "$vides" ]; then
  echo "check-i18n-extracted : fr.po a des msgstr vides :" >&2
  echo "$vides" | head -20 | sed 's/^/  /' >&2
  exit 1
fi
echo "check-i18n-extracted : catalogues a jour, fr.po complet"
