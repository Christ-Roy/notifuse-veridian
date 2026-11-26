#!/usr/bin/env bash
# prepush-go-tests.sh : les tests Go des paquets que le push modifie, avant qu'il parte (pre-push Husky).
# Avant le 26/11/2026, le pre-push ne lançait AUCUN test Go : un test cassé n'était vu qu'en CI, 20 à 30 min plus tard.
# Paquets = dossiers des .go modifiés entre l'amont et HEAD (tests compris). go.mod ou go.sum modifiés : `go build ./...` en plus.
# Lance par scripts/ci/lourd-ou-local.sh --go (lourd sur le bastion, sur place ailleurs). La CI rejoue la suite entière.
# Codes : 0 vert (ou rien à tester), 1 rouge.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 1
BASE="$(git rev-parse '@{upstream}' 2>/dev/null || git rev-parse origin/veridian 2>/dev/null)"
[ -n "$BASE" ] || { echo "prepush-go-tests : amont introuvable, tests Go de tout le dépôt" >&2; BASE=""; }
if [ -n "$BASE" ]; then
  CHANGED="$(git diff --name-only "$BASE"...HEAD -- '*.go' go.mod go.sum)"
else
  CHANGED="go.mod"
fi
[ -n "$CHANGED" ] || { echo "prepush-go-tests : aucun fichier Go dans le push"; exit 0; }
PKGS=""
for f in $CHANGED; do
  case "$f" in *.go) d="$(dirname "$f")"; [ -d "$d" ] && ls "$d"/*.go >/dev/null 2>&1 && PKGS="$PKGS ./$d" ;; esac
done
PKGS="$(echo "$PKGS" | tr ' ' '\n' | grep -v '^$' | sort -u | tr '\n' ' ')"
BUILD=0; echo "$CHANGED" | grep -q -E '^go\.(mod|sum)$' && BUILD=1
# Le module Go est à la racine ; les sous-modules (telemetry/...) se testent dans leur propre dossier : ici, seulement ceux du module racine.
PKGS="$(for p in $PKGS; do d="${p#./}"; m="$d"; while [ "$m" != "." ] && [ ! -f "$m/go.mod" ]; do m="$(dirname "$m")"; done; [ "$m" = "." ] && echo "$p"; done | tr '\n' ' ')"
echo "prepush-go-tests : paquets ${PKGS:-aucun}$([ "$BUILD" = 1 ] && echo ' + go build ./...')"
cat > /tmp/prepush-go-tests.$$ <<EOF
set -e
$([ "$BUILD" = 1 ] && echo 'go build ./...')
$([ -n "$PKGS" ] && echo "go vet $PKGS && go test -count=1 $PKGS")
EOF
CMD="$(cat /tmp/prepush-go-tests.$$)"; rm -f /tmp/prepush-go-tests.$$
[ -n "$(echo "$CMD" | grep -v -E '^(set -e)?$')" ] || { echo "prepush-go-tests : rien à lancer"; exit 0; }
exec "$ROOT/scripts/ci/lourd-ou-local.sh" --go "$CMD"
