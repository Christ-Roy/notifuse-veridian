# 2026-05-19 — CI : Ephemeral staging par branche feature (low prio)

> **Spec** : `../CI-ARCHITECTURE.md` Constitution §24
> **Sévérité** : 🟢 P3
> **Effort** : L (1-2j)

## Constat

Notifuse est en mode **trunk-based strict sur `veridian`** (cf CLAUDE.md racine §"Règle d'or trunk-based"). Donc en pratique on n'a **pas de branches feature**, donc pas de besoin d'ephemeral staging par branche.

Constitution §24 exige des ephemeral staging stacks (`/opt/staging/<app>-<branch-slug>/`) **mais c'est utile uniquement si tu réintroduis des branches feature**, ce qui contredit le mode trunk-based actuel.

## Décision

**Skip ce ticket**. La Constitution §24 est utile pour les apps qui ne sont pas trunk-based. Tant que Notifuse reste trunk-based, pas de feature branches → pas d'ephemeral staging.

## Quand le réactiver

Si un jour tu ouvres Notifuse à des contributeurs externes (open source contribs), tu repassera en mode PR → là, ephemeral staging devient utile pour preview les PRs avant merge.

**Reco** : fermer ce ticket en attendant. Garder ce fichier en `done/` avec décision documentée.
