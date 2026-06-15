#!/usr/bin/env bash
# ============================================================================
# notifuse-iac — CLI IAC idempotent pour la config cold "as files"
# ============================================================================
# But (Robert 2026-06-15) : « tout poser comme IAC idempotent pour avoir d'un
# coup d'œil notre contenu et l'éditer facilement depuis des fichiers ».
#
# Applique le manifeste DÉCLARATIF iac/coldtunnel/workspace.yaml contre l'API
# Notifuse (POST /api/<resource>.<verb>) de façon idempotente :
#   plan   : montre le diff entre l'état déclaré et l'état réel (read-only).
#   apply  : converge l'état réel vers le déclaré (upsert, jamais de doublon).
#
# Cible STAGING par défaut (sans risque). --env prod = opt-in EXPLICITE.
# NE DÉCLENCHE AUCUN ENVOI DE MAIL : configure l'infra, n'envoie rien.
#
# 🔴 SÉCURITÉ secrets :
#   - les ${VAR} du manifeste sont résolus AU RUNTIME depuis
#     ~/credentials/.all-creds.env (jamais en clair dans un fichier versionné).
#   - le manifeste résolu (secrets en clair) ne vit que dans un fichier TEMP
#     en 0600 sous $TMPDIR, supprimé en fin de run (trap). Le `plan` affiché
#     masque les secrets (render.py --redact).
#
# AUTH OWNER (point dur) : createIntegration/updateIntegration sont OWNER-ONLY
# (cf docs/AGENT-API.md §1). On obtient un JWT owner programmatiquement via le
# pattern validé (cf scripts/e2e/tunnel-send.sh) : provision idempotente HMAC →
# auto_login_url (TTL 60s) → JWT embarqué dans la page HTML. workspaces.update
# (settings cold) exige aussi une session user, pas un api_key.
#
# Usage :
#   notifuse-iac.sh plan  [--env staging|prod] [--manifest PATH]
#   notifuse-iac.sh apply [--env staging|prod] [--manifest PATH] [--yes]
#
# Env requis (chargés depuis ~/credentials/.all-creds.env si présent) :
#   NOTIFUSE_HUB_API_SECRET   secret HMAC Hub→Notifuse (provision + owner login)
#   + tous les ${VAR} référencés par le manifeste (SMTP_AGENCES_*, LARK_IMAP_*)
# Env optionnel :
#   NOTIFUSE_URL              override l'URL cible (sinon dérivée de --env)
#   CREDS_FILE                override le chemin du fichier de secrets
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DEFAULT_MANIFEST="$REPO_ROOT/iac/coldtunnel/workspace.yaml"
CREDS_FILE="${CREDS_FILE:-$HOME/credentials/.all-creds.env}"

ENV_NAME="staging"
MANIFEST="$DEFAULT_MANIFEST"
ASSUME_YES=0
CMD="${1:-}"
shift || true

while [ $# -gt 0 ]; do
  case "$1" in
    --env) ENV_NAME="$2"; shift 2;;
    --manifest) MANIFEST="$2"; shift 2;;
    --yes) ASSUME_YES=1; shift;;
    *) echo "arg inconnu: $1" >&2; exit 2;;
  esac
done

c_blue=$'\033[1;34m'; c_grn=$'\033[1;32m'; c_yel=$'\033[1;33m'; c_red=$'\033[1;31m'; c_off=$'\033[0m'
log()  { printf '%s[iac]%s %s\n' "$c_blue" "$c_off" "$*" >&2; }
ok()   { printf '%s[iac]%s %s\n' "$c_grn" "$c_off" "$*" >&2; }
warn() { printf '%s[iac]%s %s\n' "$c_yel" "$c_off" "$*" >&2; }
fail() { printf '%s[iac] FAIL:%s %s\n' "$c_red" "$c_off" "$*" >&2; exit 1; }

case "$CMD" in plan|apply) ;; *) fail "usage: notifuse-iac.sh {plan|apply} [--env staging|prod] [--manifest PATH] [--yes]";; esac
[ -f "$MANIFEST" ] || fail "manifeste introuvable: $MANIFEST"

# --- Cible -------------------------------------------------------------------
case "$ENV_NAME" in
  staging) BASE_DEFAULT="https://notifuse.staging.veridian.site";;
  prod)    BASE_DEFAULT="https://notifuse.app.veridian.site";;
  *) fail "--env doit être 'staging' ou 'prod' (reçu: $ENV_NAME)";;
esac
BASE="${NOTIFUSE_URL:-$BASE_DEFAULT}"

if [ "$ENV_NAME" = "prod" ] && [ "$CMD" = "apply" ]; then
  warn "🔴 CIBLE PROD — apply va modifier la config cold de production ($BASE)."
  if [ "$ASSUME_YES" != "1" ]; then
    fail "apply prod exige --yes explicite (opt-in). Relance avec --yes si c'est voulu (GO lead)."
  fi
fi

# --- Secrets -----------------------------------------------------------------
# Chargement non-interactif depuis le coffre. On NE source PAS le fichier (`.`
# l'exécuterait : une valeur non quotée avec espace — ex SENDER_NAME=Robert
# Brunon — ferait planter le shell « Brunon: commande introuvable »). On parse
# ligne par ligne KEY=VALUE et on exporte sans évaluation shell. JAMAIS loggué.
if [ -f "$CREDS_FILE" ]; then
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line#"${line%%[![:space:]]*}"}"      # trim espaces de tête
    line="${line#export }"                        # tolère la syntaxe `export KEY=VAL`
    case "$line" in
      ''|'#'*) continue;;                         # ligne vide ou commentaire
    esac
    [ "${line#*=}" = "$line" ] && continue        # pas de '=' → ignore
    key="${line%%=*}"
    val="${line#*=}"
    # Clé valide (identifiant shell strict : [A-Za-z_][A-Za-z0-9_]*) uniquement.
    case "$key" in
      *[!A-Za-z0-9_]*|'') continue;;
      [0-9]*) continue;;
    esac
    # Retire un éventuel quotage simple/double entourant la valeur.
    case "$val" in
      \"*\") val="${val#\"}"; val="${val%\"}";;
      \'*\') val="${val#\'}"; val="${val%\'}";;
    esac
    export "$key=$val"
  done < "$CREDS_FILE"
  log "secrets chargés depuis $CREDS_FILE"
else
  warn "fichier secrets absent ($CREDS_FILE) — on compte sur l'environnement courant"
fi
: "${NOTIFUSE_HUB_API_SECRET:?NOTIFUSE_HUB_API_SECRET requis (provision + owner login)}"

# --- Fichiers temp (0600, nettoyés) -----------------------------------------
TMPD="$(mktemp -d)"
chmod 700 "$TMPD"
cleanup() { rm -rf "$TMPD"; }
trap cleanup EXIT
RESOLVED="$TMPD/resolved.json"           # manifeste résolu, secrets EN CLAIR (0600)

# --- HTTP helpers ------------------------------------------------------------
# retry : rejoue une commande idempotente jusqu'à 3 fois avec backoff (un hoquet
# TCP transitoire ne doit pas faire planter tout l'apply via set -e — vécu en
# test staging 2026-06-15). N'enrobe QUE des opérations idempotentes (GET, HMAC
# provision idempotent, lecture auto-login). curl --max-time borne chaque essai.
retry() {
  local n=0 max=3 rc=0
  while :; do
    "$@" && return 0
    rc=$?
    n=$((n+1))
    [ "$n" -ge "$max" ] && return "$rc"
    sleep "$n"   # backoff linéaire 1s, 2s
  done
}
hmac_post() { # $1=path $2=body — HMAC Hub→Notifuse, canonical ${ts}.${rawBody}
  local ts sig
  ts=$(date +%s%3N)
  sig=$(printf '%s.%s' "$ts" "$2" | openssl dgst -sha256 -hmac "$NOTIFUSE_HUB_API_SECRET" -r | awk '{print $1}')
  curl -sf --max-time 20 -X POST "$BASE$1" -H 'content-type: application/json' \
    -H 'x-veridian-app: hub' -H "x-veridian-timestamp: $ts" \
    -H "X-Veridian-Hub-Signature: $sig" -d "$2"
}
api()     { curl -sf --max-time 20 -X POST "$BASE$1" -H 'content-type: application/json' -H "Authorization: Bearer $OWNER_TOKEN" -d "$2"; }
api_get() { retry curl -sf --max-time 20 "$BASE$1" -H "Authorization: Bearer $OWNER_TOKEN"; }

# --- Rendu manifeste ---------------------------------------------------------
WID="$(python3 -c 'import yaml,sys;print(yaml.safe_load(open(sys.argv[1]))["workspace"]["id"])' "$MANIFEST")"
OWNER_EMAIL="$(python3 -c 'import yaml,sys;print(yaml.safe_load(open(sys.argv[1]))["workspace"]["owner_email"])' "$MANIFEST")"
WS_NAME="$(python3 -c 'import yaml,sys;print(yaml.safe_load(open(sys.argv[1]))["workspace"]["name"])' "$MANIFEST")"
WS_PLAN="$(python3 -c 'import yaml,sys;print(yaml.safe_load(open(sys.argv[1]))["workspace"].get("plan","free"))' "$MANIFEST")"

log "cible: $ENV_NAME ($BASE) — workspace '$WID' — manifeste $MANIFEST"

# --- Owner session -----------------------------------------------------------
# Provision idempotente (contrat §5.1) puis échange auto_login_url → JWT owner.
# Le workspace coldtunnel est déjà provisionné (HMAC) ; re-provision = no-op
# idempotent qui nous régénère une auto_login_url fraîche pour la session.
ensure_owner_session() {
  log "owner session : provision idempotente + auto-login"
  local prov auto_url
  # retry : provision idempotente (created:false si workspace existe) → safe à
  # rejouer ; un 5xx/timeout transitoire ne doit pas tuer l'apply (vécu staging).
  prov=$(retry hmac_post /api/tenants/provision \
    "{\"tenant_id\":\"$WID\",\"owner_email\":\"$OWNER_EMAIL\",\"workspace_name\":\"$WS_NAME\",\"plan\":\"$WS_PLAN\"}") \
    || fail "provision (HMAC) — vérifier NOTIFUSE_HUB_API_SECRET pour $ENV_NAME"
  auto_url=$(printf '%s' "$prov" | python3 -c 'import json,sys;print(json.load(sys.stdin)["auto_login_url"])') \
    || fail "auto_login_url absent de la réponse provision"
  # L'auto_login_url a un TTL court (~60s) : on l'échange tout de suite. Le fetch
  # est idempotent (régénère juste un JWT) → retry safe.
  OWNER_TOKEN=$(retry curl -sf --max-time 20 "$auto_url" | grep -oP "setItem\('auth_token', \"\K[^\"]+") \
    || fail "extraction JWT owner (auto-login) — la page n'a pas embarqué auth_token"
  ok "owner session établie (workspace $WID)"
}

# --- Construction des payloads d'apply (depuis le manifeste résolu) ----------
# Émet, pour une action donnée, le body JSON exact à POSTer. Les secrets en
# clair ne transitent QUE par $RESOLVED (0600) → stdin python → curl. Jamais
# affichés. rate_limit_per_minute (upstream, obligatoire >0) dérivé : on prend
# un plafond large (la vraie limite cold vient des veridian rates par classe).
build_email_create_body() { # $1=integration_name
  RESOLVED="$RESOLVED" WID="$WID" python3 - "$1" <<'PY'
import json,os,sys
man=json.load(open(os.environ["RESOLVED"]))
name=sys.argv[1]
si=next(s for s in man["sending_integrations"] if s["id"]==name)
cold=man.get("cold_outreach",{})
smtp=si.get("smtp",{})
senders=[{"email":s["email"],"name":s.get("name",s["email"]),"is_default":bool(s.get("is_default",False))} for s in si.get("senders",[])]
# Au moins un sender doit être default (upstream Validate l'exige pour l'envoi).
if senders and not any(x["is_default"] for x in senders):
    senders[0]["is_default"]=True
provider={
  "kind":"smtp",
  "smtp":{
    "host":smtp.get("host"),
    "port":int(smtp.get("port",587)),
    "username":senders[0]["email"] if senders else "",
    "password":next((s.get("password") for s in si.get("senders",[]) if s.get("password")),""),
    "use_tls":bool(smtp.get("use_tls",True)),
  },
  "senders":senders,
  "rate_limit_per_minute":600,
}
if "provider_class_rates" in cold: provider["veridian_provider_class_rates"]=cold["provider_class_rates"]
if "provider_class_daily_cap" in cold: provider["veridian_provider_class_daily_cap"]=cold["provider_class_daily_cap"]
if "per_recipient_daily_cap" in cold: provider["veridian_per_recipient_daily_cap"]=int(cold["per_recipient_daily_cap"])
if si.get("veridian_tracking_domain"): provider["veridian_tracking_domain"]=si["veridian_tracking_domain"]
print(json.dumps({"workspace_id":os.environ["WID"],"name":name,"type":"email","provider":provider}))
PY
}

build_email_update_body() { # $1=integration_name $2=existing_id
  RESOLVED="$RESOLVED" WID="$WID" python3 - "$1" "$2" <<'PY'
import json,os,sys
man=json.load(open(os.environ["RESOLVED"]))
name=sys.argv[1]; iid=sys.argv[2]
si=next(s for s in man["sending_integrations"] if s["id"]==name)
cold=man.get("cold_outreach",{})
smtp=si.get("smtp",{})
senders=[{"email":s["email"],"name":s.get("name",s["email"]),"is_default":bool(s.get("is_default",False))} for s in si.get("senders",[])]
if senders and not any(x["is_default"] for x in senders):
    senders[0]["is_default"]=True
provider={
  "kind":"smtp",
  "smtp":{
    "host":smtp.get("host"),
    "port":int(smtp.get("port",587)),
    "username":senders[0]["email"] if senders else "",
    "password":next((s.get("password") for s in si.get("senders",[]) if s.get("password")),""),
    "use_tls":bool(smtp.get("use_tls",True)),
  },
  "senders":senders,
  "rate_limit_per_minute":600,
}
if "provider_class_rates" in cold: provider["veridian_provider_class_rates"]=cold["provider_class_rates"]
if "provider_class_daily_cap" in cold: provider["veridian_provider_class_daily_cap"]=cold["provider_class_daily_cap"]
if "per_recipient_daily_cap" in cold: provider["veridian_per_recipient_daily_cap"]=int(cold["per_recipient_daily_cap"])
if si.get("veridian_tracking_domain"): provider["veridian_tracking_domain"]=si["veridian_tracking_domain"]
# UpdateIntegrationRequest : pas de champ 'type' (le type ne change pas à l'update).
print(json.dumps({"workspace_id":os.environ["WID"],"integration_id":iid,"name":name,"provider":provider}))
PY
}

build_imap_body() { # $1=op(create|update) $2=name $3=existing_id(si update)
  RESOLVED="$RESOLVED" WID="$WID" python3 - "$1" "$2" "${3:-}" <<'PY'
import json,os,sys
man=json.load(open(os.environ["RESOLVED"]))
op=sys.argv[1]; name=sys.argv[2]; iid=sys.argv[3] if len(sys.argv)>3 else ""
imap=man["reply_inbox"]["imap"]
settings={
  "host":imap.get("host"),
  "port":int(imap.get("port",993)),
  "username":imap.get("username"),
  "password":imap.get("password",""),
  "use_tls":bool(imap.get("use_tls",True)),
  "folder":imap.get("folder","INBOX"),
  "polling_interval_seconds":int(imap.get("polling_interval_seconds",120)),
}
body={"workspace_id":os.environ["WID"],"name":name,"imap_settings":settings}
if op=="create":
    body["type"]="imap"
else:
    body["integration_id"]=iid
print(json.dumps(body))
PY
}

build_settings_body() { # $1 = action JSON {target:{...}} ; relit le workspace réel
  local action_json real
  action_json="$1"
  real=$(api_get "/api/workspaces.get?id=$WID") || return 1
  REAL="$real" TARGET="$action_json" WID="$WID" python3 - <<'PY'
import json,os
real=json.loads(os.environ["REAL"])["workspace"]
action=json.loads(os.environ["TARGET"])
target=action.get("target",{})
settings=real.get("settings",{}) or {}
# On ne touche QUE les clés cold gouvernées par l'IAC. Le reste des settings
# (providers marketing/transactional, etc.) est préservé tel quel.
for k,v in target.items():
    settings[k]=v
print(json.dumps({"id":real["id"],"name":real.get("name") or os.environ["WID"],"settings":settings}))
PY
}

# --- PLAN --------------------------------------------------------------------
# Le diff se calcule sur le manifeste RÉSOLU (clair, fichier 0600) : il faut les
# vraies valeurs (host/port/senders) pour comparer fiablement à l'état réel. Le
# RÉSUMÉ affiché ne montre QUE op/kind/name/reason — et plan.py ne met dans
# `reason` que des NOMS de champs (« smtp.host »), jamais leurs valeurs → aucun
# secret n'apparaît à l'écran. Les passwords ne sont jamais comparés ni affichés.
do_plan() {
  python3 "$SCRIPT_DIR/render.py" "$MANIFEST" > "$RESOLVED" || fail "render (plan) — \${VAR} non résolu ? vérifier $CREDS_FILE"
  chmod 600 "$RESOLVED"
  ensure_owner_session
  log "lecture état réel (workspaces.get)"
  local real plan
  real=$(api_get "/api/workspaces.get?id=$WID") || fail "workspaces.get"
  plan=$(printf '%s' "$real" | python3 "$SCRIPT_DIR/plan.py" "$RESOLVED") || fail "calcul du plan"
  echo "$plan" > "$TMPD/plan.json"

  printf '\n%s===== PLAN (%s) — workspace %s =====%s\n' "$c_blue" "$ENV_NAME" "$WID" "$c_off" >&2
  PLAN_JSON="$plan" python3 - <<'PY'
import json,os,sys
p=json.loads(os.environ["PLAN_JSON"])
C={"create":"\033[1;32m+ create\033[0m","update":"\033[1;33m~ update\033[0m","noop":"\033[2m= noop  \033[0m"}
KIND={"integration_email":"SMTP infra ","integration_imap":"IMAP inbox ","workspace_settings":"WS settings"}
for a in p["actions"]:
    print("  %s  %-11s  %-34s  %s" % (C.get(a["op"],a["op"]), KIND.get(a["kind"],a["kind"]), a["name"], a["reason"]), file=sys.stderr)
ops=[a["op"] for a in p["actions"]]
print("\n  → %d create, %d update, %d noop" % (ops.count("create"),ops.count("update"),ops.count("noop")), file=sys.stderr)
PY
  printf '%s======================================%s\n\n' "$c_blue" "$c_off" >&2
}

# --- APPLY -------------------------------------------------------------------
do_apply() {
  # Résout les secrets EN CLAIR pour cette phase uniquement (fichier 0600).
  python3 "$SCRIPT_DIR/render.py" "$MANIFEST" > "$RESOLVED" || fail "render (apply) — ${VAR} non résolu ?"
  chmod 600 "$RESOLVED"

  # Recalcul du plan sur l'état réel courant (on ne fait QUE ce qui diverge).
  ensure_owner_session
  local real plan
  real=$(api_get "/api/workspaces.get?id=$WID") || fail "workspaces.get"
  plan=$(printf '%s' "$real" | python3 "$SCRIPT_DIR/plan.py" "$RESOLVED") || fail "calcul du plan"

  local n_changes
  n_changes=$(printf '%s' "$plan" | python3 -c 'import json,sys;print(sum(1 for a in json.load(sys.stdin)["actions"] if a["op"]!="noop"))')
  if [ "$n_changes" = "0" ]; then
    ok "rien à appliquer — état déjà convergé (idempotent)."
    return 0
  fi
  log "$n_changes action(s) à appliquer"

  # Itère les actions et exécute selon op. jq pour découper le JSON proprement.
  local count
  count=$(printf '%s' "$plan" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["actions"]))')
  local i
  for ((i=0; i<count; i++)); do
    local a kind op name eid
    a=$(printf '%s' "$plan" | python3 -c "import json,sys;print(json.dumps(json.load(sys.stdin)['actions'][$i]))")
    kind=$(printf '%s' "$a" | python3 -c 'import json,sys;print(json.load(sys.stdin)["kind"])')
    op=$(printf '%s'   "$a" | python3 -c 'import json,sys;print(json.load(sys.stdin)["op"])')
    name=$(printf '%s' "$a" | python3 -c 'import json,sys;print(json.load(sys.stdin)["name"])')
    eid=$(printf '%s'  "$a" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("existing_id") or "")')
    [ "$op" = "noop" ] && continue

    case "$kind" in
      integration_email)
        if [ "$op" = "create" ]; then
          log "create SMTP infra '$name'"
          api /api/workspaces.createIntegration "$(build_email_create_body "$name")" >/dev/null || fail "createIntegration $name"
        else
          log "update SMTP infra '$name' ($eid)"
          api /api/workspaces.updateIntegration "$(build_email_update_body "$name" "$eid")" >/dev/null || fail "updateIntegration $name"
        fi
        ok "  SMTP infra '$name' $op OK";;
      integration_imap)
        if [ "$op" = "create" ]; then
          log "create IMAP inbox '$name'"
          api /api/workspaces.createIntegration "$(build_imap_body create "$name")" >/dev/null || fail "createIntegration IMAP"
        else
          log "update IMAP inbox '$name' ($eid)"
          api /api/workspaces.updateIntegration "$(build_imap_body update "$name" "$eid")" >/dev/null || fail "updateIntegration IMAP"
        fi
        ok "  IMAP inbox '$name' $op OK";;
      workspace_settings)
        log "update workspace settings cold"
        api /api/workspaces.update "$(build_settings_body "$a")" >/dev/null || fail "workspaces.update settings"
        ok "  workspace settings cold $op OK";;
    esac
  done

  ok "apply terminé."
  # Re-plan post-apply : prouve l'idempotence (doit être 100% noop).
  log "vérification post-apply (doit être tout noop)…"
  real=$(api_get "/api/workspaces.get?id=$WID") || fail "workspaces.get (verify)"
  plan=$(printf '%s' "$real" | python3 "$SCRIPT_DIR/plan.py" "$RESOLVED") || fail "re-plan"
  local left
  left=$(printf '%s' "$plan" | python3 -c 'import json,sys;print(sum(1 for a in json.load(sys.stdin)["actions"] if a["op"]!="noop"))')
  if [ "$left" = "0" ]; then
    ok "idempotence vérifiée : re-plan = 0 changement."
  else
    warn "re-plan montre encore $left changement(s) — divergence à investiguer (champ non convergeable ?)."
    printf '%s' "$plan" | python3 -c 'import json,sys;[print("   ",a["op"],a["kind"],a["name"],"-",a["reason"],file=sys.stderr) for a in json.load(sys.stdin)["actions"] if a["op"]!="noop"]'
  fi
}

case "$CMD" in
  plan)  do_plan;;
  apply) do_apply;;
esac
