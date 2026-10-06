# Rendu au dépilage des emails d automation (06/10/2026)

**Défaut supprimé** : le noeud email rendait sujet, texte et html à l inscription et les figeait dans
`email_queue.payload`. Corriger un modèle ne touchait pas les messages déjà en file (25 942 sur
`robertbrunon` le 06/10) : ils partaient avec l ANCIEN texte.

**Règle** : pour toute entrée `source_type=automation` avec `template_id`, le worker re-rend
sujet, texte, html, `plain_text_only`, reply-to et `template_version` JUSTE AVANT l envoi, depuis le
modèle COURANT (`GetTemplateByID(..., 0)`) et le contact COURANT.

- Moteur unique : `renderAutomationEmail` (`internal/service/veridian_automation_email_render.go`),
  appelé par le noeud email (enqueue) ET par `VeridianQueueEmailRenderer` (dépilage).
- Gate : `veridianRenderAtSend` (`internal/service/queue/veridian_render_at_send.go`), dans
  `processEntry` après le pré-filtre, avant le rate limiter, la claim et la réservation de quota.
  Branchement : `SetQueuedEmailRenderer` dans `app.go`. Sans renderer = comportement upstream.
- Échec de rendu : le message ne part JAMAIS. Permanent (modèle ou contact supprimé, Liquid/MJML
  invalide, sujet ou corps vide) : entrée supprimée, `message_history.status_info` =
  `render_at_send: <raison>`, callback d échec (le contact d automation sort). Transitoire (lecture
  DB) : retry avec le backoff habituel.
- Inchangés : From / intégration / failover de pool (le rendu n y touche pas), plafonds, throttle,
  anti-hash, rotation, garde-fous stop-on-reply.
- Hors périmètre : les broadcasts (spintax, re-spin et hash anti-collision posés à l enqueue).
  À traiter séparément si un broadcast cold longue durée est utilisé.
