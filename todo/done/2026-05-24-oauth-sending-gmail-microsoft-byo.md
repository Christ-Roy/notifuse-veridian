# OAuth Sending — connecter Gmail / Microsoft BYO pour envoi emails

> **Sévérité** : 🔴 P1 — débloque la stratégie BYO (cf. memory [[project_email_sending_strategy]])
> **Owner** : agent Notifuse (côté SMTP/SendingProvider) + DÉPEND agent Hub (OAuth flow + token storage)
> **Créé** : 2026-05-24
> **Spec parent** : (à créer côté Hub — cf. ticket compagnon ci-dessous)

---

## ⚠️ STATUT — DÉPEND DU HUB

**Action attendue côté Hub** (ticket compagnon à créer chez `veridian-hub/todo/`) :

1. **Flow OAuth Sending Gmail (Google)** : ajouter un consent flow dédié
   (scope `https://www.googleapis.com/auth/gmail.send`) → distinct du flow
   Sign-in Google (scope `openid email profile`).
2. **Flow OAuth Sending Microsoft (M365)** : scope `Mail.Send` (M365 Graph
   API) → distinct du sign-in Microsoft.
3. **Storage refresh_token chiffré** : côté Hub DB, table dédiée
   `hub_oauth_sending_credentials` ou similaire.
4. **Endpoint Hub → Notifuse** : `GET /api/oauth-sending/credentials/{tenant_id}`
   HMAC. Retourne `{provider: gmail|microsoft, access_token, refresh_token,
   expires_at, from_email, from_name}` pour un tenant donné.
5. **Webhook Hub → Notifuse** : `tenant.oauth_sending_changed` quand le
   user (re)connecte ou révoque un compte d'envoi.

Sans ces 5 livrables Hub, **ce ticket Notifuse ne peut pas avancer**.

---

## Contexte business (memory [[project_email_sending_strategy]])

Robert a acté 2026-05-20 :
- **Phase A** : BYO email pur — chaque client connecte son Gmail / Microsoft /
  SMTP custom pour envoyer ses mails. Notifuse ne fournit PAS de provider
  géré (pas de Resend / SendGrid Veridian-side).
- **Phase B** : prestation managée 500-2500€/campagne (Robert opérateur direct)
- **Phase C** : provider managé Veridian (Resend) — différé, conditionnel
  12+ mois.

**Conséquence** : l'OAuth Sending Gmail + Microsoft est **le seul moyen
viable** pour qu'un client Free/Pro envoie un email transactionnel ou
broadcast aujourd'hui. C'est BLOQUANT pour la commercialisation Notifuse
non-SMTP-custom.

---

## Spec Notifuse (post-livraison Hub)

### 1. Provider SendingOAuth dans `internal/service/`

Nouveau type `EmailProvider` ou `SendingProvider` :
- `provider_type: "oauth_gmail" | "oauth_microsoft"`
- Champs : `access_token`, `refresh_token`, `expires_at`, `from_email`,
  `from_name` (récupérés du Hub)
- Code SMTP via Gmail/Microsoft API en utilisant le `access_token`
- Refresh automatique du `access_token` quand `expires_at` < 5 min

### 2. UI Notifuse Settings → Email Providers

Sur la page `console/src/pages/settings/EmailProviders.tsx` (probablement
existante) :

- Bouton "Connecter Gmail" → redirige vers Hub OAuth consent flow Gmail
- Bouton "Connecter Microsoft 365" → idem Microsoft
- Section "Compte connecté" : affiche `from_email`, dernière sync, bouton
  "Reconnecter" / "Déconnecter"
- Fallback : "Ou utilisez un SMTP custom" (config existante)

### 3. Send pipeline

Dans `internal/service/email_service.go` (upstream) ou plutôt nouveau
`veridian_oauth_sending_provider.go` :

- Si tenant a un OAuth provider actif → utiliser l'API Gmail/MS au lieu de SMTP
- Sinon → fallback SMTP custom upstream
- Gestion refresh_token : si expired, appel Hub pour refresh, retry once

### 4. Webhook consommer côté Notifuse

Endpoint `POST /api/webhooks/hub/tenant.oauth_sending_changed` HMAC qui :
- Update le sending_provider local
- Invalide le cache (si on en a un)

### 5. Tests E2E

`tests/e2e-veridian/specs/oauth-sending-byo.spec.ts` :
- Provision tenant → pas de sending provider → POST transactional → 503 + body
  "no_sending_provider"
- Mock Hub credentials Gmail → POST transactional → 200, mail envoyé via API
  Gmail mock
- Refresh token expired → Notifuse refresh transparently → 200
- Token révoqué → 503 + body "sending_provider_revoked"

---

## Travail Notifuse (estimation)

- Provider abstraction : 1 jour
- UI settings : 1 jour
- Pipeline send + refresh : 1.5 jour
- Webhook : 0.5 jour
- Tests : 1 jour
**Total : ~5 jours** (post-livraison Hub)

---

## Risques

- **Tokens en clair en mémoire** : ne JAMAIS logger access_token /
  refresh_token (audit ZIP + alertes Gitleaks)
- **Quota Gmail API** : 1 milliard de quotas/jour/projet — pas un risque
  pour les clients early stage mais à monitorer post-100 clients
- **Quota Microsoft Graph** : 10k requêtes/10 min par app → plus tendu, à
  monitorer dès les 1ers clients

---

## Définition de done

- [ ] Hub a livré les 5 points ci-dessus
- [ ] UI Notifuse Settings affiche les 2 boutons + statut compte connecté
- [ ] Pipeline send route via OAuth provider quand disponible
- [ ] Refresh token transparent
- [ ] Spec E2E `oauth-sending-byo.spec.ts` verte
- [ ] Doc client (notion / blog) : "Comment connecter votre Gmail"

---

## Ticket compagnon à créer côté Hub

```
veridian-hub/todo/2026-05-24-oauth-sending-gmail-microsoft-flow.md

# OAuth Sending — flow Hub-side Gmail + Microsoft (BYO email pour Notifuse + futur)

Spec : Hub doit héberger 2 consent flows OAuth supplémentaires (scope SENDING,
distinct du SIGN-IN), persister refresh_token chiffré, exposer un endpoint
HMAC pour que Notifuse récupère les credentials.

Phase A stratégie email (Robert 2026-05-20). Bloque le ticket Notifuse
2026-05-24-oauth-sending-gmail-microsoft-byo.md.

(détails à enrichir par agent Hub)
```

**À déposer chez `veridian-hub/todo/` par Robert ou à pinger l'agent Hub.**

---

## ⛔ SUPERSEDED — 2026-05-25 (team-lead)

Ce ticket est superseded par `todo/2026-05-25-mail-send-as-user-via-hub-gateway.md`.

**Raison** : Robert a tranché 2026-05-25 sur une architecture **Hub-centric** au lieu de Notifuse-storage-de-tokens :
- L'OAuth Sending tokens sont stockés **côté Hub** (table `Account` du Hub via Auth.js v5)
- L'envoi Gmail se fait via le Hub Mail Gateway (`POST /api/mail/send-as-user`)
- Notifuse fait juste un call HMAC au Hub (cf. spec dans le ticket superseded)

L'architecture initialement spec dans CE ticket (Notifuse stocke `refresh_token chiffré`, endpoint `GET /api/oauth-sending/credentials/{tenant_id}` etc.) n'est plus pertinente. Pas de double effort.

Ticket archivé. La suite Notifuse-side est dans le ticket Hub gateway.
