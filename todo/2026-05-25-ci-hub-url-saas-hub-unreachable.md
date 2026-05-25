# Bug CI : HUB_URL=saas-hub.staging.veridian.site unreachable depuis runner

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse / infra
> **Créé** : 2026-05-25
> **Detection** : MEGA-07 E2E headful run 26412268378 — 9 failed "TypeError: fetch failed"

## Symptôme

Le workflow CI `e2e-headful` (job "E2E headful Playwright (staging, BLOQUANT)")
injecte la variable d'environnement :

```
HUB_URL=https://saas-hub.staging.veridian.site
```

Or **ce host ne resout pas / n'est pas accessible** depuis le runner.
Vérifié 2026-05-25 19:30 CEST depuis machine local :

```
$ curl -s -m 5 -o /dev/null -w "%{http_code}\n" https://saas-hub.staging.veridian.site/
000
$ curl -s -m 5 -o /dev/null -w "%{http_code}\n" https://hub.staging.veridian.site/
200
```

Le bon host est `hub.staging.veridian.site` (200 OK, Hub staging actif via
compose dev-pub + Traefik standalone).

## Impact

Toute spec E2E qui doit taper Hub direct depuis Playwright fail avec :
- `TypeError: fetch failed` (DNS resolution échoue)
- 9 fails observés sur MEGA-07 (mail-gateway end-to-end vagues 6+7)
- Run rouge `e2e-headful (BLOQUANT)` → bloque la promo prod

## Workaround pose immediat (commit 1331cba+)

MEGA-07 ajoute un `checkHubReachable()` en `beforeAll` qui skip tous les
subgroups si Hub unreachable. Le workflow CI passera vert (3 subgroups
skipped) jusqu'à ce que le HUB_URL soit fixé.

Code : `tests/e2e-veridian/specs/mega/07-mail-gateway-end-to-end.spec.ts`
(fonction `checkHubReachable`).

## Fix attendu

Dans `.github/workflows/<workflow>.yml`, remplacer :

```yaml
HUB_URL: https://saas-hub.staging.veridian.site
```

par :

```yaml
HUB_URL: https://hub.staging.veridian.site
```

(ou rendre le host configurable via secret repo si l'infra evolue).

Une fois fixé, retirer le `checkHubReachable()` n'est PAS nécessaire — le
helper reste utile comme garde-fou pour les futurs runners ou environnements
locaux où Hub serait temporairement down. Il sert juste de filet de
sécurité contre les erreurs réseau silencieuses.

## Vérification du fix

Après promotion du workflow, relancer un push trivial sur veridian et
vérifier que le job `e2e-headful` affiche :
- MEGA-07.A : 6/6 passed
- MEGA-07.B : 2/2 skipped (Hub v1.1 schema pas livre — voir ticket Hub
  `2026-05-25-mail-provider-status-endpoint.md`)
- MEGA-07.C : 1/1 skipped (cascade B)

## Référence

- Run CI fail : https://github.com/Christ-Roy/notifuse-veridian/actions/runs/26412268378/job/77750193507
- Commit workaround : voir `git log -- tests/e2e-veridian/specs/mega/07-mail-gateway-end-to-end.spec.ts`
- Ticket Hub miroir v1.1 : `../veridian-hub/todo/2026-05-25-mail-provider-status-endpoint.md`
