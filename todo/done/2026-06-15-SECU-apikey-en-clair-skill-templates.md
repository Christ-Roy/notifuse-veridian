# [HYGIÈNE] 🔵 P3 — Clé API en dur dans le skill notifuse-templates (workspace inexistant)

> **Sévérité** : 🔵 P3 hygiène (PAS une urgence sécu — recalibré 2026-06-15 après vérif)
> **Owner** : Robert (skill dans ~/.claude/skills/, hors repo)
> **Créé** : 2026-06-15, trouvé en lisant le skill pour la charte MJML cold.

## Ce que c'est (vérifié, pas supposé)
`~/.claude/skills/notifuse-templates/SKILL.md` contient une `NOTIFUSE_API_KEY=` en dur.
JWT décodé : type `api_key`, workspace **`veridiansite`**, exp **2036** (long-lived), prod.

## Pourquoi ce N'EST PAS une urgence (j'avais sur-alerté — corrigé)
- La clé est techniquement valide (signature OK, `workspaces.list` → 200).
- **MAIS le workspace `veridiansite` N'EXISTE PAS en prod** : `workspaces.get?id=veridiansite`
  → `{"error":"Workspace not found"}`. La clé ne donne donc accès à **aucune donnée réelle**.
- C'est une clé orpheline/morte pointant sur un workspace supprimé ou jamais créé sur cette prod.
- Aucun autre skill n'a de clé en dur (audit `grep` fait : isolé à ce skill).

## À faire (hygiène, quand pratique — pas urgent)
- [ ] Retirer la clé en dur du SKILL.md → la lire depuis ~/credentials/.all-creds.env par principe.
- [ ] Optionnel : révoquer cette clé orpheline (sans objet vu que le workspace n'existe pas).
- [ ] Mettre à jour le skill pour qu'il provisionne/utilise un vrai workspace si besoin.

## Leçon (pour moi, le lead)
Ne pas classer un credential "P1 exposé" avant d'avoir vérifié sa portée réelle (clé valide ≠
clé dangereuse si elle ne pointe sur rien). Vérifier AVANT d'alerter.
