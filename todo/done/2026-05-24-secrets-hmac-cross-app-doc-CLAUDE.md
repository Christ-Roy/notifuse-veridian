# Doc CLAUDE.md — matrice des secrets HMAC cross-app

> **Sévérité** : 🟢 P3 — doc/onboarding, gain de temps agent
> **Owner** : agent Notifuse
> **Créé** : 2026-05-24 (vu pendant la session — agents perdent du temps à chercher)

## Constat session 2026-05-23/24

Pendant la session, plusieurs agents ont perdu 10-30 min chacun à chercher :
- Quel secret HMAC utiliser pour quel endpoint ?
- Quel header signature exactement (différent par endpoint !) ?
- Quelle string canonique signer ?
- Où trouver le secret en staging vs prod ?

Exemples vécus :
- `HUB_API_SECRET` : utilisé pour `POST /api/tenants/*` Notifuse, header
  `x-veridian-hub-signature`, canonical = `${ts}.${body}`
- `HUB_API_SECRET` (le MÊME) : utilisé pour `GET /api/users/by-email` Hub,
  header `x-veridian-hub-signature`, canonical = `${ts}.GET.${path}?${sortedQuery}`
  (string différente !)
- `HUB_INVITATION_SECRET_NOTIFUSE` : `POST /api/invitations/create` Hub,
  header `x-veridian-invitation-signature`, canonical = `${ts}.${body}`
- `HUB_WEBHOOK_SECRET` : webhooks app→Hub, header différent
- `NOTIFUSE_HUB_API_SECRET` (côté Hub env) = MÊME valeur que `HUB_API_SECRET`
  (côté Notifuse env) — symétrique mais nommage divergent !

## Demande

Ajouter une **section dédiée dans `CLAUDE.md` Notifuse** (table de référence) :

```markdown
## 🔐 Secrets HMAC cross-app — matrice exhaustive

| Sens du flux | Secret env Notifuse | Secret env Hub | Header signature | Canonical string | Endpoint(s) |
|---|---|---|---|---|---|
| Hub → Notifuse | `HUB_API_SECRET` | `NOTIFUSE_HUB_API_SECRET` (même valeur) | `x-veridian-hub-signature` | `${ts}.${body}` (POST) ou `${ts}.GET.${path}?${sortedQuery}` (GET) | `/api/tenants/*`, `/api/veridian/workspaces/*`, `/api/sso/*` |
| Notifuse → Hub (discovery) | `HUB_API_SECRET` | `NOTIFUSE_HUB_API_SECRET` (même) | `x-veridian-hub-signature` | `${ts}.GET.${path}?${sortedQuery}` | `GET /api/users/by-email` |
| Notifuse → Hub (invitation) | `HUB_INVITATION_SECRET_NOTIFUSE` | idem | `x-veridian-invitation-signature` | `${ts}.${body}` | `POST /api/invitations/create` |
| Notifuse → Hub (webhook) | `HUB_WEBHOOK_SECRET` | `NOTIFUSE_HUB_WEBHOOK_SECRET` | `Veridian-Webhook-Signature` | `${ts}.${body}` | `POST /api/webhooks/notifuse` |

### Headers communs

- `x-veridian-app` : nom de l'app caller (`notifuse`, `prospection`, `analytics`, `cms`)
  — utilisé pour sélectionner le bon secret côté Hub
- `x-veridian-timestamp` : unix ms epoch, drift max 5 min anti-replay

### Où trouver les valeurs

- **Staging** : container `notifuse-staging-db` env (`ssh dev-pub docker exec`)
  ou compose Dokploy `compose-bypass-bluetooth-feed-tbayqr`
- **Prod** : compose Dokploy `WN0jglLj5bDIrXUFZHNmw` (Notifuse) et
  `_kxAHDCv1LhvsdwNRX3Vk` (Hub) via API Dokploy
- **Local dev** : `~/credentials/.all-creds.env` (le fichier source de vérité)

### Pièges historiques

- `HUB_INVITATION_SECRET_NOTIFUSE` était **absent des composes Dokploy prod**
  jusqu'au 2026-05-23 (corrigé pendant session). Si nouvel env nécessaire,
  toujours vérifier `WN0jglLj5bDIrXUFZHNmw` (Notifuse) ET `_kxAHDCv1LhvsdwNRX3Vk`
  (Hub) en parallèle.
- Le canonical string POUR LE MÊME SECRET diffère entre POST et GET.
  Pour GET : trier les query params alphabétiquement (anti-malléabilité).
- Validation UUID stricte côté Notifuse `hub_user_id` (V46) — si payload Hub
  envoie un non-UUID, stocké comme NULL silencieusement (cf. fix `7d5b352d`).
```

## Pourquoi

- Onboarding nouvel agent : voit la matrice → comprend en 2 min au lieu de
  30 min de fouille code
- Sécurité : risque réduit de mauvais secret/header copié-collé entre features
- Audit : à chaque ajout d'un nouvel endpoint HMAC cross-app, l'agent doit
  étendre cette table → forçant la doc à jour

## Travail

1. Étendre `CLAUDE.md` Notifuse avec la section ci-dessus
2. Cross-check côté Hub : créer un ticket similaire `veridian-hub/todo/`
   (l'agent Hub maintient la VUE Hub-side, on garde la VUE Notifuse-side ici)
3. Mentionner cette matrice depuis le ticket de tout nouveau endpoint HMAC

## Définition de done

- [ ] Section ajoutée à CLAUDE.md Notifuse
- [ ] Au moins 1 lien depuis un ticket récent qui pointe vers cette doc
- [ ] Pas besoin d'agents cross-app (pure doc)

---

## ✅ Archivé — 2026-05-25 (team-lead)
Livré en prod sur SHA 8f90b538 (giga E2E 226/241 passed). Voir notes ci-dessus.
