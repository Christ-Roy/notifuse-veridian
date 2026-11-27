#!/usr/bin/env bash
# staging-stable.sh <base_url> <sha_attendu> : n'autorise l'E2E que sur une staging STABLE, pas seulement réveillée.
#
# Mesuré le 27/11/2026 (run 45912109689, infra) : le déploiement v1100 rend la main à 23:47:05Z, l'ancienne allocation se vide
# et le routage bascule pendant ~30 s. L'ancienne garde passait dès qu'UNE lecture voyait le bon SHA ; l'E2E démarrait à
# 23:47:42Z et prenait « Failed to fetch » et « redirect count exceeded » sur ses premiers tests, puis 279 tests passaient.
#
# Une lecture est saine si, SANS suivre de redirection :
#   1. /api/version sert le SHA attendu ;
#   2. /api/setup.status rend le JSON de l'application (la page d'attente de Sablier est du HTML) ;
#   3. POST /api/transactional.send (clé bidon) rend du JSON et pas 404 (routes upstream montées) ;
#   4. /console/ rend 200 et la page de la console (marqueur /config.js), pas une redirection.
# Il faut STABLE_N lectures saines CONSÉCUTIVES (défaut 6, une toutes les STABLE_PAS s, défaut 5) ; un seul échec remet le
# compteur à zéro. Borne STABLE_BORNE s (défaut 180), puis échec franc : jamais de relance silencieuse.
# Codes : 0 stable, 1 jamais stable dans la borne, 2 usage.
set -uo pipefail
BASE="${1:-}"; SHA="${2:-}"
[ -n "$BASE" ] && [ -n "$SHA" ] || { echo "usage : staging-stable.sh <base_url> <sha_attendu>" >&2; exit 2; }
N="${STABLE_N:-6}"; PAS="${STABLE_PAS:-5}"; BORNE="${STABLE_BORNE:-180}"
C=(curl -sS --max-time 10 --max-redirs 0)
# Boucle locale (épreuve) : jamais par un proxy.
case "$BASE" in http://127.0.0.1*|http://localhost*) C+=(--noproxy "*") ;; esac

lecture() {
  local v s code type console
  v=$("${C[@]}" "$BASE/api/version" 2>/dev/null) || { echo "version injoignable ou redirigée"; return 1; }
  echo "$v" | jq -e --arg sha "$SHA" '.git_sha | type == "string" and startswith($sha)' >/dev/null 2>&1 \
    || { echo "version ≠ $SHA"; return 1; }
  s=$("${C[@]}" "$BASE/api/setup.status" 2>/dev/null) || { echo "setup.status injoignable ou redirigé"; return 1; }
  echo "$s" | jq -e 'type == "object" and has("is_installed")' >/dev/null 2>&1 || { echo "setup.status n'est pas le JSON de l'app"; return 1; }
  read -r code type < <("${C[@]}" -o /dev/null -w '%{http_code} %{content_type}' -X POST -H 'Content-Type: application/json' \
    -H 'Authorization: Bearer health-check-fake-key' -d '{}' "$BASE/api/transactional.send" 2>/dev/null || echo "000 -")
  { [ "$code" != 404 ] && [ "${code:0:1}" != 3 ] && [ "$code" != 000 ] && [[ "$type" == application/json* ]]; } \
    || { echo "transactional.send → $code $type"; return 1; }
  console=$("${C[@]}" -w '\n%{http_code}' "$BASE/console/" 2>/dev/null) || { echo "console injoignable ou redirigée"; return 1; }
  [ "${console##*$'\n'}" = 200 ] && grep -q '/config.js' <<<"$console" || { echo "console ≠ 200 ou sans marqueur"; return 1; }
  return 0
}

debut=$(date +%s); suite=0
while :; do
  if motif=$(lecture); then
    suite=$((suite + 1)); echo "  lecture saine $suite/$N"
    [ "$suite" -ge "$N" ] && { echo "Staging STABLE sur $SHA : $N lectures saines consécutives en $(( $(date +%s) - debut )) s"; exit 0; }
  else
    [ "$suite" -gt 0 ] && echo "  compteur remis à zéro après $suite lecture(s) : $motif" || echo "  pas encore sain : $motif"
    suite=0
  fi
  [ $(( $(date +%s) - debut )) -ge "$BORNE" ] && { echo "::error::Staging jamais stable sur $SHA en ${BORNE} s (dernier motif : ${motif:-?})"; exit 1; }
  sleep "$PAS"
done
