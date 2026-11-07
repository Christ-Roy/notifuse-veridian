#!/usr/bin/env sh
# Prouve que check-nomad-contract.sh refuse un jobspec dont le plafond de connexions de l'appli
# (DB_MAX_CONNECTIONS) ne tient pas dans max_connections de PostgreSQL, et l'accepte une fois corrige.
# Incident du 07/11/2026 : PostgreSQL a 100, appli a 100, ~100 workspaces => "too many clients already"
# sur des workspaces clients. Lance a la main ou en CI ; ne cree aucune tache planifiee.
set -eu
cd "$(dirname "$0")/../.."
root="$(pwd)"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
fail=0
expect() { # expect <ok|ko> <label> <file>
  if sh "$root/scripts/ci/check-nomad-contract.sh" "$3" >"$tmp/out" 2>&1; then got=ok; else got=ko; fi
  if [ "$got" = "$1" ]; then echo "OK   $2 ($got)"; else echo "FAIL $2: attendu $1, obtenu $got"; sed 's/^/     /' "$tmp/out"; fail=1; fi
}
mkdir -p "$tmp/deploy"; cp deploy/*.nomad.hcl "$tmp/deploy/"; cd "$tmp"
prod=deploy/notifuse.nomad.hcl
expect ok "jobspec prod du depot" "$prod"
sed -i 's/"max_connections=[0-9]*"/"max_connections=100"/' "$prod"
expect ko "appli (250) au-dessus de PostgreSQL (100)" "$prod"
cp "$root/deploy/notifuse.nomad.hcl" "$prod"; sed -i '/^DB_MAX_CONNECTIONS=/d' "$prod"
expect ko "DB_MAX_CONNECTIONS non declare (defaut 100)" "$prod"
cp "$root/deploy/notifuse.nomad.hcl" "$prod"; sed -i 's/"-c", "max_connections=[0-9]*",//' "$prod"
expect ko "max_connections de PostgreSQL non declare (defaut 100)" "$prod"
exit "$fail"
