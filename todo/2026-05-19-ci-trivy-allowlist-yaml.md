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
