#!/usr/bin/env bash
# frozen-files-check.sh : un diff ne doit pas ajouter de logique Veridian aux fichiers gelés (lot L0, 29/10/2026).
#
# Les fichiers listés dans scripts/veridian/frozen-files.tsv n'acceptent, par diff (BASE_REF...HEAD), qu'UNE ligne
# de logique ajoutée (le branchement vers internal/app/veridian_wiring.go). Ne comptent pas : les suppressions, les
# lignes vides, les commentaires, les lignes qui ne font que fermer un bloc.
#
#   exit 0 = conforme (ou dérogation tracée) ; exit 1 = un fichier gelé reçoit plus d'une ligne de logique ;
#   exit 2 = NON ABOUTI (base introuvable, liste illisible, fichier gelé disparu, autotest en échec).
#
# Usage : BASE_REF=origin/veridian scripts/veridian/frozen-files-check.sh      (défaut : origin/veridian)
#         scripts/veridian/frozen-files-check.sh --selftest                      (l'outil s'éprouve sur des diffs provoqués)
# Dérogation (sync upstream, déplacement de câblage) : trailer « Frozen-Override: <raison> » dans un commit de la plage,
# ou FROZEN_OVERRIDE="<raison>". Elle passe, mais s'affiche en tête de la sortie.
# Hébergement : hook .husky/pre-push (local) et job « Constitution CI §1 » de veridian-ci.yml (runner auto-hébergé).
set -uo pipefail

SELF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SELF/../.." && pwd)"

# Lignes de logique ajoutées lues sur un diff -U0 : stdin = diff d'un fichier, stdout = une ligne par ajout comptable.
logic_added() {
  awk '
    /^\+\+\+/ { next }
    /^\+/ {
      s = substr($0, 2); gsub(/^[ \t]+|[ \t]+$/, "", s)
      if (s == "") next
      if (s ~ /^(\/\/|\/\*|\*|\*\/|#)/) next
      if (s ~ /^[])}, ;]+$/) next
      print s
    }'
}

# check <repo> <liste> <base> : écrit le rapport, rend 0/1/2.
check() {
  local repo="$1" list="$2" base="$3" override="${FROZEN_OVERRIDE:-}" rc=0 path max n lines
  [ -r "$list" ] || { echo "frozen-files-check : NON ABOUTI (liste $list illisible)" >&2; return 2; }
  git -C "$repo" rev-parse --verify --quiet "$base^{commit}" >/dev/null || { echo "frozen-files-check : NON ABOUTI (base $base introuvable)" >&2; return 2; }
  if [ -z "$override" ]; then
    override="$(git -C "$repo" log --format=%B "$base..HEAD" 2>/dev/null | sed -n 's/^Frozen-Override:[[:space:]]*//p' | head -1)"
  fi
  local report="" checked=0
  while IFS=$'\t' read -r path max; do
    case "$path" in ''|'#'*) continue;; esac
    [ -f "$repo/$path" ] || { echo "frozen-files-check : NON ABOUTI (fichier gelé introuvable : $path, la liste a dérivé)" >&2; return 2; }
    max="${max:-1}"
    checked=$((checked + 1))
    lines="$(git -C "$repo" diff -U0 "$base...HEAD" -- "$path" 2>/dev/null | logic_added)"
    n=0; [ -n "$lines" ] && n="$(printf '%s\n' "$lines" | wc -l)"
    if [ "$n" -gt "$max" ]; then
      rc=1
      report+="  GELE $path : $n lignes de logique ajoutées (maximum $max)"$'\n'
      report+="$(printf '%s\n' "$lines" | head -8 | sed 's/^/      + /')"$'\n'
    fi
  done < "$list"
  if [ "$checked" -eq 0 ]; then echo "frozen-files-check : NON ABOUTI (aucun fichier gelé dans $list)" >&2; return 2; fi
  if [ "$rc" -ne 0 ] && [ -n "$override" ]; then
    echo "frozen-files-check : DEROGATION « $override » (Frozen-Override) : $checked fichiers gelés contrôlés, dépassement toléré pour cette plage." >&2
    printf '%s' "$report" >&2
    return 0
  fi
  if [ "$rc" -ne 0 ]; then
    echo "frozen-files-check : ECHEC, logique ajoutée dans un fichier gelé. Le code Veridian vit dans internal/veridian/<module>/ ;" >&2
    echo "  le fichier gelé ne reçoit qu'UNE ligne de branchement vers internal/app/veridian_wiring.go (docs/veridian/MODULARITE.md)." >&2
    printf '%s' "$report" >&2
    return 1
  fi
  echo "frozen-files-check : $checked fichiers gelés, aucune logique ajoutée au-delà du branchement (base $base)."
  return 0
}

# Autotest : sur un dépôt jetable, le détecteur doit accepter ce qu'il doit accepter ET échouer sur le diff provoqué.
selftest() {
  local tmp rc=0; tmp="$(mktemp -d)" || return 2
  trap 'rm -rf "$tmp"' RETURN
  git -C "$tmp" init -q . && git -C "$tmp" config user.email t@t && git -C "$tmp" config user.name t || return 2
  mkdir -p "$tmp/internal/app"
  printf 'package app\n\nfunc a() {\n\tx := 1\n\t_ = x\n}\n' > "$tmp/internal/app/app.go"
  printf 'internal/app/app.go\t1\n' > "$tmp/list.tsv"
  git -C "$tmp" add -A && git -C "$tmp" commit -q -m base || return 2
  local base; base="$(git -C "$tmp" rev-parse HEAD)"
  expect() { # nom attendu message-de-commit contenu
    local name="$1" want="$2" msg="$3" body="$4" got
    git -C "$tmp" reset -q --hard "$base"
    printf '%b' "$body" > "$tmp/internal/app/app.go"
    git -C "$tmp" add -A && git -C "$tmp" commit -q -m "$msg" || { echo "selftest : commit impossible ($name)" >&2; rc=2; return; }
    check "$tmp" "$tmp/list.tsv" "$base" >/dev/null 2>&1; got=$?
    if [ "$got" -ne "$want" ]; then echo "selftest ECHEC : $name, attendu $want, obtenu $got" >&2; [ "$rc" -eq 0 ] && rc=1; fi
  }
  local HEAD_='package app\n\nfunc a() {\n\tx := 1\n\t_ = x\n'
  expect "une ligne de branchement" 0 "ok" "${HEAD_}\tWireVeridian(a)\n}\n"
  expect "trois lignes de logique" 1 "mal" "${HEAD_}\tfoo()\n\tbar()\n\tbaz()\n}\n"
  expect "deux lignes de logique" 1 "mal" "${HEAD_}\tfoo()\n\tbar()\n}\n"
  expect "commentaires et lignes vides seulement" 0 "ok" "${HEAD_}\n\t// === Veridian patch ===\n\t// plus de commentaire\n}\n"
  expect "suppression seule" 0 "ok" "package app\n\nfunc a() {\n}\n"
  expect "bloc if de trois lignes (deux de logique)" 1 "mal" "${HEAD_}\tif err := w(a); err != nil {\n\t\treturn err\n\t}\n}\n"
  expect "dérogation par trailer" 0 $'mal mais déclaré\n\nFrozen-Override: test de l\'autotest' "${HEAD_}\tfoo()\n\tbar()\n\tbaz()\n}\n"
  # un fichier gelé qui disparaît doit rendre « non abouti », jamais « conforme »
  git -C "$tmp" reset -q --hard "$base"; printf 'internal/app/absent.go\t1\n' > "$tmp/list2.tsv"
  check "$tmp" "$tmp/list2.tsv" "$base" >/dev/null 2>&1; [ $? -eq 2 ] || { echo "selftest ECHEC : fichier gelé absent non signalé" >&2; [ "$rc" -eq 0 ] && rc=1; }
  [ "$rc" -eq 0 ] && echo "frozen-files-check --selftest : 8 cas, le détecteur accepte le branchement et échoue sur le diff provoqué."
  return "$rc"
}

if [ "${1:-}" = "--selftest" ]; then selftest; exit $?; fi
cd "$ROOT" || exit 2
check "$ROOT" "${FROZEN_LIST:-$SELF/frozen-files.tsv}" "${BASE_REF:-origin/veridian}"
exit $?
