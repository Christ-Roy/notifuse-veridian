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
