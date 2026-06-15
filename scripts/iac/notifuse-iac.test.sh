#!/usr/bin/env bash
# Smoke test du CLI IAC — SANS RÉSEAU. Valide le rendu (render.py) et le moteur
# de diff (plan.py) sur des fixtures, et que les bodies d'apply sont du JSON
# valide avec les bons champs. Ne touche AUCUNE API.
#
# Lancer : scripts/iac/notifuse-iac.test.sh
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PASS=0; FAIL=0
ok()  { PASS=$((PASS+1)); echo "  ok   $1"; }
ko()  { FAIL=$((FAIL+1)); echo "  FAIL $1"; }
# chk NAME : exécute le bloc qui suit (statut $?) — usage: cmd; chk "desc"
chk() { if [ "$?" = "0" ]; then ok "$1"; else ko "$1"; fi; }

TMPD="$(mktemp -d)"; trap 'rm -rf "$TMPD"' EXIT

# --- Fixture manifeste minimal ----------------------------------------------
cat > "$TMPD/manifest.yaml" <<'YAML'
workspace:
  id: coldtest
  name: Cold Test
  owner_email: owner@example.com
  plan: enterprise
sending_integrations:
  - id: relay-a
    type: email
    kind: smtp
    smtp:
      host: ${IAC_TEST_HOST}
      port: 587
      use_tls: true
    senders:
      - email: a@example.com
        password: ${IAC_TEST_PW}
        name: A
        is_default: true
    veridian_tracking_domain: track.example.com
reply_inbox:
  type: imap
  imap:
    host: imap.example.com
    port: 993
    username: bounce@example.com
    password: ${IAC_TEST_IMAP_PW}
    use_tls: true
    folder: INBOX
    polling_interval_seconds: 120
cold_outreach:
  per_recipient_daily_cap: 1
  provider_class_rates:
    google: 0.5
  provider_class_daily_cap:
    google: 50
  open_pixel_by_class:
    google: false
    freemail_fr: true
YAML

export IAC_TEST_HOST="10.0.0.5" IAC_TEST_PW="s3cr3t" IAC_TEST_IMAP_PW="imapsecret"

# --- render.py : résolution + redaction -------------------------------------
RESOLVED=$(python3 "$SCRIPT_DIR/render.py" "$TMPD/manifest.yaml")
REDACTED=$(python3 "$SCRIPT_DIR/render.py" --redact "$TMPD/manifest.yaml")

grep -q "10.0.0.5" <<<"$RESOLVED"; chk "render résout \${VAR} en clair (host)"
grep -q "s3cr3t"   <<<"$RESOLVED"; chk "render résout \${VAR} en clair (password)"
{ grep -q '[*][*][*]' <<<"$REDACTED" && ! grep -q "s3cr3t" <<<"$REDACTED"; }; chk "render --redact masque le password"
{ ! grep -q "10.0.0.5" <<<"$REDACTED"; }; chk "render --redact masque le host"
python3 -c 'import json,sys;json.load(sys.stdin)' <<<"$RESOLVED"; chk "render JSON valide"

# --- render.py : variable manquante = échec dur en mode apply ---------------
if ! ( unset IAC_TEST_PW; python3 "$SCRIPT_DIR/render.py" "$TMPD/manifest.yaml" >/dev/null 2>&1 ); then true; else false; fi
chk "render échoue si \${VAR} manquant (apply)"
( unset IAC_TEST_PW; python3 "$SCRIPT_DIR/render.py" --redact "$TMPD/manifest.yaml" >/dev/null 2>&1 ); chk "render --redact tolère \${VAR} manquant"

# --- plan.py : diff calculé sur le manifeste RÉSOLU (clair), pas le redacté --
# (le diff a besoin des vraies valeurs host/port pour comparer à l'état réel ;
#  le redacté servirait à l'affichage seul — cf. commentaire do_plan du CLI).
echo "$RESOLVED" > "$TMPD/resolved.json"
EMPTY_REAL='{"workspace":{"id":"coldtest","name":"Cold Test","integrations":[],"settings":{}}}'
PLAN_EMPTY=$(python3 "$SCRIPT_DIR/plan.py" "$TMPD/resolved.json" <<<"$EMPTY_REAL")
op_of() { python3 -c "import json,sys;a=json.load(sys.stdin)['actions'];print(next((x['op'] for x in a if x['kind']=='$1'),'?'))"; }
[ "$(op_of integration_email  <<<"$PLAN_EMPTY")" = "create" ]; chk "plan: SMTP infra = create sur WS vide"
[ "$(op_of integration_imap   <<<"$PLAN_EMPTY")" = "create" ]; chk "plan: IMAP = create sur WS vide"
[ "$(op_of workspace_settings <<<"$PLAN_EMPTY")" = "update" ]; chk "plan: settings = update sur WS vide"

# --- plan.py : idempotence (état réel = déclaré → tout noop) -----------------
CONVERGED='{"workspace":{"id":"coldtest","name":"Cold Test","settings":{"veridian_provider_class_rates":{"google":0.5},"veridian_provider_class_daily_cap":{"google":50},"veridian_per_recipient_daily_cap":1,"veridian_open_pixel_by_class":{"google":false,"freemail_fr":true}},"integrations":[
  {"id":"int-1","name":"relay-a","type":"email","email_provider":{"kind":"smtp","smtp":{"host":"10.0.0.5","port":587,"use_tls":true},"senders":[{"email":"a@example.com","name":"A","is_default":true}],"veridian_tracking_domain":"track.example.com","veridian_provider_class_rates":{"google":0.5},"veridian_provider_class_daily_cap":{"google":50},"veridian_per_recipient_daily_cap":1}},
  {"id":"int-2","name":"Return inbox (cold bounce/reply)","type":"imap","imap_settings":{"host":"imap.example.com","port":993,"username":"bounce@example.com","use_tls":true,"folder":"INBOX","polling_interval_seconds":120}}
]}}'
PLAN_CONV=$(python3 "$SCRIPT_DIR/plan.py" "$TMPD/resolved.json" <<<"$CONVERGED")
python3 -c 'import json,sys;a=json.load(sys.stdin)["actions"];sys.exit(0 if all(x["op"]=="noop" for x in a) else 1)' <<<"$PLAN_CONV"; chk "plan: idempotent = tout noop si convergé"

# --- plan.py : changement de rate → update détecté --------------------------
DRIFT=$(python3 -c 'import json,sys;d=json.load(sys.stdin);d["workspace"]["integrations"][0]["email_provider"]["veridian_provider_class_rates"]={"google":99};print(json.dumps(d))' <<<"$CONVERGED")
PLAN_DRIFT=$(python3 "$SCRIPT_DIR/plan.py" "$TMPD/resolved.json" <<<"$DRIFT")
[ "$(op_of integration_email <<<"$PLAN_DRIFT")" = "update" ]; chk "plan: drift de rate → SMTP update"

# --- build body email create : JSON valide + champs clés --------------------
echo "$RESOLVED" > "$TMPD/resolved.json"
EMAIL_CREATE=$(RESOLVED="$TMPD/resolved.json" WID="coldtest" python3 - "relay-a" <<'PY'
import json,os,sys
man=json.load(open(os.environ["RESOLVED"]))
name=sys.argv[1]
si=next(s for s in man["sending_integrations"] if s["id"]==name)
cold=man.get("cold_outreach",{})
smtp=si.get("smtp",{})
senders=[{"email":s["email"],"name":s.get("name",s["email"]),"is_default":bool(s.get("is_default",False))} for s in si.get("senders",[])]
if senders and not any(x["is_default"] for x in senders): senders[0]["is_default"]=True
provider={"kind":"smtp","smtp":{"host":smtp.get("host"),"port":int(smtp.get("port",587)),"username":senders[0]["email"] if senders else "","password":next((s.get("password") for s in si.get("senders",[]) if s.get("password")),""),"use_tls":bool(smtp.get("use_tls",True))},"senders":senders,"rate_limit_per_minute":600}
if "provider_class_rates" in cold: provider["veridian_provider_class_rates"]=cold["provider_class_rates"]
if si.get("veridian_tracking_domain"): provider["veridian_tracking_domain"]=si["veridian_tracking_domain"]
print(json.dumps({"workspace_id":os.environ["WID"],"name":name,"type":"email","provider":provider}))
PY
)
python3 -c 'import json,sys;json.load(sys.stdin)' <<<"$EMAIL_CREATE"; chk "body email create : JSON valide"
python3 -c 'import json,sys;assert json.load(sys.stdin)["type"]=="email"' <<<"$EMAIL_CREATE"; chk "body email create : type=email"
python3 -c 'import json,sys;assert json.load(sys.stdin)["provider"]["smtp"]["password"]=="s3cr3t"' <<<"$EMAIL_CREATE"; chk "body email create : password en clair"
python3 -c 'import json,sys;assert json.load(sys.stdin)["provider"]["rate_limit_per_minute"]>0' <<<"$EMAIL_CREATE"; chk "body email create : rate_limit>0"
python3 -c 'import json,sys;assert any(s["is_default"] for s in json.load(sys.stdin)["provider"]["senders"])' <<<"$EMAIL_CREATE"; chk "body email create : sender default"

echo "----"
echo "PASS=$PASS FAIL=$FAIL"
[ "$FAIL" = "0" ]
