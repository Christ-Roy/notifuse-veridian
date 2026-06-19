# Staging — 716 workspace DBs orphelins starvent le worker + wipe ne DROP pas la DB

> **Sévérité** : 🟡 P1 (dégrade le staging pour tous les agents, fausse les E2E throttle)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-17 (trouvé pendant la batterie garde-fous E2E)

## Contexte / symptôme

Pendant la batterie E2E garde-fous (2026-06-17), le 3e run a vu le gate **throttle
par classe** mettre > 7 min à étaler 3 mails google (vs 214s au run précédent).
Diagnostic : la DB staging `notifuse-staging-db` héberge **716 bases
`notifuse_ws_*`** (chaos*, canary*, gfcheck*, clf*, tsti*, … = tenants de test
accumulés sur des dizaines de sessions). Le worker `EmailQueueWorker` poll TOUS
les workspaces en round-robin → avec 716 bases, la cadence de re-check des entrées
reschedulées (throttle/cap) s'allonge énormément → un E2E qui dépend du timing
réel (throttle 1/min) devient non fiable, et le throttle réel d'une vraie campagne
serait lui aussi ralenti.

## Deux problèmes distincts

1. **Accumulation d'orphelins** : les provisions de test ne sont pas toutes wipées
   → 716 bases. Starve le worker (round-robin), gonfle le disque dev-pub (déjà
   tendu), ralentit `pg`.
2. **`wipe-test-tenants` ne DROP pas la base physique** (vérifié 2026-06-17) :
   `POST /api/veridian/admin/wipe-test-tenants {tenant_ids:[...]}` renvoie
   `{"wiped":[...]}` (supprime le RECORD tenant) MAIS la base `notifuse_ws_<id>`
   reste présente dans `pg_database` (DROP DATABASE bloqué/différé — connexions
   actives du worker, ou DROP best-effort silencieux). Donc même un wipe propre
   laisse la base. → l'orphelin DB n'est plus servi (pas de record) mais occupe
   disque + ralentit le scan workspaces.

## Demande

- **Job de GC des workspaces de test orphelins** sur staging : DROP les bases
  `notifuse_ws_*` qui n'ont plus de record tenant correspondant (ou dont le prefix
  est un prefix de test connu chaos/gfcheck/clf/tsti/canary-exclu). À faire
  proprement (terminer les connexions worker sur la base avant `DROP DATABASE ...
  WITH (FORCE)` PG13+), pas en batch sur goroutines actives (cf. piège mémoire
  `reference_admin_tenants_listing_api` : `include_orphans:true` en batch a déjà
  crashé le container).
- **Fixer `wipe-test-tenants` pour qu'il DROP réellement la base** (FORCE +
  terminate backends), ou documenter que le DROP est asynchrone et ajouter un
  sweep périodique.
- **Idéalement** : auto-wipe TTL sur les tenants de test (prefix test + créé il y
  a > 24h sans activité) via un cron staging-only.

## Impact si non traité

- E2E timing-dépendants (throttle, sending-window) deviennent flaky sur staging.
- Disque dev-pub saturé (déjà 92-98 % pendant cette session — voir ci-dessous).
- Vrai throttle cold ralenti si la prod accumulait autant (à surveiller).

## Note

Trouvé en marge de la batterie garde-fous (ticket
`2026-06-17-batterie-e2e-onpremise-garde-fous-domaine.md`, livré). Les 10 gates
ont TOUS été prouvés malgré ça (le run 2 a eu un timing sain) ; ce ticket est de
l'hygiène staging, pas un blocage des gates.

---

## ✅ Résolu — 2026-06-18 (SHA `8e9dcf84`, prod tier 🔴)

**Cause racine confirmée** : `workspaceRepository.DeleteDatabase` (upstream)
fait `DROP DATABASE IF EXISTS` **sans `WITH (FORCE)`**. Sur staging, le worker
round-robin rouvre une connexion entre le `pg_terminate_backend` et le `DROP`
→ `ERROR: database "X" is being accessed by other users` → DROP raté → base
orpheline. Prouvé à la racine sur staging : avec 1 backend actif, le DROP nu
échoue, le `DROP ... WITH (FORCE)` réussit.

**Fix livré (zéro patch upstream)** :
1. `internal/repository/veridian_workspace_drop.go` — méthodes veridian sur
   `workspaceRepository` : `VeridianForceDropDatabase` (REVOKE CONNECT +
   terminate best-effort + `DROP DATABASE ... WITH (FORCE)`, idempotent, ident
   sanitize), `VeridianListOrphanWorkspaceDBs` (pg_database moins records),
   `VeridianForceDropDatabaseByName`, `VeridianWorkspaceDBPrefix`.
2. **Wipe fix** (Partie A) — `wipeOneTenant` (veridian_service.go) appelle un
   DROP FORCE de rattrapage APRÈS `DeleteWorkspace` (couvre la race ET le cas
   record-absent/base-restante). Activé via `ConfigureWorkspaceDBCleanup(prefix)`
   dans app.go. Best-effort (un échec ne bloque pas le wipe). Détection de la
   capacité du repo par type-assertion → pas de cycle import service↔repository.
3. **GC orphelins** (Partie B) — endpoint STAGING-ONLY (503 hors staging, HMAC)
   `POST/GET /api/veridian/admin/gc-orphan-workspace-dbs` (service
   `VeridianGCOrphanWorkspaceDBs`) : DROP les bases sans record
   **SÉQUENTIELLEMENT** (jamais en rafale = piège crash container), FORCE, exclut
   `canary` + clients réels (defaultSafetyClientPrefixes), `dry_run` + `max_drops`.

**Preuve on-premise (tier 🔴)** :
- Mécanisme racine : DROP nu échoue (backend actif), DROP FORCE réussit (psql).
- GC réel staging : **811 → 153 bases physiques** en 2 passes (838 DROP, 0 erreur,
  ~95s la 1ʳᵉ passe de 702), container `notifuse-staging` healthy tout du long
  (pas de crash, /api/health 200), **3 canary intactes**, 1 base safety préservée.
  Le résiduel = bases nées des E2E qui tournaient pendant le GC (record présent →
  pas orphelines → re-nettoyées à la passe suivante).
- Wipe fix : tenant jetable `gcwipe828492` provisionné (base présente) → wipé →
  **base ABSENTE de pg_database** (count 0) + record parti. Avant le fix, la base
  serait restée.

**Partie C — auto-TTL (état)** :
- Le cron `VeridianTestTenantsCleanupService` (2026-05-24) auto-wipe déjà le
  prefix `tst*` > 1h toutes les 30 min, et **profite désormais du DROP FORCE de
  rattrapage** (les tenants `tst*` qu'il wipe voient enfin leur base droppée).
- **Reste-à-faire (non sur-ingénié ici)** : (a) étendre le cron aux autres
  prefixes de test connus (chaos/gfcheck/clf/qgr/regrmp…) OU (b) ajouter un sweep
  cron staging-only qui appelle `VeridianGCOrphanWorkspaceDBs` périodiquement pour
  ramasser les bases orphelines (sans record) que le wipe-par-prefix ne voit pas.
  Le GC on-demand existe (endpoint) ; un cron qui l'invoque est l'incrément
  naturel. À faire dans un ticket de suite si l'accumulation reprend — le DROP
  FORCE ayant supprimé la cause racine, l'accumulation devrait fortement ralentir.

→ Archivé dans `todo/done/`.
