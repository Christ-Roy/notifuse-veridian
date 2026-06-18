#!/usr/bin/env bash
# ============================================================================
# E2E ON-PREMISE — cap journalier par CLASSE keyé PAR INFRA ÉMETTRICE
# ============================================================================
# Ticket : todo/2026-06-18-cap-journalier-par-infra-emettrice-x-classe.md (P1, 🔴).
#
# PROUVE EN CONDITIONS RÉELLES (vraie DB staging, vrai repo Create via cold-simulate,
# prédicat EXACT du gate veridian_daily_cap.go:veridianCountClassForInfra) que le
# compteur du cap-CLASSE est SÉPARÉ PAR DOMAINE ÉMETTEUR : deux infras d'envoi
# frappant la même classe destinataire ont chacune leur propre plafond.
#
# 🔴 ZÉRO mail : on n'envoie RIEN. On seed des entrées message_history réelles
#    (mode seed_sent du VRAI repo Create) puis on interroge le prédicat exact du
#    gate (mode class_cap_decision étendu, param sender_domain). Aucune intégration
#    SMTP, aucun broadcast, aucun worker d'envoi sollicité → 0 risque de fuite.
#
# SCÉNARIO 2-INFRAS (le cœur de la preuve) :
#   1. workspace jetable (prefix gfinfra<stamp>, wipe à la fin).
#   2. cap classe google = 1/jour (posé en config workspace, par cohérence ; le
#      prédicat interrogé prend le cap en PARAMÈTRE, donc on ne dépend pas de la
#      persistance — mais on le pose quand même pour documenter le réglage réel).
#   3. seed 1 envoi google (contact @gmail.com) DEPUIS le domaine émetteur infra-a.fr.
#   4. class_cap_decision google, sender_domain=infra-a.fr → would_be_capped=TRUE
#      (compteur infra-a = 1 >= cap 1).  ← infra A est au plafond
#   5. class_cap_decision google, sender_domain=infra-b.fr → would_be_capped=FALSE
#      (compteur infra-b = 0 < cap 1).   ← PREUVE : infra B a son propre compteur
#   6. (garde-fou non-régression) class_cap_decision SANS sender_domain → COUNT
#      workspace-global = 1 (toutes infras) → would_be_capped=TRUE. Le legacy compte
#      bien l'envoi de A.
#   7. recoupe le COUNT DB direct (psql) : 1 row google total, 1 row sender infra-a,
#      0 row sender infra-b.
#
# ⚠️ ISOLATION (memory project_batterie_garde_fous_cold) : workspace JETABLE et
#    VIERGE → aucune pollution de classe possible (rien n'a jamais envoyé dedans).
#    La classe google est dérivée du suffixe @gmail.com (domaines connus, pas de MX).
#
# Usage : scripts/e2e/cold-cap-par-infra.sh [--keep] [--workspace ID]
# Env requis : NOTIFUSE_HUB_API_SECRET (HMAC Hub→Notifuse staging)
# Env opt.  : NOTIFUSE_URL (défaut staging) · DEV_SSH (défaut dev-pub)
#             DB_CONTAINER (défaut notifuse-staging-db)
set -uo pipefail

BASE="${NOTIFUSE_URL:-https://notifuse.staging.veridian.site}"
DEV_SSH="${DEV_SSH:-dev-pub}"
DB_CONTAINER="${DB_CONTAINER:-notifuse-staging-db}"
KEEP=0
STAMP="$(date +%s | tail -c 7)"
WID="gfinfra${STAMP}"

while [ $# -gt 0 ]; do
  case "$1" in
    --keep) KEEP=1; shift;;
    --workspace) WID="$2"; shift 2;;
    *) echo "arg inconnu: $1" >&2; exit 2;;
  esac
done

: "${NOTIFUSE_HUB_API_SECRET:?NOTIFUSE_HUB_API_SECRET requis (HMAC Hub staging)}"

INFRA_A="infra-a-${STAMP}.fr"
INFRA_B="infra-b-${STAMP}.fr"
SENDER_A="bot@${INFRA_A}"
SENDER_B="bot@${INFRA_B}"
GOOGLE_CONTACT="gf-infra-${STAMP}@gmail.com"   # classe google par suffixe

log()  { printf '\033[1;34m[ci]\033[0m %s\n' "$*" >&2; }
ok()   { printf '\033[1;32m[ci PASS]\033[0m %s\n' "$*" >&2; }
bad()  { printf '\033[1;31m[ci FAIL]\033[0m %s\n' "$*" >&2; }
fatal(){ printf '\033[1;31m[ci FATAL]\033[0m %s\n' "$*" >&2; cleanup; exit 1; }

FAILS=0
check() { # $1=label $2=got $3=want
  if [ "$2" = "$3" ]; then ok "$1 : $2 (attendu $3)"; else bad "$1 : got=$2 attendu=$3"; FAILS=$((FAILS+1)); fi
}

# --- HMAC Hub helpers (canonical ${ts}.${rawBody}, cf. CLAUDE.md matrice) ----
hmac() { # $1=path $2=method $3=body
  local ts sig
  ts=$(date +%s%3N)
  sig=$(printf '%s.%s' "$ts" "$3" | openssl dgst -sha256 -hmac "$NOTIFUSE_HUB_API_SECRET" -r | awk '{print $1}')
  curl -s -X "$2" "$BASE$1" -H 'content-type: application/json' \
    -H 'x-veridian-app: hub' -H "x-veridian-timestamp: $ts" \
    -H "X-Veridian-Hub-Signature: $sig" -d "$3"
}
api()     { curl -s -X POST "$BASE$1" -H 'content-type: application/json' -H "Authorization: Bearer $OWNER_TOKEN" -d "$2"; }
api_get() { curl -s "$BASE$1" -H "Authorization: Bearer $OWNER_TOKEN"; }
cs()      { hmac /api/veridian/admin/cold-simulate POST "$1"; }
jget()    { python3 -c "import json,sys;d=json.load(sys.stdin);print(d.get('$1'))" 2>/dev/null; }

WSDB="notifuse_ws_${WID}"
psqlq() { ssh "$DEV_SSH" "docker exec $DB_CONTAINER psql -U postgres -d $WSDB -tAc \"$1\"" 2>/dev/null | tr -d '\r'; }

cleanup() {
  if [ "$KEEP" = "1" ]; then log "--keep : workspace $WID conservé"; return; fi
  [ -n "${WID:-}" ] || return
  log "wipe workspace $WID"
  hmac /api/veridian/admin/wipe-test-tenants POST \
    "{\"tenant_ids\":[\"$WID\"],\"safety_client_prefixes\":[\"canary\"]}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ============================================================================
# SETUP : workspace jetable + owner session
# ============================================================================
log "=== SETUP workspace JETABLE $WID sur $BASE ==="
PROV=$(hmac /api/tenants/provision POST \
  "{\"tenant_id\":\"$WID\",\"owner_email\":\"gf-${STAMP}@e2e.veridian.site\",\"plan\":\"free\"}")
AUTO_URL=$(echo "$PROV" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("auto_login_url",""))' 2>/dev/null)
[ -n "$AUTO_URL" ] || fatal "provision KO: ${PROV:0:200}"
OWNER_TOKEN=$(curl -s "$AUTO_URL" | grep -oP "setItem\('auth_token', \"\K[^\"]+")
[ -n "$OWNER_TOKEN" ] || fatal "owner token (auto-login) KO"
log "owner session ok"

# Pose le cap classe google=1 au NIVEAU WORKSPACE (réglage réel documenté ; le
# prédicat interrogé prend toutefois le cap en paramètre, donc le test ne dépend
# pas de cette persistance — il prouve le COMPTEUR, pas le réglage).
SETT=$(api_get "/api/workspaces.get?id=$WID" | python3 -c '
import json,sys
w=json.load(sys.stdin)["workspace"]; s=w["settings"]
s["veridian_provider_class_daily_cap"]={"google":1}
print(json.dumps({"id":w["id"],"name":w["name"],"settings":s}))')
api /api/workspaces.update "$SETT" >/dev/null || log "(workspaces.update cap : non bloquant pour le test)"
log "cap classe google=1 posé (config workspace, documenté)"

# ============================================================================
# SCÉNARIO 2-INFRAS
# ============================================================================
log "########## SCÉNARIO CAP CLASSE PAR INFRA ÉMETTRICE ##########"
log "infra A=$INFRA_A (sender $SENDER_A) · infra B=$INFRA_B (sender $SENDER_B) · classe=google ($GOOGLE_CONTACT)"

# Baseline : workspace vierge → 0 envoi google quelle que soit l'infra.
r=$(cs "{\"mode\":\"class_cap_decision\",\"workspace_id\":\"$WID\",\"provider_class\":\"google\",\"class_cap\":1,\"sender_domain\":\"$INFRA_A\"}")
base_a=$(echo "$r" | jget sent_today); base_a_cap=$(echo "$r" | jget would_be_capped); pa=$(echo "$r" | jget per_infra); sda=$(echo "$r" | jget sender_domain)
log "baseline infra-a: sent_today=$base_a capped=$base_a_cap per_infra=$pa sender_domain=$sda  (raw: ${r:0:200})"
check "baseline infra-a count" "${base_a:-x}" "0"
check "baseline infra-a capped" "${base_a_cap:-x}" "False"
check "per_infra flag (sender_domain fourni)" "${pa:-x}" "True"
check "sender_domain renvoyé normalisé" "${sda:-x}" "$INFRA_A"

# SEED : 1 envoi google DEPUIS infra-a.fr (le VRAI repo Create pose contact_email
# @gmail.com + veridian_sender_email bot@infra-a.fr).
log "seed 1 envoi google depuis $SENDER_A"
r=$(cs "{\"mode\":\"seed_sent\",\"workspace_id\":\"$WID\",\"contact_email\":\"$GOOGLE_CONTACT\",\"count\":1,\"sender_email\":\"$SENDER_A\"}")
seeded=$(echo "$r" | jget sent_today)
log "seed_sent → sent_today(contact)=$seeded (raw: ${r:0:160})"
check "seed posé (1 entrée)" "${seeded:-x}" "1"

# Laisse la DB committer (best-effort, le seed est synchrone mais on est large).
sleep 1

# (1) infra-A frappe google → compteur infra-a = 1 >= cap 1 → CAPÉ.
r=$(cs "{\"mode\":\"class_cap_decision\",\"workspace_id\":\"$WID\",\"provider_class\":\"google\",\"class_cap\":1,\"sender_domain\":\"$INFRA_A\"}")
a_count=$(echo "$r" | jget sent_today); a_capped=$(echo "$r" | jget would_be_capped)
log "infra-a après seed: sent_today=$a_count capped=$a_capped  (raw: ${r:0:200})"
check "infra-a count = 1" "${a_count:-x}" "1"
check "infra-a would_be_capped = TRUE" "${a_capped:-x}" "True"

# (2) infra-B frappe google → compteur infra-b = 0 < cap 1 → NON capé.  ← LA PREUVE
r=$(cs "{\"mode\":\"class_cap_decision\",\"workspace_id\":\"$WID\",\"provider_class\":\"google\",\"class_cap\":1,\"sender_domain\":\"$INFRA_B\"}")
b_count=$(echo "$r" | jget sent_today); b_capped=$(echo "$r" | jget would_be_capped)
log "infra-b après seed: sent_today=$b_count capped=$b_capped  (raw: ${r:0:200})"
check "infra-b count = 0 (compteur séparé)" "${b_count:-x}" "0"
check "infra-b would_be_capped = FALSE (PREUVE isolation)" "${b_capped:-x}" "False"

# (3) NON-RÉGRESSION : SANS sender_domain → COUNT workspace-global = 1 (l'envoi de A
#     est bien compté globalement), per_infra=false.
r=$(cs "{\"mode\":\"class_cap_decision\",\"workspace_id\":\"$WID\",\"provider_class\":\"google\",\"class_cap\":1}")
g_count=$(echo "$r" | jget sent_today); g_capped=$(echo "$r" | jget would_be_capped); pg=$(echo "$r" | jget per_infra)
log "global (legacy): sent_today=$g_count capped=$g_capped per_infra=$pg  (raw: ${r:0:200})"
check "global count = 1 (toutes infras)" "${g_count:-x}" "1"
check "global would_be_capped = TRUE" "${g_capped:-x}" "True"
check "per_infra = FALSE (chemin global)" "${pg:-x}" "False"

# ============================================================================
# RECOUPEMENT DB DIRECT (psql) — la vérité brute, pas seulement le prédicat HTTP
# ============================================================================
log "########## RECOUPEMENT DB DIRECT (psql sur $WSDB) ##########"
db_total=$(psqlq "SELECT count(*) FROM message_history WHERE lower(split_part(contact_email,'@',2))='gmail.com' AND sent_at >= date_trunc('day', now() AT TIME ZONE 'utc')")
db_a=$(psqlq "SELECT count(*) FROM message_history WHERE lower(split_part(contact_email,'@',2))='gmail.com' AND lower(split_part(veridian_sender_email,'@',2))='$INFRA_A' AND sent_at >= date_trunc('day', now() AT TIME ZONE 'utc')")
db_b=$(psqlq "SELECT count(*) FROM message_history WHERE lower(split_part(contact_email,'@',2))='gmail.com' AND lower(split_part(veridian_sender_email,'@',2))='$INFRA_B' AND sent_at >= date_trunc('day', now() AT TIME ZONE 'utc')")
log "DB direct: total_gmail=$db_total  sender_infra_a=$db_a  sender_infra_b=$db_b"
check "DB total gmail aujourd'hui = 1" "${db_total:-x}" "1"
check "DB sender=infra-a = 1" "${db_a:-x}" "1"
check "DB sender=infra-b = 0" "${db_b:-x}" "0"

# ============================================================================
# RÉCAP
# ============================================================================
echo "" >&2
printf '\033[1;36m=========== RÉCAP CAP CLASSE PAR INFRA (%s) ===========\033[0m\n' "$WID" >&2
if [ "$FAILS" -eq 0 ]; then
  printf '\033[1;32m✓ COMPTEUR CAP-CLASSE SÉPARÉ PAR INFRA ÉMETTRICE — infra-a capée, infra-b libre, DB recoupée. Feu vert prod.\033[0m\n' >&2
  exit 0
else
  printf '\033[1;31m✗ %d ASSERTION(S) EN ÉCHEC — NE PAS PROMOUVOIR.\033[0m\n' "$FAILS" >&2
  exit 1
fi
