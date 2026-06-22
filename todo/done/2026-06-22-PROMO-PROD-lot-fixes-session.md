# 🚀 PROMO PROD — lot de 5 commits prêts (session 2026-06-19/21)

> **Sévérité** : 🟡 P1 (livrables prouvés bloqués hors prod par incidents infra)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-22
> **But** : SOURCE DE VÉRITÉ unique de ce qui doit partir en prod. Prod est sur
> `3bbc1cce` ; AUCUN des commits ci-dessous n'y est. Tout est sur `veridian`
> (staging), prouvé, mais jamais promu (les promos ont échoué sur build/E2E puis
> l'incident OVH rescue 2026-06-22).

## État prod constaté 2026-06-22

- Prod = `v54.0-veridian.3bbc1cce` (health 200, stable après reboot OVH rescue→local).
- Les 5 commits ci-dessous sont sur `origin/veridian`, PAS en prod.

## Les 5 commits à promouvoir (du plus ancien au plus récent)

| SHA | Quoi | Tier | Preuve faite ? |
|---|---|---|---|
| `514e6c12` | fix wipe-recrée (record-first, coupe la régénération de bases) | 🔴 | ✅ on-premise : provision→wipe→base reste à 0 après 90s |
| `a39c9f15` | fix warmup = cap TOTAL par infra (robuste classes MX) | 🔴 | ✅ on-premise : seed google+ovh MX → cap total=2 → capped |
| `daab810b` | fix UI en-tête colonne triée noir illisible (thème Table) | 🟢 | ✅ live : token transparent après fix |
| `f62e7914` | ci: build image self-hosted (dev-pub) → ubuntu-latest | 🔴 CI | À valider PAR le run de promo (le build tourne enfin sur GH) |
| `204f5824` | infra: bump pool connexions DB staging 350→600 / pg 800 | 🟡 | Appliqué en live staging (app healthy, conns=600) |

## Mécanique de promo (quand prod stable + dev-pub OK)

`gh workflow run veridian-ci.yml -R Christ-Roy/notifuse-veridian -f deploy_prod=true --ref veridian`

⚠️ **Obstacle connu** : le job E2E headful staging (BLOQUANT) peut échouer sur la
saturation du pool DB (cf. ticket `2026-06-20-e2e-sature-staging-provisioning-pic.md`).
Le bump connexions (`204f5824`) + le cron de purge idle (dev-pub */2min) doivent
absorber. Si 2 tests `chaos-provisioning` (5 provisions concurrentes) re-pètent →
le pansement ne suffit pas, escalader vers le vrai fix (globalTeardown E2E).

## Définition of done

- [ ] Run promo vert (build ubuntu-latest + e2e + deploy-prod + e2e-prod-smoke)
- [ ] `/api/version` prod = SHA tête (514e6c12..204f5824 inclus)
- [ ] `/api/health` prod 200 + monitoring 10 min sans crashloop
- [ ] Archiver ce ticket + les 2 tickets de fix (warmup, wipe) UNE FOIS EN PROD
- [ ] Repasser le cron dev-pub purge idle de */2min → */30min (le bump conns suffit)
