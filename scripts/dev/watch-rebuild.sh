#!/usr/bin/env bash
# watch-rebuild.sh — watcher Go simple et fiable (alternative à Air).
#
# Usage : make dev OU ./scripts/dev/watch-rebuild.sh
#
# Cycle :
#   1. Build initial → start server
#   2. inotifywait monitor, à chaque change : kill server + rebuild + restart
#
# Le serveur écrit ses logs dans tmp/server.log — tail -f dans un autre
# terminal pour les voir.

# IMPORTANT : pas de set -a/set -e qui pollue l'env shell. On stay simple.

BIN="./tmp/main-dev"
mkdir -p tmp

cleanup() {
  echo ""
  echo "→ Cleanup..."
  [ -n "${SERVER_PID:-}" ] && kill -9 "$SERVER_PID" 2>/dev/null
  pkill -9 -f "$BIN" 2>/dev/null
  exit 0
}
trap cleanup INT TERM

build_and_run() {
  # Kill l'ancien serveur s'il existe
  if [ -n "${SERVER_PID:-}" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill -9 "$SERVER_PID" 2>/dev/null
    wait "$SERVER_PID" 2>/dev/null
  fi
  pkill -9 -f "$BIN" 2>/dev/null
  sleep 0.2

  # Rebuild
  echo "→ Building..."
  if ! go build -o "$BIN" ./cmd/api 2>&1; then
    echo "❌ Build failed — will retry on next change"
    return 1
  fi
  echo "✓ Built ($(date +%H:%M:%S))"

  # Start serveur
  "$BIN" > tmp/server.log 2>&1 &
  SERVER_PID=$!
  echo "→ Server started (PID $SERVER_PID) — tail -f tmp/server.log"
}

# Vérif inotifywait
if ! command -v inotifywait >/dev/null 2>&1; then
  echo "❌ inotifywait missing : apt install inotify-tools"
  exit 1
fi

# Initial run
build_and_run || { echo "Fix the build error first"; exit 1; }

echo "→ Watching internal/ pkg/ cmd/ config/ for *.go (excluding _test.go)"
echo "→ Ctrl+C to stop"
echo ""

# Boucle simple — inotifywait avec -e modify,close_write
# (close_write capture les "save" éditeurs même sans modify event)
inotifywait -mr -e close_write,modify,move \
  --include '\.go$' \
  --exclude '_test\.go$|/tmp/|/node_modules/|/\.git/' \
  internal pkg cmd config 2>/dev/null |
while read -r path event file; do
  # Petit debounce — attendre 200ms qu'un éventuel save groupé soit fini
  sleep 0.2
  # Drain les events en attente
  read -r -t 0.1 _ 2>/dev/null || true

  echo ""
  echo "═══ CHANGED: $path$file ($event) at $(date +%H:%M:%S) ═══"
  build_and_run
done
