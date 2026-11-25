#!/usr/bin/env bash
# Audit EN LECTURE SEULE des gabarits Notifuse — fuite de libellé interne.
#
# Incident 2026-08-24 : 215 mails cold sont partis avec « Ouverture observation »
# — le nom interne du gabarit `coldtunnel/ecom-t1-a` — en première ligne du corps
# texte. Deux défauts distincts :
#   1. l'app défaussait le PRÉHEADER (mj-preview, texte LU par le destinataire
#      dans l'aperçu de sa boîte) sur le nom interne du gabarit — corrigé dans
#      internal/service/template_service.go ;
#   2. l'aplatissement HTML→texte ne filtrait pas les nœuds masqués — corrigé
#      dans internal/service/veridian_html_to_text.go, doublé du garde-fou
#      internal/service/veridian_text_part_guard.go qui REFUSE l'envoi.
#
# Ce script répond, AVANT tout déploiement, à la seule question qui compte :
# « quels gabarits, dans quels workspaces, verraient leurs envois refusés ? ».
# Il ne modifie rien : lecture des bases workspace, puis exécution des VRAIES
# fonctions de production sur chaque gabarit (compilation MJML incluse).
#
# Usage : VERIDIAN_TEMPLATE_DUMP=/chemin/dump.ndjson scripts/veridian/audit-template-text-leak.sh
#
# Le dump (NDJSON, une ligne par gabarit : ws, id, name, version, plain_text_only, text, subject, html, mjml) est FOURNI. Ce script n'ouvre plus aucun
# accès à la base (VER-250, 25/11/2026) : il visait un conteneur `notifuse-db-*` de prod disparu depuis pg-saas (09/11), par ssh + docker exec, et une
# lecture en échec donnait un dump vide, donc « aucun refus ». Pour revivre en prod : l'API ou un contrôle en lecture, jamais un exec.
#
# Sortie : une ligne par gabarit (OK / REFUSÉ, workspace, id, origine du HTML, première ligne du corps texte, présence de mj-preview/mj-title).
# Code retour : 0 aucun gabarit refusé (et au moins un audité), 1 au moins un refusé, 2 NON ABOUTI (pas de dump, dump vide ou illisible).
set -euo pipefail

DUMP="${VERIDIAN_TEMPLATE_DUMP:-}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
abandon() { echo "NON ABOUTI : $1, rien n'a été audité" >&2; exit 2; }
[ $# -eq 0 ] || abandon "option inconnue « $1 »"
[ -n "$DUMP" ] || abandon "VERIDIAN_TEMPLATE_DUMP absent (le dump est fourni, ce script ne lit aucune base)"
[ -r "$DUMP" ] || abandon "dump illisible : $DUMP"
DUMP="$(realpath "$DUMP")"
N="$(grep -c '[^[:space:]]' "$DUMP" || true)"
[ "${N:-0}" -gt 0 ] || abandon "dump vide : $DUMP"
python3 -c 'import json,sys
for l in open(sys.argv[1]):
    if l.strip() and not (json.loads(l).get("ws") and json.loads(l).get("id")): raise SystemExit(1)' "$DUMP" 2>/dev/null || abandon "dump mal formé (NDJSON ws, id attendus)"
cd "$REPO_ROOT"
echo "▸ $N gabarits à auditer" >&2
VERIDIAN_TEMPLATE_DUMP="$DUMP" go test ./internal/service/ -run TestVeridianAuditTemplateTextLeak -v -count=1 || exit 1
