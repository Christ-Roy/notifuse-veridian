# Review UI avec Robert — features Veridian shippées côté backend/API

> **Type** : Catalogue vivant — file d'attente de polish UI
> **Convention** : ticket **incrémental**, on ajoute en bas chaque feature backend qui mériterait un coup d'œil UI ensemble. Quand Robert dit "on polish", on prend ce qui est en haut, on hot-reload, on tranche, on barre.
> **Mode** : "hot reload avec Robert" = `cd console && npm run dev`, on regarde l'UX en live sur staging ou local, on aligne.
> **Statut** : aucune feature ci-dessous n'a de **régression critique** UX — tout fonctionne. Mais plusieurs ont été shippées **backend-only** parce que le Hub était le consommateur direct (HMAC) et qu'on n'a pas touché à la console Notifuse pour les exposer ou les expliquer aux clients finaux.

---

## Comment lire ce ticket

Chaque section ci-dessous = **1 feature backend** shippée depuis le fork upstream. Pour chacune :
- **Backend** : ce qui existe côté API (endpoint, comportement, payload).
- **UI actuelle** : ce que voit le client aujourd'hui dans la console Notifuse.
- **UI manquante / à revoir** : ce qu'on devrait montrer / cacher / améliorer.
- **Priorité de polish** : 🔴 visible client = a un impact direct sur l'UX d'un client payant · 🟡 admin / Robert = principalement pour l'opérateur · 🟢 invisible = pas besoin d'UI

Quand on traite une feature → on passe à **✅ POLISHED** et on garde la note finale.

---

## 1. Mode `veridian-managed` — détection serveur 🟢

- **Backend** : `GET /api/veridian/mode` (public, pas de HMAC) renvoie `{mode: "veridian-managed" | "self-hosted", signin_url, hub_url}`. Activé dès que `HUB_API_SECRET` est set.
- **UI actuelle** : `CreateWorkspacePage.tsx` détecte le mode et affiche un écran `<Result>` "Workspace creation disabled" avec 2 boutons (Sign in magic link / Subscribe at Veridian) au lieu du formulaire upstream.
- **UI manquante** :
  - Aucun **branding Veridian** sur la page signin / la console (logo, footer "Powered by Veridian", lien retour Hub). Aujourd'hui c'est encore le logo + look Notifuse upstream → un client qui clique "Open Notifuse" depuis le Hub a l'impression de sortir de l'écosystème.
  - Pas de **lien "Retour au dashboard Veridian"** dans le header de la console.
- **Polish à faire ensemble** : décider du niveau de white-label qu'on veut (full white-label = on reskin tout · co-brand = "Notifuse by Veridian" partout · pas touche = on accepte le mix). Reco : co-brand minimal (header lien retour + footer "Powered by Veridian"). 🔴 visible client.

## 2. Cacher la clé API Hub-managed des Team Settings 🔴

- **Backend** : flag DB `users.veridian_managed BOOLEAN` (migration V7), users `veridian-api-*@notifuse...` sont filtrés des listings membres + DELETE refusé en 403.
- **UI actuelle** : le user technique n'apparaît plus dans Settings → Team. Côté backend OK, confirmé en staging via Chrome MCP.
- **UI manquante** :
  - Décider si on veut une **section read-only** "Integration credentials" qui montre "Veridian Hub — connected" (transparence vs opacité totale).
  - Aujourd'hui c'est opacité totale = simple et safe, mais si un user dev demande "où sont mes clés API ?" on n'a aucune surface pour expliquer.
- **Polish à faire ensemble** : juste un check visuel ensemble que le filtre marche bien sur tous les écrans qui listent les membres (Team Settings, broadcasts envoyés-par, audit log si existant). 🔴 visible client.

## 3. Soft-delete / Restore / Purge / Touch — lifecycle complet 🔴

- **Backend** : 4 endpoints `/api/tenants/{id}/{soft-delete,restore,purge,touch}` + `GET /usage-summary`. Soft-delete pose `deleted_at` + `purge_eligible_at = NOW + 30j`. Restore annule. Purge exige `confirm=PURGE` + `reason` + > 30j. Touch = heartbeat anti-soft-delete debounced 24h.
- **UI actuelle** : **AUCUNE**. Côté console Notifuse, un tenant soft-deleted n'a aucun feedback visuel — l'user peut toujours se connecter via vieux magic link et voir ses données comme si de rien n'était.
- **UI manquante** (gros morceau) :
  - **Bandeau "Account scheduled for deletion in N days — restore"** en haut de chaque page si `deleted_at != NULL`. Lien `restore_url` du Hub.
  - **Mode dégradé** (voir ticket `paywall-obfuscation-degrade.md` en pending — pas encore shippé) : obfusquer les données sensibles, refuser les writes avec 402.
  - **Pas de page dédiée "Account status"** côté console qui montre soft-deleted/active/purged.
- **Polish à faire ensemble** : décider si on veut implémenter le bandeau **avant** ou **après** la PR `paywall-obfuscation-degrade`. Reco : bandeau d'abord (1h de boulot, déjà très utile), obfuscation après. 🔴 visible client.

## 4. Quotas configurables au provision/update-plan 🔴

- **Backend** : `MonthlyEmailQuota` par tenant, paramétrable au provision (`PlanQuotasInput`). Defaults : free=500 / pro=10k / business=50k / enterprise=unlimited. Override env `VERIDIAN_QUOTA_OVERRIDE`.
  - **🆕 2026-05-20 — wire-increment shippé** : `veridian_message_history_decorator` incrémente `emails_sent_this_month` à chaque envoi réussi. **Avant** : compteur perpétuellement à 0, paywall désarmé. **Maintenant** : quota enforcé. **Conséquence UI** : le widget quota devient *réellement* utile, les valeurs ne sont plus toutes à 0.
- **UI actuelle** : **partielle**. La console upstream a quelques compteurs mais pas un dashboard "quota mensuel". Quand le quota est atteint, l'envoi échoue avec un 402 du paywall (voir §5 ci-dessous) sans contexte UX clair pour le user.
- **UI manquante** :
  - **Widget "Email quota" sur le Dashboard** : `EmailsSentThisMonth / MonthlyEmailQuota` avec barre de progression, couleur qui passe en orange à 80%, rouge à 95%.
  - **Cas particulier `quota=-1` (enterprise / grant unlimited)** : afficher "Unlimited" au lieu de la barre, ou icône ∞. Ne PAS afficher de pourcentage.
  - **Notification toast "Vous approchez du quota mensuel"** à 80/95%.
  - **Lien "Upgrade plan" → Hub** sur l'écran quota épuisé.
- **Polish à faire ensemble** : design du widget (placement Dashboard, copy, breakpoints, traitement -1=unlimited). 🔴 visible client.
- **Endpoint exposé pour la UI** : `GET /api/tenants/{id}/status` renvoie déjà `{plan, monthly_email_quota, emails_sent_this_month, quota_remaining}`. Le widget peut le consommer directement. Mais **endpoint dédié `/api/veridian/limits`** prévu dans le ticket V37 pour cumuler limites + usage en 1 call.

## 5. Paywall — blocage 402 sur envois 🔴

- **Backend** : middleware `VeridianPaywallPathFilter` sur 4 paths (`transactional.send`, `broadcasts.create/schedule/sendToIndividual`). Si tenant blocked (suspended / quota / deleted) → `402 Payment Required` + JSON `{error: "Payment required: <reason>", tenant_status: "..."}`.
- **UI actuelle** : la console upstream ne sait pas gérer le 402 spécifiquement — elle affiche un message d'erreur générique. Le client voit "Failed to send" sans comprendre pourquoi.
- **UI manquante** :
  - **Intercepteur axios** côté console qui mappe 402 → modal claire "Your account is suspended / over quota / deleted" avec CTA "Manage subscription → app.veridian.site".
  - **Couleur / icône** distinctive du toast (rouge + cadenas) vs un échec réseau classique.
- **Polish à faire ensemble** : copy du modal selon `tenant_status` (suspended vs quota_exceeded vs deleted). 🔴 visible client.

## 6. Format d'erreur standardisé (CONTRAT-HUB §5.10) 🟡

- **Backend** : tous les errors renvoient maintenant `{error_code, message, details: {...}}` (ex: `tenant_not_found`, `plan_locked`, `owner_mismatch`, `purge_not_eligible`). `error_code` = chaîne machine-readable.
- **UI actuelle** : la console ne lit que `message` upstream, ignore `error_code`. Bonne nouvelle : pas de régression, mauvaise nouvelle : on rate l'opportunité d'afficher des messages localisés par code.
- **UI manquante** :
  - Mapping `error_code → message i18n` dans le client API console (au lieu d'afficher le `message` raw du serveur, qui est en anglais et parfois technique).
- **Polish à faire ensemble** : décider si on prend le temps de mapper les ~10 codes ou si on laisse upstream gérer son truc. Reco : on mappe les 3 codes les plus visibles (`tenant_soft_deleted`, `plan_locked`, `quota_exceeded`), le reste reste en pass-through. 🟡 admin / Robert (impact UX faible si on garde les messages serveur en anglais).

## 7. Plan source + immunité Stripe (CONTRAT-HUB §3.3) 🟡

- **Backend** : champ `plan_source` (`stripe`, `manual`, `lifetime_site_vitrine`, `lifetime_partner`, `internal`). Plans non-stripe sont **immuns** à un downgrade via webhook Stripe (renvoient `409 plan_locked`).
  - **🆕 2026-05-20** — depuis le ship de grant-unlimited (§16), `lifetime_partner` est devenu **fréquent** (équipe interne + clients fidèles). Le badge devient plus utile qu'avant car il y aura régulièrement des comptes avec ce plan_source.
- **UI actuelle** : **invisible côté console**. Le client ne voit pas pourquoi son plan est "lifetime offert par Veridian" vs "stripe payé".
- **UI manquante** :
  - **Badge** sur Settings → Plan : "Plan offert par Veridian — lifetime", "Plan Stripe", "Plan manuel admin".
  - **Copy proposée par plan_source** :
    - `lifetime_partner` → "Lifetime partner — accès illimité offert par Veridian"
    - `lifetime_site_vitrine` → "Lifetime — inclus avec votre site vitrine"
    - `internal` → "Compte interne Veridian" (visible seulement pour toi/équipe)
    - `manual` → "Plan admin manuel"
    - `stripe` / "" → afficher la subscription Stripe normalement
  - Pas critique mais bon pour la confiance / transparence client.
- **Polish à faire ensemble** : design du badge + faut-il afficher la prochaine date de renouvellement / fin de validité ? 🟡 admin / Robert.

## 8. Idempotency-Key middleware (CONTRAT-HUB §5.11) 🟢

- **Backend** : header `Idempotency-Key` sur tous les mutateurs `/api/tenants/*` → replay safe pendant 24h.
- **UI actuelle** : N/A — endpoints consommés uniquement par le Hub.
- **UI manquante** : RIEN. 🟢 invisible — pas concerné.

## 9. Auto-login URL self-contained 🟢

- **Backend** : `GET /veridian/auto-login?token=<HMAC>` log le user owner directement via localStorage. TTL 60s. Utilisé par le bouton "Open Notifuse" du Hub.
- **UI actuelle** : marche. Le user clique depuis le Hub, atterrit déjà loggé sur le dashboard Notifuse.
- **UI manquante** :
  - **Toast de bienvenue** "Welcome back, <email>!" pour confirmer le login (sinon ça paraît un peu magique, l'user peut penser qu'il s'est passé un truc bizarre).
  - **Détection token expiré** : si `?token=...` est dans l'URL mais que le serveur renvoie 401, afficher une page "Login link expired — request a new one from the Veridian dashboard" au lieu de tomber sur la signin upstream confuse.
- **Polish à faire ensemble** : ces 2 micro-touchs. 🟡 admin / Robert (rare mais frustrant quand ça arrive).

## 10. Magic link via Hub (`POST /api/workspaces.generateMagicLink`) 🟢

- **Backend** : endpoint protégé `RequireAuth` (JWT du user owner) qui génère un nouveau magic link + auto-login URL fresh.
- **UI actuelle** : N/A — consommé exclusivement par le Hub côté serveur.
- **UI manquante** : RIEN. 🟢 invisible.

## 11. Health endpoint observable (livrable 3 contrat) 🟡

- **Backend** : `GET /api/tenants/{id}/health` renvoie `{status, owner_attached, api_key_valid, magic_link_capable, members_count, plan}`. Polled par Hub en cron 1×/h pour détecter régressions silencieuses.
- **UI actuelle** : N/A — endpoint HMAC, consommé par Hub.
- **UI manquante (potentielle)** :
  - **Page admin Robert** (sur le Hub, pas sur Notifuse) qui affiche pour chaque tenant : owner_attached ✓/✗, api_key_valid ✓/✗, etc. — debug rapide quand un client râle.
  - C'est plutôt côté **Hub** que ça se passe. À noter ici pour ne pas oublier de demander à l'agent Hub de câbler ça.
- **Polish** : pas pour Notifuse, mais à router vers Hub. 🟡 admin / Robert.

## 12. AttachOwner endpoint (repair) 🟢

- **Backend** : `POST /api/veridian/admin/attach-owner` répare un workspace pré-existant en y attachant un user humain comme owner. Idempotent, safe en batch.
- **UI actuelle** : N/A — utilisé en repair one-off ou par Hub.
- **UI manquante** : RIEN. 🟢 invisible.

## 13. Cache invalidate endpoint 🟢

- **Backend** : `POST /api/veridian/admin/cache/invalidate` purge le cache paywall sur demande Hub.
- **UI actuelle** : N/A — admin endpoint.
- **UI manquante** : RIEN. 🟢 invisible.

## 14. Wipe test tenants endpoint 🟢

- **Backend** : `POST /api/veridian/admin/wipe-test-tenants` hard delete tenants matching prefix (CI / tests).
- **UI actuelle** : N/A.
- **UI manquante** : RIEN. 🟢 invisible.

## 15. Webhooks Notifuse → Hub (en cours, pas tous shippés) 🟢

- **Backend** : `VeridianWebhookEmitter` envoie `tenant.provisioned`, `tenant.plan_changed`, `tenant.suspended`, `tenant.resumed`, `tenant.deleted`, `tenant.owner_changed`. **Manquants** (ticket `webhooks-manquants.md` pending) : `tenant.soft_deleted`, `tenant.restored`, `tenant.purged`, `tenant.touched`, `tenant.quota_exceeded`.
- **UI actuelle** : N/A.
- **UI manquante** : RIEN côté Notifuse, mais ces events alimenteront probablement des écrans/notifs côté Hub.
- **Polish** : à router vers Hub quand les events seront émis. 🟢 invisible côté Notifuse.

## 16. Grant-unlimited admin endpoint (2026-05-20) 🟡

- **Backend** : `POST /api/veridian/admin/grant-unlimited` HMAC-protected. 1 curl pour passer un tenant en `plan=enterprise + quota=-1 + plan_source=lifetime_partner`. Idempotent, refuse `stripe`, reason obligatoire, resume auto, invalidation cache paywall immédiate. Cf. mémoire `project_grant_unlimited_endpoint.md`.
- **UI actuelle** : **AUCUNE** — endpoint HMAC, appelé en curl par Robert directement (ou par automation Hub à terme).
- **UI manquante (potentielle)** :
  - **Page admin Robert côté Hub** (pas Notifuse) avec un bouton "Offrir l'accès illimité" sur la fiche tenant. Saisie : `reason` (libre) + `plan_source` (dropdown). Click → POST grant-unlimited. À router vers agent Hub.
  - **Côté console Notifuse du tenant grant-é** : aucune UI dédiée nécessaire. Le tenant verra juste son plan passer à `enterprise` + quota `unlimited` (via §4 widget quota et §7 badge plan_source). C'est exactement ce qu'on veut.
- **Polish à faire ensemble** : décider si Robert veut un bouton dans le Hub ou s'il fait juste du curl à la main pour les rares cas. Reco : on commence par curl manuel (déjà testé live 2026-05-20), on câble un bouton Hub quand le volume justifie. 🟡 admin / Robert seul.
- **Pré-requis pour V37** : quand les nouvelles dimensions arriveront (seats, contacts, oauth_accounts, custom_domains, sequences, A/B, branding, white_label, history), grant-unlimited devra aussi forcer toutes ces dimensions à `-1` / `true`. Pas encore câblé.

## 17. Attach-member endpoint (lot B 2026-05-21) 🔴

- **Backend** : `POST /api/tenants/{tenantId}/attach-member` HMAC Hub. Quand un user accepte une invitation cross-app côté Hub, le Hub appelle ce endpoint pour attacher le user au workspace Notifuse. Idempotent. Crée le user si absent, INSERT/UPDATE `user_workspaces`, génère `login_url` magic link. Codes : 201 first attach, 200 already_member, 401/404/400/423/409.
- **UI actuelle** : **AUCUNE indication** côté console Notifuse. Un user invité via Hub apparaît dans Settings → Team comme n'importe quel autre membre, sans contexte sur l'origine de l'invitation.
- **UI manquante** :
  - **Badge "Invité via Veridian"** sur les rows membres dont la `metadata` indique `via_hub_invitation_id` (à câbler : stocker l'invitation_id reçu dans `user_workspaces.metadata`).
  - **Bouton "Gérer dans Veridian"** sur la fiche membre → redirige vers `app.veridian.site/team/{tenantId}` (route Hub à venir).
  - **Tooltip explicatif** : "Ce membre a été invité depuis le dashboard Veridian. Pour révoquer l'accès, gérez ses permissions cross-app depuis là."
- **Polish à faire ensemble** : décider si on stocke `via_hub_invitation_id` en metadata (impact backend mineur, ~10 lignes) ou si on accepte d'avoir tous les membres uniformes côté UI Notifuse. Reco : **stocker la metadata** maintenant, polish UI plus tard quand le flow invitation Hub aura plus de volume. 🔴 visible client (multi-membre).

## 18. Discovery endpoint by-email (lot D 2026-05-21) 🟢

- **Backend** : `POST /api/users/by-email` HMAC Hub. Hub interroge Notifuse au login user pour savoir quels workspaces ce user possède côté Notifuse. Read-only, retourne `{found, user_email, workspaces[]}` avec `magic_link_capable: true` toujours.
- **UI actuelle** : N/A — endpoint HMAC, consommé par Hub uniquement.
- **UI manquante** : RIEN côté Notifuse. Côté Hub : la carte "Notifuse" du dashboard user sera désormais alimentée par discovery (vs colonnes dénormalisées `tenants.notifuse_*`). 🟢 invisible.

## 19. V38 — Signal trial activity_threshold (lot C 2026-05-21) 🟢

- **Backend** : V38 ajoute `emails_sent_lifetime BIGINT` + `activity_threshold_reached_at TIMESTAMPTZ` sur `veridian_plan`. Quand le tenant atteint 5 mails envoyés (cumul lifetime, jamais reset), Notifuse émet event webhook `tenant.activity_threshold_reached` vers Hub (1 fois, idempotent). Le Hub orchestre ensuite le trial 2j wait → 15j visible.
- **UI actuelle** : N/A — signal métier interne, pas exposé client.
- **UI manquante** : **INTERDITE** par CLAUDE.md projet (philosophie pivot 2026-05-21) :
  - ❌ PAS de "Vous avez envoyé X mails depuis votre activation"
  - ❌ PAS de progress bar "3/5 vers votre trial Pro"
  - ❌ PAS de compteur visible AVANT phase 3 (J+2 post-5 mails côté Hub)
  - ✅ Le bandeau trial 15j (puis +30j si CB) sera affiché par le Hub uniquement, **après** J+2. Notifuse reste muet jusque-là.
- **Polish** : RIEN, intentionnel. 🟢 invisible.

## 20. Admin listing dry-run + wipe orphelins (lot G 2026-05-21) 🟢

- **Backend** : `GET /api/veridian/admin/tenants?prefix=X&include_orphans=true&limit=N` dry-run listing (managed vs orphans buckets). `WipeTestTenantsInput.IncludeOrphans bool` étend le scan à `workspaceRepo.List` (source de vérité globale). Le step CI cleanup utilise désormais `include_orphans:true`.
- **UI actuelle** : N/A — endpoint admin curl, consommé par Robert et le CI cron cleanup.
- **UI manquante** :
  - **Page admin Robert** côté **Hub** (pas Notifuse) : "Tenants orphelins détectés" — liste les workspaces qui traînent sans `veridian_plan`. Bouton "Wipe" qui appelle `/api/veridian/admin/wipe-test-tenants` avec confirmation `confirm:"WIPE"`.
  - Pas urgent : le cron CI nettoie déjà automatiquement les orphelins matchant les prefixes E2E.
- **Polish** : RIEN côté Notifuse, à router vers agent Hub si volume orphelins justifie un dashboard. 🟢 invisible côté Notifuse.

## 21. Paywall mode dégradé soft-deleted (PENDING `2026-05-21-paywall-degraded-mode-soft-deleted.md`) 🔴

- **Backend** : ⏳ **PAS ENCORE SHIPPÉ**. Ticket actif. Remplacera le mur béton actuel (`402 Payment Required: tenant deleted`) par un mode dégradé :
  - Routes **lecture** : 33% des chars en clair + obfuscation `fooba••••••` (sauf `SENSITIVE_FIELDS` = password/api_key/billing → toujours obfusqués)
  - Routes **écriture** : `402` + `error: tenant_soft_deleted` + lien `restore_url` du Hub
- **UI actuelle** : **catastrophique UX**. Un user soft-deleted via Hub voit la console crasher avec page d'erreur cryptique, sans chemin de récupération visible.
- **UI manquante** (gros morceau, lié à §3 bandeau soft-delete) :
  - **Bandeau persistant top de page** : "Votre compte est supprimé — N jours pour restaurer. [Restaurer dans Veridian →]"
  - **Modal au login** si soft-deleted détecté : "Bonjour, votre compte est suspendu depuis le DD/MM. Vous pouvez consulter vos données en lecture seule. [Restaurer →] [Exporter mes contacts →]"
  - **Export bouton dédié** dans Settings → Account pour télécharger ses contacts/templates avant la purge.
- **Polish à faire ensemble** : ATTENDRE que le backend soit shippé. À polish dès que le middleware obfuscation est en place. 🔴 visible client (impact rétention + image de marque).

## 22. PIVOT PRICING 2026-05-21 — UI doit purger les compteurs visibles 🔴🔴🔴

> **PRIORITÉ #1 ABSOLUE.** Le pivot pricing 2026-05-21 (`PRICING-VERIDIAN.md`) **interdit explicitement les compteurs visibles UI client** — or notre console actuelle en affiche encore.

- **Backend** : `DefaultPlanLimits` figé à `-1` (illimité) sur **toutes** les dimensions sauf `FeatureWhiteLabel` (Business+ only). Free a TOUT illimité y compris contacts, OAuth, seats, automation, A/B, branding optionnel, custom domains, historique. **Seules différenciations** : Free → durée 15j révélée à J+2 + Business → white-label custom.
- **UI actuelle** : la console upstream Notifuse affiche encore des compteurs / limites partout (contacts list, members, templates, etc.) — ces écrans **violent CLAUDE.md** qui interdit :
  - ❌ Compteur visible "il vous reste X mails / contacts / domaines"
  - ❌ Menu grisé "🔒 Pro", pop-up "passez Pro pour faire ça"
  - ❌ Toute limite enforced sur contacts / OAuth / seats / automation / historique / custom domains / A/B
- **UI à polish** (gros chantier) :
  - **Widget quota Dashboard (§4)** : afficher "∞ Illimité" au lieu de progress bar quand `quota=-1`. Ne JAMAIS afficher de pourcentage.
  - **Page Contacts** : retirer "X / 1000 contacts utilisés" (si présent upstream)
  - **Page Templates / Broadcasts** : retirer toute mention de "limite mensuelle"
  - **Settings → Plan** : afficher "Plan Free — accès illimité pendant 15 jours" (pour les Free actifs). **Pas** de feature list grisée.
  - **Aucun mur béton** sur les features upstream qui auraient des limites natives (A/B testing notamment — déjà reverté côté backend lot 4a, vérifier UI cohérente)
- **Vérification systématique nécessaire** : audit page-par-page de la console pour identifier tous les écrans qui violent le pivot. Robert + agent en hot reload, 1h grand max.
- **Polish à faire ensemble** : **URGENT** — chaque jour qui passe avec ces compteurs visibles = friction artificielle vs la promesse "générosité maximale". 🔴🔴🔴 visible client (#1 priorité).

---

## Récap visuel — priorité de polish UI

| # | Feature | Priorité | Effort estimé |
|---|---|---|---|
| **22** | **PIVOT PRICING — purger compteurs/limites visibles** | **🔴🔴🔴 URGENT** | **M (2-3h audit + fix)** |
| 4 | Widget quota mensuel → "∞ Illimité" quand quota=-1 (sous-cas du §22) | 🔴 | S (1h) |
| 5 | Intercepteur 402 + modal paywall | 🔴 | S (1-2h) |
| 21 | Paywall mode dégradé soft-deleted (attend backend, post-§3) | 🔴 | M (2-4h) après backend shippé |
| 3 | Bandeau soft-delete + page status | 🔴 | M (2-4h) bandeau seul, L pour obfuscation |
| 17 | Attach-member — badge "Invité via Veridian" + bouton "Gérer dans Veridian" | 🔴 | S (1-2h, dont metadata backend) |
| 1 | Branding/white-label `veridian-managed` | 🔴 | M (2-4h) |
| 2 | Vérif filtre api_key sur toutes les surfaces | 🔴 | S (30min) |
| 6 | Mapping error_code i18n | 🟡 | S (1h) si on prend que les top 3 |
| 7 | Badge plan_source sur Settings (**+ copy par source** depuis grant-unlimited 2026-05-20) | 🟡 | S (1h) |
| 9 | Toast auto-login + page token expiré | 🟡 | S (1h) |
| 16 | Bouton Hub "Offrir accès illimité" (**nouveau 2026-05-20**) | 🟡 admin | S (1h) côté Hub, 0 Notifuse |

**Total polish 🔴 estimé** : ~12-18h de travail UI ensemble, qui ferait passer la console Notifuse de "Notifuse upstream avec quelques redirections" à "vraie expérience SaaS Veridian-managed cohérente avec le pivot pricing 2026-05-21 et le dashboard Hub".

**Changements depuis le ship du 2026-05-20** (wire-increment + grant-unlimited) :
- §4 widget quota devient *réellement utile* (avant : compteur perpétuellement à 0)
- §4 doit gérer le cas `quota=-1 = unlimited` (enterprise + lifetime + grant)
- §7 badge plan_source devient *plus visible* (lifetime_partner fréquent maintenant)
- §16 nouvelle section grant-unlimited (côté Hub seul, pas côté Notifuse)

**Changements depuis le sprint 2026-05-21** (lots B/C/D/E/G + pivot pricing) :
- **§22 PIVOT PRICING** = **nouvelle priorité #1 absolue** — la console doit cesser d'afficher tous les compteurs/limites/menus grisés qui violent la philosophie "générosité maximale" gravée par Robert. Audit page-par-page nécessaire.
- §17 nouvelle section attach-member (post-lot B) — badge + bouton "Gérer dans Veridian"
- §18-§20 nouvelles sections invisibles (Hub-only, pas d'impact UI Notifuse)
- §19 V38 trial signal : **UI explicitement interdite** côté Notifuse (compteur invisible jusqu'à phase 3 Hub J+2)
- §21 paywall mode dégradé : backend pending, gros polish UI à prévoir dès qu'il est shippé

---

## Comment on incrémentera ce ticket

Quand une nouvelle feature backend est shippée (nouveau endpoint, nouveau comportement visible, nouveau champ exposé) → on ajoute une section **# N+1. <nom>** à la fin avec le même format. Quand on polish une section ensemble → on la marque `✅ POLISHED — <date>` avec une note rapide.

Le `done/` n'est pas pour ce ticket : il reste actif tant que des sections 🔴 / 🟡 ne sont pas traitées.

---

# DA & Design System — audit 2026-05-22

> **Type** : audit direction artistique de la console (`console/src/`).
> **Méthode** : lecture `App.tsx` (ThemeConfig), `index.css`, `App.css`,
> `styles/`, `index.html`, layouts + grep hex/fontSize/margins sur tout
> `console/src/`. Lecture seule, rien modifié côté code.
> **Constat global** : la console tourne encore sur du **template Vite
> brut + thème Antd quasi vide**. Aucune DA Veridian formalisée. Le violet
> `#7763F1` est posé sur `colorPrimary` mais tout le reste — typo, gris,
> espacements, surfaces — est laissé au défaut Antd ou hardcodé à la main
> écran par écran. Ça « marche » mais ça ne ressemble pas à un produit
> Veridian, ça ressemble à du Notifuse upstream avec un accent violet.

## D1. `index.css` — boilerplate Vite qui contredit la DA 🔴

Le fichier contient des vestiges du template Vite jamais nettoyés, dont
certains **cassent activement** le rendu :

- `a { color: #646cff }` + `a:hover { color: #535bf2 }` (lignes 27-34) :
  **bleu Vite**, pas le violet Veridian `#7763F1`. Tout lien `<a>` natif
  hors composant Antd s'affiche en bleu Vite. → **Fix** : remplacer par
  `var(--color-primary)` (#7763f1) ou retirer le bloc et laisser Antd
  gérer (`colorLink` est déjà à #7763F1 dans le thème — mais ne s'applique
  qu'aux `<Typography.Link>`/`<a>` Antd, pas aux `<a>` bruts).
- `color: rgba(255, 255, 255, 0.87)` sur `:root` (ligne 18) : **vestige
  dark mode** — couleur de texte blanche sur fond clair. Invisible
  seulement parce qu'Antd réécrit la couleur sur ses propres composants.
  Tout texte rendu hors Antd hérite de blanc sur blanc. → **Fix** :
  `color: var(--foreground)` (#171717, déjà défini juste au-dessus mais
  jamais consommé).
- `color-scheme: light dark` (ligne 17) : déclare un support dark mode
  qui n'existe pas. → **Fix** : `color-scheme: light` uniquement.
- `h1 { font-size: 3.2em; line-height: 1.1 }` (lignes 36-39) : titre
  géant du template Vite. Aucun écran console ne veut un h1 à 3.2em. →
  **Fix** : supprimer, laisser la hiérarchie typo (cf. D4) décider.
- `font-synthesis: none` + smoothing : OK à garder.
- Vestige `--background: rgb(243, 246, 252)` (ligne 9) vs le fond réel
  appliqué partout `#F9F9F9` (App.tsx Layout, WorkspaceLayout). Deux
  valeurs de fond qui divergent. → **Fix** : aligner sur une seule valeur
  tokenisée.

## D2. `App.css` — fichier 100 % mort à supprimer 🟢

`console/src/App.css` est le **CSS par défaut Vite intégral** (`#root`
max-width 1280px, `.logo` spin animation, `#646cffaa` drop-shadow,
`.read-the-docs`). **Il n'est importé nulle part** (`grep "App.css"
src/` = 0 résultat). C'est du fichier fantôme. → **Fix** : `git rm
console/src/App.css`. Zéro risque, pur nettoyage de dette template.

## D3. Thème Antd (`App.tsx`) — squelette à étoffer 🔴

Le `ThemeConfig` est une coquille : `colorPrimary` + `colorLink` + une
poignée d'overrides Layout/Card/Table/Drawer/Modal, dont **plusieurs
lignes en commentaires morts** (`bodyBg` commenté, `Button.primaryColor`
commenté, `Card.headerBg` commenté). Ce qui manque pour avoir une vraie
DA tokenisée :

- **Tokens globaux absents** : `colorSuccess`, `colorWarning`,
  `colorError`, `colorInfo` laissés au défaut Antd (vert/orange/rouge/bleu
  génériques). À ce stade les statuts ne sont pas accordés au violet
  Veridian. → décider une palette sémantique Veridian.
- **`borderRadius` global** : non défini → défaut Antd 6px. Or `Card` est
  forcé à 4px localement. Incohérence : Cards à 4px, Buttons/Inputs/Modals
  à 6px. → **Fix** : poser un `token.borderRadius` global unique.
- **`fontFamily`** : non déclaré dans le token Antd. Antd utilise sa
  propre stack système. `index.css` déclare `Inter` sur `:root` mais Antd
  ne lit pas `:root` pour sa font. → poser `token.fontFamily` (cf. D5).
- **`colorBgLayout` / `colorBgContainer`** : le fond `#F9F9F9` est
  réinjecté **à la main 11 fois** dans `WorkspaceLayout.tsx` + dans 5
  composants du thème (`Layout.bodyBg/siderBg`, `Card`, `Drawer`,
  `Modal`, `Timeline.dotBg`). → **Fix** : un seul `token.colorBgLayout`
  + `token.colorBgContainer` → supprime les 11 inline.
- **`controlHeight`, `fontSize`, espacements (`padding*`, `margin*`)** :
  tous au défaut Antd. Aucun rythme d'espacement Veridian.
- `Table.fontSize: 12` + `Card.headerFontSize: 16` : tailles posées
  isolément sans échelle typo cohérente (cf. D4).
- **Commentaires morts à purger** : lignes 47, 53-54, 57 de `App.tsx`.

→ **Fix structurant** : créer un module `console/src/theme/veridian.ts`
(ou `styles/theme.ts`) qui exporte le `ThemeConfig` complet — tokens
couleur sémantiques, `borderRadius`, `fontFamily`, échelle d'espacement,
échelle typo — et l'importer dans `App.tsx`. Sortir le thème du fichier
`App.tsx` = base d'un vrai design system.

## D4. Typographie — pas de hiérarchie, tailles arbitraires 🟡

Aucune échelle typo. Les tailles sont posées à la main, au cas par cas :

- **53 occurrences** de `fontSize: <number>` inline dans des `.tsx` (hors
  tests). Valeurs vues en vrac : `6, 8, 9, 10, 11, 12, 13, 14, 16, 18`.
  Dix tailles différentes sans logique d'échelle.
- Tailles **micro illisibles** : `fontSize: 6` et `fontSize: 8`
  (`EmailBlockClass.tsx` — labels de blocs email), `fontSize: '9px'`
  (footer version `WorkspaceLayout`, crédit photo `MainLayout`
  `text-[9px]`). En dessous de 11px c'est sous le seuil de lisibilité
  confortable. À auditer écran par écran : légitime (badge dense) ou
  négligence ?
- `Menu` de la sidebar : `fontSize: '13px', fontWeight: 600` hardcodé
  inline dans `WorkspaceLayout` — devrait être un override `Menu` du
  thème.
- → **Fix** : définir une échelle (ex. `xs 11 / sm 12 / base 14 / md 16
  / lg 20 / xl 24`), la poser dans le thème, et remplacer progressivement
  les `fontSize:` inline. Au minimum bannir `< 11px` sauf cas justifié
  documenté.

## D5. Font Inter déclarée mais JAMAIS chargée 🔴

`index.css` déclare `font-family: Inter, system-ui, ...` sur `:root`.
**Mais Inter n'est chargée nulle part** : aucun `<link>` Google Fonts
dans `index.html`, aucun `@import`, aucun `.woff2` self-hosted (`find
-name "*.woff*"` = 0 résultat). Résultat : **la console n'affiche jamais
Inter** — elle tombe en silence sur `system-ui` (donc des polices
différentes selon OS : Segoe UI sur Windows, San Francisco sur Mac,
Cantarell/Ubuntu sur Linux). La DA typo n'est ni Inter, ni cohérente
cross-plateforme. → **Fix** : soit self-host Inter (woff2 dans `public/`
+ `@font-face`, recommandé — pas de dépendance Google, meilleure perf),
soit `<link>` Google Fonts. Puis poser `token.fontFamily` Antd sur la
même stack pour que les composants Antd l'utilisent aussi.

## D6. Couleurs en dur — 797 hex dans `console/src/` 🟡

`grep -rE '#[0-9a-fA-F]{3,8}' src/` (hors tests) = **797 occurrences**.
Répartition :

- **~574 dans `email_builder/` + `blog_editor/` + `blog/`** : en grande
  partie **légitime** — ce sont des outils d'édition de contenu
  utilisateur (color pickers, presets de thème blog, défauts MJML). Top
  fichiers : `TiptapToolbar.tsx` (83), `ColorPickerWithPresets.tsx`
  (82), `EmailBlockClass.tsx` (45), `blog/themePresets.ts` (45). Ces hex
  appartiennent au *contenu* édité, pas au *chrome* de l'app. À ne PAS
  tokeniser de force.
- **~223 dans le chrome de l'app** (hors email/blog) : **c'est là que
  se concentre la dette DA**. Cas concrets à tokeniser :
  - `WorkspaceLayout.tsx` : **11 hex** — `#F9F9F9` ×7, `#f0f0f0` ×3
    (bordures), `#000`. Tout le fond + les bordures de la coquille
    principale en dur. → tokens.
  - `formatters.tsx` : `#999` ×5 (texte secondaire / bordures dotted).
  - `automations/nodes/constants.ts` : 10 hex de couleurs de nœuds
    (`#52c41a`, `#1890ff`, `#722ed1`...) = **palette Ant Design legacy**
    (les couleurs « daybreak blue / polar green » d'Antd v4), pas la
    palette Veridian. À reaccorder.
  - `analytics/EmailMetricsChart.tsx` : 8 hex Tailwind (`#3b82f6`
    blue-500, `#10b981` green-500, `#8b5cf6` purple-500...) — une 3e
    palette encore différente (Tailwind). → la console mélange palette
    Antd v4 (automations), palette Tailwind (analytics) et le violet
    Veridian. Trois systèmes de couleur cohabitent.
  - `DashboardPage.tsx` : `#1890ff` (bleu Antd v4) + `#f5f5f5` +
    `#e6f7ff` inline pour l'avatar workspace.
  - `BaseNode.tsx` : `#7763F1` hardcodé en dur (devrait lire le token).
  - `veridian_brand_footer.tsx` : `#9ca3af` ×2 hardcodé (le composant
    Veridian lui-même n'utilise pas de token — à corriger en passant).
  - `index.css` : `rgba(78, 108, 255, 0.4)` sur le hover de table
    (ligne 46) = encore une **4e nuance de bleu**, ni Antd ni Veridian.
- → **Fix** : (1) ne toucher à rien dans email_builder/blog (contenu
  user). (2) Sur le chrome : remplacer les hex par les tokens du thème
  Veridian (D3). (3) Trancher **une** palette unique (Veridian violet +
  gris neutres + 4 couleurs sémantiques) et tuer le mélange Antd-v4 /
  Tailwind / nuances bleues random.

## D7. Espacements — 276 marges inline, pas de rythme 🟡

`grep marginBottom/Top/Left/Right: <number>` (hors tests) = **276
occurrences**. Les écrans posent leurs espacements à la main
(`marginBottom: '8px'`, `'12px'`, `'20px'`, `'24px'`, `margin: '24px
0'`...) au lieu d'utiliser les primitives Antd (`<Space>`, `<Row
gutter>`, `<Flex gap>`). Exemple `DashboardPage.tsx` : `gap: '8px'`,
`gap: '12px'`, `marginBottom: '8px'`, `margin: '24px 0'` — quatre
valeurs d'espacement sur un seul petit écran, toutes en dur. Pas de
catastrophe visuelle mais **aucun rythme d'espacement** → micro-
incohérences partout. → **Fix** : définir une échelle (4/8/12/16/24/32)
et privilégier `<Space size>` / `<Flex gap>` plutôt que `style={{
margin... }}`. Chantier progressif, pas bloquant.

## D8. Incohérence d'usage Antd — composants & tailles 🟡

- **`borderRadius` mixte** : `Card` forcé à 4px (thème), tout le reste à
  6px (défaut Antd). Cards anguleuses à côté de Buttons arrondis. →
  unifier (cf. D3).
- **`tabs-in-header` dans `index.css`** (lignes 73-86) : positionnement
  absolu avec des magic numbers fragiles (`width: 40%; right: 30%; left:
  30%; height: 65px; line-height: 41px`). Casse au moindre changement de
  hauteur de header (le header fait 64px, le CSS dit 65px). → à revoir,
  hack responsive fragile.
- **Override `.ant-alert`** (index.css 91-111) : reskin Alert avec
  bordure gauche colorée — mais les 4 couleurs sont les hex Antd v4
  (`#1890ff`, `#52c41a`, `#faad14`, `#ff4d4f`), pas des tokens. Cohérent
  visuellement mais hardcodé.
- Le crédit photo `MainLayout` (`text-[9px]` overlay noir 60%) traîne
  sur les écrans pré-login — détail mais ça fait « template ».
- `index.html` : `<title>Console | Notifuse</title>` + splash logo
  `logo.png` Notifuse + `<meta name="msapplication-TileColor"
  content="#da532c">` (orange random) + `mask-icon color="#5bbad5"`
  (cyan) — le **branding du document HTML est encore 100 % Notifuse
  upstream**, couleurs de tuile sans rapport avec la DA. → à reskin
  quand on traitera le co-brand (§1).

## D9. Écrans à prioriser pour un polish 🟡

Pages les plus rentables à repasser en session calme, avec la raison :

- **`SignInPage.tsx`** 🔴 — première impression. Aujourd'hui : `<Card>`
  Antd brut 400px centré sur la photo Unsplash, titre « Sign In ». Zéro
  branding. C'est l'écran le plus vu d'un nouvel utilisateur → mérite le
  plus de soin (logo Veridian, copy, accent violet).
- **`DashboardPage.tsx`** 🔴 — sélecteur de workspace. Avatar en
  `#1890ff` (bleu Antd, pas Veridian), 4 valeurs d'espacement en dur,
  `<Empty>` brut. Petit écran, vite repassable.
- **`MainLayout.tsx`** 🟡 — fond photo Unsplash + crédit `text-[9px]`.
  Décider si on garde le fond photo ou un fond DA Veridian.
- **`WorkspaceLayout.tsx`** 🔴 — la coquille principale (sidebar +
  header). 11 hex en dur, footer version `9px`, logo Notifuse. C'est le
  cadre vu en permanence → le tokeniser a le meilleur ROI visuel.
- **`CreateWorkspacePage.tsx`** 🟡 — écran `<Result>` mode
  veridian-managed (déjà custom Veridian) — vérifier que le branding est
  raccord une fois la DA posée.
- **`SetupWizard.tsx`** (29 KB) 🟡 — gros wizard, à auditer pour
  cohérence espacements/typo une fois l'échelle posée.

## D10. Plan de remise en ordre suggéré (ordre de ROI)

1. 🟢 **Nettoyage template** (15 min, zéro risque) : `git rm App.css`,
   purger le boilerplate Vite de `index.css` (liens bleus, h1 3.2em,
   `color-scheme dark`, `rgba(255,255,255,.87)`).
2. 🔴 **Charger Inter** (self-host woff2 + `@font-face` + `token.
   fontFamily`).
3. 🔴 **Extraire le thème** dans `console/src/theme/veridian.ts` :
   tokens couleur sémantiques, `borderRadius` unique, échelle typo,
   échelle d'espacement. Importer dans `App.tsx`.
4. 🟡 **Tokeniser le chrome** : remplacer les ~223 hex du chrome (hors
   email/blog) par les tokens — priorité `WorkspaceLayout`, `DashboardPage`,
   `formatters`, `automations/constants`, `analytics`.
5. 🟡 **Unifier la palette** : tuer le mélange Antd-v4 / Tailwind /
   nuances bleues, une seule palette Veridian.
6. 🟡 **Rythme d'espacement** : migrer progressivement les 276 marges
   inline vers `<Space>` / `<Flex gap>` + échelle.

Étapes 1-3 = base du design system, faisables vite et à fort impact
visuel. Étapes 4-6 = chantier progressif, page par page en hot-reload.
