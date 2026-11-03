#!/usr/bin/env bash
# Plan DNS en LECTURE SEULE : compare la zone Cloudflare à iac/dns/veridian-site.yaml.
# N'écrit jamais dans Cloudflare. Sorties : 0 conforme, 1 dérive (affichée), 2 non abouti.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MANIFEST="$ROOT/iac/dns/veridian-site.yaml"
VAULT="${VERIDIAN_CREDS_FILE:-$HOME/credentials/.all-creds.env}"

[ -r "$MANIFEST" ] || { echo "manifeste absent : $MANIFEST" >&2; exit 2; }
[ -r "$VAULT" ] || { echo "coffre illisible" >&2; exit 2; }
command -v python3 >/dev/null || { echo "python3 absent" >&2; exit 2; }

# Le jeton est chargé dans l'environnement du seul processus Python : jamais affiché.
set -a; . "$VAULT"; set +a
: "${CF_API_TOKEN:?jeton DNS absent du coffre}"
: "${CF_ZONE_ID:?zone absente du coffre}"

python3 - "$MANIFEST" <<'PY'
import json, os, sys, urllib.request, urllib.parse
import yaml

manifest = yaml.safe_load(open(sys.argv[1], encoding="utf-8"))
zone = manifest["zone"]
token = os.environ["CF_API_TOKEN"]
zone_id = os.environ["CF_ZONE_ID"]

def cf_get(path):
    req = urllib.request.Request(
        f"https://api.cloudflare.com/client/v4{path}",
        headers={"Authorization": f"Bearer {token}"},
    )
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.load(r)

drift = 0
for rec in manifest.get("records", []):
    fqdn = f"{rec['name']}.{zone}"
    q = urllib.parse.urlencode({"name": fqdn, "type": rec["type"]})
    try:
        data = cf_get(f"/zones/{zone_id}/dns_records?{q}")
    except Exception as e:  # réseau ou jeton : non abouti, pas une dérive
        print(f"NON ABOUTI {fqdn} {rec['type']} : {e}")
        sys.exit(2)
    found = data.get("result", [])
    if not found:
        print(f"DÉRIVE {fqdn} {rec['type']} : absent de la zone, attendu {rec['content']}")
        drift += 1
        continue
    actual = found[0]
    if actual.get("content") != rec["content"] or bool(actual.get("proxied")) != bool(rec.get("proxied", False)):
        print(f"DÉRIVE {fqdn} {rec['type']} : zone={actual.get('content')} proxied={actual.get('proxied')} / manifeste={rec['content']} proxied={rec.get('proxied', False)}")
        drift += 1
    else:
        print(f"CONFORME {fqdn} {rec['type']} -> {rec['content']}")

sys.exit(1 if drift else 0)
PY
