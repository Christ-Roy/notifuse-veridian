# Audit E2E multi-profils Gmail

> **Date** : 2026-08-05
> **Base auditée** : `47be18f0`
> **Remédiation locale** : `c9720604`
> **Verdict courant** : contrat implémenté et hooks locaux verts ; activation multi-profils PROD bloquée jusqu'à la preuve du harnais sink en staging

La matrice ci-dessous reste le constat historique sur `47be18f0`. Elle ne doit
pas être lue comme l'état du HEAD remédié.

## Contrat retenu

- `WorkspaceSettings.veridian_marketing_email_provider_ids[]` contient les intégrations/profils actifs ; `marketing_email_provider_id` reste le fallback legacy.
- Le profil choisi est figé dans `EmailQueueEntry.IntegrationID` avant l'exécution asynchrone.
- `EmailProvider.VeridianProfileDailyCap`, JSON `veridian_profile_daily_cap`, vaut 30 par défaut pour Gmail personnel et ne peut jamais dépasser 50.
- La réservation journalière est atomique et keyée par workspace + jour + integration/profile ID. `message_history` conserve le même ID.
- Rotation entre intégrations, compatible avec le modèle app-password actuel et conçue pour ne pas bloquer un futur OAuth. Cet audit ne valide pas OAuth Gmail comme opérationnel.
- Toute preuve staging passe exclusivement par le `smtp-sink` loopback de l'allocation Nomad. Aucun envoi externe.

## Matrice de preuves

| Axe | Preuve dans `47be18f0` | État | Risque / exigence |
|---|---|---:|---|
| Modèle workspace | `WorkspaceSettings` ne contient que `marketing_email_provider_id`; `GetEmailProviderWithIntegrationID(true)` n'en retourne qu'un | ❌ | Ajouter le tableau, dédupliquer/valider ses IDs, conserver le fallback legacy |
| Orchestrateur broadcast | `orchestrator.go` résout une seule intégration avant le batching | ❌ | Choisir un profil par message avant l'insert queue, puis figer `IntegrationID` |
| Rotation | `VeridianSenderRotator` tourne seulement entre `EmailProvider.Senders` d'une intégration, avec curseur RAM par process | ❌ | Tourner entre intégrations actives sans divergence multi-worker/restart |
| Queue | `EmailQueueEntry.IntegrationID` existe et le worker recharge cette intégration | ✅ partiel | Garder ce gel ; ne jamais refaire la sélection au worker |
| UI Gmail | Le preset crée une intégration avec un sender ; « Use for Marketing » remplace l'unique ID | ❌ | Sélection multiple explicite, cap visible, défaut 30, validation dure 50 |
| Quota profil | V55 réserve seulement `provider_class` et `warmup`, keyés par domaine émetteur | ❌ | Nouveau quota atomique profil keyé par integration ID ; réserver juste avant SMTP |
| Ancien cap sender | `veridian_per_sender_daily_cap` fait un `COUNT`, best-effort et opt-in | ❌ | Ne pas le recycler comme quota profil : granularité et concurrence fausses |
| Historique durable | `message_history` garde sender email et classe, pas integration ID | ❌ | Migration additive + index et remplissage lors de l'upsert |
| Analytics | La queue a `integration_id`, mais la ligne réussie est supprimée | ❌ | Agrégations par profil depuis `message_history`, libellé stable même si profil supprimé |
| Mot de passe app | Le JSON masque le clair mais renvoie le ciphertext stocké afin de permettre le round-trip DB | ❌ P0 | DTO de réponse write-only ; aucun ciphertext dans API, cache, logs ou état UI |
| Endpoint test SMTP | Authentifie un membre du workspace mais ne vérifie pas owner ; accepte provider + destinataire arbitraires | ❌ P0 | Owner obligatoire, rate limit dédié, test par integration ID ou secret neuf non persisté |
| OAuth multi-profils | Cache token = provider + tenant + client ID, sans mailbox ni refresh token | ❌ P0 futur | Isoler par profil/compte sans inclure de secret brut dans clé/logs |
| Fenêtre d'envoi | Le worker utilise la fenêtre du provider rechargé via `IntegrationID` | ✅ partiel | Une entrée figée garde son profil ; test fermé/ouvert robuste, sans fenêtre relative à minuit |
| Réponses | Corrélation Message-ID/contact globale au workspace | ✅ partiel | Conserver profile ID pour attribution et rattacher IMAP au profil si plusieurs inboxes |
| Bounces | Le consumer choisit la première intégration SMTP du workspace | ❌ | Résoudre le profil depuis message/history/envelope, jamais « first SMTP » |
| Unsubscribe/bounce tardif | Recheck final reply/statut seulement pour les automations | ❌ | Recheck suppression final pour tout marketing, après fenêtre/attente et avant SMTP |
| Concurrence | Claim queue et réservation V55 sont atomiques ; curseur rotation est local | ⚠️ | Sélection déterministe ou état DB atomique, quota profil anti-TOCTOU |
| Suppression profil | Une queue figée vers une intégration supprimée devient inexécutable | ❌ | Bloquer suppression ou prévoir drain/pause/remap explicite et audité |
| Logs | Plusieurs logs worker incluent `integration_id`; historique et bounce perdent la provenance | ⚠️ | Structurer profile ID, jamais username/password/token/ciphertext |

## Ce que la base couvre déjà

- La queue possède déjà `IntegrationID` et le worker recharge le provider correspondant : c'est la bonne primitive de gel, il manque la sélection multi-profils en amont.
- Claim multi-worker et ledger V55 fournissent déjà le pattern transactionnel à étendre au quota profil.
- Fenêtres par provider, throttles par intégration, sender email et classe destinataire sont déjà propagés au worker.
- Create/update/delete d'intégration vérifient déjà le rôle owner et les credentials sont chiffrés au repos. Le trou est le DTO sortant partagé avec la persistance et l'endpoint `email.testProvider` moins strict.
- La corrélation reply par Message-ID/contact et les suppressions workspace existent ; il manque la provenance profil et le recheck final pour les broadcasts.

Le preset SMTP Gmail/app-password, son chiffrement et son UI existent dans la base. Ce sous-audit n'a volontairement réalisé aucune authentification Gmail réelle et ne transforme donc pas ces éléments en preuve live.

## Frontière OAuth

Le code contient un refresh-token Google et un transport Gmail API, mais aucune preuve E2E de consentement, scopes, rattachement du bon compte, révocation, rotation ou émission réelle n'est apportée ici. Corriger la clé de cache est nécessaire pour l'isolation multi-profils, jamais suffisant pour déclarer OAuth Gmail opérationnel. Cette déclaration exige un flow OAuth complet et une preuve contrôlée sur comptes de test dans un chantier séparé.

## Suivi après remédiation

1. [Contrat, rotation, quota et historique](2026-08-05-p0-multi-profils-gmail-rotation-quota-history.md) : livré localement, preuve staging restante.
2. [Secrets write-only et test provider](2026-08-05-p0-secrets-profils-email-write-only-test-provider.md) : livré localement, redaction étendue à tout le workspace.
3. [Isolation OAuth multi-profils](2026-08-05-p0-oauth-token-cache-isolation-multi-profils.md) : isolation cache livrée ; flow OAuth Gmail complet toujours futur.
4. [Replies, bounces et unsubscribe](2026-08-05-p1-replies-bounces-unsubscribe-multi-profils.md) : provenance profil livrée ; corrélation multi-inbox et gate marketing universel restent ouverts.

## Gate d'acceptation

`scripts/e2e/gmail-multi-profile-sink.sh` est la preuve staging attendue. Il échoue volontairement avant création de campagne si l'API ne persiste pas le tableau ou le cap exact. Il impose deux providers SMTP loopback distincts, prouve rotation, gel de l'intégration, fenêtre par profil, quota atomique et attribution historique. Il refuse tout host autre que `127.0.0.1`.

Le simple fait que deux cartes Gmail apparaissent dans la console ne constitue pas une preuve : sur cette base, une seule carte est active pour le marketing.
