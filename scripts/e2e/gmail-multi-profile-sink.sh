#!/usr/bin/env bash
# E2E staging du contrat multi-profils Gmail, sans aucun envoi externe.
#
# Ce harnais utilise deux intégrations SMTP distinctes qui simulent deux profils
# Gmail, mais leur host est obligatoirement le smtp-sink loopback dans
# l'allocation Nomad staging. Il prouve le contrat backend commun aux profils
# app-password et OAuth, pas l'authentification auprès de Google.
#
# Usage :
#   scripts/e2e/gmail-multi-profile-sink.sh --self-check
#   NOTIFUSE_HUB_API_SECRET=... scripts/e2e/gmail-multi-profile-sink.sh [--keep]
#
# Gate actuel attendu sur une version sans le contrat : échec au round-trip de
# veridian_marketing_email_provider_ids, AVANT toute création de broadcast.
set -euo pipefail

BASE="${NOTIFUSE_URL:-https://notifuse.staging.veridian.site}"
SINK_HOST="${SINK_HOST:-127.0.0.1}"
SINK_PORT="${SINK_PORT:-1025}"
SINK_TASK="${SINK_TASK:-smtp-sink}"
STAGING_JOB="${STAGING_JOB:-notifuse-staging}"
NOMAD_V="/home/brunon5/bin/nomad-v"
KEEP=0
SELF_CHECK=0
STAMP="$(date +%s | tail -c 7)"
WID="gfcheckg${STAMP}"
OWNER_TOKEN=""

while [ $# -gt 0 ]; do
  case "$1" in
    --keep) KEEP=1; shift ;;
    --self-check) SELF_CHECK=1; shift ;;
    *) echo "argument inconnu: $1" >&2; exit 2 ;;
  esac
done

log() { printf '\033[1;34m[gmail-mp]\033[0m %s\n' "$*" >&2; }
ok() { printf '\033[1;32m[gmail-mp PASS]\033[0m %s\n' "$*" >&2; }
fatal() { printf '\033[1;31m[gmail-mp FAIL]\033[0m %s\n' "$*" >&2; exit 1; }
unexpected_error() {
  local rc="$?" line="${BASH_LINENO[0]:-unknown}"
  fatal "arrêt inattendu ligne $line (code $rc)"
}
trap unexpected_error ERR

assert_safe_target() {
  [ "$SINK_HOST" = "127.0.0.1" ] || fatal "SINK_HOST=$SINK_HOST interdit, seul 127.0.0.1 est autorisé"
  [ "$SINK_PORT" = "1025" ] || fatal "SINK_PORT=$SINK_PORT interdit, seul le sink staging :1025 est autorisé"
  [ "$SINK_TASK" = "smtp-sink" ] || fatal "SINK_TASK=$SINK_TASK interdit, task attendue: smtp-sink"
  [ "$STAGING_JOB" = "notifuse-staging" ] || fatal "STAGING_JOB=$STAGING_JOB interdit, job attendu: notifuse-staging"
  case "$BASE" in
    https://notifuse.staging.veridian.site|https://notifuse.staging.veridian.site/) ;;
    *) fatal "NOTIFUSE_URL=$BASE interdit, ce harnais ne tourne qu'en staging" ;;
  esac
}

if [ "$SELF_CHECK" = "1" ]; then
  assert_safe_target
  [ "$WID" != "" ] || fatal "workspace self-check vide"
  ok "garde-fous statiques: staging uniquement, sink loopback uniquement"
  exit 0
fi

assert_safe_target
: "${NOTIFUSE_HUB_API_SECRET:?NOTIFUSE_HUB_API_SECRET requis pour provisionner staging}"

hmac() {
  local path="$1" method="$2" body="$3" ts sig
  ts="$(date +%s%3N)"
  sig="$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$NOTIFUSE_HUB_API_SECRET" -r | awk '{print $1}')"
  curl -fsS -X "$method" "$BASE$path" -H 'content-type: application/json' \
    -H 'x-veridian-app: hub' -H "x-veridian-timestamp: $ts" \
    -H "X-Veridian-Hub-Signature: $sig" -d "$body"
}

api() {
  local raw status body
  raw="$(curl -sS -w $'\n%{http_code}' -X POST "$BASE$1" -H 'content-type: application/json' \
    -H "Authorization: Bearer $OWNER_TOKEN" -d "$2")" || fatal "transport API impossible pour $1"
  status="${raw##*$'\n'}"
  body="${raw%$'\n'*}"
  case "$status" in
    2??) printf '%s' "$body" ;;
    *) fatal "API $1 HTTP $status: ${body:0:500}" ;;
  esac
}

api_get() {
  curl -fsS "$BASE$1" -H "Authorization: Bearer $OWNER_TOKEN"
}

staging_alloc() {
  "$NOMAD_V" raw job allocs -json "$STAGING_JOB" 2>/dev/null \
    | python3 -c 'import json,sys; a=json.load(sys.stdin); r=[x for x in a if x.get("ClientStatus")=="running" and x.get("DesiredStatus")=="run"]; print(r[0]["ID"] if len(r)==1 else "")'
}

psqlq() {
  local alloc
  alloc="$(staging_alloc)"
  [ -n "$alloc" ] || return 1
  "$NOMAD_V" raw alloc exec -task db "$alloc" \
    psql -U postgres -d "notifuse_ws_${WID}" -tAc "$1" 2>/dev/null | tr -d '\r'
}

psql_int() {
  local sql="$1" label="$2" value="" i
  for i in $(seq 1 10); do
    value="$(psqlq "$sql" || true)"
    if [[ "$value" =~ ^[0-9]+$ ]]; then
      printf '%s' "$value"
      return 0
    fi
    sleep 1
  done
  fatal "$label illisible après 10 tentatives (valeur=${value:-vide})"
}

sink_messages() {
  local alloc
  alloc="$(staging_alloc)"
  [ -n "$alloc" ] || return 1
  # La suite CI console utilise le même sink et peut produire >1 Mio pendant
  # ce scénario. Lecture bornée à 8 Mio, puis filtrage immédiat sur le WID.
  "$NOMAD_V" raw alloc logs -task "$SINK_TASK" -tail -c 8388608 "$alloc" 2>&1 \
    | RUN_WID="$WID" python3 -c '
import os,re,sys
data=sys.stdin.read()
for block in re.findall(r"---------- MESSAGE FOLLOWS ----------.*?------------ END MESSAGE ------------", data, re.S):
    if os.environ["RUN_WID"] in block:
        print(block)
'
}

ensure_sink() {
  local alloc state active_count
  assert_safe_target
  active_count="$($NOMAD_V raw job allocs -json "$STAGING_JOB" 2>/dev/null \
    | python3 -c 'import json,sys; a=json.load(sys.stdin); print(sum(x.get("ClientStatus")=="running" and x.get("DesiredStatus")=="run" for x in a))')"
  [ "$active_count" = "1" ] || fatal "$STAGING_JOB instable: $active_count allocation(s) active(s), attendre la fin du rolling deploy"
  alloc="$(staging_alloc)"
  [ -n "$alloc" ] || fatal "allocation running de $STAGING_JOB introuvable"
  state="$($NOMAD_V raw alloc status -json "$alloc" 2>/dev/null \
    | SINK_TASK_NAME="$SINK_TASK" python3 -c 'import json,os,sys; d=json.load(sys.stdin); print((d.get("TaskStates",{}).get(os.environ["SINK_TASK_NAME"],{}) or {}).get("State",""))')"
  [ "$state" = "running" ] || fatal "task $SINK_TASK non running (state=${state:-absent})"
  sink_messages >/dev/null || fatal "logs du sink illisibles"
  ok "sink $SINK_HOST:$SINK_PORT running dans l'allocation staging"
}

cleanup() {
  [ "$KEEP" = "1" ] && { log "workspace $WID conservé (--keep)"; return; }
  [ -n "$WID" ] || return
  log "wipe du workspace jetable $WID"
  hmac /api/veridian/admin/wipe-test-tenants POST \
    "{\"tenant_ids\":[\"$WID\"],\"safety_client_prefixes\":[\"canary\"]}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

open_window='{"days":[0,1,2,3,4,5,6],"start_hour":0,"end_hour":24,"timezone":"UTC"}'
# Fenêtre valide mais certainement fermée pendant le test : même jour UTC,
# créneau d'une heure commençant deux heures dans le futur (ou déjà passé si
# le modulo franchit minuit). Cela évite toute ambiguïté de convention weekday.
closed_day="$(date -u +%w)"
closed_start=$(( (10#$(date -u +%H) + 2) % 24 ))
closed_end=$((closed_start + 1))
closed_window="{\"days\":[$closed_day],\"start_hour\":$closed_start,\"end_hour\":$closed_end,\"timezone\":\"UTC\"}"

provider_json() {
  local sender="$1" cap="$2" window="$3"
  python3 - "$SINK_HOST" "$SINK_PORT" "$sender" "$cap" "$window" <<'PY'
import json,sys
host,port,sender,cap,window=sys.argv[1:]
print(json.dumps({
  "kind":"smtp",
  "smtp":{"host":host,"port":int(port),"use_tls":False},
  "senders":[{"email":sender,"name":"Gmail sink profile","is_default":True}],
  "rate_limit_per_minute":600,
  "veridian_profile_daily_cap":int(cap),
  "veridian_sending_window":json.loads(window),
  "veridian_anti_hash_enabled":False
}))
PY
}

create_profile() {
  local name="$1" sender="$2" cap="$3" window="$4" provider body response
  provider="$(provider_json "$sender" "$cap" "$window")"
  body="$(PROFILE_NAME="$name" PROFILE_PROVIDER="$provider" WID_VALUE="$WID" python3 - <<'PY'
import json,os
print(json.dumps({"workspace_id":os.environ["WID_VALUE"],"name":os.environ["PROFILE_NAME"],"type":"email","provider":json.loads(os.environ["PROFILE_PROVIDER"])}))
PY
)"
  response="$(api /api/workspaces.createIntegration "$body")"
  printf '%s' "$response" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("integration_id",""))'
}

verify_profile() {
  local integration_id="$1" owner_email="$2" response success
  response="$(api /api/email.testProvider "{\"workspace_id\":\"$WID\",\"integration_id\":\"$integration_id\",\"to\":\"$owner_email\"}")"
  success="$(printf '%s' "$response" | python3 -c 'import json,sys; print(str(json.load(sys.stdin).get("success", False)).lower())')"
  [ "$success" = "true" ] || fatal "vérification transport du profil $integration_id impossible: ${response:0:200}"
}

update_profile() {
  local integration_id="$1" name="$2" sender="$3" cap="$4" window="$5" provider body
  provider="$(provider_json "$sender" "$cap" "$window")"
  body="$(WID_VALUE="$WID" INTEGRATION_ID="$integration_id" PROFILE_NAME="$name" PROFILE_PROVIDER="$provider" python3 - <<'PY'
import json,os
print(json.dumps({"workspace_id":os.environ["WID_VALUE"],"integration_id":os.environ["INTEGRATION_ID"],"name":os.environ["PROFILE_NAME"],"provider":json.loads(os.environ["PROFILE_PROVIDER"])}))
PY
)"
  api /api/workspaces.updateIntegration "$body" >/dev/null
}

set_profile_pool() {
  local first_profile="$1" profile_ids_csv="$2" workspace_json body
  workspace_json="$(api_get "/api/workspaces.get?id=$WID")"
  body="$(printf '%s' "$workspace_json" | FIRST_PROFILE="$first_profile" PROFILE_IDS_CSV="$profile_ids_csv" python3 -c '
import json,os,sys
w=json.load(sys.stdin)["workspace"]; s=w["settings"]
s["marketing_email_provider_id"]=os.environ["FIRST_PROFILE"]
s["veridian_marketing_email_provider_ids"]=[x for x in os.environ["PROFILE_IDS_CSV"].split(",") if x]
print(json.dumps({"id":w["id"],"name":w["name"],"settings":s}))
')"
  api /api/workspaces.update "$body" >/dev/null
}

create_list_with_contacts() {
  local list_id="$1" count="$2" contacts i
  api /api/lists.create "{\"workspace_id\":\"$WID\",\"id\":\"$list_id\",\"name\":\"$list_id\",\"is_double_optin\":false,\"is_public\":false}" >/dev/null
  contacts="["
  for i in $(seq 1 "$count"); do
    [ "$i" -gt 1 ] && contacts+=","
    contacts+="{\"email\":\"${list_id}-${i}-${STAMP}@example.com\",\"first_name\":\"Sink${i}\"}"
  done
  contacts+="]"
  api /api/contacts.import "{\"workspace_id\":\"$WID\",\"subscribe_to_lists\":[\"$list_id\"],\"contacts\":$contacts}" >/dev/null
}

fire_broadcast() {
  local name="$1" list_id="$2" response bid
  response="$(api /api/broadcasts.create "{\"workspace_id\":\"$WID\",\"name\":\"$name\",\"audience\":{\"list\":\"$list_id\",\"exclude_unsubscribed\":true},\"test_settings\":{\"enabled\":false,\"sample_percentage\":100,\"variations\":[{\"variation_name\":\"a\",\"template_id\":\"gmail-mp-template\"}]},\"metadata\":{\"veridian_anti_hash_enabled\":false}}")"
  bid="$(printf '%s' "$response" | python3 -c 'import json,sys; d=json.load(sys.stdin); print((d.get("broadcast") or d).get("id",""))')"
  [ -n "$bid" ] || fatal "création broadcast $name impossible: ${response:0:200}"
  api /api/broadcasts.schedule "{\"workspace_id\":\"$WID\",\"id\":\"$bid\",\"send_now\":true}" >/dev/null
  printf '%s' "$bid"
}

wait_for_sql() {
  local sql="$1" expected="$2" label="$3" value="" i
  for i in $(seq 1 40); do
    value="$(psqlq "$sql" || true)"
    [ "$value" = "$expected" ] && { ok "$label = $expected"; return 0; }
    sleep 3
  done
  fatal "$label attendu=$expected observé=${value:-vide}"
}

ensure_sink
log "provision du workspace jetable $WID"
OWNER_EMAIL="${WID}@e2e.veridian.site"
PROVISION="$(hmac /api/tenants/provision POST "{\"tenant_id\":\"$WID\",\"owner_email\":\"$OWNER_EMAIL\",\"plan\":\"free\"}")"
AUTO_URL="$(printf '%s' "$PROVISION" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("auto_login_url",""))')"
[ -n "$AUTO_URL" ] || fatal "provision impossible: ${PROVISION:0:200}"
OWNER_TOKEN="$(curl -fsS "$AUTO_URL" | grep -oP "setItem\('auth_token', \"\K[^\"]+" || true)"
[ -n "$OWNER_TOKEN" ] || fatal "token owner auto-login introuvable"

SENDER_A="gmail-mp-a-${STAMP}@agences-veridian.fr"
SENDER_B="gmail-mp-b-${STAMP}@agences-veridian.fr"
log "création de deux profils indépendants, tous deux pointés vers le sink"
PROFILE_A="$(create_profile gmail-profile-a "$SENDER_A" 30 "$closed_window")"
PROFILE_B="$(create_profile gmail-profile-b "$SENDER_B" 30 "$open_window")"
[ -n "$PROFILE_A" ] && [ -n "$PROFILE_B" ] || fatal "création des deux profils impossible"
[ "$PROFILE_A" != "$PROFILE_B" ] || fatal "les deux profils ont le même integration ID"
verify_profile "$PROFILE_A" "$OWNER_EMAIL"
verify_profile "$PROFILE_B" "$OWNER_EMAIL"
ok "deux profils vérifiés par l'endpoint owner-bound, exclusivement via le sink"

# Sonde write-only séparée, jamais ajoutée aux profils marketing et jamais
# utilisée pour envoyer. Les valeurs sont fausses et restent confinées au
# workspace jetable. Elle transforme le check de secret en preuve réelle.
SECRET_PROBE_BODY="$(WID_VALUE="$WID" SINK_HOST_VALUE="$SINK_HOST" SINK_PORT_VALUE="$SINK_PORT" STAMP_VALUE="$STAMP" python3 - <<'PY'
import json,os
print(json.dumps({
  "workspace_id":os.environ["WID_VALUE"], "name":"gmail-secret-write-only-probe", "type":"email",
  "provider":{
    "kind":"smtp",
    "smtp":{"host":os.environ["SINK_HOST_VALUE"],"port":int(os.environ["SINK_PORT_VALUE"]),"use_tls":False,
            "username":"write-only-probe@example.invalid","password":"fake-app-password-"+os.environ["STAMP_VALUE"]},
    "senders":[{"email":"gmail-mp-secret-probe@agences-veridian.fr","name":"Secret probe","is_default":True}],
    "rate_limit_per_minute":1,
    "veridian_profile_daily_cap":1
  }
}))
PY
)"
SECRET_PROBE_RESPONSE="$(api /api/workspaces.createIntegration "$SECRET_PROBE_BODY")"
SECRET_PROBE_ID="$(printf '%s' "$SECRET_PROBE_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("integration_id",""))')"
[ -n "$SECRET_PROBE_ID" ] || fatal "création de la sonde write-only impossible"

WORKSPACE_JSON="$(api_get "/api/workspaces.get?id=$WID")"
HOSTS="$(printf '%s' "$WORKSPACE_JSON" | PROFILE_A="$PROFILE_A" PROFILE_B="$PROFILE_B" SECRET_PROBE_ID="$SECRET_PROBE_ID" python3 -c '
import json,os,sys
w=json.load(sys.stdin)["workspace"]
wanted={os.environ["PROFILE_A"],os.environ["PROFILE_B"],os.environ["SECRET_PROBE_ID"]}
print(",".join(sorted(i.get("email_provider",{}).get("smtp",{}).get("host","") for i in w.get("integrations",[]) if i.get("id") in wanted)))
')"
[ "$HOSTS" = "127.0.0.1,127.0.0.1,127.0.0.1" ] || fatal "DOUBLE-CHECK anti-fuite: hosts observés '$HOSTS'"

SETTINGS_BODY="$(printf '%s' "$WORKSPACE_JSON" | PROFILE_A="$PROFILE_A" PROFILE_B="$PROFILE_B" python3 -c '
import json,os,sys
w=json.load(sys.stdin)["workspace"]; s=w["settings"]
s["marketing_email_provider_id"]=os.environ["PROFILE_A"]
s["veridian_marketing_email_provider_ids"]=[os.environ["PROFILE_A"],os.environ["PROFILE_B"]]
print(json.dumps({"id":w["id"],"name":w["name"],"settings":s}))
')"
api /api/workspaces.update "$SETTINGS_BODY" >/dev/null

# CONTRAT GATE : sur la base auditée, les champs inconnus sont perdus ici. Le
# script s'arrête donc avant template/list/broadcast et n'envoie strictement rien.
WORKSPACE_JSON="$(api_get "/api/workspaces.get?id=$WID")"
printf '%s' "$WORKSPACE_JSON" | PROFILE_A="$PROFILE_A" PROFILE_B="$PROFILE_B" SECRET_PROBE_ID="$SECRET_PROBE_ID" CLOSED_DAY="$closed_day" CLOSED_START="$closed_start" CLOSED_END="$closed_end" python3 -c '
import json,os,sys
w=json.load(sys.stdin)["workspace"]
expected=[os.environ["PROFILE_A"],os.environ["PROFILE_B"]]
got=w.get("settings",{}).get("veridian_marketing_email_provider_ids")
assert got == expected, f"veridian_marketing_email_provider_ids attendu={expected}, observe={got}"
providers={i["id"]:i.get("email_provider",{}) for i in w.get("integrations",[]) if i.get("id") in expected}
assert set(providers)==set(expected), "profils absents du workspace"
assert all(p.get("veridian_profile_daily_cap")==30 for p in providers.values()), "veridian_profile_daily_cap=30 non persiste"
assert all(p.get("smtp",{}).get("host")=="127.0.0.1" and p.get("smtp",{}).get("port")==1025 for p in providers.values()), "profil actif hors sink"
window=providers[os.environ["PROFILE_A"]].get("veridian_sending_window",{})
assert window.get("days")==[int(os.environ["CLOSED_DAY"])], f"jours fenêtre A altérés: {window}"
assert window.get("start_hour")==int(os.environ["CLOSED_START"]) and window.get("end_hour")==int(os.environ["CLOSED_END"]), f"heures fenêtre A altérées: {window}"
probe=next((i.get("email_provider",{}) for i in w.get("integrations",[]) if i.get("id")==os.environ["SECRET_PROBE_ID"]), None)
assert probe is not None, "sonde write-only absente"
smtp=probe.get("smtp",{})
encrypted_keys=sorted(k for k in smtp if k.startswith("encrypted_"))
assert not encrypted_keys, f"ciphertext expose: {encrypted_keys}"
assert "password" not in smtp and "oauth2_client_secret" not in smtp and "oauth2_refresh_token" not in smtp, "secret runtime expose"
' || fatal "contrat multi-profils absent/incomplet; arrêt sûr avant toute campagne"
ok "contrat API exact et secrets write-only"
api /api/workspaces.deleteIntegration "{\"workspace_id\":\"$WID\",\"integration_id\":\"$SECRET_PROBE_ID\"}" >/dev/null

TEMPLATE="$(python3 - "$WID" <<'PY'
import json,sys
wid=sys.argv[1]
tree={"id":"root","type":"mjml","attributes":{"version":"4.0.0"},"children":[{"id":"body","type":"mj-body","children":[{"id":"section","type":"mj-section","children":[{"id":"column","type":"mj-column","children":[{"id":"text","type":"mj-text","content":"Bonjour {{ contact.first_name }}, preuve sink multi-profils."}]}]}]}]}
print(json.dumps({"workspace_id":wid,"id":"gmail-mp-template","name":"Gmail multi-profile sink","channel":"email","category":"marketing","email":{"sender_id":"default","subject":"Preuve sink multi-profils","text":"Bonjour {{ contact.first_name }}, preuve sink multi-profils.","plain_text_only":True,"visual_editor_tree":tree}}))
PY
)"
api /api/templates.create "$TEMPLATE" >/dev/null

log "phase fenêtre: A fermé sur le créneau UTC courant, B ouvert 24/7"
create_list_with_contacts gmailmpwindow 40
BID_WINDOW="$(fire_broadcast gmail-mp-window gmailmpwindow)"
wait_for_sql "SELECT (SELECT count(*) FROM message_history WHERE broadcast_id='$BID_WINDOW') + (SELECT count(*) FROM email_queue WHERE source_id='$BID_WINDOW')" 40 "40 affectations figées"
A_PENDING="$(psql_int "SELECT count(*) FROM email_queue WHERE source_id='$BID_WINDOW' AND integration_id='$PROFILE_A' AND status='pending'" "pending profil A")"
B_SENT="$(psql_int "SELECT count(*) FROM message_history WHERE broadcast_id='$BID_WINDOW' AND veridian_profile_id='$PROFILE_B'" "historique profil B")"
[ "$A_PENDING" -gt 0 ] || fatal "rotation non prouvée: aucune affectation au profil A sur 40 messages"
[ "$B_SENT" -gt 0 ] || fatal "rotation non prouvée: aucune affectation au profil B sur 40 messages"
[ "$(psqlq "SELECT count(*) FROM message_history WHERE broadcast_id='$BID_WINDOW' AND veridian_profile_id='$PROFILE_A'")" = "0" ] \
  || fatal "profil A fermé a envoyé hors fenêtre"
[ "$(psqlq "SELECT count(*) FROM email_queue WHERE source_id='$BID_WINDOW' AND integration_id='$PROFILE_B'")" = "0" ] \
  || fatal "le profil B ouvert conserve encore une entrée en queue"
ok "rotation prouvée: $A_PENDING messages figés sur A fermé, $B_SENT envoyés par B ouvert"

log "ouverture de A: l'entrée doit repartir sans changer de profil"
update_profile "$PROFILE_A" gmail-profile-a "$SENDER_A" 30 "$open_window"
wait_for_sql "SELECT count(*) FROM message_history WHERE broadcast_id='$BID_WINDOW'" 40 "historique complet après ouverture de A"
A_USED="$(psql_int "SELECT count(*) FROM message_history WHERE veridian_profile_id='$PROFILE_A'" "usage profil A")"
B_USED="$(psql_int "SELECT count(*) FROM message_history WHERE veridian_profile_id='$PROFILE_B'" "usage profil B")"
A_CAP=$((A_USED + 2))
B_CAP=$((B_USED + 2))

log "phase quota: deux envois supplémentaires autorisés par profil, le troisième reporté"
update_profile "$PROFILE_A" gmail-profile-a "$SENDER_A" "$A_CAP" "$open_window"
update_profile "$PROFILE_B" gmail-profile-b "$SENDER_B" "$B_CAP" "$open_window"

set_profile_pool "$PROFILE_A" "$PROFILE_A"
create_list_with_contacts gmailmpcapa 3
BID_CAP_A="$(fire_broadcast gmail-mp-cap-a gmailmpcapa)"
wait_for_sql "SELECT count(*) FROM message_history WHERE veridian_profile_id='$PROFILE_A'" "$A_CAP" "cap atomique profil A"
wait_for_sql "SELECT count(*) FROM email_queue WHERE source_id='$BID_CAP_A' AND integration_id='$PROFILE_A' AND status='pending'" 1 "une entrée A reportée"

set_profile_pool "$PROFILE_B" "$PROFILE_B"
create_list_with_contacts gmailmpcapb 3
BID_CAP_B="$(fire_broadcast gmail-mp-cap-b gmailmpcapb)"
wait_for_sql "SELECT count(*) FROM message_history WHERE veridian_profile_id='$PROFILE_B'" "$B_CAP" "cap atomique profil B"
wait_for_sql "SELECT count(*) FROM email_queue WHERE source_id='$BID_CAP_B' AND integration_id='$PROFILE_B' AND status='pending'" 1 "une entrée B reportée"
wait_for_sql "SELECT count(*) FROM email_queue WHERE status='pending'" 2 "deux entrées reportées par quota"

SINK_DATA="$(sink_messages)"
SINK_COUNT="$(printf '%s' "$SINK_DATA" | grep -c -- '---------- MESSAGE FOLLOWS ----------' || true)"
[ "$SINK_COUNT" = "46" ] || fatal "sink attendu=46 messages (2 vérifications + 44 campagnes) observé=$SINK_COUNT"
printf '%s' "$SINK_DATA" | grep -Fq "$SENDER_A" || fatal "sender A absent des messages sink"
printf '%s' "$SINK_DATA" | grep -Fq "$SENDER_B" || fatal "sender B absent des messages sink"
printf '%s' "$SINK_DATA" | python3 -c '
import sys

blocks = sys.stdin.read().split("---------- MESSAGE FOLLOWS ----------")[1:]
campaigns = [block for block in blocks if "Subject: Preuve sink multi-profils" in block]
assert len(campaigns) == 44, f"44 campagnes attendues, {len(campaigns)} observées"
for index, block in enumerate(campaigns, 1):
    lowered = block.lower()
    assert "content-type: text/plain" in lowered, f"campagne {index}: text/plain absent"
    assert "content-type: text/html" not in lowered, f"campagne {index}: text/html présent"
    assert "multipart/alternative" not in lowered, f"campagne {index}: multipart présent"
' || fatal "les campagnes reçues ne sont pas du MIME texte brut pur"
ok "46 messages exclusivement au sink; 44 campagnes MIME texte brut, rotation, fenêtre et quotas indépendants prouvés"

printf '\nWorkspace: %s\nProfils: %s %s\nBroadcasts: %s %s %s\n' "$WID" "$PROFILE_A" "$PROFILE_B" "$BID_WINDOW" "$BID_CAP_A" "$BID_CAP_B" >&2
ok "E2E multi-profils Gmail terminé sans envoi externe"
