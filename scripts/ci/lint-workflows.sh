#!/usr/bin/env bash
# lint-workflows.sh — garde-fous sur les `runs-on` des workflows.
#
# Trois familles de runner, trois regles :
#
#  1. Runner HEBERGE GitHub (`ubuntu-*`, `macos-*`, `windows-*`) : interdit
#     sur un depot PRIVE (loi 7 : un depassement de facturation bloque le
#     COMPTE ENTIER). Ce depot est PUBLIC, donc gratuit, mais on ne le laisse
#     pas se propager sans decision : la ligne `runs-on:` doit porter le
#     marqueur `# gitops-ci-allow-hosted: <raison>` sur la MEME ligne.
#
#  2. Runner JIT du parc (`[self-hosted, veridian-jit...]`) : un runner par job,
#     sur le tailnet, avec des secrets du parc. Aucune PR de fork ne doit y
#     arriver : le job doit porter, dans son `if:` (avant `steps:`), la garde
#     `github.event.pull_request.head.repo.full_name == github.repository`.
#     Pas de nettoyage disque a exiger : le runner est detruit apres le job.
#
#  3. Runner self-hosted PERMANENT (tout autre `self-hosted`) : le job doit
#     garder un step `if: always()` qui nettoie (sinon le disque se remplit).
#     Plus aucun job n'en a ici ; la regle reste si l'un revient.
#
# Usage :
#   scripts/ci/lint-workflows.sh            # lit .github/workflows
#   WORKFLOWS_DIR=<dossier> scripts/ci/lint-workflows.sh
#   scripts/ci/lint-workflows.sh --selftest # prouve que le lint rougit
#     sur un cas fautif de chaque famille, et verdit sur un cas conforme.
# Sortie : 0 = conforme, 1 = violation, 2 = non abouti (autotest rate).

set -euo pipefail

APP_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

analyse() {  # $1 = dossier de workflows ; ecrit les violations sur stdout
  local dir="$1" wf
  for wf in "$dir"/*.yml "$dir"/*.yaml; do
    [ -f "$wf" ] || continue
    awk -v wf="$wf" '
      function fin() {
        if (job == "") return
        if (hosted && !marque) printf "HEBERGE|%s:%s\n", wf, job
        if (jit && !garde) printf "JIT|%s:%s\n", wf, job
        if (perm && !always) printf "PERMANENT|%s:%s\n", wf, job
      }
      /^jobs:[[:space:]]*$/ { in_jobs=1; next }
      in_jobs && /^[a-zA-Z_-]/ { fin(); job=""; in_jobs=0 }
      !in_jobs { next }
      /^  [a-zA-Z_][a-zA-Z0-9_-]*:[[:space:]]*$/ {
        fin(); job=$1; sub(/:$/, "", job)
        hosted=0; marque=0; jit=0; garde=0; perm=0; always=0; steps=0; next
      }
      job == "" { next }
      /^    steps:/ { steps=1 }
      /^    runs-on:/ {
        if ($0 ~ /veridian-jit/) jit=1
        else if ($0 ~ /self-hosted/) perm=1
        else if ($0 ~ /(ubuntu-|macos-|windows-)/) {
          hosted=1
          if ($0 ~ /gitops-ci-allow-hosted:/) marque=1
        }
      }
      !steps && /head\.repo\.full_name[[:space:]]*==[[:space:]]*github\.repository/ { garde=1 }
      /if:[[:space:]]*always\(\)/ { always=1 }
      END { fin() }
    ' "$wf"
  done
}

if [ "${1:-}" = "--selftest" ]; then
  t="$(mktemp -d)"; trap 'rm -rf "$t"' EXIT
  w() { mkdir -p "$t/$1"; cat > "$t/$1/x.yml"; }
  w ok <<'Y'
name: x
jobs:
  a:
    runs-on: ubuntu-latest  # gitops-ci-allow-hosted: public
    steps:
      - run: echo
  b:
    if: github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository
    runs-on: [self-hosted, veridian-jit]
    steps:
      - run: echo
Y
  w heberge <<'Y'
name: x
jobs:
  a:
    runs-on: ubuntu-latest
    steps:
      - run: echo
Y
  w jit <<'Y'
name: x
jobs:
  b:
    runs-on: [self-hosted, veridian-jit]
    steps:
      - run: echo
Y
  w permanent <<'Y'
name: x
jobs:
  c:
    runs-on: [self-hosted, linux, x64]
    steps:
      - run: echo
Y
  rc=0
  [ -z "$(analyse "$t/ok")" ] || { echo "AUTOTEST NON ABOUTI : le cas conforme rougit"; rc=2; }
  for k in heberge jit permanent; do
    analyse "$t/$k" | grep -qi "^${k}|" || { echo "AUTOTEST NON ABOUTI : le cas fautif '$k' ne rougit pas"; rc=2; }
  done
  [ "$rc" = 0 ] && echo "autotest OK : 3 cas fautifs rougissent, 1 cas conforme verdit"
  exit "$rc"
fi

cd "$APP_ROOT"
DIR="${WORKFLOWS_DIR:-.github/workflows}"
[ -d "$DIR" ] || { echo "pas de $DIR : rien a controler"; exit 0; }

# Le lint se prouve avant de juger.
"$0" --selftest >/dev/null || { echo "le lint ne passe pas son autotest : non abouti"; exit 2; }

V="$(analyse "$DIR")"
n_jobs=$(grep -cE '^  [a-zA-Z_][a-zA-Z0-9_-]*:[[:space:]]*$' "$DIR"/*.yml 2>/dev/null | awk -F: '{s+=$2} END {print s+0}')
echo "workflows : $DIR ($n_jobs entrees de niveau 2, jobs et cles confondus)"
if [ -n "$V" ]; then
  echo "VIOLATIONS :"
  echo "$V" | while IFS='|' read -r kind where; do
    case "$kind" in
      HEBERGE)   echo "  $where : runner heberge sans marqueur '# gitops-ci-allow-hosted: <raison>' (loi 7)";;
      JIT)       echo "  $where : runner JIT sans garde fork (head.repo.full_name == github.repository)";;
      PERMANENT) echo "  $where : runner self-hosted permanent sans step if: always() de nettoyage";;
    esac
  done
  exit 1
fi
echo "OK : runners heberges marques, jobs JIT gardes contre les forks, aucun runner permanent sans nettoyage"
