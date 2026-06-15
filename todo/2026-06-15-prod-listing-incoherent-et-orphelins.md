# [NOTIFUSE] 🟡 P1 — Listing admin tenants incohérent + 20 workspaces orphelins en prod

> **Sévérité** : 🟡 P1 (cohérence admin + hygiène data prod)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-15 par le lead, en peuplant la prod (workspace cold `coldtunnel`).
> Découvert en conditions réelles sur `notifuse.app.veridian.site`.

## ✅ Problème 1 LIVRÉ (2026-06-15, commit 1b3700b0)

Fix : `collectPlanIDs(prefix=="")` scannait `nil` → bucket managed vide. Ajout
`VeridianPlanRepository.ListAllIDs(ctx, limit)` (SELECT workspace_id FROM
veridian_plan LIMIT 1000) + test contractuel de cohérence (managed sans prefix
== managed avec prefix). Confirmé en amont par le lead : coldtunnel + canary*
ONT bien leur plan (vu via `?prefix=`), donc le bug était purement listing,
ZÉRO perte de plan réelle. → une fois ce fix en prod, la vraie liste d'orphelins
(Pb2) sera fiable.

**⏳ Problème 2 (20 orphelins) reste pending** : investigation data prod. Sur les
20, au moins coldtunnel + canaryenterprise/pro/free sont en fait MANAGÉS (artefact
du Pb1, maintenant corrigé). Restent ~16 vrais orphelins à trier (résidus test vs
vrais clients sans plan). À reprendre une fois le fix Pb1 déployé en prod.

## Problème 1 — `GET /api/veridian/admin/tenants` : bucket `managed` incohérent selon `prefix`

Symptôme reproductible en prod (HMAC admin) :
- `GET /api/veridian/admin/tenants` (sans param) → `{"managed":[],"total":0}`
- `GET ...?prefix=coldtunnel` → `{"managed":[{"tenant_id":"coldtunnel","has_plan":true,"plan":"enterprise","status":"active"}],"total":1}`
- `GET ...?include_orphans=true` (sans prefix) → `{"managed":[], "orphans":[...20 tenants...]}`

→ Le MÊME tenant `coldtunnel` (qui A un plan enterprise actif) apparaît en **managed** quand on filtre par prefix, mais est **ABSENT du bucket managed** (voire rangé en orphan) quand on liste sans prefix. Le listing par défaut dit "0 managed" alors qu'il y a des tenants managés.

**Cause probable (à confirmer)** : la requête de listing `ListTenants` (`internal/service/veridian_service.go` + `internal/repository/veridian_plan_postgres.go`, handler `veridian_handler.go:797 handleListTenants`) construit le bucket `managed` différemment selon que `prefix`/`include_orphans` sont posés — le scan "managed" (table `veridian_plan`) n'est peut-être déclenché que dans certaines branches. À auditer : pourquoi le bucket managed est vide sans prefix alors qu'il liste coldtunnel avec prefix.

**DoD P1** :
- [ ] `GET /api/veridian/admin/tenants` sans param liste TOUS les tenants managés (coldtunnel + canary* qui ont un plan).
- [ ] Cohérence : un tenant avec `has_plan:true` est TOUJOURS dans `managed`, jamais en orphan, quel que soit le param.
- [ ] Test contractuel (sqlmock ou E2E @prod-safe) qui verrouille : managed sans prefix == managed avec prefix pour un même tenant.

## Problème 2 — 20 workspaces ORPHELINS en prod (dans `workspaces` sans `veridian_plan`)

Liste complète (prod, 2026-06-15) :
```
ermineconan, cazalse13, doungrothylarrysisow, lyonmultiservices690, tramtech,
tramtechservices, robinixbox, brunon5robert, antjacquet, darysisowath,
ismailelmouaddab, guilhemjacquet1, guilhemjacquet, robertbrunon, rbrunon, truy
+ canaryenterprise/canarypro/canaryfree (TÉMOINS canary — LÉGITIMES, NE PAS TOUCHER)
+ coldtunnel (le nouveau, OK, a un plan — apparu en orphan à cause du Pb1)
```

→ ~16 workspaces (hors canary + coldtunnel) existent dans `workspaces` SANS ligne `veridian_plan`.
**Danger** : certains noms ressemblent à de VRAIS humains (`antjacquet`, `guilhemjacquet`,
`ismailelmouaddab`, `darysisowath`, `truy`, `lyonmultiservices690`, `tramtech`...) → soit
d'anciens tests à nettoyer, soit de VRAIS workspaces clients qui ont perdu/jamais eu leur
plan (bug provisioning ?). Plusieurs variantes du nom de Robert (`robertbrunon`, `rbrunon`,
`brunon5robert`, `robinixbox`) = probablement d'anciens tests perso.

🔴 **NE PAS WIPE en aveugle** — investiguer d'abord chaque orphelin :
- [ ] Pour chacun : a-t-il des contacts/broadcasts/de l'activité réelle ? (vrai client) ou vide ? (résidu test)
- [ ] Les vrais clients sans plan → leur poser le bon plan (bug à corriger : pourquoi un workspace existe sans veridian_plan ?).
- [ ] Les résidus de test → wipe ciblé (PAS les canary, PAS coldtunnel).
- [ ] Comprendre la CAUSE : un workspace peut-il être créé sans veridian_plan ? (provision partiel échoué ? création UI directe ? legacy ?). Corriger pour que ça n'arrive plus.

## Note
Le provision lui-même est OK et idempotent (re-provision coldtunnel → pas de doublon, plan
enterprise posé). Ces 2 problèmes sont de la cohérence listing + hygiène data, pas un blocage
du provisioning. Découverts en peuplant la prod pour le tunnel de vente cold.
