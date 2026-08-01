#!/usr/bin/env bash
# check-migration-safety.sh — Constitution CI §12 (Expand & Contract)
#
# Bloque les migrations Go (internal/migrations/v*.go) qui contiennent des
# operations DESTRUCTIVES OU NON-ZERO-DOWNTIME en 1 seul deploy.
#
# Règle Expand & Contract : le tag Docker précédent doit toujours pouvoir
# tourner sur le schéma actuel. Les contractions (DROP, ALTER NOT NULL sur
# table peuplée, RENAME) exigent 2 PRs séparées sur 2 deploys distincts.
#
# Patterns refusés (sauf si dans tests-pending-migrations.txt) :
#   - DROP COLUMN sans IF EXISTS  → casse rollback (l'ancienne app lit la colonne)
#   - DROP TABLE                  → catastrophe
#   - ALTER COLUMN ... NOT NULL   → bloque sur table peuplée si nulls existants
#   - RENAME COLUMN / RENAME TO   → casse rollback (ancien nom)
#   - CREATE INDEX (sans CONCURRENTLY) → lock table pendant la création
#   - TRUNCATE                    → catastrophe
#
# Usage : appelé depuis check-test-mapping.sh OU autonome
#   BASE_REF=origin/veridian scripts/ci/check-migration-safety.sh
#
# Output : exit 1 si pattern dangereux trouvé. Le diff exact est affiché.

set -euo pipefail

BASE_REF="${BASE_REF:-origin/veridian}"
APP_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$APP_ROOT"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
BLUE=$'\033[0;34m'
NC=$'\033[0m'

ALLOWLIST_FILE="migrations-pending.txt"

# ─── Détermine la liste des fichiers de migration modifiés ──────────────────
if [ "$BASE_REF" = "HEAD" ]; then
  CHANGED=$(git diff --name-only HEAD; git diff --cached --name-only)
  CHANGED=$(echo "$CHANGED" | sort -u | grep -v '^$' || true)
else
  if ! git rev-parse --verify --quiet "$BASE_REF" >/dev/null 2>&1; then
    echo "${YELLOW}⚠ $BASE_REF inaccessible, skip migration safety check${NC}"
    exit 0
  fi
  CHANGED=$(git diff --name-only "$BASE_REF"...HEAD 2>/dev/null || true)
fi

MIGRATIONS=$(echo "$CHANGED" | grep -E '^internal/migrations/v[0-9]+\.go$' || true)

if [ -z "$MIGRATIONS" ]; then
  echo "${BLUE}ℹ Aucune migration modifiée — skip safety check${NC}"
  exit 0
fi

echo "${BLUE}── Migration safety check (Constitution §12 Expand & Contract) ──${NC}"
echo "Fichiers de migration modifiés :"
echo "$MIGRATIONS" | sed 's/^/  /'
echo

# ─── Patterns refusés ───────────────────────────────────────────────────────
# Regex case-insensitive (option -i). On match les lignes ajoutées dans le diff
# (préfixées par +) pour ne pas faire fail sur du SQL préexistant.
#
# Note : on accepte explicitement :
#   - DROP ... IF EXISTS (les guards rendent la migration idempotente, mais ATTENTION
#     ça ne protège PAS contre le break de rollback. On laisse passer pour les
#     DROP TRIGGER / DROP INDEX / DROP CONSTRAINT (commun et safe) mais on bloque
#     toujours DROP COLUMN même avec IF EXISTS.
#   - CREATE INDEX CONCURRENTLY (safe sans lock)
#   - CREATE INDEX IF NOT EXISTS CONCURRENTLY (combo idempotent + non-bloquant)

FAILED=0

check_pattern() {
  local file="$1"
  local pattern="$2"
  local label="$3"
  local hint="$4"

  # Diff lines added (préfixe +, hors header +++)
  local added
  added=$(git diff "$BASE_REF"...HEAD -- "$file" 2>/dev/null \
    | grep -E '^\+[^+]' \
    | grep -iE "$pattern" || true)

  if [ -n "$added" ]; then
    echo "${RED}✗ $file : $label${NC}"
    echo "$added" | sed 's/^/    /'
    echo "  ${YELLOW}→ $hint${NC}"
    FAILED=$((FAILED + 1))
  fi
}

for f in $MIGRATIONS; do
  # Allowlist (e.g. migration de bootstrap, déjà déployée, pas applicable)
  if [ -f "$ALLOWLIST_FILE" ] && grep -Fxq "$f" "$ALLOWLIST_FILE"; then
    echo "${YELLOW}⏸  $f en allowlist (migrations-pending.txt)${NC}"
    continue
  fi

  echo "${BLUE}Vérification $f...${NC}"

  # DROP COLUMN : toujours dangereux (rollback casse, ancienne app lit la colonne)
  check_pattern "$f" \
    'drop[[:space:]]+column' \
    'DROP COLUMN détecté — casse le rollback (ancienne image lit la colonne)' \
    'Expand & Contract : (1) deploy code qui n''écrit plus la colonne, (2) PR séparée pour DROP COLUMN après confirmation que rollback n''est plus nécessaire.'

  # DROP TABLE (hors IF EXISTS pour les tables de transition / dead code)
  check_pattern "$f" \
    'drop[[:space:]]+table[[:space:]]+(?!if[[:space:]]+exists)' \
    'DROP TABLE sans IF EXISTS — destructif et non idempotent' \
    'Migrer en 2 phases : (1) stop écriture côté code, (2) DROP TABLE IF EXISTS dans PR ultérieure.'

  # ALTER ... NOT NULL (sans DEFAULT — bloque sur rows existants null)
  # On match SET NOT NULL ou ALTER COLUMN ... NOT NULL
  check_pattern "$f" \
    'set[[:space:]]+not[[:space:]]+null|alter[[:space:]]+column[[:space:]]+[a-z_]+[[:space:]]+(set[[:space:]]+)?not[[:space:]]+null' \
    'ALTER COLUMN ... NOT NULL — bloque la migration si la table a des rows avec NULL' \
    'Expand & Contract : (1) ajouter contrainte CHECK + backfill async, (2) SET NOT NULL dans PR ultérieure une fois les NULLs purgés.'

  # RENAME COLUMN / RENAME TO (casse rollback : ancienne app lit l'ancien nom)
  check_pattern "$f" \
    'rename[[:space:]]+(column|to)' \
    'RENAME détecté — casse le rollback (ancienne image utilise l''ancien nom)' \
    'Expand & Contract : (1) ADD nouvelle colonne + dual-write, (2) DROP ancienne colonne dans PR ultérieure.'

  # CREATE INDEX sans CONCURRENTLY : lock table en écriture pendant la création
  # On exige soit CONCURRENTLY soit IF NOT EXISTS CONCURRENTLY.
  # Pattern : CREATE [UNIQUE] INDEX [IF NOT EXISTS] <name> ON ...
  # Refusé si pas de CONCURRENTLY dans la même ligne logique.
  added_idx=$(git diff "$BASE_REF"...HEAD -- "$f" 2>/dev/null \
    | grep -E '^\+[^+]' \
    | grep -iE 'create[[:space:]]+(unique[[:space:]]+)?index' \
    | grep -ivE 'concurrently' || true)
  if [ -n "$added_idx" ]; then
    echo "${RED}✗ $f : CREATE INDEX sans CONCURRENTLY${NC}"
    echo "$added_idx" | sed 's/^/    /'
    echo "  ${YELLOW}→ Sur Postgres, CREATE INDEX prend un AccessExclusiveLock — toutes les écritures sont bloquées le temps de la création. Utiliser CREATE INDEX CONCURRENTLY.${NC}"
    FAILED=$((FAILED + 1))
  fi

  # TRUNCATE : suppression de toutes les rows, jamais voulu en migration
  check_pattern "$f" \
    'truncate' \
    'TRUNCATE détecté — supprime toutes les rows, non récupérable' \
    'Si vraiment nécessaire, justifier dans la PR et déclarer dans migrations-pending.txt.'
done

echo
if [ "$FAILED" -gt 0 ]; then
  echo "${RED}╔════════════════════════════════════════════════════════════════════╗${NC}"
  echo "${RED}║ MIGRATION SAFETY — $FAILED violation(s) Expand & Contract           ║${NC}"
  echo "${RED}╚════════════════════════════════════════════════════════════════════╝${NC}"
  echo "Constitution CI §12 : le tag Docker précédent doit toujours pouvoir tourner sur le schéma actuel."
  echo "Si la migration est validée (cas de bootstrap, table morte, etc.), ajouter le fichier à $ALLOWLIST_FILE avec justification dans la PR."
  exit 1
fi

echo "${GREEN}✓ Migrations safe (Expand & Contract OK)${NC}"
exit 0
