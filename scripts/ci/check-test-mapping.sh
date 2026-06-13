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

# Note : on ne sort PAS si CHANGED est vide. La règle "couverture routes API"
# tourne dans tous les cas pour bloquer la dette préexistante (mode Nuclear).
# Le mapping 1-pour-1 et le check migrations sont skip si CHANGED vide.
if [ -z "$CHANGED" ]; then
  echo "${BLUE}ℹ Aucun fichier modifié dans le diff — vérification routes API uniquement${NC}"
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

is_file_upstream_only() {
  # Variante PER-FICHIER de is_upstream_only_diff, pour les SYNC UPSTREAM mergés.
  #
  # Pourquoi : un `git merge upstream/main` crée une plage BASE_REF...HEAD dont
  # les auteurs sont MIXTES — les 13 commits Notifuse (@notifuse.com / bazoge)
  # ET les merge commits Veridian (auteur brunon5). is_upstream_only_diff() ci-
  # dessus lit les auteurs de TOUTE la plage en tout-ou-rien, donc le bypass
  # global n'active jamais sur un merge → les fichiers upstream-purs touchés
  # SEULEMENT par les commits Notifuse (ex. transactional_service.go) tombent à
  # tort sous la discipline mapping alors qu'ils ont déjà leurs tests upstream.
  #
  # Cette fonction restreint le test d'auteur aux commits qui ont RÉELLEMENT
  # touché le fichier `f` dans la plage. Bypass UNIQUEMENT si TOUS ces commits
  # matchent UPSTREAM_EMAIL_REGEX (réutilisé, pas dupliqué). Si un seul commit
  # Veridian (brunon5 ou autre non-upstream) a touché `f` → return 1 → `f` reste
  # sous discipline mapping STRICTE. Conséquence voulue : les fichiers upstream
  # qu'un agent Veridian a modifiés cette session (diffs INLINE : workspace.go,
  # queue_message_sender.go, message_sender.go, workspace_service.go,
  # template_compilation.go…) ne sont JAMAIS bypassés — leur merge commit
  # Veridian les a touchés. Les fichiers veridian_*.go sont en plus exclus par
  # is_veridian_file() à l'appel (cf. boucle principale).
  local f="$1"
  [ "$MODE" = "working-tree" ] && return 1
  local authors
  authors=$(git log --pretty=format:'%ae' "$BASE_REF"...HEAD -- "$f" 2>/dev/null | sort -u)
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
    # Mocks auto-générés (mockgen) : pas de test colocalisé.
    internal/domain/mocks/*.go|internal/*/mocks/*.go)
      return 1
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

  # Bypass upstream GLOBAL : si TOUTE la plage est upstream (diff linéaire pur,
  # ex. cherry-pick d'un commit Notifuse) ET le fichier n'est pas veridian_*,
  # on skip. Les fichiers veridian_* restent sous discipline même si un commit
  # upstream les a touchés (ne devrait jamais arriver vu la convention, filet
  # de sécurité).
  if [ "$UPSTREAM_BYPASS" = "1" ] && ! is_veridian_file "$f"; then
    echo "${BLUE}↷ $f bypass upstream (plage 100%% upstream)${NC}"
    continue
  fi

  # Bypass upstream PER-FICHIER : cas d'un SYNC UPSTREAM mergé (plage à auteurs
  # mixtes upstream+Veridian, où le bypass global ne s'active pas). On skip un
  # fichier non-veridian_* si TOUS les commits qui l'ont touché dans la plage
  # sont upstream — il a déjà ses tests upstream et aucun commit Veridian ne l'a
  # modifié. Un fichier qu'un commit Veridian a touché (y compris le merge
  # commit s'il y a résolu un conflit) reste sous discipline mapping stricte.
  if ! is_veridian_file "$f" && is_file_upstream_only "$f"; then
    echo "${BLUE}↷ $f bypass upstream (fichier touché uniquement par des commits Notifuse)${NC}"
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

# ─── Migrations Go (Notifuse — internal/migrations/v*.go) ──────────────────
# 2 checks distincts :
#   a) Mapping migration ↔ test colocalisé (vXX.go ↔ vXX_test.go)
#   b) Safety check : Expand & Contract (Constitution §12) via script dédié.
MIGRATION_CHANGES=$(echo "$CHANGED" | grep -E '^internal/migrations/v[0-9]+\.go$' || true)
if [ -n "$MIGRATION_CHANGES" ]; then
  echo
  echo "${BLUE}── Migrations Go détectées ──${NC}"
  # a) Test colocalisé : chaque vXX.go doit avoir un vXX_test.go modifié.
  for mf in $MIGRATION_CHANGES; do
    expected="${mf%.go}_test.go"
    if ! echo "$CHANGED" | grep -Fxq "$expected"; then
      echo "${RED}✗ $mf modifié sans $expected${NC}"
      FAILED=$((FAILED + 1))
    fi
  done

  # b) Safety check Expand & Contract (DROP COLUMN, NOT NULL, RENAME, etc.)
  SAFETY_SCRIPT="$(dirname "$0")/check-migration-safety.sh"
  if [ -x "$SAFETY_SCRIPT" ]; then
    if ! BASE_REF="$BASE_REF" "$SAFETY_SCRIPT"; then
      FAILED=$((FAILED + 1))
    fi
  else
    echo "${YELLOW}⚠ $SAFETY_SCRIPT manquant ou non exécutable — safety check SKIPPED${NC}"
  fi
fi

# ─── Routes API — couverture stricte (Nuclear, 0 dette autorisée) ──────────────
# Règle 1 : toute route déclarée (mux.Handle("/api/...")) doit avoir au moins
#           un test qui exerce cette route via httptest.NewRequest(... "/api/...").
# Règle 2 : si une route est modifiée dans le diff (déclaration OU code du
#           handler), au moins un test exerçant cette route doit aussi être
#           modifié dans le même push.
#
# Le bypass upstream s'applique : si le diff est 100% upstream Notifuse,
# on skip ces checks (l'upstream a sa propre discipline).

if [ "$UPSTREAM_BYPASS" != "1" ]; then
  echo
  echo "${BLUE}── Vérification couverture routes API ──${NC}"

  # Collecte routes déclarées (hors _test.go et hors lignes commentées)
  # On grep dans les fichiers non-test du package http, puis on filtre les
  # lignes commençant par // (commentaires de doc avec exemples).
  # Le regex matche AUSSI le pattern Go 1.22+ "METHOD /path" :
  #   mux.Handle("/api/foo", ...)              → ancien style
  #   mux.Handle("POST /api/foo", ...)         → Go 1.22+
  #   mux.HandleFunc("GET /api/bar", ...)      → Go 1.22+
  # `|| true` après chaque pipe-with-grep car set -e fait fail un pipe vide.
  #
  # Routes REST parametrées avec `{id}` (cf. /api/tenants/{id}/status) sont
  # EXCLUES du check Nuclear — sinon il faut écrire un check de "préfixe
  # contient" côté tests, et la résolution se ferait à coup de heuristiques
  # fragiles. À la place : les tests de ces routes utilisent leur propre
  # mux.Handler() check (cf. TestVeridianHandleAttachOwner_RegisteredInRoutes).
  declared_routes=$( { find internal/http -name '*.go' ! -name '*_test.go' \
      -exec grep -hE 'mux\.(Handle|HandleFunc)\("([A-Z]+ )?(/api/[^"]+)"' {} + 2>/dev/null || true; } \
    | { grep -vE '^\s*//' || true; } \
    | { grep -oE '/api/[a-zA-Z._-]+' || true; } \
    | sort -u)

  # Collecte routes testées — 2 patterns supportés :
  #   a) httptest.NewRequest(method, "/api/foo.bar", ...)
  #   b) httptest.NewServer(mux) + URL via fmt.Sprintf("%s/api/foo.bar", serverURL)
  #      ou variantes ("...%s/api/...", concat strings, etc.)
  # On extrait toute occurrence de /api/<route> dans les fichiers _test.go,
  # ce qui couvre les 2 patterns (et tout autre usage légitime de la route).
  tested_routes=$( { find internal -name '*_test.go' \
      -exec grep -hoE '/api/[a-zA-Z._-]+' {} + 2>/dev/null || true; } \
    | sort -u)

  # Règle 1 : routes orphelines (déclarées sans test)
  orphans=$(comm -23 <(echo "$declared_routes") <(echo "$tested_routes"))
  if [ -n "$orphans" ]; then
    orphan_count=$(echo "$orphans" | wc -l)
    echo "${RED}✗ $orphan_count route(s) API déclarée(s) sans test httptest.NewRequest :${NC}"
    echo "$orphans" | sed 's/^/    /'
    echo "  Règle Nuclear : ajouter un test qui fait httptest.NewRequest(..., \"<route>\") pour chacune."
    FAILED=$((FAILED + orphan_count))
  else
    echo "${GREEN}✓ Toutes les routes déclarées ont un test (couverture 100%)${NC}"
  fi

  # Règle 2 : routes touchées dans le diff (handlers modifiés) doivent avoir
  # leur test correspondant aussi modifié.
  #
  # Pour chaque fichier handler modifié, on extrait les routes qu'il déclare,
  # puis on vérifie qu'au moins un test modifié dans le push exerce cette route.
  HANDLERS_TOUCHED=$(echo "$CHANGED" | grep -E '^internal/http/[^/]+\.go$' | grep -vE '_test\.go$' || true)
  # Bypass upstream PER-FICHIER (cf. is_file_upstream_only) : un handler touché
  # UNIQUEMENT par des commits Notifuse dans un sync mergé garde ses tests de
  # routes upstream — il ne doit pas exiger un test Veridian. Un handler que TU
  # as modifié (auteur Veridian dans la plage) ou veridian_* reste sous discipline.
  if [ -n "$HANDLERS_TOUCHED" ]; then
    _filtered_handlers=""
    for _h in $HANDLERS_TOUCHED; do
      if ! is_veridian_file "$_h" && is_file_upstream_only "$_h"; then
        echo "${BLUE}↷ routes de $_h bypass upstream (handler touché uniquement par des commits Notifuse)${NC}"
        continue
      fi
      _filtered_handlers="$_filtered_handlers $_h"
    done
    HANDLERS_TOUCHED=$(echo "$_filtered_handlers" | tr ' ' '\n' | grep -v '^$' || true)
  fi
  if [ -n "$HANDLERS_TOUCHED" ]; then
    TESTS_TOUCHED_FILES=$(echo "$CHANGED" | grep -E '^internal/(http|service|repository|domain)/.*_test\.go$' || true)
    if [ -z "$TESTS_TOUCHED_FILES" ]; then
      # Aucun _test.go modifié dans tout le push, mais des handlers oui → block
      # (sauf si tous les handlers touchés sont uniquement des _test.go : déjà filtré)
      # Note : ce cas devrait déjà être bloqué par la règle 1-pour-1 ci-dessus,
      # mais on garde un message clair côté routes.
      :
    else
      # Pour chaque handler touché, ses routes doivent être exercées par un test touché
      for handler in $HANDLERS_TOUCHED; do
        # Skip fichiers en pending (déjà couverts par la dette globale, mais en Nuclear
        # tests-pending est vide donc tous les handlers veridian_* sont in scope)
        in_pending "$handler" && continue

        # Skip fichiers qui ne déclarent pas de route (utils, helpers, etc.)
        # Même regex Go 1.22+ tolerant aux préfixes METHOD que la règle 1.
        # Pipes safe (|| true) sinon set -e tue le script sur grep no-match.
        # Routes parametrées `{id}` exclues : même logique que règle 1.
        handler_routes=$( { grep -hE 'mux\.(Handle|HandleFunc)\("([A-Z]+ )?(/api/[^"]+)"' "$handler" 2>/dev/null || true; } \
          | { grep -vE '^\s*//' || true; } \
          | { grep -oE '/api/[a-zA-Z._-]+' || true; } \
          | sort -u)
        [ -z "$handler_routes" ] && continue

        # Vérifie qu'au moins une route du handler est exercée par un _test.go modifié
        route_test_found=0
        while IFS= read -r route; do
          [ -z "$route" ] && continue
          for tf in $TESTS_TOUCHED_FILES; do
            [ ! -f "$tf" ] && continue
            if grep -qF "\"$route" "$tf" 2>/dev/null; then
              route_test_found=1
              break 2
            fi
          done
        done <<< "$handler_routes"

        if [ "$route_test_found" = "0" ]; then
          echo "${RED}✗ $handler modifié (déclare des routes API) mais aucun test modifié n'exerce ses routes :${NC}"
          echo "$handler_routes" | sed 's/^/    /'
          echo "  Règle Nuclear : modifier un _test.go qui fait httptest.NewRequest sur ces routes."
          FAILED=$((FAILED + 1))
        fi
      done
    fi
  fi
fi

echo
if [ "$FAILED" -gt 0 ]; then
  echo "${RED}╔══════════════════════════════════════════════════════════════════╗${NC}"
  echo "${RED}║ PUSH REFUSÉ — $FAILED violation(s) (mapping 1-pour-1 + routes API) ║${NC}"
  echo "${RED}╚══════════════════════════════════════════════════════════════════╝${NC}"
  echo "Mode Nuclear : 0 dette autorisée."
  echo "Fix puis re-tente. JAMAIS --no-verify (Constitution CI §3)."
  echo
  echo "${YELLOW}message de robert: NE BACLE PAS LES TESTS, il faut les tester${NC}"
  echo "${YELLOW}et s'assurer qu'ils soient pertinent et ne casse pas la ci pour${NC}"
  echo "${YELLOW}rien et qu'ils durent !${NC}"
  exit 1
fi

if [ "$WARNINGS" -gt 0 ]; then
  echo "${YELLOW}⚠ $WARNINGS fichier(s) en dette tests-pending.txt — à résorber.${NC}"
fi

echo "${GREEN}✓ Mapping handler↔test OK (Go)${NC}"
exit 0
