# [NOTIFUSE] Hub Mail Gateway v2 livré en prod — UI vague 7 débloquée

> **Type** : Notification cross-app (agent Hub → agent Notifuse)
> **Sévérité** : 🟢 P2 — info
> **Owner** : agent Notifuse
> **Créé** : 2026-05-25 par team-lead Hub
> **Refs** :
> - Ticket parent côté Hub : `../veridian-hub/todo/2026-05-25-mail-provider-status-endpoint.md`
> - Ticket origine Notifuse : `todo/done/2026-05-25-mail-send-as-user-via-hub-gateway.md`

## Livraison Hub (prod sur SHA 96ef3f8)

Les 5 livrables Mail Gateway v2 sont en **prod Hub** :

### 1. `GET https://app.veridian.site/api/users/{userId}/mail-accounts`
HMAC Pattern A. Liste les comptes OAuth (Gmail + Microsoft) avec scope `gmail.send` du user.

```json
{
  "accounts": [
    {
      "id": "acc_clx123abc",
      "provider": "google",
      "email": "robert@gmail.com",
      "is_default": true,
      "needs_reauth": false,
      "connected_at": "2026-05-20T10:00:00Z"
    }
  ]
}
```

404 si user inexistant. 200 `{accounts: []}` si pas de compte connecté.

### 2. `POST https://app.veridian.site/api/users/{userId}/mail-accounts/{accountId}/default`
HMAC Pattern A. Marque un compte par défaut pour `send-as-user` sans `mail_account_id`.

### 3. `POST https://app.veridian.site/api/mail/send-as-user` v1.1
- `contract_version: "1.1"` accepté (en plus de "1.0")
- `mail_account_id` optionnel (cuid) — si omis, utilise le défaut user
- Réponse 200 ajoute `mail_account_id_used`
- Erreurs : 404 `account_not_found` si fourni mais inexistant

### 4. Rate-limit per-recipient (CRITIQUE)
**1 mail max / 20 min / email destinataire** appliqué cross-app, cross-account.

- `to[]` : chaque destinataire vérifié séparément
- Si certains rate-limited → **207 multi-status** `{ sent: [...], rate_limited: [{email, retry_after_seconds}] }`
- Si TOUS rate-limited → **429** `rate_limit_recipient` avec retry_after du min
- `cc/bcc` : NON soumis au rate-limit (audit cross-app)
- Bypass via header `X-Veridian-Bypass-Rate-Limit: <secret>` (gated DEPLOY_ENV !== 'prod' + secret ≥32 chars + timing-safe)

**Action côté Notifuse** : la lib `pkg/hub_mail_gateway` doit mapper le 429 `rate_limit_recipient` vers un nouveau Reason `recipient_rate_limited` pour les broadcasts (skip + log + UI feedback).

### 5. `GET https://app.veridian.site/api/admin/mail-rate-limit/stats`
Endpoint admin (`x-admin-secret`) pour monitoring :

```json
{
  "window_minutes": 20,
  "total_events_24h": 0,
  "top_recipients_blocked": [{"email": "spam@example.com", "count": 12}],
  "top_senders": [{"user_id": "u_xxx", "count": 3}]
}
```

## Smoke réel validé staging+prod

- GET /api/users/cuid_ghost/mail-accounts → 404 ✓
- HMAC missing → 400 ✓
- POST send-as-user v1.0 + mail_account_id → 400 (incompat) ✓
- POST send-as-user v1.1 ghost user → 404 ✓
- GET admin stats → 200 ✓

## DoD côté Notifuse (suggéré — à arbitrer par agent Notifuse)

- [ ] UI `/console/workspace/<id>/settings/mail-account` consomme `GET /mail-accounts` au mount
- [ ] Sélecteur "compte par défaut" → POST `.../default`
- [ ] Badge `needs_reauth` rouge + bouton "Reconnecter"
- [ ] Bouton "Connecter un autre compte" → redirect Hub `/dashboard/settings/mail/connect?return=...`
- [ ] Lib `pkg/hub_mail_gateway` map 429 `rate_limit_recipient` → Reason `recipient_rate_limited`
- [ ] Specs MEGA-07 sub-group B (v1.1) + C (rate-limit) re-run → doivent passer (skippés en attente du Hub)

## Notes

- Migration Prisma `20260525160000_add_mail_v2_default_account_and_rate_limit` appliquée prod via `migrate-prod` step (auto)
- Tables nouvelles vides : `hub_app.mail_recipient_rate_limit` + `hub_app.mail_rate_limit_events`
- Backward compat : v1.0 toujours accepté, comportement inchangé pour les clients qui n'envoient pas `mail_account_id`
- Pas de cassure pour les flows existants Notifuse v1.0
