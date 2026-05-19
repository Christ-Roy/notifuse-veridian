# 2026-05-19 — CI : cron hebdo tracker dette `tests-pending.txt`

> **Spec** : `../CI-ARCHITECTURE.md` Constitution §1 (allowlist transitoire, cible 0 sous 90j)
> **Sévérité** : 🟢 P3
> **Effort** : S (<2h)

## Constat

`tests-pending.txt` à la racine contient 8 fichiers Go critiques sans test colocalisé (baseline historique). Constitution §1 dit : cible 0 sous 90j, cron hebdo qui trace la dette.

Pas de cron, pas de visibilité sur la décroissance.

## Travail

1. Créer `.github/workflows/tests-pending-debt-tracker.yml` :
   ```yaml
   name: Tests pending debt tracker
   on:
     schedule:
       - cron: '0 9 * * 1'  # Lundi 9h
     workflow_dispatch:
   jobs:
     report:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v4
         - name: Count debt
           id: count
           run: |
             COUNT=$(wc -l < tests-pending.txt)
             echo "count=$COUNT" >> $GITHUB_OUTPUT
         - name: Open or update tracking issue
           run: |
             gh issue list --label tests-pending --state open --json number ...
             # if exists: update body with new count + delta vs last week
             # if not: create
   ```

2. L'issue affiche : count actuel, delta semaine précédente, liste des fichiers, deadline 90j à partir de la baseline.

## Reco

Petit ticket. À shipper en passant. Crée une dynamique psychologique pour faire baisser le compteur (gamification light).
