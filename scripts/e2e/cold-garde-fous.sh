#!/usr/bin/env bash
# ============================================================================
# BATTERIE E2E ON-PREMISE — garde-fous anti-cramage de domaine (cold outbound)
# ============================================================================
# Ticket : todo/2026-06-17-batterie-e2e-onpremise-garde-fous-domaine.md (P0).
#
# BUT : avant le 1er vrai envoi cold en prod, PROUVER EN CONDITIONS RÉELLES
# (vrai worker Notifuse staging, vraie DB staging, vrai chemin SMTP → sink local)
# que CHAQUE gate de protection du domaine tient. Une faille = domaine grillé.
#
# 🔴 CONTRAINTE ABSOLUE (Robert) : ZÉRO mail vers un provider externe.
#    SMTP cible = aiosmtpd -n -d dans LA MEME allocation Nomad que Notifuse,
#    uniquement sur 127.0.0.1:1025 (aucun port hote/public/Tailscale).
#    aiosmtpd -n = PAS de relayhost : il
#    imprime le mail dans ses logs et le JETTE (cul-de-sac prouvé). Aucune des
#    intégrations créées ici ne pointe vers le relai sortant 100.92.215.42 ni
#    vers un MX externe. DOUBLE-CHECK du host avant tout envoi (abort sinon),
#    et vérification finale que le relai sortant (mail-relay) n'a vu AUCUN mail.
#
# MÉTHODE :
#  - Provisionne un workspace JETABLE (prefix gfcheck<stamp>), wipe à la fin.
#  - 6 gates "qui bloquent un envoi frais" prouvés par CAMPAGNE RÉELLE → sink :
#    on lit les logs Nomad smtp-sink (RCPT TO + HTML émis) + message_history.
#      G2 exclusion classe · G3 throttle/min · G7 pré-filtre · G8 anti-hash+spintax
#      G9 pixel par classe · G10 round-robin senders
#  - 4 gates "état/temps" prouvés par l'endpoint cold-simulate (prédicat EXACT
#    du gate, sans envoi) ET, pour les caps, par ENFORCEMENT RÉEL au worker
#    (seed à la limite → broadcast → le contact ne part PAS au sink) :
#      G1 circuit breaker · G4 daily cap (dest+classe) · G5 per-sender cap
#      G6 sending window
#  - Pour chaque gate : un cas NON-RÉGRESSION (config absente = no-op) ET un cas
#    ENFORCED (config active = bloque/étale). Les DEUX comptent.
#
# Usage : scripts/e2e/cold-garde-fous.sh [--keep] [--workspace ID]
#   --keep        : ne pas wiper le workspace en fin de run (debug).
#   --workspace W : forcer l'id (défaut : gfcheck<stamp aléatoire>).
#
# Env requis : NOTIFUSE_HUB_API_SECRET  (HMAC Hub→Notifuse staging)
# Env optionnel : NOTIFUSE_URL (défaut https://notifuse.staging.veridian.site)
#                 DEV_SSH (défaut dev-pub) · SINK_HOST (doit rester 127.0.0.1)
#                 SINK_PORT (défaut 1025) · SINK_TASK (défaut smtp-sink)
#                 STAGING_JOB (défaut notifuse-staging) · NOMAD_V
#
# Sortie : log PASS/FAIL par gate sur stderr ; tableau récapitulatif final +
#          exit non nul si un gate FAIL.
set -uo pipefail

BASE="${NOTIFUSE_URL:-https://notifuse.staging.veridian.site}"
DEV_SSH="${DEV_SSH:-dev-pub}"
SINK_HOST="${SINK_HOST:-127.0.0.1}"
SINK_PORT="${SINK_PORT:-1025}"
SINK_TASK="${SINK_TASK:-smtp-sink}"
RELAY_CONTAINER="${RELAY_CONTAINER:-mail-relay}"
STAGING_JOB="${STAGING_JOB:-notifuse-staging}"
NOMAD_V="${NOMAD_V:-$HOME/bin/nomad-v}"
KEEP=0
STAMP="$(date +%s | tail -c 7)"
WID="gfcheck${STAMP}"

while [ $# -gt 0 ]; do
  case "$1" in
    --keep) KEEP=1; shift;;
    --workspace) WID="$2"; shift 2;;
    *) echo "arg inconnu: $1" >&2; exit 2;;
  esac
done

: "${NOTIFUSE_HUB_API_SECRET:?NOTIFUSE_HUB_API_SECRET requis (HMAC Hub staging)}"

# Domaine "corporate" de CE run : DOIT être RÉSOLVABLE (A ou MX) pour PASSER le
# pré-filtre dead-DNS (Lot 7) — sinon le contact corporate serait filtré et la
# non-régression du pré-filtre serait faussée. On utilise example.com (RFC 6761,
# A records publics stables) avec un sous-adressage +stamp pour l'unicité au sink
# (la local-part varie, le domaine résout). NB : Notifuse envoie via l'intégration
# SMTP (smtp-sink), JAMAIS via le MX du destinataire → le domaine ne sert qu'à la
# classification (on TAGUE custom_string_5, prime) et au pré-filtre DNS.
RUN_DOM="example.com"                   # résolvable (A) → passe le pré-filtre
# Local-part stampée pour l'unicité au sink (corporate du run).
corp_addr() { echo "gf-$1-${STAMP}@${RUN_DOM}"; }
SENDER_DOM="agences-veridian.fr"        # domaine d'envoi cold (dev), jamais le principal
SENDER_A="gf-bot-a-${STAMP}@${SENDER_DOM}"
SENDER_B="gf-bot-b-${STAMP}@${SENDER_DOM}"

log()  { printf '\033[1;34m[gf]\033[0m %s\n' "$*" >&2; }
ok()   { printf '\033[1;32m[gf PASS]\033[0m %s\n' "$*" >&2; }
bad()  { printf '\033[1;31m[gf FAIL]\033[0m %s\n' "$*" >&2; }
fatal(){ printf '\033[1;31m[gf FATAL]\033[0m %s\n' "$*" >&2; cleanup; exit 1; }

declare -A RESULT   # gate -> PASS|FAIL
declare -A DETAIL   # gate -> observed vs expected
record() { # $1=gate $2=PASS|FAIL $3=detail
  RESULT["$1"]="$2"; DETAIL["$1"]="$3"
  if [ "$2" = "PASS" ]; then ok "$1 — $3"; else bad "$1 — $3"; fi
}

# --- HMAC Hub helpers -------------------------------------------------------
hmac() { # $1=path $2=method $3=body  -> stdout response
  local ts sig
  ts=$(date +%s%3N)
  sig=$(printf '%s.%s' "$ts" "$3" | openssl dgst -sha256 -hmac "$NOTIFUSE_HUB_API_SECRET" -r | awk '{print $1}')
  curl -s -X "$2" "$BASE$1" -H 'content-type: application/json' \
    -H 'x-veridian-app: hub' -H "x-veridian-timestamp: $ts" \
    -H "X-Veridian-Hub-Signature: $sig" -d "$3"
}
api() { # $1=path $2=body  (Bearer owner)
  curl -s -X POST "$BASE$1" -H 'content-type: application/json' \
    -H "Authorization: Bearer $OWNER_TOKEN" -d "$2"
}
api_get() { curl -s "$BASE$1" -H "Authorization: Bearer $OWNER_TOKEN"; }

# --- DB / sink readers ------------------------------------------------------
WSDB="notifuse_ws_${WID}"
staging_alloc() {
  "$NOMAD_V" raw job allocs -json "$STAGING_JOB" 2>/dev/null \
    | python3 -c 'import json,sys; a=json.load(sys.stdin); r=[x for x in a if x.get("ClientStatus")=="running"]; print(r[0]["ID"] if r else "")'
}
psqlq() {
  local alloc
  alloc="$(staging_alloc)"
  [ -n "$alloc" ] || return 1
  "$NOMAD_V" raw alloc exec -task db "$alloc" \
    psql -U postgres -d "$WSDB" -tAc "$1" 2>/dev/null | tr -d '\r'
}
# Les adresses de chaque run sont uniques, donc lire la queue tail des logs de
# l'allocation suffit et evite toute dependance a un conteneur Docker host.
sink_since() {
  local alloc
  alloc="$(staging_alloc)"
  [ -n "$alloc" ] || return 1
  "$NOMAD_V" raw alloc logs -task "$SINK_TASK" -tail -c 1048576 "$alloc" 2>&1
}
notifuse_since() {
  local alloc
  alloc="$(staging_alloc)"
  [ -n "$alloc" ] || return 1
  "$NOMAD_V" raw alloc logs -task notifuse -stdout -tail -c 1048576 "$alloc" 2>&1
}
relay_since() { ssh "$DEV_SSH" "docker logs $RELAY_CONTAINER --since '$RUN_START_ISO' 2>&1"; }

ensure_sink() {
  local alloc state
  [ "$SINK_HOST" = "127.0.0.1" ] || fatal "SINK_HOST=$SINK_HOST interdit : le sink doit rester loopback dans l'allocation"
  alloc="$(staging_alloc)"
  [ -n "$alloc" ] || fatal "allocation $STAGING_JOB introuvable"
  state="$($NOMAD_V raw alloc status -json "$alloc" 2>/dev/null \
    | python3 -c 'import json,sys; print((json.load(sys.stdin).get("TaskStates",{}).get("smtp-sink",{}) or {}).get("State",""))' 2>/dev/null)"
  [ "$state" = "running" ] || fatal "task Nomad $SINK_TASK non running (state=${state:-absent})"
  sink_since >/dev/null || fatal "logs Nomad $SINK_TASK illisibles"
  log "sink Nomad $SINK_TASK actif sur $SINK_HOST:$SINK_PORT (loopback allocation uniquement)"
}

cleanup() {
  if [ "$KEEP" = "1" ]; then log "--keep : workspace $WID conservé"; return; fi
  [ -n "${WID:-}" ] || return
  log "wipe workspace $WID"
  hmac /api/veridian/admin/wipe-test-tenants POST \
    "{\"tenant_ids\":[\"$WID\"],\"safety_client_prefixes\":[\"canary\"]}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

RUN_START_ISO="$(date -u +%Y-%m-%dT%H:%M:%S)"
ensure_sink

# ============================================================================
# SETUP
# ============================================================================
log "=== SETUP workspace $WID sur $BASE (sink=$SINK_HOST:$SINK_PORT) ==="
PROV=$(hmac /api/tenants/provision POST \
  "{\"tenant_id\":\"$WID\",\"owner_email\":\"gf-${STAMP}@e2e.veridian.site\",\"plan\":\"free\"}")
AUTO_URL=$(echo "$PROV" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("auto_login_url",""))' 2>/dev/null)
[ -n "$AUTO_URL" ] || fatal "provision KO: ${PROV:0:200}"
OWNER_TOKEN=$(curl -s "$AUTO_URL" | grep -oP "setItem\('auth_token', \"\K[^\"]+")
[ -n "$OWNER_TOKEN" ] || fatal "owner token (auto-login) KO"
log "owner session ok"

# Intégration SMTP → SINK. 2 senders pour le round-robin. use_tls=false (sink en
# clair) → pas besoin de skip_tls_verify.
log "create intégration SMTP → SINK (2 senders)"
INTEG=$(api /api/workspaces.createIntegration "{\"workspace_id\":\"$WID\",\"name\":\"sink\",\"type\":\"email\",\"provider\":{\"kind\":\"smtp\",\"smtp\":{\"host\":\"$SINK_HOST\",\"port\":$SINK_PORT,\"use_tls\":false},\"senders\":[{\"id\":\"sa\",\"email\":\"$SENDER_A\",\"name\":\"Bot A\",\"is_default\":true},{\"id\":\"sb\",\"email\":\"$SENDER_B\",\"name\":\"Bot B\"}],\"rate_limit_per_minute\":600}}")
INTEG_ID=$(echo "$INTEG" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("integration_id",""))' 2>/dev/null)
[ -n "$INTEG_ID" ] || fatal "createIntegration KO: ${INTEG:0:200}"

# 🔴 DOUBLE-CHECK host = sink (anti-fuite externe). Abort si autre chose.
HOST_CHECK=$(api_get "/api/workspaces.get?id=$WID" | python3 -c "
import json,sys
w=json.load(sys.stdin)['workspace']
for i in w.get('integrations') or []:
    if i['id']=='$INTEG_ID':
        print(i['email_provider']['smtp']['host']); break
" 2>/dev/null)
log "DOUBLE-CHECK host intégration d'envoi = '$HOST_CHECK' (attendu '$SINK_HOST')"
[ "$HOST_CHECK" = "$SINK_HOST" ] || fatal "host d'envoi = '$HOST_CHECK' ≠ '$SINK_HOST' → RISQUE FUITE EXTERNE, ABORT (rien envoyé)"

# Provider marketing+transactionnel = sink.
SETT=$(api_get "/api/workspaces.get?id=$WID" | WS_INTEG="$INTEG_ID" python3 -c '
import json,sys,os
w=json.load(sys.stdin)["workspace"]; s=w["settings"]
s["marketing_email_provider_id"]=os.environ["WS_INTEG"]
s["transactional_email_provider_id"]=os.environ["WS_INTEG"]
print(json.dumps({"id":w["id"],"name":w["name"],"settings":s}))')
api /api/workspaces.update "$SETT" >/dev/null || fatal "workspaces.update KO"
log "setup ok — workspace prêt"

# Template avec SPINTAX dans le corps + un lien (clic /r/ partout). Le sujet aussi
# spintaxé. Le pixel /t/ est gouverné par la résolution pixel-par-classe.
make_template() { # $1=template_id
  python3 -c "
import json
tree={'id':'root','type':'mjml','attributes':{'version':'4.0.0'},'children':[
 {'id':'b','type':'mj-body','children':[
  {'id':'s','type':'mj-section','children':[
   {'id':'c','type':'mj-column','children':[
    {'id':'t','type':'mj-text','content':'{Bonjour|Salut|Coucou} {cher prospect|à vous|bonjour}, {voici|découvrez} votre audit personnalisé de site web réalisé par notre équipe. {Cordialement|Bien à vous|Merci}.'},
    {'id':'btn','type':'mj-button','attributes':{'href':'https://veridian.site/audit/gf-$STAMP'},'content':'Voir mon audit'}
   ]}]}]}]}
print(json.dumps({'workspace_id':'$WID','id':'$1','name':'gf tpl','channel':'email','category':'marketing',
 'email':{'sender_id':'sa','subject':'{Votre|Un} audit Veridian {gratuit|offert}','visual_editor_tree':tree}}))"
}
api /api/templates.create "$(make_template gf-tpl)" >/dev/null 2>&1 || log "(template existant)"

# Helper : crée une liste, importe des contacts (JSON array), retourne rien.
ensure_list() { api /api/lists.create "{\"workspace_id\":\"$WID\",\"id\":\"$1\",\"name\":\"$1\",\"is_double_optin\":false,\"is_public\":false}" >/dev/null 2>&1 || true; }
import_contacts() { # $1=list_id $2=contacts_json
  api /api/contacts.import "{\"workspace_id\":\"$WID\",\"subscribe_to_lists\":[\"$1\"],\"contacts\":$2}" >/dev/null || fatal "contacts.import ($1) KO"
}

# Crée + schedule un broadcast et attend que N messages apparaissent en history.
# $1=name $2=list $3=metadata_json $4=expected_total_messages $5=timeout_iters(×3s)
fire_broadcast() {
  local name="$1" list="$2" meta="$3" exp="$4" iters="${5:-40}" bid
  bid=$(api /api/broadcasts.create "{\"workspace_id\":\"$WID\",\"name\":\"$name\",\"audience\":{\"list\":\"$list\",\"exclude_unsubscribed\":true},\"test_settings\":{\"enabled\":false,\"sample_percentage\":100,\"variations\":[{\"variation_name\":\"a\",\"template_id\":\"gf-tpl\"}]},\"metadata\":$meta}" \
    | python3 -c 'import json,sys;d=json.load(sys.stdin);print((d.get("broadcast") or d).get("id",""))' 2>/dev/null)
  [ -n "$bid" ] || { bad "broadcasts.create ($name) KO"; echo ""; return 1; }
  api /api/broadcasts.schedule "{\"workspace_id\":\"$WID\",\"id\":\"$bid\",\"send_now\":true}" >/dev/null || { bad "schedule ($name) KO"; echo ""; return 1; }
  local i cnt
  for i in $(seq 1 "$iters"); do
    cnt=$(psqlq "SELECT count(*) FROM message_history WHERE broadcast_id='$bid'")
    [ "${cnt:-0}" -ge "$exp" ] && break
    sleep 3
  done
  echo "$bid"
}

# ============================================================================
# Les gates "campagne réelle → sink"
# ============================================================================
source "$(dirname "$0")/cold-garde-fous-gates.sh"

run_campaign_gates
run_campaign_gates_b
run_simulate_gates
verify_no_external

# ============================================================================
# RÉCAP
# ============================================================================
echo "" >&2
printf '\033[1;36m================= RÉCAP BATTERIE GARDE-FOUS (%s) =================\033[0m\n' "$WID" >&2
FAILS=0
for g in G1_circuit_breaker G2_exclusion G3_throttle G4_daily_cap G5_per_sender G6_sending_window G7_prefilter G8_anti_hash_spintax G9_pixel_tracking G10_round_robin NO_EXTERNAL; do
  st="${RESULT[$g]:-SKIP}"
  printf '  %-22s %s — %s\n' "$g" "$st" "${DETAIL[$g]:-}" >&2
  [ "$st" = "FAIL" ] && FAILS=$((FAILS+1))
done
echo "" >&2
if [ "$FAILS" -eq 0 ]; then
  printf '\033[1;32m✓ TOUS LES GARDE-FOUS TIENNENT — feu vert envoi prod (sous réserve copy/contenu)\033[0m\n' >&2
  exit 0
else
  printf '\033[1;31m✗ %d GATE(S) EN ÉCHEC — NE PAS ENVOYER EN PROD. Ticket P0 par gate FAIL.\033[0m\n' "$FAILS" >&2
  exit 1
fi
