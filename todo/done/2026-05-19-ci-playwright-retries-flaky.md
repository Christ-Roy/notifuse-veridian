# 2026-05-19 — CI : Playwright retries:2 + flaky detection

> **Spec** : `../CI-ARCHITECTURE.md` Constitution §16
> **Sévérité** : 🟢 P2
> **Effort** : M (2-8h)

## Constat

`tests/e2e-veridian/playwright.config.ts` ligne 13 : `retries: 0`. Commentaire ligne 11-12 justifie : "tests séquentiels, un retry refait tout depuis zéro et sature le pool DB". **Mais** la Constitution §16 exige `retries: 2` en CI pour éviter qu'un faux positif E2E déclenche un rollback prod injustifié.

Pas de workflow `playwright-flaky-detector.yml` non plus pour mettre en quarantaine les tests instables (>5% flake rate).

## Travail

### Étape 1 — retries:2 contrôlés

```ts
retries: process.env.CI ? 2 : 0,
fullyParallel: false,  // garder séquentiel
workers: 1,
```

Le worry "pool DB saturé" est traité par `workers: 1` + `fullyParallel: false`. Un retry rejouera juste ce test, pas toute la suite.

### Étape 2 — Flaky detection workflow

Créer `.github/workflows/playwright-flaky-detector.yml` (cron hebdo) :
- Récupère les N derniers runs CI de la branche veridian
- Parse `playwright-results.json` (déjà uploadé en artifact)
- Calcule `flake_rate = retries_count / total_count` par test
- Si `flake_rate > 5%` sur 4 semaines glissantes → ouvre une issue GitHub avec le test name + run IDs

### Étape 3 — Quarantaine auto

Tag `@quarantine` sur les tests flaky identifiés, filtré dans le `e2e-staging` job via `--grep-invert "@quarantine"`. Une issue suit chaque test quarantaine avec deadline 30j pour fix ou suppression.

## Risque

P2 — `retries:2` peut masquer un bug intermittent côté Notifuse. Mitigation : log les retries comme warning + alerte si même test retry >2x semaine.
