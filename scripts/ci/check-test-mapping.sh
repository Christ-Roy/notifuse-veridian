#!/usr/bin/env bash
# check-test-mapping.sh (Go variant — Notifuse)
#
# Standard CI Veridian — règle 1-pour-1 stricte pour Go.
# Bloque tout push qui modifie un handler/service/repo sans test colocalisé.
#
# Convention Go : test colocalisé à côté du source.
#   internal/http/user_handler.go → internal/http/user_handler_test.go
#   internal/service/foo_service.go → internal/service/foo_service_test.go
#
# Scopes critiques :
#   internal/http/**/*.go        (handlers HTTP)
#   internal/service/**/*.go     (business logic)
#   internal/repository/**/*.go  (data access)
#   internal/domain/**/*.go      (domain models avec methods)
#
# Comptage 1-pour-1 :
#   Nouvelle func exportée (majuscule initiale) → exige nouveau Test* dans _test.go
#
set -euo pipefail

BASE_REF="${BASE_REF:-origin/main}"
APP_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$APP_ROOT"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
BLUE=$'\033[0;34m'
NC=$'\033[0m'

# ─── Diff Git ────────────────────────────────────────────────────────────────
MODE="committed"
if [ "$BASE_REF" = "HEAD" ]; then
  CHANGED=$(git diff --name-only HEAD; git diff --cached --name-only)
  CHANGED=$(echo "$CHANGED" | sort -u | grep -v '^$' || true)
  MODE="working-tree"
else
  if ! git rev-parse --verify --quiet "$BASE_REF" >/dev/null 2>&1; then
    echo "${YELLOW}⚠ $BASE_REF inaccessible, fallback sur HEAD~1${NC}"
    BASE_REF="HEAD~1"
  fi
  CHANGED=$(git diff --name-only "$BASE_REF"...HEAD 2>/dev/null || true)
fi

if [ -z "$CHANGED" ]; then
  echo "${GREEN}✓ Aucun fichier modifié${NC}"
  exit 0
fi

PENDING_FILE="tests-pending.txt"
COVERAGE_MAP="test-coverage-map.yaml"

# ─── Bypass upstream sync ────────────────────────────────────────────────────
# Si tous les commits du diff sont d'un auteur upstream Notifuse, on bypass
# le mapping pour les fichiers non-veridian_*. Les fichiers veridian_*.go
# restent sous discipline stricte même dans un sync upstream.
#
# Auteurs upstream connus (Notifuse, Inc.) :
#   - pierre@notifuse.com
#   - pierre@bazoge.com
#   - pierre@Air-de-Pierre.lan, pierre@Host-002.lan (hostnames Pierre)
#   - *@notifuse.com (catch-all futurs contributeurs upstream)
UPSTREAM_EMAIL_REGEX='@notifuse\.com$|^pierre@bazoge\.com$|^pierre@(Air-de-Pierre|Host-[0-9]+)\.lan$'

is_upstream_only_diff() {
  # Retourne 0 si tous les commits du diff range sont d'un auteur upstream.
  # Retourne 1 si au moins 1 commit est d'un auteur Veridian (ou si pas de range).
  [ "$MODE" = "working-tree" ] && return 1
  local authors
  authors=$(git log --pretty=format:'%ae' "$BASE_REF"...HEAD 2>/dev/null | sort -u)
  [ -z "$authors" ] && return 1
  while IFS= read -r email; do
    [ -z "$email" ] && continue
    echo "$email" | grep -qE "$UPSTREAM_EMAIL_REGEX" || return 1
  done <<< "$authors"
  return 0
}

is_veridian_file() {
  # Tout fichier dont le basename commence par veridian_ ou qui s'appelle
  # veridian.go / veridian_token.go (etc.) est Veridian-custom.
  local f="$1"
  case "$(basename "$f")" in
    veridian_*|veridian.go) return 0 ;;
    *) return 1 ;;
  esac
}

UPSTREAM_BYPASS=0
if is_upstream_only_diff; then
  UPSTREAM_BYPASS=1
  echo "${BLUE}ℹ Diff 100%% upstream (auteur Notifuse), bypass mapping sur fichiers non-veridian_*${NC}"
fi

in_pending() {
  local f="$1"
  [ -f "$PENDING_FILE" ] && grep -Fxq "$f" "$PENDING_FILE"
}

find_covering_tests() {
  local src="$1"
  [ ! -f "$COVERAGE_MAP" ] && return 1
  awk -v src="$src" '
    /^- sources:/        { in_sources=1; in_covered=0; matched=0; covers=""; next }
    /^  covered_by:/     { in_sources=0; in_covered=1; next }
    /^  reason:/         { in_covered=0; if (matched && covers) print covers; matched=0; covers=""; next }
    in_sources && /^    - / { gsub(/^    - /,""); if ($0 == src) matched=1 }
    in_covered && /^    - / { gsub(/^    - /,""); covers = covers (covers?"\n":"") $0 }
    END { if (matched && covers) print covers }
  ' "$COVERAGE_MAP"
}

# ─── Mapping canonique Go ────────────────────────────────────────────────────
expected_test_for() {
  local f="$1"
  case "$f" in
    internal/http/*_test.go|internal/service/*_test.go|internal/repository/*_test.go|internal/domain/*_test.go)
      return 1  # C'est un test, pas un source
      ;;
    internal/http/*.go|internal/service/*.go|internal/repository/*.go|internal/domain/*.go)
      # Convention Go : foo.go → foo_test.go au même niveau
      echo "${f%.go}_test.go"
      ;;
    *)
      return 1
      ;;
  esac
}

# ─── Comptage 1-pour-1 Go ────────────────────────────────────────────────────
diff_for() {
  local f="$1"
  if [ "$MODE" = "working-tree" ]; then
    git diff HEAD -- "$f" 2>/dev/null
  else
    git diff "$BASE_REF"...HEAD -- "$f" 2>/dev/null
  fi
}

# Nouvelles funcs exportées (majuscule initiale) — receivers ET top-level
count_new_exports() {
  local f="$1"
  diff_for "$f" \
    | grep -E '^\+[^+]' \
    | grep -cE '^\+\s*func\s+(\([^)]+\)\s+)?[A-Z][a-zA-Z0-9_]*\s*\(' || true
}

# Nouveaux TestXxx (convention go test)
count_new_tests() {
  local f="$1"
  [ ! -f "$f" ] && echo 0 && return
  diff_for "$f" \
    | grep -E '^\+[^+]' \
    | grep -cE '^\+\s*func\s+Test[A-Z][a-zA-Z0-9_]*\s*\(' || true
}

# ─── Boucle principale ───────────────────────────────────────────────────────
FAILED=0
WARNINGS=0

for f in $CHANGED; do
  [ ! -f "$f" ] && continue

  if ! expected_test=$(expected_test_for "$f" 2>/dev/null); then
    continue
  fi

  # Bypass upstream : si tout le diff est upstream ET le fichier n'est pas
  # veridian_*, on skip. Les fichiers veridian_* restent sous discipline
  # même si un commit upstream les a touchés (ce qui ne devrait jamais arriver
  # vu la convention, mais filet de sécurité).
  if [ "$UPSTREAM_BYPASS" = "1" ] && ! is_veridian_file "$f"; then
    echo "${BLUE}↷ $f bypass upstream${NC}"
    continue
  fi

  if in_pending "$f"; then
    echo "${YELLOW}⏸  $f en dette (tests-pending.txt)${NC}"
    WARNINGS=$((WARNINGS + 1))
    continue
  fi

  # Existence du test colocalisé
  test_file=""
  if [ -f "$expected_test" ]; then
    test_file="$expected_test"
  else
    covering=$(find_covering_tests "$f")
    if [ -n "$covering" ]; then
      while IFS= read -r cov; do
        [ -z "$cov" ] && continue
        if [ -f "$cov" ] && echo "$CHANGED" | grep -Fxq "$cov"; then
          test_file="$cov"
          break
        fi
      done <<< "$covering"
      if [ -z "$test_file" ]; then
        echo "${RED}✗ $f${NC}"
        echo "  Couvert par coverage map : $(echo "$covering" | tr '\n' ' ')"
        echo "  Mais aucun de ces tests n'est modifié."
        FAILED=$((FAILED + 1))
        continue
      fi
    else
      echo "${RED}✗ $f modifié sans test correspondant${NC}"
      echo "  Test attendu : ${expected_test}"
      echo "  OU déclarer dans test-coverage-map.yaml qu'un autre test le couvre."
      FAILED=$((FAILED + 1))
      continue
    fi
  fi

  # Test modifié dans la même PR
  if ! echo "$CHANGED" | grep -Fxq "$test_file"; then
    echo "${RED}✗ $f modifié, mais $test_file non touché${NC}"
    FAILED=$((FAILED + 1))
    continue
  fi

  # Comptage 1-pour-1
  new_exports=$(count_new_exports "$f")
  new_tests=$(count_new_tests "$test_file")

  if [ "$new_exports" -gt "$new_tests" ]; then
    echo "${RED}✗ $f : $new_exports nouvelles funcs exportées vs $new_tests nouveaux Test*${NC}"
    echo "  Règle 1-pour-1 : chaque func exportée doit avoir au moins 1 TestXxx."
    FAILED=$((FAILED + 1))
    continue
  fi

  echo "${GREEN}✓ $f → $test_file${NC} (exports=$new_exports tests=$new_tests)"
done

# ─── Migrations SQL (Notifuse a des migrations dans migrations/) ────────────
MIGRATION_CHANGES=$(echo "$CHANGED" | grep -E '^internal/migrations/.*\.sql$' || true)
if [ -n "$MIGRATION_CHANGES" ]; then
  echo
  echo "${BLUE}── Migrations SQL détectées ──${NC}"
  # Au moins un test integration ou repository test doit être modifié
  TESTS_TOUCHED=$(echo "$CHANGED" | grep -E '^internal/(repository|service)/.*_test\.go$' || true)
  if [ -z "$TESTS_TOUCHED" ]; then
    echo "${RED}✗ Migration SQL sans test repository/service modifié${NC}"
    FAILED=$((FAILED + 1))
  else
    echo "${GREEN}✓ Migration accompagnée de tests${NC}"
  fi
fi

echo
if [ "$FAILED" -gt 0 ]; then
  echo "${RED}╔════════════════════════════════════════════════════════════╗${NC}"
  echo "${RED}║ PUSH REFUSÉ — $FAILED violation(s) de la règle 1-pour-1     ║${NC}"
  echo "${RED}╚════════════════════════════════════════════════════════════╝${NC}"
  echo "Fix puis re-tente. JAMAIS --no-verify (Constitution CI §3)."
  exit 1
fi

if [ "$WARNINGS" -gt 0 ]; then
  echo "${YELLOW}⚠ $WARNINGS fichier(s) en dette tests-pending.txt — à résorber.${NC}"
fi

echo "${GREEN}✓ Mapping handler↔test OK (Go)${NC}"
exit 0
