# 2026-05-19 — Idempotency-Key header standard

> **Spec** : `../CONTRAT-HUB.md` §5.11
> **Sévérité** : 🟡 P1 sécu (si Hub envoie le header) / P2 (si Hub n'envoie pas)
> **Effort** : L (1-2 jours)

## Contexte

Le contrat v1.1 §5.11 exige un middleware **Idempotency-Key** sur les endpoints write :
- `provision`, `update-plan`, `soft-delete`, `restore`, `purge`, `touch`

Pattern : le client (Hub) envoie un header `Idempotency-Key: <uuid>`. Si Notifuse a déjà vu cette clé dans les dernières 24h, il rejoue la réponse cachée au lieu d'exécuter à nouveau l'action. Évite les double-charges en cas de retry réseau.

**Aujourd'hui** : Notifuse fait de l'idempotence logique (Provision retourne `created:false` si tenant existe), mais ce n'est pas formel — un Hub qui retry verra deux executions avec des side-effects différents (webhook émis 2×, audit trail double).

## Plomberie à créer

1. **Migration V33** : table `veridian_idempotency_keys`
   ```sql
   CREATE TABLE veridian_idempotency_keys (
       key VARCHAR(64) PRIMARY KEY,
       endpoint VARCHAR(128) NOT NULL,
       tenant_id VARCHAR(64),
       request_hash CHAR(64) NOT NULL,
       response_status INT NOT NULL,
       response_body JSONB NOT NULL,
       created_at TIMESTAMP NOT NULL DEFAULT NOW(),
       expires_at TIMESTAMP NOT NULL
   );
   CREATE INDEX ON veridian_idempotency_keys (expires_at);
   ```

2. **Middleware `internal/http/middleware/idempotency.go`** :
   - Lire header `Idempotency-Key`.
   - Si absent → pass-through (rétro-compat).
   - Si présent → SELECT par key. Hit + même request_hash → renvoyer réponse cachée. Hit + hash différent → 422 `idempotency_key_mismatch`.
   - Si miss → wrapper la response, l'INSERT post-handler avec TTL 24h.

3. **Cron cleanup** : daily job qui DELETE `WHERE expires_at < NOW()`.

4. **Wire** sur les 6 endpoints mutateurs dans `veridian_handler.go::RegisterRoutes`.

5. **Tests** : colocalisé sur idempotency.go (replay même key, mismatch hash, expiration).

## Coordination Hub

**Vérifier d'abord** : est-ce que le Hub envoie le header aujourd'hui ?
- Si **non** : ce ticket est P2 (nice-to-have, on l'implémente pour préparer le futur).
- Si **oui** : P1 sécu (Hub croit qu'il a la garantie alors qu'on l'ignore — risque double-execution).

Grep côté Hub : `Idempotency-Key` dans `veridian-hub/lib/notifuse/` ou similar.

## Risque migration

P1 — table nouvelle, idempotente. Cron à activer après que la table est en prod.
