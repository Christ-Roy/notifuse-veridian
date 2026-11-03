#!/usr/bin/env bash
# Émission du certificat DÉDIÉ au relais SMTP (fiche 71) : un seul nom, relais-smtp.veridian.site,
# DNS-01 Cloudflare via lego v5.6.0 (somme vérifiée). Le script S'ARRÊTE après émission et contrôles :
# il ne pose rien dans Nomad. La pose (variable de tâche) se fait à part, par qui a le jeton d'écriture.
# Aucune tâche planifiée créée (loi 2) : le renouvellement est une relance manuelle de ce script.
#
# Sorties : un dossier 0700 contenant cert.b64 et key.b64 (base64 du PEM, mode 0600). Le script affiche
# le chemin du dossier et l'échéance, jamais une valeur de secret.
#
# Usage : scripts/veridian/relais-smtp-cert.sh [--staging-acme]   (--staging-acme : autorité de test)
set -euo pipefail

DOMAIN="relais-smtp.veridian.site"
MAIL="robert@veridian.site"
LEGO="${LEGO:-$HOME/.local/bin/lego}"
LEGO_VERSION="5.6.0"
# Coffre des identifiants Veridian (valeurs jamais affichées). Surchargeable pour les tests.
VAULT="${VERIDIAN_CREDS_FILE:-$HOME/credentials/.all-creds.env}"
ACME_SERVER="https://acme-v02.api.letsencrypt.org/directory"
[ "${1:-}" = "--staging-acme" ] && ACME_SERVER="https://acme-staging-v02.api.letsencrypt.org/directory"

[ -x "$LEGO" ] || { echo "lego absent : $LEGO (version ${LEGO_VERSION}, somme vérifiée à l'installation)" >&2; exit 2; }
"$LEGO" --version | grep -q "version ${LEGO_VERSION}" || { echo "mauvaise version de lego (attendu ${LEGO_VERSION})" >&2; exit 2; }
[ -r "$VAULT" ] || { echo "coffre illisible" >&2; exit 2; }

# Le jeton DNS est chargé pour lego uniquement : jamais écrit, jamais affiché.
set -a; . "$VAULT"; set +a
: "${CF_API_TOKEN:?jeton DNS absent du coffre}"
export CLOUDFLARE_DNS_API_TOKEN="$CF_API_TOKEN" CLOUDFLARE_ZONE_API_TOKEN="$CF_API_TOKEN"

OUT="$(mktemp -d "$HOME/.relais-smtp-cert.XXXXXX")"
chmod 700 "$OUT"

# Vérification de propagation sur des résolveurs publics : le résolveur local ne voit pas toujours le TXT.
LEGO_PATH="$OUT/lego" "$LEGO" run --accept-tos --email "$MAIL" --dns cloudflare --domains "$DOMAIN" \
        --server "$ACME_SERVER" --dns.resolvers 1.1.1.1:53 --dns.resolvers 8.8.8.8:53

CRT="$OUT/lego/certificates/$DOMAIN.crt"
KEY="$OUT/lego/certificates/$DOMAIN.key"
[ -s "$CRT" ] && [ -s "$KEY" ] || { echo "émission incomplète" >&2; exit 1; }

# Contrôles : le SAN couvre le nom exact, la clé correspond au certificat, et la validité.
openssl x509 -in "$CRT" -noout -ext subjectAltName | grep -q "DNS:$DOMAIN" || { echo "SAN absent" >&2; exit 1; }
[ "$(openssl x509 -in "$CRT" -noout -pubkey | openssl sha256)" = "$(openssl pkey -in "$KEY" -pubout | openssl sha256)" ] \
  || { echo "clé et certificat ne correspondent pas" >&2; exit 1; }
openssl x509 -in "$CRT" -checkend 0 -noout || { echo "certificat déjà expiré" >&2; exit 1; }

# Formats attendus par le bridge (SMTP_BRIDGE_TLS_CERT_BASE64 / _KEY_BASE64) : base64 du PEM.
ECHEANCE="$(openssl x509 -in "$CRT" -noout -enddate | cut -d= -f2)"
base64 -w0 "$CRT" > "$OUT/cert.b64"
base64 -w0 "$KEY" > "$OUT/key.b64"
chmod 600 "$OUT/cert.b64" "$OUT/key.b64"
rm -rf "$OUT/lego"   # compte ACME et clés intermédiaires : inutiles après émission

echo "cert prêt : $OUT"
echo "échéance : $ECHEANCE"
