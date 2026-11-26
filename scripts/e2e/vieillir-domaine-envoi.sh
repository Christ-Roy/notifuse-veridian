#!/usr/bin/env bash
# VER-166, épreuve de la revérification à l'usage : fait remonter de 25 h le DERNIER CONTRÔLE d'UN domaine d'envoi d'épreuve
# (le temps qui passe, sans attendre 24 h). Une seule ligne, désignée par son id ET son espace ET son nom de domaine d'épreuve ;
# refuse tout le reste. Mot de passe lu dans Nomad, jamais affiché ; script temporaire effacé. Usage : vieillir_domaine.sh <id> <espace>
set -uo pipefail
ID="$1"; WS="$2"
[[ "$ID" =~ ^[0-9a-f-]{36}$ ]] || { echo "id invalide"; exit 2; }
[[ "$WS" =~ ^e2esso[a-z0-9]+$ ]] || { echo "espace d'épreuve seulement (e2esso…)"; exit 2; }
PW=$(nomad-v raw var get -item PG_SAAS_PASSWORD nomad/jobs/notifuse 2>/dev/null)
[ -n "$PW" ] || { echo "NON ABOUTI : mot de passe introuvable"; exit 2; }
TMP=$(mktemp); chmod 600 "$TMP"; trap 'rm -f "$TMP"' EXIT
{ printf "export PGPASSWORD='%s'\n" "$PW"
  echo "psql -h 172.26.64.1 -p 15432 -U notifuse -d notifuse_system -At -v ON_ERROR_STOP=1 <<'FIN_SQL'"
  echo "UPDATE veridian_sending_domain SET last_checked_at = last_checked_at - INTERVAL '25 hours'"
  echo " WHERE id = '$ID' AND workspace_id = '$WS' AND domain = 'essai-envoi.veridian.site' RETURNING id, status, last_checked_at;"
  echo "FIN_SQL"; } > "$TMP"
for i in 1 2 3 4 5 6 7 8; do
  export NOMAD_EXEC_TOKEN=$(nomad-v acl mint exec 2>/dev/null)
  out=$(nomad-v exec -i -task pg-saas-replica 29c33426 sh -s < "$TMP" 2>&1 | grep -v -i "experimental\|trace-warnings")
  if ! grep -q "ressource occupee" <<<"$out"; then echo "$out"; exit 0; fi
  sleep 20
done
echo "NON ABOUTI : verrou control-plane toujours pris"; exit 2
