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
# Usage :
#   scripts/veridian/audit-template-text-leak.sh                 # dump prod + audit
#   scripts/veridian/audit-template-text-leak.sh --dump-only     # dump seul
#   VERIDIAN_TEMPLATE_DUMP=/chemin/dump.ndjson \
#     scripts/veridian/audit-template-text-leak.sh --no-dump     # audit d'un dump existant
#
# Sortie : une ligne par gabarit (OK / REFUSÉ, workspace, id, origine du HTML,
# première ligne du corps texte, présence de mj-preview/mj-title).
# Code retour : 0 si aucun gabarit ne serait refusé, 1 sinon.
set -euo pipefail

SSH_HOST="${VERIDIAN_NOTIFUSE_SSH:-prod}"
DUMP="${VERIDIAN_TEMPLATE_DUMP:-/tmp/notifuse-templates-$(date +%Y%m%d-%H%M%S).ndjson}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DO_DUMP=1; DO_AUDIT=1
for a in "$@"; do
  case "$a" in
    --dump-only) DO_AUDIT=0 ;;
    --no-dump)   DO_DUMP=0 ;;
    *) echo "option inconnue : $a" >&2; exit 2 ;;
  esac
done

if [[ "$DO_DUMP" == 1 ]]; then
  echo "▸ dump des gabarits depuis $SSH_HOST (lecture seule)…" >&2
  # Le conteneur Postgres porte l'ID d'allocation Nomad : on le découvre, jamais
  # de nom codé en dur (une liste en dur devient fausse au premier redéploiement).
  ssh "$SSH_HOST" 'bash -s' > "$DUMP" <<'REMOTE'
set -euo pipefail
C=$(docker ps --format '{{.Names}}' | grep '^notifuse-db-' | head -1)
[ -n "$C" ] || { echo "conteneur notifuse-db introuvable" >&2; exit 1; }
for db in $(docker exec "$C" psql -U postgres -tAc \
      "SELECT datname FROM pg_database WHERE datname LIKE 'notifuse_ws_%' ORDER BY 1;"); do
  ws=${db#notifuse_ws_}
  docker exec "$C" psql -U postgres -d "$db" -tAc "
    SELECT json_build_object(
      'ws','$ws','id',t.id,'name',t.name,'version',t.version,
      'plain_text_only', COALESCE((t.email->>'plain_text_only')::boolean,false),
      'text', COALESCE(t.email->>'text',''),
      'subject', COALESCE(t.email->>'subject',''),
      'html', COALESCE(t.email->>'compiled_preview',''),
      'mjml', COALESCE(t.email->>'mjml_source','')
    )::text
    FROM templates t
    JOIN (SELECT id, max(version) v FROM templates WHERE deleted_at IS NULL GROUP BY id) m
      ON m.id=t.id AND m.v=t.version
    WHERE t.deleted_at IS NULL AND t.channel='email';" 2>/dev/null || true
done
REMOTE
  echo "▸ $(wc -l < "$DUMP") gabarits → $DUMP" >&2
fi

if [[ "$DO_AUDIT" == 1 ]]; then
  cd "$REPO_ROOT"
  VERIDIAN_TEMPLATE_DUMP="$DUMP" \
    go test ./internal/service/ -run TestVeridianAuditTemplateTextLeak -v -count=1
fi
