# V55 — réservation atomique des quotas journaliers (2026-08-05)

Le `COUNT(message_history) → SMTP` historique était un TOCTOU : plusieurs
workers pouvaient lire la même capacité puis dépasser le plafond. V55 conserve
les COUNT comme préfiltre, mais l'autorisation finale passe par un ledger
PostgreSQL dans chaque DB tenant :

- `veridian_daily_quota_counters`, clé jour UTC + workspace + type + domaine
  émetteur + classe finale, incrément conditionnel `used < cap` ;
- `veridian_daily_quota_reservations`, idempotence `(workspace, message_id,
  quota_kind)` et compensation avant acceptation SMTP ;
- le cap par classe ET le cap warmup total se réservent tous les deux lorsqu'ils
  sont configurés ; échec du second → libération du premier créé par ce worker ;
- toute erreur COUNT/réservation est fail-closed (report horaire, sans consommer
  d'attempt grâce au remboursement atomique du claim) ;
- réservation seulement après exclusion, fenêtre, préfiltre et garde automation,
  juste avant SMTP ;
- `message_history.veridian_provider_class` persiste la classe finale payload/MX.
  Au premier passage du jour, les succès historiques sans classe sont classifiés
  puis backfillés. Les lignes `failed_at IS NOT NULL` ne consomment jamais le cap.

Migration V55 additive/expand-safe, index sans `CONCURRENTLY` car le runner de
migrations est transactionnel (allowlist `migrations-pending.txt`).

