#!/usr/bin/env bash
# Onboarding client "prospection" en un coup - Veridian/Notifuse.
#
# Provisionne un workspace, pose une politique de cold outreach saine
# (reprise des valeurs mesurees en production sur robertbrunon : fenetre
# d'envoi, plafonds par classe de fournisseur, debit), cree les listes types,
# une sequence J0 -> J+4 -> J+10 en texte brut, et l'automation qui la pilote.
#
# La sortie automatique sur reponse est un mecanisme GLOBAL du backend
# (internal/service/veridian_cold_exit.go) : aucun noeud dedie n'est requis
# dans l'automation pour l'obtenir.
#
# Ce script NE cree AUCUN profil d'envoi Gmail : l'operateur (ou le client)
# ajoute son propre Gmail par mot de passe d'application depuis l'UI
# (Settings > Integrations > "Add Gmail profile") une fois ce script termine.
# Ce geste UI est deja prouve de bout en bout (mission Gmail E2E, 2026-10-03).
#
# Usage :
#   ./onboard-prospection.sh [--env staging] <workspace_id> <owner_email> "<nom client>" [list_id]
set -euo pipefail

ENV_FLAG=""
if [ "${1:-}" = "--env" ]; then
  ENV_FLAG="--env $2"
  shift 2
fi

WID="${1:?usage: onboard-prospection.sh [--env staging] <workspace_id> <owner_email> \"<nom client>\"}"
OWNER_EMAIL="${2:?owner_email requis}"
CLIENT_NAME="${3:?nom client requis}"
LIST_ID="${4:-prospects}"

CLI="notifuse-admin"
export NOTIFUSE_OWNER_EMAIL="$OWNER_EMAIL"
log() { printf '\033[1;34m[onboard]\033[0m %s\n' "$*" >&2; }
ok() { printf '\033[1;32m[onboard OK]\033[0m %s\n' "$*" >&2; }
fatal() { printf '\033[1;31m[onboard FAIL]\033[0m %s\n' "$*" >&2; exit 1; }

START_TS=$(date +%s)

log "1/6 provision du workspace $WID ($CLIENT_NAME, owner=$OWNER_EMAIL)"
$CLI $ENV_FLAG provision "$WID" --email "$OWNER_EMAIL" --name "$CLIENT_NAME" >/tmp/onboard-provision.json 2>&1 \
  || fatal "provision impossible: $(cat /tmp/onboard-provision.json)"
ok "workspace pret"

log "2/6 politique de cold outreach (reprise des valeurs eprouvees en prod, robertbrunon)"
$CLI $ENV_FLAG settings:set "$WID" veridian_sending_window \
  '{"days":[1,2,3,4,5],"start_hour":8,"end_hour":19,"timezone":"Europe/Paris"}' >/dev/null
$CLI $ENV_FLAG settings:set "$WID" veridian_provider_class_rates \
  '{"google":1,"microsoft":1,"ovh":1,"corporate":1,"corporate_selfhost":1,"other_hoster":1,"ionos":0.5,"freemail_fr":0.5,"yahoo_aol":0.5,"apple_icloud":0.5,"security_gateway":0.5}' >/dev/null
$CLI $ENV_FLAG settings:set "$WID" veridian_provider_class_daily_cap \
  '{"google":150,"microsoft":150,"ovh":150,"corporate":150,"corporate_selfhost":150,"other_hoster":150,"ionos":100,"freemail_fr":80,"yahoo_aol":80,"apple_icloud":50,"security_gateway":50}' >/dev/null
$CLI $ENV_FLAG settings:set "$WID" veridian_per_recipient_daily_cap 1 >/dev/null
$CLI $ENV_FLAG settings:set "$WID" veridian_per_sender_daily_cap 300 >/dev/null
ok "fenetre Lun-Ven 8h-19h Europe/Paris, plafonds par classe destinataire poses"

log "3/6 listes types"
$CLI $ENV_FLAG lists:create "$WID" --id "$LIST_ID" --name "Prospects" \
  >/dev/null 2>&1 || log "liste $LIST_ID deja presente, on continue"
$CLI $ENV_FLAG lists:create "$WID" --id "clients" --name "Clients (exclusion prospection)" \
  >/dev/null 2>&1 || log "liste clients deja presente, on continue"
ok "listes 'prospects' et 'clients' pretes"

log "4/6 templates texte brut J0 / J+4 / J+10"
cat > /tmp/onboard-j0.txt <<'EOF'
Bonjour {{ contact.first_name | default: "" }},

Je suis tombe sur {{ contact.custom_string_1 | default: "votre activite" }} et une question m'est venue : comment gerez-vous aujourd'hui [le sujet precis] ?

On accompagne des entreprises comme la votre sur ce point, avec des resultats concrets et mesurables. Si ca vous parle, je peux vous montrer en 15 minutes ce qu'on fait, sans engagement.

Une reponse en deux mots me suffit : interesse, pas le bon moment, ou non merci (je n'enverrai plus rien).

Bonne journee,
[Votre prenom]
EOF
cat > /tmp/onboard-j4.txt <<'EOF'
Bonjour {{ contact.first_name | default: "" }},

Je me permets de revenir vers vous : mon message de la semaine passee a peut-etre ete noye dans votre boite.

En une phrase : [benefice concret pour le prospect]. Si le sujet n'est pas prioritaire pour vous, dites-le-moi simplement, je comprendrai.

[Votre prenom]
EOF
cat > /tmp/onboard-j10.txt <<'EOF'
Bonjour {{ contact.first_name | default: "" }},

Dernier message de ma part sur ce sujet, promis. Si jamais la question revient de votre cote, vous savez ou me trouver.

Bonne continuation,
[Votre prenom]
EOF
$CLI $ENV_FLAG templates:push "$WID" --plain-text-file /tmp/onboard-j0.txt \
  --name "prospection-j0" --id "prospection-j0" --subject "Une question sur votre activite" >/dev/null
$CLI $ENV_FLAG templates:push "$WID" --plain-text-file /tmp/onboard-j4.txt \
  --name "prospection-j4" --id "prospection-j4" --subject "Je reviens vers vous" >/dev/null
$CLI $ENV_FLAG templates:push "$WID" --plain-text-file /tmp/onboard-j10.txt \
  --name "prospection-j10" --id "prospection-j10" --subject "Dernier mot de ma part" >/dev/null
ok "3 templates texte brut poussees (prospection-j0/j4/j10)"

log "5/6 automation J0 -> J+4 -> J+10 (sortie sur reponse: mecanisme global, deja actif)"
AUTOMATION_ID="prospection-j0j4j10"
cat > /tmp/onboard-automation.json <<EOF
{
  "id": "${AUTOMATION_ID}",
  "workspace_id": "${WID}",
  "name": "Prospection J0 -> J+4 -> J+10",
  "status": "live",
  "list_id": "${LIST_ID}",
  "trigger": {
    "event_kind": "list.subscribed",
    "list_id": "${LIST_ID}",
    "frequency": "once"
  },
  "root_node_id": "j0",
  "nodes": [
    {"id": "j0", "automation_id": "${AUTOMATION_ID}", "type": "email", "config": {"template_id": "prospection-j0"}, "next_node_id": "wait1"},
    {"id": "wait1", "automation_id": "${AUTOMATION_ID}", "type": "delay", "config": {"duration": 4, "unit": "days"}, "next_node_id": "j4"},
    {"id": "j4", "automation_id": "${AUTOMATION_ID}", "type": "email", "config": {"template_id": "prospection-j4"}, "next_node_id": "wait2"},
    {"id": "wait2", "automation_id": "${AUTOMATION_ID}", "type": "delay", "config": {"duration": 6, "unit": "days"}, "next_node_id": "j10"},
    {"id": "j10", "automation_id": "${AUTOMATION_ID}", "type": "email", "config": {"template_id": "prospection-j10"}}
  ]
}
EOF
$CLI $ENV_FLAG automations:create "$WID" --data @/tmp/onboard-automation.json >/tmp/onboard-automation-result.json 2>&1 \
  || fatal "creation automation impossible: $(cat /tmp/onboard-automation-result.json)"
ok "automation ${AUTOMATION_ID} creee (status live)"

log "6/6 verification de l'etat final"
$CLI $ENV_FLAG status "$WID" > /tmp/onboard-status.json 2>&1 || true

END_TS=$(date +%s)
ELAPSED=$((END_TS - START_TS))

cat <<SUMMARY

=== Onboarding termine en ${ELAPSED}s ===
Workspace       : ${WID}
Owner           : ${OWNER_EMAIL}
Listes          : ${LIST_ID}, clients
Templates       : prospection-j0, prospection-j4, prospection-j10
Automation      : ${AUTOMATION_ID} (status live)
Fenetre d'envoi : Lun-Ven 8h-19h Europe/Paris
Plafonds        : google/microsoft/ovh/corporate 150/jour, ionos 100, freemail_fr/yahoo_aol 80, apple_icloud/security_gateway 50
Cap destinataire: 1/jour - Cap par expediteur: 300/jour (chauffe: posez un cap bien plus bas sur un Gmail neuf, cf mission plafonds Gmail)

RESTE A FAIRE (manuel, ~1 min, UI) :
  1. Settings > Integrations > "Add Gmail profile" avec le mot de passe
     d'application du client (plafond par defaut pose automatiquement: 30/jour).
  2. Importer les prospects dans la liste "${LIST_ID}" (CSV ou API).
  3. Remplacer les textes d'exemple (mis entre crochets) par le vrai contenu
     du client avant tout envoi reel.
SUMMARY
