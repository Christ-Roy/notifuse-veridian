#!/usr/bin/env bash
# lourd-ou-local.sh [--go] '<commande shell>' : une étape lourde d'un crochet git, hors du bastion quand on y est.
#
# Règle commune des dépôts (parente, 26/11/2026) : sur le bastion, l'étape part par `lourd-si-bastion` (rise ou node02, nice 19).
# Si lourd n'a RIEN fait tourner (« NON ABOUTI », code 2), l'étape ne passe PAS pour autant : elle est relancée ici, en
# `nice -n 19`. Les tests restent obligatoires avant tout push ; un nœud injoignable ne les saute jamais.
# Hors bastion (CI, autre nœud), lourd-si-bastion exécute sur place. Sans lourd-si-bastion sur la machine : local, nice 19.
# Lancer depuis la racine du dépôt (lourd copie l'arbre de travail, sans .git ni node_modules).
# Code de sortie : celui de l'étape (0 vert, autre = rouge).
set -uo pipefail
OPT=()
[ "${1:-}" = "--go" ] && { OPT=(--go); shift; }
[ $# -eq 1 ] || { echo "usage : lourd-ou-local.sh [--go] '<commande shell>'" >&2; exit 2; }
CMD="$1"
LSB="$(command -v lourd-si-bastion 2>/dev/null || echo "$HOME/bin/lourd-si-bastion")"
local_nice() {
  echo "[lourd-ou-local] étape relancée ICI en nice -n 19 : $1" >&2
  if [ "${#OPT[@]}" -gt 0 ]; then
    # --go : le Go de la machine. Sur le bastion il vit dans ~/sdk/go/bin, hors du PATH d'un crochet (mesuré le 26/11 :
    # « go: command not found », le push refusé pour une mauvaise raison). Sans Go : refus explicite, jamais un faux rouge.
    PATH="$HOME/sdk/go/bin:/usr/local/go/bin:$HOME/go/bin:$PATH"; export PATH
    if ! go version >/dev/null 2>&1; then
      echo "[lourd-ou-local] REFUS : lourd injoignable ET aucun Go sur cette machine ; les tests Go n'ont pas tourné, push refusé" >&2
      return 1
    fi
  fi
  nice -n 19 bash -c "$CMD"
}
if [ ! -x "$LSB" ]; then
  local_nice "lourd-si-bastion absent de cette machine"
  exit $?
fi
ERR="$(mktemp)"; trap 'rm -f "$ERR"' EXIT
"$LSB" "${OPT[@]}" bash -c "$CMD" 2> >(tee "$ERR" >&2)
rc=$?
wait 2>/dev/null
if [ "$rc" = 2 ] && grep -q "NON ABOUTI" "$ERR"; then
  local_nice "lourd n'a rien fait tourner"
  exit $?
fi
exit "$rc"
