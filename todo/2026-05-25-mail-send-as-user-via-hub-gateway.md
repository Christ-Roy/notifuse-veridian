# [NOTIFUSE] Envoi mail au nom du user via Hub Mail Gateway (v1 Gmail)

> **Type** : Feature — envoi mail depuis le compte Gmail de l'utilisateur
> **Sévérité** : 🟡 P1 — démarrer en parallèle du Hub (livraison en cours)
> **Owner** : agent Notifuse
> **Créé** : 2026-05-25 par team-lead Hub
> **Demandeur** : Robert
> **Refs cross-app** :
> - Vision archi : `veridian-hub/todo/2026-05-25-mail-gateway-hub-multi-provider.md`
> - Implémentation Hub (en cours) : `veridian-hub/todo/2026-05-25-gmail-send-implementation-hub.md`
> - Console OAuth livré : `veridian-hub/todo/done/2026-05-25-oauth-google-gmail-client-2-setup-console.md`

---

## 0. Décision Robert

Toutes les apps Veridian doivent permettre à l'utilisateur d'**envoyer des mails depuis SON propre compte Gmail** (pas un sender Veridian générique). Le Hub centralise tout (OAuth + Stripe pattern §8.4).

Use case Notifuse : transactionnels (welcome workspace, MFA, magic links cross-app, dunning) envoyés **depuis le Gmail de l'admin du workspace** au lieu du sender générique actuel. Délivrabilité = celle du user.

## 1. Frontière (cf vision Hub §4)

| Couche | Owner |
|---|---|
| OAuth Google client + scope `gmail.send` + refresh token + stockage `Account` | **Hub** (déjà fait console + agent en cours) |
| Construction MIME + envoi via Gmail API | **Hub** (route `POST /api/mail/send-as-user`) |
| Audit `hub_app.mail_events` cross-app | **Hub** |
| UI "Connecter mon compte d'envoi" | **Notifuse** (redirect vers Hub puis return) |
| Génération template + appel HMAC vers Hub | **Notifuse** |
| Préférence locale `mail_provider_choice` par workspace | **Notifuse** |

## 2. Contrat HMAC fourni par Hub (route LIVRÉE quand agent Hub finit)

### Endpoint
```
POST https://app.veridian.site/api/mail/send-as-user
```

### Auth HMAC Pattern A (§6.1 CONTRAT-HUB)

Headers obligatoires :
```
x-veridian-app: notifuse
X-Veridian-Timestamp: <epoch_ms>
X-Veridian-Hub-Signature: <hex sha256 hmac>
Content-Type: application/json
```

Secret réutilisé : **`NOTIFUSE_HUB_API_SECRET`** (déjà configuré côté Notifuse + Hub, pas de nouveau secret à provisionner). Signature sur `${timestamp}.${rawBody}`.

### Body Zod

```ts
{
  user_id: string,                   // hub_app.users.id du user qui envoie
  to: string | string[],             // email valide ou array
  subject: string,                   // 1..998 chars
  body_text?: string,                // text/plain (au moins un des deux body requis)
  body_html?: string,                // text/html
  cc?: string[],
  bcc?: string[],
  reply_to?: string,
  attachments?: [{ filename, content_base64, mime_type }],
  idempotency_key: string,           // UUID v4 — anti-double-envoi
  contract_version: "1.0"
}
```

### Réponses

| HTTP | Body | Action côté Notifuse |
|---|---|---|
| 200 | `{ message_id, sent_at, idempotent_replay?: bool }` | Mail OK, persiste local |
| 400 | `{ error: "invalid_payload", details }` | Bug Notifuse — fix payload |
| 401 | `{ error: "invalid_hmac" }` | Bug secret/signature — vérifier `NOTIFUSE_HUB_API_SECRET` |
| 404 | `{ error: "user_not_found" }` | user_id inconnu Hub — drift cross-app, log + retry plus tard |
| 412 | `{ error: "needs_reauth" }` | Refresh token user révoqué — afficher banner "Reconnecte ton Gmail" |
| 422 | `{ error: "provider_not_linked" }` | User n'a pas connecté Gmail côté Hub — fallback SMTP générique |
| 429 | `{ error: "rate_limit" }` | Retry exponentiel (Hub limite 5/min/user, Gmail 250/jour/user) |
| 5xx | `{ error: "provider_unreachable" }` | Retry exponentiel 3 fois puis fail propre |

## 3. Livrables Notifuse

### 3.1 UI configurateur `/settings/mail-account`

Card "Compte d'envoi mail" :
- **Si user a connecté Gmail via Hub** (statut récupéré via `GET <hub>/api/admin/tenant-billing-state` ou similaire — à confirmer avec Hub, sinon nouveau endpoint `GET /api/users/{userId}/mail-provider-status` à demander Hub) :
  - Status vert "Connecté à Gmail (email@...)"
  - Bouton "Déconnecter" qui POST `<hub>/api/gmail/disconnect`
- **Sinon** :
  - Bouton "Connecter mon Gmail" qui redirect vers `https://app.veridian.site/dashboard/settings/mail?return=https://notifuse.app.veridian.site/settings/mail-account`
  - Hub gère le consent + callback puis redirige vers `return` URL
- **Si needs_reauth** : warning rouge "Reconnexion requise" + bouton "Reconnecter"

### 3.2 Lib `services/mail-gateway-client.ts`

Client HMAC vers Hub Mail Gateway :

```ts
export async function sendMailViaHub(params: {
  userId: string;
  to: string | string[];
  subject: string;
  bodyText?: string;
  bodyHtml?: string;
  cc?: string[];
  bcc?: string[];
  replyTo?: string;
  idempotencyKey: string;
}): Promise<
  | { ok: true; messageId: string; sentAt: Date; idempotentReplay?: boolean }
  | { ok: false; reason: 'needs_reauth' | 'provider_not_linked' | 'rate_limit' | 'user_not_found' | 'unreachable'; httpStatus: number }
>;
```

Logique :
1. Construire body Zod conforme contrat v1.0
2. Calculer HMAC signature avec `NOTIFUSE_HUB_API_SECRET`
3. POST avec retry exponentiel sur 5xx (3 tentatives, 1s/3s/10s)
4. Parse réponse et map vers shape typé
5. Logger toutes les erreurs avec context (user_id, idempotency_key)

### 3.3 Refactor des envois transactionnels existants

Notifuse a déjà un système d'envoi SMTP générique (à identifier dans le code). Refactor :

```ts
async function sendTransactionalEmail(template, user, params) {
  if (user.mailProvider === 'gmail-via-hub') {
    return sendMailViaHub({ userId: user.hubUserId, ... });
  }
  return sendViaSmtp(...);  // fallback existant inchangé
}
```

**Backward compat strict** : si user n'a pas connecté Gmail, fallback SMTP comme aujourd'hui. Aucun envoi cassé.

### 3.4 Migration DB Notifuse

Ajouter un champ `users.mail_provider_choice` :
- `'smtp'` (défaut, comportement actuel)
- `'gmail-via-hub'` (activé quand user clique "Connecter Gmail" depuis settings + callback Hub OK)
- Plus tard : `'microsoft-via-hub'`, `'imap-custom'`

Pas de breaking change DB — colonne nullable avec default `'smtp'`.

### 3.5 Tests Nuclear

- `__tests__/services/mail-gateway-client.test.ts` (~10 tests : mock HTTP, HMAC sig, codes erreur, retry)
- `__tests__/components/MailAccountSettings.test.tsx` (UI states : connected / disconnected / needs_reauth)
- `__tests__/api/users/mail-provider-choice.test.ts` (toggle + audit log)

## 4. Definition of done

- [ ] UI `/settings/mail-account` livrée
- [ ] Lib `services/mail-gateway-client.ts` livrée + tests
- [ ] Refactor envois transactionnels avec fallback SMTP
- [ ] Migration DB `mail_provider_choice` (Existing tenants: tous à `smtp` par défaut, aucun comportement changé)
- [ ] Push staging
- [ ] Test bout-en-bout : connecter Gmail depuis Notifuse staging → recevoir un transactionnel test sur sa boîte
- [ ] Marker commit `[risk:medium]` (touche envois mail core)

## 5. Coordination Hub

- Attendre que `POST <hub>/api/mail/send-as-user` soit livré en prod (suivre `veridian-hub/todo/2026-05-25-gmail-send-implementation-hub.md`)
- Demander à Hub un endpoint `GET /api/users/{userId}/mail-provider-status` si pas déjà prévu (pour récup état Gmail connected/needs_reauth depuis Notifuse)
- Le secret HMAC `NOTIFUSE_HUB_API_SECRET` est déjà partagé prod + staging, RAS

## 6. Estimation

~5h dev cumulé.
