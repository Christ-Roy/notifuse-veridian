#!/usr/bin/env bash
# test-staging-stable.sh : éprouve scripts/ci/staging-stable.sh contre un faux serveur local (aucun réseau sortant).
#   mode « redirige » : toute route rend 302 (page d'attente, bascule d'allocation) -> la garde doit échouer (rc 1) ;
#   mode « bascule »  : une lecture sur trois redirige -> le compteur repart de zéro, jamais N saines d'affilée -> rc 1 ;
#   mode « sain »     : tout répond comme l'app -> rc 0.
# Faux serveur en Node (présent sur tout runner qui joue l'E2E), pas Python. Échoue si un seul cas ne rend pas le code attendu.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"; trap 'kill "${PID:-}" 2>/dev/null; rm -rf "$TMP"' EXIT
cat >"$TMP/srv.js" <<'FIN_SERVEUR'
const http = require('http')
const port = Number(process.argv[2]), MODE = process.argv[3]
let n = 0
http.createServer((q, r) => {
  const rep = (code, body = '', ct = 'application/json', loc) => {
    r.writeHead(code, Object.assign({ 'Content-Type': ct }, loc ? { Location: loc } : {}))
    r.end(body)
  }
  if (q.url.startsWith('/api/version')) n++
  if (MODE === 'redirige' || (MODE === 'bascule' && n % 3 === 0)) return rep(302, '', 'text/html', '/attente')
  if (q.url.startsWith('/api/version')) return rep(200, JSON.stringify({ git_sha: 'abcdef1234' }))
  if (q.url.startsWith('/api/setup.status')) return rep(200, '{"is_installed":true}')
  if (q.url.startsWith('/api/transactional.send')) return rep(401, '{"error":"unauthorized"}')
  if (q.url.startsWith('/console/')) return rep(200, '<html><script src="/config.js"></script></html>', 'text/html')
  rep(404, '404 page not found', 'text/plain')
}).listen(port, '127.0.0.1')
FIN_SERVEUR
# 27/11 : sur le runner JIT, le cas « sain » rendait rc 1 (« version injoignable ») : le faux serveur n'était pas joint
# (proxy de l'environnement ou démarrage lent). Les cas « redirige » et « bascule » passaient alors À VIDE. Désormais :
# aucun proxy pour la boucle locale, et le serveur doit RÉPONDRE (200 sur /api/version en mode sain) avant tout jugement,
# sinon NON ABOUTI (rc 2), jamais un vert ni un rouge de complaisance.
export NO_PROXY="127.0.0.1,localhost${NO_PROXY:+,$NO_PROXY}" no_proxy="127.0.0.1,localhost${no_proxy:+,$no_proxy}"
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy
echec=0
cas() { # cas <mode> <rc attendu>
  local port=$((20000 + RANDOM % 20000)) pret=0
  node "$TMP/srv.js" "$port" "$1" & PID=$!
  for _ in $(seq 1 50); do
    curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/console/" 2>/dev/null | grep -qE '^(200|302)$' && { pret=1; break; }
    sleep 0.2
  done
  if [ "$pret" != 1 ]; then echo "NON ABOUTI : faux serveur injoignable sur 127.0.0.1:$port (mode $1)"; kill "$PID" 2>/dev/null; exit 2; fi
  STABLE_N=4 STABLE_PAS=0.2 STABLE_BORNE=4 "$HERE/staging-stable.sh" "http://127.0.0.1:$port" abcdef12 >"$TMP/out" 2>&1; rc=$?
  kill "$PID" 2>/dev/null; wait "$PID" 2>/dev/null
  if [ "$rc" = "$2" ]; then echo "OK   $1 -> rc $rc"; else echo "ROUGE $1 -> rc $rc (attendu $2)"; tail -3 "$TMP/out"; echec=1; fi
}
cas redirige 1
cas bascule 1
cas sain 0
exit "$echec"
