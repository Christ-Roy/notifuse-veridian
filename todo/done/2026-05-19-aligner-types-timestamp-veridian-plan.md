# 2026-05-19 — Aligner les types TIMESTAMP de `veridian_plan` (drift V33-V35 vs legacy)

> **Sévérité** : 🟡 P2 (dette technique, source de bugs futurs)
> **Effort** : M (4-6h, ALTER COLUMN — risque rodage staging obligatoire)
> **Découvert pendant** : bug prod `pq: inconsistent types deduced for parameter $2` sur Touch (fix dans commit `995a9e0b`)

## Contexte

Drift de types entre les colonnes legacy (upstream Notifuse) et les colonnes ajoutées par les migrations Veridian V33-V35 sur `veridian_plan` :

```
WITHOUT TIME ZONE (legacy upstream)   |   WITH TIME ZONE (V33-V35)
─────────────────────────────────────|──────────────────────────────────
created_at                             | restored_at        (V34)
updated_at                             | purge_eligible_at  (V34)
last_reset_at                          | last_touched_at    (V34)
suspended_at                           |
deleted_at                             |
```

## Conséquences

1. **Bug réel détecté en prod 2026-05-19** : `UPDATE ... SET last_touched_at = $2, updated_at = $2` plante au runtime car Postgres ne peut pas inférer un type unique pour `$2`. Workaround actuel : passer `$2` ET `$3` avec la même valeur (cf. fix commit `995a9e0b`).
2. **Source de bugs futurs** : tout nouveau dev qui fait un `UPDATE` partageant un `$N` entre une colonne legacy et une colonne V33-V35 reproduira le bug. Garde-fou test `TestVeridianPlanRepository_NoSharedParamAcrossMixedTzColumns` mitige mais ne couvre que les méthodes existantes.
3. **Sémantique différente** : `WITHOUT TIME ZONE` stocke en local-naive (perd la TZ source au cast). `WITH TIME ZONE` stocke en UTC + offset. En pratique Go `time.Time.UTC()` masque la différence mais c'est une bombe à retardement pour les requêtes ad-hoc en SQL.

## Travail

### Option 1 (recommandée) — Migrer toutes les colonnes legacy en `WITH TIME ZONE`

Migration V36 :
```sql
ALTER TABLE veridian_plan
  ALTER COLUMN created_at TYPE TIMESTAMP WITH TIME ZONE USING created_at AT TIME ZONE 'UTC',
  ALTER COLUMN updated_at TYPE TIMESTAMP WITH TIME ZONE USING updated_at AT TIME ZONE 'UTC',
  ALTER COLUMN last_reset_at TYPE TIMESTAMP WITH TIME ZONE USING last_reset_at AT TIME ZONE 'UTC',
  ALTER COLUMN suspended_at TYPE TIMESTAMP WITH TIME ZONE USING suspended_at AT TIME ZONE 'UTC',
  ALTER COLUMN deleted_at TYPE TIMESTAMP WITH TIME ZONE USING deleted_at AT TIME ZONE 'UTC';
```

**Avantages** :
- Élimine le drift définitivement
- Pas de Workaround `$N` séparé nécessaire
- Sémantique TZ propre

**Risques** :
- `ALTER COLUMN TYPE` prend un AccessExclusiveLock sur la table (court car table petite, ~10k rows max) → downtime de quelques ms acceptable
- Rodage staging 24h **obligatoire** (Constitution §15) avant prod — pas de `[skip-gate]` pour `ALTER COLUMN`
- Le code Go ne change pas (time.Time gère les deux pareil) mais les requêtes SQL ad-hoc qui castent en `timestamp without time zone` cassent

### Option 2 — Convention "futures colonnes en WITHOUT pour matcher legacy"

Inverse du sens : toute nouvelle colonne ajoutée à `veridian_plan` doit être `TIMESTAMP WITHOUT TIME ZONE`.

**Avantages** : zéro migration ALTER, just convention.
**Inconvénient** : on garde un schéma legacy moins idiomatique, et chaque dev doit connaître la règle.

### Option 3 (no-op) — Garder le drift, documenter

Accepter le drift, garder le test régression `NoSharedParamAcrossMixedTzColumns` et écrire les UPDATEs avec `$N` séparés systématiquement.

## Reco

**Option 1** quand on aura un slot tranquille (genre pas de feature urgente sur la table). C'est la dette propre. En attendant **Option 3** (déjà en place via le garde-fou test).

## Statut 2026-05-21

**Décision en place** : Option 3 (no-op + garde-fou). Confirmé toujours
valide post-sprint 2026-05-21 (V38 + V39) — les nouvelles colonnes
ajoutées (`emails_sent_lifetime BIGINT`, `activity_threshold_reached_at
TIMESTAMPTZ`, `last_hub_sync_at TIMESTAMPTZ`) ont été codées avec
paramètres `$N` distincts pour éviter le bug type mismatch. Memory
`feedback_sqlmock_does_not_validate_postgres_types` documente le piège
pour les sessions futures.

Ce ticket reste pending comme **référence pour la future migration ALTER
COLUMN consolidée** (option 1) — à dégainer quand on aura :
- 0 feature pricing/lifecycle urgente sur `veridian_plan`
- Slot 24h+ de rodage staging dispo (Constitution §15)
- Idéalement : tous les workspaces inactifs purgés pour minimiser le
  AccessExclusiveLock impact

## Tests

- Vérifier après ALTER que tous les `time.Time` round-trip via JSON conservent leur précision (microseconde)
- Vérifier que les requêtes ad-hoc des scripts admin existants (s'il y en a) ne cassent pas
- E2E complet sur le cycle lifecycle (provision → touch → soft-delete → restore → purge)
