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
echec=0
cas() { # cas <mode> <rc attendu>
  local port=$((20000 + RANDOM % 20000))
  node "$TMP/srv.js" "$port" "$1" & PID=$!
  for _ in 1 2 3 4 5 6 7 8 9 10; do curl -s -o /dev/null "http://127.0.0.1:$port/" && break; sleep 0.2; done
  STABLE_N=4 STABLE_PAS=0.2 STABLE_BORNE=4 "$HERE/staging-stable.sh" "http://127.0.0.1:$port" abcdef12 >"$TMP/out" 2>&1; rc=$?
  kill "$PID" 2>/dev/null; wait "$PID" 2>/dev/null
  if [ "$rc" = "$2" ]; then echo "OK   $1 -> rc $rc"; else echo "ROUGE $1 -> rc $rc (attendu $2)"; tail -3 "$TMP/out"; echec=1; fi
}
cas redirige 1
cas bascule 1
cas sain 0
exit "$echec"
