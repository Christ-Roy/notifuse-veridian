#!/usr/bin/env bash
# E2E tunnel outbound — envoi test scriptable, idempotent, rejouable.
#
# Brique Notifuse du giga test CI (gate #11 / #22) : crée ou réutilise le
# workspace cold-test + son setup (intégration SMTP relai, liste, 5 contacts
# alias Lark taggués custom_string_5 par classe, template lien audit), puis
# tire UN broadcast horodaté throttlé par classe et retourne broadcast_id +
# message_ids en JSON sur stdout (tout le reste sur stderr).
#
# Idempotence : tout le setup est "ensure" (provision idempotente contrat
# §5.1, upsert contacts, create-si-absent liste/template/intégration). Seul
# le broadcast est NOUVEAU à chaque run (un broadcast envoyé n'est pas
# re-tirable) — c'est ça la rejouabilité, zéro état manuel à remettre.
#
# Garde-fous (CONTRATS-TUNNEL / consignes lead) :
#   - destinataires = UNIQUEMENT les 5 alias test-tunnel-*@veridian.site
#     (boîtes Lark de Robert). JAMAIS Gmail/Outlook froid.
#   - relai d'envoi = agences-veridian.fr (dev), JAMAIS le domaine principal.
#   - rates par classe volontairement étalés pour rendre le throttle visible.
#
# Usage :
#   scripts/e2e/tunnel-send.sh [--rounds N] [--no-send] [--workspace ID]
#     --rounds N    : N broadcasts successifs (défaut 1). Avec N≥2 le throttle
#                     devient VISIBLE : round 2 attend les tokens par classe
#                     (google +60s, microsoft +30s, …), corporate refile direct.
#     --no-send     : fait tout le setup + crée le broadcast en DRAFT, ne
#                     schedule pas (pré-vol sans envoi).
#     --workspace W : id workspace (défaut: coldtest)
#
# Env requis :
#   NOTIFUSE_HUB_API_SECRET   secret HMAC Hub→Notifuse de l'instance cible
#   SMTP_RELAY_HOST/PORT/USER/PASS  relai d'envoi (ex 100.92.215.42:587 SASL)
# Env optionnel :
#   NOTIFUSE_URL    (défaut https://notifuse.staging.veridian.site)
#   SMTP_RELAY_TLS  "true"|"false" (défaut true — STARTTLS)
#   AUDIT_URL       lien cliqué dans le mail (défaut: page audit réelle du
#                   batch test, vérifiée 200)
#   SENDER_EMAIL    From (défaut robert.brunon@agences-veridian.fr)
#   RATE_GOOGLE/RATE_MICROSOFT/RATE_YAHOO_AOL/RATE_FREEMAIL_FR/RATE_CORPORATE
#                   emails/min par classe (défauts 1/2/3/4/60)
#
# Sortie (stdout, JSON unique) :
#   { "workspace_id", "rounds": [ { "broadcast_id",
#     "messages": [ {"id","contact_email"} ] } ] }
set -euo pipefail

BASE="${NOTIFUSE_URL:-https://notifuse.staging.veridian.site}"
WID="coldtest"
ROUNDS=1
SEND=1
while [ $# -gt 0 ]; do
  case "$1" in
    --rounds) ROUNDS="$2"; shift 2;;
    --no-send) SEND=0; shift;;
    --workspace) WID="$2"; shift 2;;
    *) echo "arg inconnu: $1" >&2; exit 2;;
  esac
done

: "${NOTIFUSE_HUB_API_SECRET:?NOTIFUSE_HUB_API_SECRET requis}"
: "${SMTP_RELAY_HOST:?SMTP_RELAY_HOST requis}"
: "${SMTP_RELAY_PORT:?SMTP_RELAY_PORT requis}"
: "${SMTP_RELAY_USER:?SMTP_RELAY_USER requis}"
: "${SMTP_RELAY_PASS:?SMTP_RELAY_PASS requis}"
SMTP_RELAY_TLS="${SMTP_RELAY_TLS:-true}"
SENDER_EMAIL="${SENDER_EMAIL:-robert.brunon@agences-veridian.fr}"
AUDIT_URL="${AUDIT_URL:-https://veridian.site/audit/superpiman-t08rbbuj}"

log() { printf '\033[1;34m[tunnel-send]\033[0m %s\n' "$*" >&2; }
fail() { printf '\033[1;31m[tunnel-send] FAIL:\033[0m %s\n' "$*" >&2; exit 1; }

# Garde-fou destinataires : aucun envoi hors alias test (défense en profondeur,
# la liste est construite ici-même mais on refuse toute dérive future).
ALIASES=(google microsoft yahoo_aol freemail_fr corporate)
alias_email() { echo "test-tunnel-${1//_/-}@veridian.site"; }

hmac_post() { # $1=path $2=body — HMAC Hub→Notifuse ${ts}.${rawBody}
  local ts sig
  ts=$(date +%s%3N)
  sig=$(printf '%s.%s' "$ts" "$2" | openssl dgst -sha256 -hmac "$NOTIFUSE_HUB_API_SECRET" -r | awk '{print $1}')
  curl -sf -X POST "$BASE$1" -H 'content-type: application/json' \
    -H 'x-veridian-app: hub' -H "x-veridian-timestamp: $ts" \
    -H "X-Veridian-Hub-Signature: $sig" -d "$2"
}

api() { # $1=path $2=body — Bearer owner
  curl -sf -X POST "$BASE$1" -H 'content-type: application/json' \
    -H "Authorization: Bearer $OWNER_TOKEN" -d "$2"
}

api_get() { # $1=path+query
  curl -sf "$BASE$1" -H "Authorization: Bearer $OWNER_TOKEN"
}

log "1/7 provision idempotente workspace '$WID' sur $BASE"
PROV=$(hmac_post /api/tenants/provision \
  "{\"tenant_id\":\"$WID\",\"owner_email\":\"robert.brunon@veridian.site\",\"workspace_name\":\"Cold test tunnel\",\"plan\":\"free\"}") \
  || fail "provision"
AUTO_URL=$(echo "$PROV" | python3 -c 'import json,sys;print(json.load(sys.stdin)["auto_login_url"])')

# Session OWNER (créer intégration / settings exige owner ; la clé API est
# member). L'auto_login_url est régénérée à chaque provision, TTL 60s — on
# l'échange immédiatement contre le JWT embarqué dans la page HTML.
OWNER_TOKEN=$(curl -sf "$AUTO_URL" | grep -oP "setItem\('auth_token', \"\K[^\"]+") \
  || fail "owner token (auto-login)"
log "    owner session ok"

log "2/7 ensure intégration SMTP relai"
INTEG_ID=$(api_get "/api/workspaces.get?id=$WID" | python3 -c '
import json,sys
w=json.load(sys.stdin)["workspace"]
for i in w.get("integrations") or []:
    if i.get("name")=="relai-agences": print(i["id"]); break
')
if [ -z "$INTEG_ID" ]; then
  INTEG_ID=$(api /api/workspaces.createIntegration "{\"workspace_id\":\"$WID\",\"name\":\"relai-agences\",\"type\":\"email\",\"provider\":{\"kind\":\"smtp\",\"smtp\":{\"host\":\"$SMTP_RELAY_HOST\",\"port\":$SMTP_RELAY_PORT,\"username\":\"$SMTP_RELAY_USER\",\"password\":\"$SMTP_RELAY_PASS\",\"use_tls\":$SMTP_RELAY_TLS},\"senders\":[{\"id\":\"sender-tunnel\",\"email\":\"$SENDER_EMAIL\",\"name\":\"Veridian Audit\",\"is_default\":true}],\"rate_limit_per_minute\":120}}" \
    | python3 -c 'import json,sys;print(json.load(sys.stdin)["integration_id"])') \
    || fail "createIntegration"
  log "    intégration créée: $INTEG_ID"
else
  log "    intégration réutilisée: $INTEG_ID"
fi

log "3/7 ensure workspace settings (marketing provider = relai)"
NEWSETTINGS=$(api_get "/api/workspaces.get?id=$WID" | WS_INTEG="$INTEG_ID" python3 -c '
import json,sys,os
w=json.load(sys.stdin)["workspace"]
s=w["settings"]
s["marketing_email_provider_id"]=os.environ["WS_INTEG"]
s["transactional_email_provider_id"]=os.environ["WS_INTEG"]
print(json.dumps({"id":w["id"],"name":w["name"],"settings":s}))')
api /api/workspaces.update "$NEWSETTINGS" >/dev/null || fail "workspaces.update"

log "4/7 ensure liste 'tunnel' + 5 contacts alias taggués (upsert)"
api /api/lists.create "{\"workspace_id\":\"$WID\",\"id\":\"tunnel\",\"name\":\"Tunnel test\",\"is_double_optin\":false,\"is_public\":false}" >/dev/null 2>&1 \
  || log "    (liste existante)"
CONTACTS=$(python3 -c '
import json
classes=["google","microsoft","yahoo_aol","freemail_fr","corporate"]
print(json.dumps([{"email":"test-tunnel-%s@veridian.site"%c.replace("_","-"),"custom_string_5":c} for c in classes]))')
api /api/contacts.import "{\"workspace_id\":\"$WID\",\"subscribe_to_lists\":[\"tunnel\"],\"contacts\":$CONTACTS}" >/dev/null \
  || fail "contacts.import"

log "5/7 ensure template 'e2e-tunnel-tpl'"
TPL_BODY=$(AUDIT_URL="$AUDIT_URL" python3 -c '
import json,os
url=os.environ["AUDIT_URL"]
tree={"id":"root","type":"mjml","attributes":{"version":"4.0.0"},"children":[
 {"id":"body1","type":"mj-body","children":[
  {"id":"sec1","type":"mj-section","children":[
   {"id":"col1","type":"mj-column","children":[
    {"id":"txt1","type":"mj-text","content":"Bonjour, votre audit de site est disponible."},
    {"id":"btn1","type":"mj-button","attributes":{"href":url},"content":"Voir mon audit"}
   ]}]}]}]}
print(json.dumps({"id":"e2e-tunnel-tpl","name":"E2E tunnel","channel":"email","category":"marketing",
 "email":{"sender_id":"sender-tunnel","subject":"Votre audit Veridian (test E2E)","visual_editor_tree":tree}}))')
api /api/templates.create "$(echo "$TPL_BODY" | python3 -c "import json,sys;d=json.load(sys.stdin);d['workspace_id']='$WID';print(json.dumps(d))")" >/dev/null 2>&1 \
  || log "    (template existant)"

OUT_ROUNDS="[]"
for ROUND in $(seq 1 "$ROUNDS"); do
  BNAME="e2e-tunnel-$(date +%s)-r$ROUND"
  log "6/7 broadcast $BNAME (rates par classe, tracking clics ON)"
  RATES=$(python3 -c "
import json,os
print(json.dumps({
 'google': float(os.environ.get('RATE_GOOGLE',1)),
 'microsoft': float(os.environ.get('RATE_MICROSOFT',2)),
 'yahoo_aol': float(os.environ.get('RATE_YAHOO_AOL',3)),
 'freemail_fr': float(os.environ.get('RATE_FREEMAIL_FR',4)),
 'corporate': float(os.environ.get('RATE_CORPORATE',60))}))")
  BID=$(api /api/broadcasts.create "{\"workspace_id\":\"$WID\",\"name\":\"$BNAME\",\"audience\":{\"list\":\"tunnel\",\"exclude_unsubscribed\":true},\"test_settings\":{\"enabled\":false,\"sample_percentage\":100,\"variations\":[{\"variation_name\":\"a\",\"template_id\":\"e2e-tunnel-tpl\"}]},\"tracking_enabled\":true,\"metadata\":{\"veridian_provider_class_rates\":$RATES}}" \
    | python3 -c 'import json,sys;d=json.load(sys.stdin);print((d.get("broadcast") or d)["id"])') \
    || fail "broadcasts.create"

  if [ "$SEND" = "0" ]; then
    log "    --no-send : broadcast $BID en draft, pas de schedule"
    OUT_ROUNDS=$(echo "$OUT_ROUNDS" | python3 -c "import json,sys;r=json.load(sys.stdin);r.append({'broadcast_id':'$BID','scheduled':False,'messages':[]});print(json.dumps(r))")
    continue
  fi

  api /api/broadcasts.schedule "{\"workspace_id\":\"$WID\",\"id\":\"$BID\",\"send_now\":true}" >/dev/null \
    || fail "broadcasts.schedule"
  log "    schedulé — attente des 5 messages en history (timeout 8 min)"

  MESSAGES="[]"
  for i in $(seq 1 96); do
    MESSAGES=$(api_get "/api/messages.list?workspace_id=$WID&broadcast_id=$BID&limit=20" \
      | python3 -c 'import json,sys;d=json.load(sys.stdin);ms=d.get("messages") or d.get("data") or [];print(json.dumps([{"id":m["id"],"contact_email":m["contact_email"]} for m in ms]))' \
      2>/dev/null || echo "[]")
    COUNT=$(echo "$MESSAGES" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')
    [ "$COUNT" -ge 5 ] && break
    sleep 5
  done
  COUNT=$(echo "$MESSAGES" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')
  log "    round $ROUND : $COUNT/5 messages en history"
  [ "$COUNT" -ge 5 ] || fail "round $ROUND incomplet ($COUNT/5) — voir docker logs notifuse + mailq relai"
  OUT_ROUNDS=$(echo "$OUT_ROUNDS" | MSGS="$MESSAGES" python3 -c "
import json,sys,os
r=json.load(sys.stdin)
r.append({'broadcast_id':'$BID','scheduled':True,'messages':json.loads(os.environ['MSGS'])})
print(json.dumps(r))")
done

log "7/7 done"
python3 -c "import json;print(json.dumps({'workspace_id':'$WID','rounds':json.loads('''$OUT_ROUNDS''')},indent=1))"
