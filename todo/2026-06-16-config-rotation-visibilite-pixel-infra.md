# Visibilité rotation multi-senders + pixel par classe PAR INFRA

> **Sévérité** : 🟢 P2
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-16
> **Type** : UI (parité + découvrabilité) — zéro code Go

## Contexte

Deux petits écarts de parité config UI ↔ backend, non bloquants mais qui nuisent à
la compréhension/contrôle de la politique d'envoi.

### A. Round-robin multi-senders : NATIF mais INVISIBLE dans l'UI cold

Réponse à la question de Robert « la gestion des différentes adresses mail de nos
domaines d'envoi est-elle native ? » → **OUI, c'est natif** :
- `EmailProvider.Senders []EmailSender` (`internal/domain/email_provider.go:64`) —
  N senders par infra, sans plafond ; chacun a Email/Name/IsDefault.
- L'UI upstream `console/src/components/settings/Integrations.tsx` permet
  d'ajouter/éditer/supprimer plusieurs senders par intégration (`addSender`/
  `editSender`/`deleteSender`, lignes ~625-643).
- Le round-robin EST implémenté : `internal/domain/veridian_sender_rotation.go`
  (`VeridianSelectSender`, curseur `integrationID|classe`) — rotation PAR CLASSE de
  provider destinataire, OPT-IN en contexte cold (`VeridianIsColdContext`) ET si
  `len(senders) > 1`. Sinon sender figé (upstream).

**Trou (mineur)** : rien dans l'UI cold (`veridian_cold_outreach_settings.tsx`) ni
dans `Integrations.tsx` n'EXPLIQUE qu'ajouter plusieurs senders à une infra active
le round-robin cold, ni ne montre combien de senders rotent. Un admin ne sait pas
que « 3 boîtes sur agences-veridian.fr » = répartition automatique. Risque : il
croit qu'un seul sender est utilisé et sous-dimensionne, ou il ignore que la
capacité s'aligne sur `RateLimitPerMinute × N senders`
(`EmailProvider.VeridianEffectiveRateLimit`, `email_provider.go:176`).

### B. Pixel d'ouverture par classe : workspace OUI, par infra NON

- Backend pixel par classe résolu au niveau workspace+broadcast
  (`veridian_open_pixel.go`, `WorkspaceSettings.VeridianOpenPixelByClass`).
- `EmailProvider` n'a PAS de champ pixel par classe (vérifié :
  `grep -i pixel internal/domain/email_provider.go` → vide). Donc à la différence
  des rates/caps/window (réglables par infra), le pixel ne se règle PAS par infra.

C'est cohérent (le pixel suit la classe destinataire, pas l'IP émettrice) — mais à
confirmer avec Robert : veut-il un override pixel par infra ? Si NON → simple note
de doc. Si OUI → c'est un champ backend à ajouter (comme R2), ticket à promouvoir.

## Demande précise

### A. Visibilité rotation (UI, recommandé)

1. Dans `InfraLimitsCard` (`veridian_cold_outreach_settings.tsx`), à côté du nom de
   chaque intégration, afficher un badge « N senders · round-robin par classe
   actif » quand `senders.length > 1`, sinon « 1 sender (pas de rotation) ». Donnée
   déjà disponible : `integration.email_provider.senders`.
2. Dans la carte tracking/infra, une ligne d'aide : « En cold, si une infra a
   plusieurs adresses d'envoi, Notifuse les alterne automatiquement par provider
   destinataire (round-robin) ; la capacité = rate/min × nombre d'adresses. »
3. (Optionnel) lien « Gérer les adresses » pointant vers Settings → Integrations.

### B. Pixel par infra (décision Robert)

4. Confirmer avec Robert si un override pixel par infra est souhaité. Par défaut :
   **NON** (le pixel dépend du destinataire, pas de l'émetteur) → documenter dans
   l'aide de la section. Si OUI → ouvrir un ticket backend (champ
   `EmailProvider.VeridianOpenPixelByClass` + cascade dans
   `veridian_open_pixel.go`/`veridian_pixel_resolver.go` + UI dans `InfraLimitsCard`).

## Impact

- A : évite le sous-dimensionnement / l'incompréhension de la capacité réelle. Pure
  UX, aucun risque.
- B : clarifie une asymétrie de cascade (rates/caps/window par infra mais pas
  pixel). Probablement juste de la doc.

## Fichiers exacts

| Fichier | Action |
|---|---|
| `console/src/components/settings/veridian_cold_outreach_settings.tsx` | badge senders + aide rotation dans `InfraLimitsCard` |
| (si B=oui) `internal/domain/email_provider.go` + `veridian_pixel_resolver.go` + UI | champ pixel par infra (ticket séparé) |

Tier 🟢 BAS (UI affichage only pour A) → `[risk:low]` acceptable pour la partie A.
