#!/usr/bin/env bash
# lint-workflows.sh — Constitution CI §20 (defense in depth runners)
#
# Vérifie que TOUS les jobs `runs-on: [self-hosted, ...]` ont au moins
# un step avec `if: always()` qui fait du cleanup runner. Sans ça, le
# disk se remplit avec les artefacts Docker abandonnés et le runner
# crash silencieusement après quelques cycles.
#
# Règle :
#   Si un job a `runs-on: [self-hosted, ...]` ou `runs-on: self-hosted`,
#   il DOIT contenir au moins une ligne `if: always()` dans ses steps.
#   Le check est conservateur : on ne valide pas le CONTENU du step
#   always() (libre de faire docker prune, df, sudo rm, etc.), juste
#   qu'il existe.
#
# Usage :
#   scripts/ci/lint-workflows.sh
#   → exit 0 si tous les jobs self-hosted ont un cleanup always().
#   → exit 1 sinon, avec la liste des jobs en faute.

set -euo pipefail

APP_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$APP_ROOT"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
BLUE=$'\033[0;34m'
NC=$'\033[0m'

WORKFLOWS_DIR=".github/workflows"
if [ ! -d "$WORKFLOWS_DIR" ]; then
  echo "${BLUE}ℹ Pas de répertoire $WORKFLOWS_DIR — skip${NC}"
  exit 0
fi

FAILED=0
TOTAL_JOBS=0
SELF_HOSTED_JOBS=0

# Parse chaque .yml du dossier workflows.
# Pour chaque job (ligne `<indent>job-id:` au début d'un bloc jobs:),
# on capture :
#   - Le nom du job (clé YAML)
#   - Si runs-on contient self-hosted
#   - Si le bloc contient au moins une ligne `if: always()`
#
# Méthode : on traite chaque fichier comme un flux. Un job se termine
# au prochain "<indent>id:" de même niveau OU à la fin du fichier.
# Pour simplifier, on utilise yq si dispo, sinon fallback awk.

# Bash + awk + while-pipe perdrait les variables, donc on parse chaque
# fichier dans un loc et on agrège dans le scope du script.
VIOLATIONS=""
for wf in "$WORKFLOWS_DIR"/*.yml "$WORKFLOWS_DIR"/*.yaml; do
  [ ! -f "$wf" ] && continue
  echo "${BLUE}── $wf${NC}"

  parse_output=$(awk '
    /^jobs:$/             { in_jobs=1; next }
    in_jobs && /^[a-zA-Z_-]/ { in_jobs=0 }
    !in_jobs              { next }
    /^  [a-zA-Z_][a-zA-Z0-9_-]*:[[:space:]]*$/ {
      if (current_job != "") { printf "%s|%d|%d\n", current_job, has_self_hosted, has_always }
      gsub(/^  /,""); gsub(/:.*/,"")
      current_job = $0
      has_self_hosted = 0; has_always = 0; next
    }
    current_job != "" && /^[[:space:]]+runs-on:.*self-hosted/ { has_self_hosted = 1 }
    current_job != "" && /if:[[:space:]]*always\(\)/ { has_always = 1 }
    END {
      if (current_job != "") { printf "%s|%d|%d\n", current_job, has_self_hosted, has_always }
    }
  ' "$wf")

  while IFS='|' read -r job_id is_self_hosted has_always; do
    [ -z "$job_id" ] && continue
    TOTAL_JOBS=$((TOTAL_JOBS + 1))
    if [ "$is_self_hosted" = "1" ]; then
      SELF_HOSTED_JOBS=$((SELF_HOSTED_JOBS + 1))
      if [ "$has_always" = "1" ]; then
        echo "  ${GREEN}✓ $job_id (self-hosted, cleanup always() présent)${NC}"
      else
        echo "  ${RED}✗ $job_id (self-hosted SANS step if: always())${NC}"
        VIOLATIONS="${VIOLATIONS}${wf}:${job_id}\n"
        FAILED=$((FAILED + 1))
      fi
    fi
  done <<< "$parse_output"
done

echo
echo "Total jobs analysés : $TOTAL_JOBS"
echo "Self-hosted jobs    : $SELF_HOSTED_JOBS"
echo "Violations Sec.20    : $FAILED"

# --- Loi 7 (CLAUDE.md, rouge) : zero runner GitHub heberge -----------------
# Un depassement de facturation Actions bloque le COMPTE ENTIER (incident
# du 14/09 : une boutique n'a plus deploye pendant 5 jours). Toute ligne
# `runs-on: ubuntu-*|macos-*|windows-*` est interdite, sauf exception
# explicite et mesuree : marqueur `# gitops-ci-allow-hosted: <raison>` sur
# la MEME ligne (ex: build docker limite par le disque du runner mesure a
# 13G/72G libres, ou cle SSH prod isolee sur runner ephemere). Voir
# docs/AUDIT-FORK-UPSTREAM-2026-09-28.md Sec.5 et les 4 exceptions marquees
# dans veridian-ci.yml (build, deploy-prod, rollback, test-go).
echo
echo "${BLUE}-- Loi 7 : zero runner GitHub heberge (sauf exception explicite) --${NC}"
HOSTED_VIOLATIONS=$(grep -rnE "runs-on:[[:space:]]*(\[[[:space:]]*)?['\"]?(ubuntu-|macos-|windows-)" "$WORKFLOWS_DIR" --include='*.yml' --include='*.yaml' | grep -v 'gitops-ci-allow-hosted' || true)
HOSTED_FAILED=0
if [ -n "$HOSTED_VIOLATIONS" ]; then
  HOSTED_FAILED=$(echo "$HOSTED_VIOLATIONS" | grep -c .)
  echo "${RED}✗ $HOSTED_FAILED runs-on sur runner GitHub heberge SANS exception :${NC}"
  echo "$HOSTED_VIOLATIONS"
else
  echo "${GREEN}✓ Aucun runner GitHub heberge sans exception explicite${NC}"
fi
echo "Violations Loi 7     : $HOSTED_FAILED"

TOTAL_FAILED=$((FAILED + HOSTED_FAILED))
echo
echo "Violations totales   : $TOTAL_FAILED"

if [ "$TOTAL_FAILED" -gt 0 ]; then
  echo
  echo "${RED}╔════════════════════════════════════════════════════════════════════╗${NC}"
  echo "${RED}║ WORKFLOW LINT -- $TOTAL_FAILED violation(s) (Sec.20 cleanup + Loi 7 runner)${NC}"
  echo "${RED}╚════════════════════════════════════════════════════════════════════╝${NC}"
  if [ "$FAILED" -gt 0 ]; then
    echo "Constitution CI Sec.20 : tout runner self-hosted DOIT avoir un step cleanup if: always()."
    echo "Sinon le disk dev-pub se remplit et le runner crash silencieusement."
    echo -e "Violations Sec.20 :\n$VIOLATIONS"
  fi
  if [ "$HOSTED_FAILED" -gt 0 ]; then
    echo "Loi 7 (CLAUDE.md) : aucun runner GitHub heberge sans exception mesuree et marquee."
    echo -e "Violations Loi 7 :\n$HOSTED_VIOLATIONS"
  fi
  exit 1
fi

echo "${GREEN}✓ Tous les jobs self-hosted ont un cleanup always() (Constitution Sec.20) et aucun runner GitHub heberge sans exception (Loi 7)${NC}"
exit 0
