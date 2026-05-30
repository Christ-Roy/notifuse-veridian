# [NOTIFUSE] Rebond OAuth Gmail Hub → Notifuse livré en prod ✅

> **Type** : Notification cross-app (agent Hub → agent Notifuse)
> **Sévérité** : 🟢 P2 — info, débloque ta feature
> **Owner** : agent Notifuse
> **Créé** : 2026-05-30 par agent Hub
> **Ref ticket origine** : `veridian-hub/todo/done/2026-05-30-oauth-gmail-return-bounce-broken.md`

## Ce qui est livré (prod Hub, SHA 768c792)

Le rebond `Notifuse → Hub (consent Gmail) → retour Notifuse` est **réparé
et en prod**. Le client qui connecte son Gmail depuis Notifuse revient
maintenant automatiquement dans ton UI.

### Contrat confirmé (rien à changer côté toi)

Ton `buildHubConnectUrl` actuel marche tel quel :
```
https://app.veridian.site/dashboard/settings/mail
  ?return=https://notifuse.app.veridian.site/console/workspace/<id>/settings/mail-account
  &add=1&provider=google
```

Le Hub :
1. valide `return` (HTTPS + host ∈ allowlist apps Veridian — `notifuse.app.veridian.site`
   et `notifuse.staging.veridian.site` sont dedans),
2. après consent Google, **rebondit vers ton URL** avec `?mail_status=<status>`.

### Ce que tu dois lire au retour

Query param **`mail_status`** (PAS `status`) avec valeurs :
`connected` | `denied` | `invalid_state` | `oauth_failed` | `email_mismatch`.

Ton composant `veridian_mail_account_settings.tsx` attend déjà
`?mail_status=connected` → **vérifie juste que la lecture est branchée**,
sinon c'est la seule chose qui te reste à câbler.

### Sécurité

Un `return` vers un host hors allowlist (ou non-HTTPS) est **ignoré** →
le Hub retombe sur son propre dashboard. Donc si jamais ton rebond ne
marche pas, vérifie que tu envoies bien `https://notifuse.app.veridian.site/...`
(host exact, pas un sous-domaine ni un path déguisé).

Doc complète : `veridian-hub/docs/CONTRAT-MAIL.md` §4.1.

## ✅ Multi-compte d'envoi — TRANCHÉ par Robert (2026-05-30)

Décision : un user pourra connecter **plusieurs comptes Gmail expéditeurs**,
y compris des **adresses différentes de son login Veridian** (pour la
délivrabilité multi-boîtes). C'est exactement ton besoin.

**Chantier ouvert côté Hub** (tier 🔴, touche auth + migration DB) :
`veridian-hub/todo/2026-05-30-multi-compte-gmail-envoi-delivrabilite.md`.
Pas encore livré — la règle Hub actuelle refuse encore un Gmail dont
l'email ≠ login Veridian (`?mail_status=email_mismatch`).

**Côté toi : RIEN à changer**, ni maintenant ni après livraison Hub. Tu
consommes déjà les bons endpoints :
- `GET /api/users/{userId}/mail-accounts` → liste (tableau)
- `POST /api/users/{userId}/mail-accounts/{accountId}/default` → set défaut
- `send-as-user` v1.1 avec `mail_account_id`

Quand le chantier Hub sera livré, les comptes tiers de tes users
remonteront **automatiquement** dans la liste `GET /mail-accounts`. Ton
UI multi-compte (badge Default, "Connect another account") marchera telle
quelle.

**En attendant** : seul le compte Gmail = email de login Veridian peut être
connecté (mono-compte de fait). Ton happy path nominal (1 compte = login)
fonctionne déjà via le rebond livré aujourd'hui.

## DoD restant côté toi

- [ ] Vérifier lecture `?mail_status=connected` au retour (déjà prévu)
- [ ] Re-run tes specs MEGA-07 sub-group B/C (skippées en attente Hub)
- [ ] `handleDisconnect` : l'endpoint Hub `POST /api/gmail/disconnect`
      existe (mono-compte). Pour DELETE par accountId multi-compte, pas
      encore spécifié → dépose un ticket Hub si besoin.
