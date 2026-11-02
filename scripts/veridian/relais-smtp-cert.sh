#!/usr/bin/env bash
# Émission du certificat DÉDIÉ au relais SMTP (fiche 71) : un seul nom, relais-smtp.veridian.site,
# DNS-01 Cloudflare via lego. NE S'EXÉCUTE PAS SEUL : aucune tâche planifiée (loi 2). Décision Robert en attente.
#
# Ce script ne lit ni n'affiche jamais la valeur d'un secret. Il écrit le certificat et la clé dans une
# variable de tâche Nomad (chemin propre au job, lecture seule pour le job, écriture réservée à un token
# dédié). Sans token d'écriture, l'étape Nomad échoue et rien n'est transmis ailleurs.
#
# Usage : scripts/veridian/relais-smtp-cert.sh [--staging-acme]   (--staging-acme : autorité de test)
set -euo pipefail

DOMAIN="relais-smtp.veridian.site"
MAIL="robert@veridian.site"
JOB_VAR_PATH="nomad/jobs/notifuse-staging/stack/notifuse-staging"
# Coffre des identifiants Veridian (valeurs jamais affichées). Surchargeable pour les tests.
VAULT="${VERIDIAN_CREDS_FILE:-$HOME/credentials/.all-creds.env}"
LEGO_VERSION="v4.21.0"   # à épingler sur la version réellement installée, somme de contrôle vérifiée
ACME_SERVER="https://acme-v02.api.letsencrypt.org/directory"
[ "${1:-}" = "--staging-acme" ] && ACME_SERVER="https://acme-staging-v02.api.letsencrypt.org/directory"

command -v lego >/dev/null 2>&1 || { echo "lego absent (installer la version ${LEGO_VERSION} et vérifier sa somme avant usage)" >&2; exit 2; }
command -v nomad >/dev/null 2>&1 || { echo "nomad absent" >&2; exit 2; }
[ -r "$VAULT" ] || { echo "coffre illisible" >&2; exit 2; }

# Le token DNS est chargé pour lego uniquement : jamais écrit, jamais affiché.
set -a; . "$VAULT"; set +a
: "${CF_API_TOKEN:?jeton DNS absent du coffre}"
export CLOUDFLARE_DNS_API_TOKEN="$CF_API_TOKEN" CLOUDFLARE_ZONE_API_TOKEN="$CF_API_TOKEN"

WORK="$(mktemp -d)"
chmod 700 "$WORK"
trap 'rm -rf "$WORK"' EXIT

lego --accept-tos --email "$MAIL" --dns cloudflare --domains "$DOMAIN" \
     --server "$ACME_SERVER" --path "$WORK" run

CRT="$WORK/certificates/$DOMAIN.crt"
KEY="$WORK/certificates/$DOMAIN.key"
[ -s "$CRT" ] && [ -s "$KEY" ] || { echo "émission incomplète" >&2; exit 1; }

# Contrôles : le SAN couvre le nom, la clé correspond au certificat, et la date de fin.
openssl x509 -in "$CRT" -noout -ext subjectAltName | grep -q "DNS:$DOMAIN" || { echo "SAN absent" >&2; exit 1; }
[ "$(openssl x509 -in "$CRT" -noout -pubkey | openssl sha256)" = "$(openssl pkey -in "$KEY" -pubout | openssl sha256)" ] \
  || { echo "clé et certificat ne correspondent pas" >&2; exit 1; }
echo "certificat valide jusqu'au : $(openssl x509 -in "$CRT" -noout -enddate | cut -d= -f2)"

# Transmission à Nomad par stdin : le secret n'apparaît ni en ligne de commande ni en sortie.
python3 - "$CRT" "$KEY" <<'PY' | nomad var put -out none -in json "$JOB_VAR_PATH" -
import base64, json, sys
crt, key = (open(p, "rb").read() for p in sys.argv[1:3])
json.dump({"Items": {
    "smtp_bridge_tls_cert_base64": base64.b64encode(crt).decode(),
    "smtp_bridge_tls_key_base64": base64.b64encode(key).decode(),
}}, sys.stdout)
PY
echo "variable $JOB_VAR_PATH posée. Renouvellement : relancer ce script avant l'échéance (aucune tâche planifiée créée)."
