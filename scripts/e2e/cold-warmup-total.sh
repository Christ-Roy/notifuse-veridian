#!/usr/bin/env bash
# ============================================================================
# E2E ON-PREMISE — warmup = cap TOTAL par INFRA (toutes classes, MX compris)
# ============================================================================
# Ticket : todo/done/2026-06-19-audit-warmup-cap-non-enforce-mx-et-total.md (P1, 🔴).
#
# PROUVE EN CONDITIONS RÉELLES (vraie DB staging, vrai repo Create via cold-simulate,
# prédicat EXACT de la branche warmup de veridian_daily_cap.go :
# CountSentSinceForSenderDomain) que le cap WARMUP est un plafond HOLISTIQUE du
# VOLUME TOTAL émis par une infra (un domaine d'envoi), TOUTES classes destinataires
# confondues — y compris les classes MX que l'ancien COUNT-par-classe bypassait.
#
# 🔴 ZÉRO mail : on n'envoie RIEN. On seed des entrées message_history réelles
#    (mode seed_sent du VRAI repo Create) puis on interroge le prédicat exact du
#    gate (mode warmup_cap_decision). Aucune intégration SMTP, aucun broadcast,
#    aucun worker d'envoi sollicité → 0 risque de fuite.
#
# SCÉNARIO (le cœur de la preuve) :
#   1. workspace jetable (prefix gfwarm<stamp>, wipe à la fin), VIERGE.
#   2. warmup cap = 2 (passé en paramètre au prédicat ; on prouve le COMPTEUR).
#   3. seed 1 envoi GOOGLE (contact @gmail.com)   DEPUIS infra-a.fr
#      seed 1 envoi OVH-MX  (contact @une-pme.fr)  DEPUIS infra-a.fr
#      → total infra-a = 2, réparti sur DEUX classes différentes (dont une MX).
#   4. warmup_cap_decision sender_domain=infra-a, cap=2 → sent_today=2,
#      would_be_capped=TRUE.  ← BUG 1 (cap TOTAL, pas 2×classes) + BUG 2 (MX compté).
#   5. warmup_cap_decision sender_domain=infra-b, cap=2 → sent_today=0,
#      would_be_capped=FALSE. ← compteur SÉPARÉ par infra (cohérent multi-domaine).
#   6. warmup_cap_decision SANS sender_domain → pas d'attribution infra → 0/false
#      (le gate dégrade en pass : warmup non enforçable sans domaine émetteur).
#   7. recoupe le COUNT DB direct (psql) : 2 rows total infra-a, 0 row infra-b,
#      ET la PREUVE MX : le total NE filtre PAS par classe (1 gmail + 1 pme).
#
# ⚠️ ISOLATION (memory project_batterie_garde_fous_cold) : workspace JETABLE et
#    VIERGE → aucune pollution possible. Le COUNT warmup ne filtre PAS la classe
#    destinataire, donc aucune dépendance à la classification (MX/suffixe).
#
# Usage : scripts/e2e/cold-warmup-total.sh [--keep] [--workspace ID]
# Env requis : NOTIFUSE_HUB_API_SECRET (HMAC Hub→Notifuse staging)
# Env opt.  : NOTIFUSE_URL (défaut staging) · DEV_SSH (défaut dev-pub)
#             DB_CONTAINER (défaut notifuse-staging-db)
set -uo pipefail

BASE="${NOTIFUSE_URL:-https://notifuse.staging.veridian.site}"
DEV_SSH="${DEV_SSH:-dev-pub}"
DB_CONTAINER="${DB_CONTAINER:-notifuse-staging-db}"
KEEP=0
STAMP="$(date +%s | tail -c 7)"
WID="gfwarm${STAMP}"

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
GOOGLE_CONTACT="gf-warm-${STAMP}@gmail.com"     # classe google (suffixe connu)
# Contact d'un domaine custom : la classe réelle dépend du MX, mais le warmup NE
# CLASSE PAS le destinataire — c'est précisément ce qu'on prouve (Bug 2). Quelle que
# soit la classe (corporate_selfhost / ovh / autre), l'envoi compte dans le total.
MX_CONTACT="gf-warm-${STAMP}@une-pme-${STAMP}.fr"

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
cs()   { hmac /api/veridian/admin/cold-simulate POST "$1"; }
jget() { python3 -c "import json,sys;d=json.load(sys.stdin);print(d.get('$1'))" 2>/dev/null; }

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
# SETUP : workspace jetable
# ============================================================================
log "=== SETUP workspace JETABLE $WID sur $BASE ==="
PROV=$(hmac /api/tenants/provision POST \
  "{\"tenant_id\":\"$WID\",\"owner_email\":\"gf-${STAMP}@e2e.veridian.site\",\"plan\":\"free\"}")
AUTO_URL=$(echo "$PROV" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("auto_login_url",""))' 2>/dev/null)
[ -n "$AUTO_URL" ] || fatal "provision KO: ${PROV:0:200}"
log "workspace provisionné"

# ============================================================================
# SCÉNARIO WARMUP = CAP TOTAL PAR INFRA
# ============================================================================
log "########## SCÉNARIO WARMUP CAP TOTAL PAR INFRA ##########"
log "infra A=$INFRA_A (sender $SENDER_A) · infra B=$INFRA_B (sender $SENDER_B)"
log "warmup cap=2 ; on seed 1 google + 1 MX-custom DEPUIS infra-a → total 2 sur DEUX classes"

# Baseline : workspace vierge → 0 total quelle que soit l'infra.
r=$(cs "{\"mode\":\"warmup_cap_decision\",\"workspace_id\":\"$WID\",\"warmup_cap\":2,\"sender_domain\":\"$INFRA_A\"}")
base_a=$(echo "$r" | jget sent_today); base_a_cap=$(echo "$r" | jget would_be_capped); pa=$(echo "$r" | jget per_infra); sda=$(echo "$r" | jget sender_domain)
log "baseline infra-a: sent_today=$base_a capped=$base_a_cap per_infra=$pa sender_domain=$sda  (raw: ${r:0:200})"
check "baseline infra-a count" "${base_a:-x}" "0"
check "baseline infra-a capped" "${base_a_cap:-x}" "False"
check "per_infra flag (sender_domain fourni)" "${pa:-x}" "True"
check "sender_domain renvoyé normalisé" "${sda:-x}" "$INFRA_A"

# SEED #1 : 1 envoi GOOGLE depuis infra-a.fr.
log "seed 1 envoi GOOGLE ($GOOGLE_CONTACT) depuis $SENDER_A"
r=$(cs "{\"mode\":\"seed_sent\",\"workspace_id\":\"$WID\",\"contact_email\":\"$GOOGLE_CONTACT\",\"count\":1,\"sender_email\":\"$SENDER_A\"}")
check "seed google posé (1 entrée vers le contact)" "$(echo "$r" | jget sent_today)" "1"

# SEED #2 : 1 envoi vers un domaine CUSTOM (classe MX/corporate_selfhost) depuis infra-a.fr.
log "seed 1 envoi MX-custom ($MX_CONTACT) depuis $SENDER_A — classe NON google"
r=$(cs "{\"mode\":\"seed_sent\",\"workspace_id\":\"$WID\",\"contact_email\":\"$MX_CONTACT\",\"count\":1,\"sender_email\":\"$SENDER_A\"}")
check "seed MX-custom posé (1 entrée vers le contact)" "$(echo "$r" | jget sent_today)" "1"

# Laisse la DB committer (best-effort, le seed est synchrone mais on est large).
sleep 1

# (1) infra-A : TOTAL = 2 (google + MX), peu importe la classe → cap 2 atteint → CAPÉ.
#     C'est la PREUVE des DEUX bugs corrigés :
#       - Bug 1 : le total agrège DEUX classes (pas un compteur par classe).
#       - Bug 2 : l'envoi MX-custom EST compté (l'ancien chemin par classe l'ignorait).
r=$(cs "{\"mode\":\"warmup_cap_decision\",\"workspace_id\":\"$WID\",\"warmup_cap\":2,\"sender_domain\":\"$INFRA_A\"}")
a_count=$(echo "$r" | jget sent_today); a_capped=$(echo "$r" | jget would_be_capped)
log "infra-a après 2 seeds (2 classes): sent_today=$a_count capped=$a_capped  (raw: ${r:0:200})"
check "infra-a TOTAL = 2 (google + MX agrégés)" "${a_count:-x}" "2"
check "infra-a would_be_capped = TRUE (cap TOTAL atteint)" "${a_capped:-x}" "True"

# Sous-preuve cap-1 : à cap=1, déjà 2 envois → toujours capé (le total prime, pas la classe).
r=$(cs "{\"mode\":\"warmup_cap_decision\",\"workspace_id\":\"$WID\",\"warmup_cap\":1,\"sender_domain\":\"$INFRA_A\"}")
check "infra-a cap=1 → would_be_capped TRUE (2>=1)" "$(echo "$r" | jget would_be_capped)" "True"
# Et à cap=3 (au-dessus du total), l'infra n'est PAS encore plafonnée (la rampe respire).
r=$(cs "{\"mode\":\"warmup_cap_decision\",\"workspace_id\":\"$WID\",\"warmup_cap\":3,\"sender_domain\":\"$INFRA_A\"}")
check "infra-a cap=3 → would_be_capped FALSE (2<3, palier supérieur)" "$(echo "$r" | jget would_be_capped)" "False"

# (2) infra-B : compteur SÉPARÉ → total = 0 < cap 2 → NON capé.  ← isolation multi-infra
r=$(cs "{\"mode\":\"warmup_cap_decision\",\"workspace_id\":\"$WID\",\"warmup_cap\":2,\"sender_domain\":\"$INFRA_B\"}")
b_count=$(echo "$r" | jget sent_today); b_capped=$(echo "$r" | jget would_be_capped)
log "infra-b: sent_today=$b_count capped=$b_capped  (raw: ${r:0:200})"
check "infra-b TOTAL = 0 (compteur séparé)" "${b_count:-x}" "0"
check "infra-b would_be_capped = FALSE (indépendant de A)" "${b_capped:-x}" "False"

# (3) SANS sender_domain → pas d'attribution infra → warmup non enforçable (pass).
r=$(cs "{\"mode\":\"warmup_cap_decision\",\"workspace_id\":\"$WID\",\"warmup_cap\":2}")
n_count=$(echo "$r" | jget sent_today); n_capped=$(echo "$r" | jget would_be_capped); pn=$(echo "$r" | jget per_infra)
log "sans sender_domain: sent_today=$n_count capped=$n_capped per_infra=$pn  (raw: ${r:0:200})"
check "no-sender count = 0 (pas d'attribution)" "${n_count:-x}" "0"
check "no-sender would_be_capped = FALSE (warmup non enforçable, pass)" "${n_capped:-x}" "False"
check "no-sender per_infra = FALSE" "${pn:-x}" "False"

# ============================================================================
# RECOUPEMENT DB DIRECT (psql) — la vérité brute, dont la PREUVE MX
# ============================================================================
log "########## RECOUPEMENT DB DIRECT (psql sur $WSDB) ##########"
# TOTAL par domaine émetteur infra-a (le prédicat exact du gate, SANS filtre classe).
db_a_total=$(psqlq "SELECT count(*) FROM message_history WHERE lower(split_part(veridian_sender_email,'@',2))='$INFRA_A' AND sent_at >= date_trunc('day', now() AT TIME ZONE 'utc')")
db_b_total=$(psqlq "SELECT count(*) FROM message_history WHERE lower(split_part(veridian_sender_email,'@',2))='$INFRA_B' AND sent_at >= date_trunc('day', now() AT TIME ZONE 'utc')")
# Détail par classe destinataire (prouve que les 2 envois infra-a sont sur 2 classes).
db_a_gmail=$(psqlq "SELECT count(*) FROM message_history WHERE lower(split_part(veridian_sender_email,'@',2))='$INFRA_A' AND lower(split_part(contact_email,'@',2))='gmail.com' AND sent_at >= date_trunc('day', now() AT TIME ZONE 'utc')")
db_a_nongmail=$(psqlq "SELECT count(*) FROM message_history WHERE lower(split_part(veridian_sender_email,'@',2))='$INFRA_A' AND lower(split_part(contact_email,'@',2))<>'gmail.com' AND sent_at >= date_trunc('day', now() AT TIME ZONE 'utc')")
log "DB direct: infra_a_total=$db_a_total (gmail=$db_a_gmail, non-gmail=$db_a_nongmail)  infra_b_total=$db_b_total"
check "DB infra-a TOTAL = 2 (toutes classes)" "${db_a_total:-x}" "2"
check "DB infra-a gmail = 1" "${db_a_gmail:-x}" "1"
check "DB infra-a non-gmail = 1 (la classe MX/custom EST comptée)" "${db_a_nongmail:-x}" "1"
check "DB infra-b TOTAL = 0" "${db_b_total:-x}" "0"

# ============================================================================
# RÉCAP
# ============================================================================
echo "" >&2
printf '\033[1;36m=========== RÉCAP WARMUP CAP TOTAL PAR INFRA (%s) ===========\033[0m\n' "$WID" >&2
if [ "$FAILS" -eq 0 ]; then
  printf '\033[1;32m✓ WARMUP = CAP TOTAL HOLISTIQUE — infra plafonnée sur le total (2 classes dont 1 MX), infra-b libre, DB recoupée. Feu vert prod.\033[0m\n' >&2
  exit 0
else
  printf '\033[1;31m✗ %d ASSERTION(S) EN ÉCHEC — NE PAS PROMOUVOIR.\033[0m\n' "$FAILS" >&2
  exit 1
fi
