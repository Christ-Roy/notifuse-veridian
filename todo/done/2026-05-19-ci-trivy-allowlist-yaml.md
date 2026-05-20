# 2026-05-19 — CI : externaliser allowlist Trivy dans `.trivyignore.yaml`

> **Spec** : `../CI-ARCHITECTURE.md` Constitution §13 (Trivy + VEX)
> **Sévérité** : 🟢 P2
> **Effort** : S (<2h)

## Constat

Aujourd'hui l'allowlist CVE (notamment `@tanstack/history GHSA-rmmr-r34h-pfm5`) est **hardcodée dans le workflow** (`veridian-ci.yml` lignes 145-160). Constitution §13 exige :
- Fichier `.trivyignore.yaml` versionné (pas `.trivyignore` plat)
- Chaque ignore accompagné d'un VEX statement (`status: not_affected`, `justification: vulnerable_code_not_in_execute_path` ou équivalent)

## Travail

1. Créer `.trivyignore.yaml` à la racine :
   ```yaml
   vulnerabilities:
     - id: GHSA-rmmr-r34h-pfm5
       paths:
         - console/package-lock.json
       status: not_affected
       justification: vulnerable_code_not_in_execute_path
       statement: |
         @tanstack/history v1.x est une dépendance transitive de
         @tanstack/router. La fonction vulnérable (X) n'est jamais
         appelée par le bundle console. Vérifié 2026-MM-DD.
       expires: 2026-12-31
   ```

2. Mettre à jour le workflow pour pointer vers `.trivyignore.yaml` :
   ```yaml
   - uses: aquasecurity/trivy-action@v0.x
     with:
       trivyignores: .trivyignore.yaml
   ```

3. Cron annuel (Constitution §13.7) : vérifier que `expires` n'est pas dépassé, ouvrir une issue si oui.

## Risque

P0 — additive. Une fois en place, retirer le hardcode du workflow.

---

## Update — 2026-05-20 — Livré (commit b0529876)

`.trivyignore.yaml` créé à la racine avec format officiel Aqua Security :
- 1 entrée : `GHSA-rmmr-r34h-pfm5` (@tanstack/history supply-chain 2026-05-11)
- Statement écrit + paths ciblés + expires 2026-12-31 (re-audit annuel forcé)
- Discipline imposée : pas d'allowlist sans VEX écrit

Workflow câblé : `trivyignores: '.trivyignore.yaml'` ajouté sur les 2 steps Trivy.

Reste hors scope (ticket séparé si besoin) : cron annuel qui ouvre une issue
sur `expires` dépassé. L'allowlist `@tanstack/*` côté npm audit (cve-scan job
lignes 200-235) est REDONDANTE mais pas urgent à retirer (npm audit + Trivy
sont 2 outils différents).

Job Constitution §13 vert en CI : run 26185051428 confirme "Trivy fs scan
→ success" avec la nouvelle config.
