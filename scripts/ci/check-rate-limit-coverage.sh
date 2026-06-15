#!/usr/bin/env bash
# check-rate-limit-coverage.sh (Notifuse Veridian)
#
# Garde-fou Constitution CI — couverture rate-limit GLOBAL de l'API.
# Complément (PAS doublon) de check-test-mapping.sh §"Routes API" qui, lui,
# vérifie que chaque route a un TEST. Ici on vérifie que chaque route est
# couverte par le RATE-LIMIT global (OWASP API4:2023, Unrestricted Resource
# Consumption).
#
# ─────────────────────────────────────────────────────────────────────────────
# CE QUE CE CHECK GARANTIT (et SES LIMITES — honnêteté avant tout) :
#
# Le rate-limit Notifuse est GLOBAL : un seul middleware
# (VeridianAPIRateLimitMiddleware) wrappe le handler racine qui sert TOUT le
# mux API (`a.mux`). Conséquence : toute route montée sur `a.mux` est
# automatiquement couverte, sans déclaration par-route. Il n'y a donc RIEN à
# vérifier route par route — ce serait un faux sentiment de sécurité.
#
# Le SEUL vrai risque de trou est qu'une route échappe à ce handler racine :
#   (a) le middleware global n'est plus branché dans app.go (régression /
#       suppression accidentelle lors d'un sync upstream ou refactor) ;
#   (b) quelqu'un monte un SECOND serveur HTTP / un mux parallèle qui sert de
#       l'API sans passer par le wrapper global.
#
# Ce check détecte EXACTEMENT ces deux cas :
#   RÈGLE 1 (BLOQUANTE) : VeridianAPIRateLimitMiddleware DOIT être appelé dans
#                         internal/app/app.go. Absent → le rate-limit global a
#                         disparu → push refusé.
#   RÈGLE 2 (INFORMATIVE / WARNING) : tout `http.NewServeMux()` hors allowlist
#                         connue est signalé. Un nouveau mux n'est PAS forcément
#                         un trou (il peut servir des metrics sur un autre port,
#                         comme pkg/tracing), mais il MÉRITE une revue humaine :
#                         s'il sert de l'API publique, il doit être wrappé.
#
# CE QUE CE CHECK NE GARANTIT PAS (assumé) :
#   - Il ne prouve pas qu'un nouveau mux parallèle est SAINEMENT wrappé ou non
#     (il ne fait pas d'analyse de flot). Il alerte, un humain tranche.
#   - Il ne vérifie pas les SEUILS (calibrage = décision produit, pas CI).
#   - Il ne remplace pas les tests du middleware (cf. veridian_rate_limit_test.go,
#     règle 1-pour-1 de check-test-mapping.sh).
#
# Fail-safe : sort en erreur si la règle 1 casse. La règle 2 n'échoue jamais le
# push (consigne Robert : "ne casse pas la CI pour rien").
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

APP_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$APP_ROOT"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
BLUE=$'\033[0;34m'
NC=$'\033[0m'

APP_FILE="internal/app/app.go"
MIDDLEWARE_FILE="internal/http/middleware/veridian_rate_limit.go"
MIDDLEWARE_FUNC="VeridianAPIRateLimitMiddleware"

# Allowlist des fichiers autorisés à contenir un http.NewServeMux() SANS être
# un trou de rate-limit :
#   - internal/app/app.go      : le mux API racine (a.mux), wrappé par le
#                                middleware global → couvert par construction.
#   - pkg/tracing/tracing.go   : mux dédié à l'endpoint /metrics Prometheus,
#                                exposé sur un PORT SÉPARÉ (pas l'API publique,
#                                pas d'accès tenant). Hors périmètre API4.
KNOWN_MUX_FILES=(
  "internal/app/app.go"
  "pkg/tracing/tracing.go"
)

echo "${BLUE}── Vérification couverture rate-limit global (OWASP API4) ──${NC}"

FAIL=0

# ─── RÈGLE 1 (BLOQUANTE) : le middleware global est branché ──────────────────
if [ ! -f "$MIDDLEWARE_FILE" ]; then
  echo "${RED}✗ $MIDDLEWARE_FILE introuvable — le middleware rate-limit global a disparu.${NC}"
  echo "  Ce middleware ferme le trou OWASP API4:2023 sur TOUTE l'API authentifiée."
  echo "  Restaure-le ou réintroduis le rate-limit global avant de pousser."
  FAIL=1
elif ! grep -q "func ${MIDDLEWARE_FUNC}" "$MIDDLEWARE_FILE"; then
  echo "${RED}✗ ${MIDDLEWARE_FUNC} absent de $MIDDLEWARE_FILE.${NC}"
  FAIL=1
fi

# grep le câblage dans app.go (hors lignes commentées : on retire le préfixe
# d'indentation puis on exclut les lignes commençant par //).
if [ -f "$APP_FILE" ]; then
  wired=$(grep -n "middleware\.${MIDDLEWARE_FUNC}" "$APP_FILE" \
    | grep -v -E '^[0-9]+:[[:space:]]*//' || true)
  if [ -z "$wired" ]; then
    echo "${RED}✗ ${MIDDLEWARE_FUNC} n'est PAS câblé dans $APP_FILE.${NC}"
    echo "  Le rate-limit global doit wrapper le handler racine (cf. Start())."
    echo "  Sans lui, TOUS les endpoints authentifiés JWT/HMAC sont sans rate-limit"
    echo "  (trou OWASP API4:2023). Re-branche middleware.${MIDDLEWARE_FUNC}."
    FAIL=1
  else
    echo "${GREEN}✓ ${MIDDLEWARE_FUNC} câblé dans $APP_FILE${NC}"
  fi
else
  echo "${RED}✗ $APP_FILE introuvable.${NC}"
  FAIL=1
fi

# ─── RÈGLE 2 (INFORMATIVE) : mux parallèle hors allowlist ────────────────────
# Cherche tout fichier non-test qui crée un http.NewServeMux().
mux_files=$(grep -rln "http\.NewServeMux()" \
  --include='*.go' internal/ pkg/ cmd/ 2>/dev/null \
  | grep -v '_test\.go' | sort -u || true)

unknown_mux=""
for f in $mux_files; do
  is_known=0
  for known in "${KNOWN_MUX_FILES[@]}"; do
    if [ "$f" = "$known" ]; then
      is_known=1
      break
    fi
  done
  if [ "$is_known" -eq 0 ]; then
    unknown_mux="${unknown_mux}${f}\n"
  fi
done

if [ -n "$unknown_mux" ]; then
  echo
  echo "${YELLOW}⚠ Mux HTTP parallèle(s) détecté(s) hors allowlist :${NC}"
  printf "%b" "$unknown_mux" | sed 's/^/    /'
  echo "${YELLOW}  → REVUE HUMAINE requise (non bloquant) :${NC}"
  echo "    Si ce mux sert des routes /api/ publiques, il N'EST PAS couvert par"
  echo "    le rate-limit global (qui ne wrappe que le handler racine app.go)."
  echo "    Soit le wrapper avec ${MIDDLEWARE_FUNC}, soit ajoute-le à"
  echo "    KNOWN_MUX_FILES dans $(basename "$0") avec un commentaire justifiant"
  echo "    pourquoi il est hors périmètre (ex: metrics sur un autre port)."
else
  echo "${GREEN}✓ Aucun mux HTTP parallèle hors allowlist (pas de bypass possible)${NC}"
fi

echo

if [ "$FAIL" -ne 0 ]; then
  echo "${RED}✗ check-rate-limit-coverage : le rate-limit global est cassé/absent.${NC}"
  exit 1
fi

echo "${GREEN}✓ Rate-limit global de l'API en place — couverture OWASP API4 OK${NC}"
exit 0
