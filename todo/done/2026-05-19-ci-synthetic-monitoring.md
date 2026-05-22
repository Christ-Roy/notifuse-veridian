# 2026-05-19 — CI : Synthetic monitoring 5 min

> **Spec** : `../CI-ARCHITECTURE.md` Constitution §11
> **Sévérité** : 🟢 P3 (utile quand le trafic prod est faible)
> **Effort** : M (2-8h)

## Constat

Pas de workflow synthetic monitoring. Aujourd'hui on découvre qu'une régression prod a eu lieu via :
- E2E prod post-deploy (réactif, dans la fenêtre de 10 min post-push)
- User qui se plaint (réactif, peut prendre des heures)

§11 exige un cron `*/5 * * * *` qui rejoue le smoke E2E read-only contre prod pour détecter les régressions silencieuses (cert expiré, DB saturée, cache foiré).

## Travail

1. Créer `.github/workflows/synthetic-monitoring.yml` :
   ```yaml
   name: Synthetic monitoring
   on:
     schedule:
       - cron: '*/5 * * * *'
     workflow_dispatch:
   jobs:
     smoke:
       runs-on: ubuntu-latest
       timeout-minutes: 5
       steps:
         - uses: actions/checkout@v4
         - uses: actions/setup-node@v4
         - run: cd tests/e2e-veridian && npm ci && npx playwright install chromium
         - run: cd tests/e2e-veridian && npx playwright test --grep "@prod-safe"
           env:
             NOTIFUSE_URL: https://notifuse.app.veridian.site
             HUB_API_SECRET: ${{ secrets.HUB_API_SECRET }}
         - name: Alert Telegram on fail
           if: failure()
           run: |
             curl -X POST ...
   ```

2. Filtrer par `@prod-safe` (read-only, déjà existant dans `prod-safe.spec.ts`).

3. Alerting : Telegram à Robert + (futur) Grafana webhook.

## Coût

Avec 5 min cron, ~288 runs/jour, ~10s par run → ~50 minutes GH Actions/jour, gratuit dans le tier public. Si on passe en repo privé : ~25 USD/mois, à valider.

## Reco

**À shipper quand Veridian a >5 clients en prod** sur Notifuse. Avant ça, l'E2E post-deploy + le monitoring système (`veridian-prod-healthcheck` qui tourne sur le dev server) sont suffisants.
