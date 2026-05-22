# 2026-05-19 — CI : freeze branche pendant PR auto-revert post-rollback

> **Spec** : `../CI-ARCHITECTURE.md` Constitution §17 (Rollback = Revert Git auto)
> **Sévérité** : 🟢 P2
> **Effort** : M (2-8h)

## Constat

Le job `rollback-prod` (`veridian-ci.yml` lignes 935-975) crée une PR auto-revert quand le rollback Docker se déclenche post-e2e-prod fail. **Manque** : aucun mécanisme qui gèle les merges sur `veridian` pendant que la PR de revert est en review.

Risque : entre le `rollback Docker` (instantané) et le merge de la PR de revert (peut prendre des heures si Robert n'est pas dispo), un autre push sur `veridian` peut arriver, se déployer en prod et conflicter avec le revert.

## Travail

1. Dans le job `rollback-prod`, après création de la PR auto-revert, ajouter un step :
   ```yaml
   - name: Freeze branch via protection
     run: |
       gh api repos/${GH_REPO}/branches/veridian/protection \
         --method PUT \
         --field required_pull_request_reviews=null \
         --field restrictions=null \
         --field enforce_admins=true \
         --field required_status_checks='{"strict":true,"contexts":["FREEZE_LOCK"]}'
     env:
       GH_TOKEN: ${{ secrets.GH_TOKEN_ADMIN }}
   ```

   Le contexte status `FREEZE_LOCK` n'existe pas → tous les pushs sont bloqués.

2. Quand la PR de revert merge, un workflow `unfreeze-after-revert.yml` détecte le merge et retire la protection.

3. Telegram alert envoyé à Robert dès le freeze pour qu'il sache pourquoi son push est rejeté.

## Risque

P1 — si Robert est seul dev et veut push un fix d'urgence, le freeze l'embête. Mitigation : il peut désactiver la protection manuellement depuis l'UI GitHub, ou utiliser `enforce_admins=false` pour s'auto-bypass.

## Alternative légère

Au lieu d'une vraie protection branch, juste émettre un commit `[freeze]` sur veridian qui devient le HEAD avec un README géant qui dit "PROD ROLLED BACK — DO NOT PUSH UNTIL REVERT MERGED". Un signal humain plutôt qu'un blocage GitHub. Moins safe mais zéro friction.

**Reco** : alternative légère pour Robert solo. La vraie protection branch est utile en équipe.
