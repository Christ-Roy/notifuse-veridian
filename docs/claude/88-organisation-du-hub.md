# 88 — L'organisation vient du Hub (VER-252, 25/11/2026) et l'abonnement aux réponses (F3, VER-247)

Robert, 24/11 : « une seule configuration sur le Hub […] pareil pour Notifuse ». Code : `internal/veridian/organisation/`, migration V79.

## `POST /api/hub/organisations/sync` (HMAC Hub)
`{hub_tenant_id, version, nom, type, marque {nom_affiche, logo_url, logo_sombre_url, favicon_url, couleur_primaire, couleur_accent}, modules, membres
[{hub_user_id, email, role}], reglages}`. Champs inconnus ignorés. 200 `{applied, reason?, workspace_id, version, membres_ajoutes, membres_refuses}`.
- **Version monotone par espace** : plus ancienne = ignorée (`stale`, rien n'est fait) ; même version = `unchanged`, rien de réécrit, les membres manquants
  sont rattrapés. 404 `espace_non_relie` ; 400 `invalid_payload`.
- Appliqué : nom de l'espace (`nom_affiche` seul, et jamais le nom fabriqué « … 's Workspace » ; `nom` = nom du compte au Hub, ignoré) et
  `settings.logo_url`, par une écriture ciblée. **Une valeur absente ne vide rien.** Intacts, tenu par test : identifiant, profils, adresses d'envoi, membres.
- **Ce qu'un client voit changer** : le nom dans la console (sélecteur, réglages, suppression) et, en lecture seule, nom et logo des réglages ; le logo du centre
  de préférences PUBLIC (et son favicon) ; couleur et favicon de la console ; « X via <nom> » sur les réponses retransmises par le transport de service ; alerte du
  fusible de campagne ; titre du blog s'il n'en a pas (0 blog actif). **Ne change pas** : adresses et noms d'expéditeur, contenu des mails, URL, API, SMTP, listes.
- Marque entière gardée dans `veridian_organisation`, lue par la console (`GET /api/veridian/organisation`, membres) : couleur, favicon. Nom et logo verrouillés
  dans les réglages (`managed`) seulement si le Hub a poussé un nom.
- Adresses en https sans identifiants, couleurs `#rrggbb` : ces valeurs finissent dans des pages.
- Membres : additif, par `sync-member` (un seul chemin d'ajout) ; jamais un retrait, un changement de rôle ni un propriétaire.
- `type`, `modules`, `reglages` : gardés, non appliqués. **Jamais un réglage d'envoi par cette voie.**
- **Relecture** : `GET /api/hub/organisations/sync?hub_tenant_id=` (HMAC Hub) rend `{workspace_id, version, name}` ; `version` 0 = rien reçu. Le Hub ne
  dit « appliqué » qu'après l'avoir relue (`veridian-hub/lib/organisations/emission.ts`) ; Notifuse reçoit une fois dans `MODULES` du Hub.

## `POST /api/hub/evenements/abonnement` (HMAC Hub)
`{hub_tenant_id, actif}`. Allumé : l'abonnement `message.received` vers `<HUB_BASE_URL>/api/evenements/notifuse` existe et est actif ; rend
`{workspace_id, subscription_id, url, events, secret}` (Standard Webhooks, `no-store`, seule sortie du secret vers le Hub). Rejouable, remis d'équerre
s'il a été modifié à l'écran. Éteint : retiré. L'adresse vient de la configuration, jamais de l'appelant. Les abonnements du client ne sont pas touchés.
Épreuve d'émission (staging) : `scripts/e2e/reponse-recue-staging.py <espace> --email <contact>`. Éteindre puis rallumer crée un NOUVEAU secret ; rejouer allumé rend le même.
Contrat de l'événement : `docs/veridian/BOITE-UNIFIEE.md`. Lien d'une conversation : `/console/workspace/<ws>/inbox?message=<data.message_id>&onglet=conversation&nature=toutes`.

Épreuve : `scripts/e2e/organisation-du-hub.py` (staging par défaut, console lue dans Chromium ; `--prod` : couleur seule).
CLI opérateur : `hub:organisation-sync`, `hub:organisation-version`, `hub:abonnement`, `organisation:get`.

## `POST /api/hub/tenants/link` (HMAC Hub) : le lien espace → tenant (26/11/2026)

Le lien `veridian_plan.hub_tenant_id` est ce par quoi un jeton d'action (`/api/veridian/actions/*`) et une organisation
poussée retrouvent l'espace : sans lui, 409 `espace_non_relie`. Incident du 26/11 (CRM, cas 48, staging) : le CRM a
appelé à 01:04:21Z, le lien n'est arrivé qu'à 01:05:02Z, par un `update-plan` manuel. Le `hub link` du Hub ne prévenait
pas Notifuse, et `update-plan` exige un `plan` qu'il réécrit (quota, événement `tenant.plan_changed`, 409 sur plan offert).

- Corps `{tenant_id, hub_tenant_id, replace?}`. Idempotent : 200 `{tenant_id, hub_tenant_id, changed}`.
- 400 `invalid_payload`, 404 `tenant_not_found` (espace sans plan), 409 `hub_tenant_conflict` avec `details`
  (`linked_hub_tenant_id` : l'espace porte un autre tenant ; `linked_tenant_id` : le tenant est porté par un autre espace vivant).
- `replace: true` re-pointe seulement CET espace. Un tenant n'est jamais posé sur deux espaces vivants (sinon la résolution
  prend le premier par ordre alphabétique). Un espace supprimé en douceur ne retient pas son tenant.
- Écriture unique : `tenantlink.Store.Link` (transaction, verrou consultatif sur le tenant). `update-plan` y passe aussi :
  un conflit n'y est pas écrit mais ne bloque pas le plan, il revient dans `hub_tenant_conflict` de la réponse et en log `error`.
- Le Hub appelle la route à chaque création, rejeu et `hub link`, et attend le 200 avant de délivrer un jeton d'action.
- CLI : `notifuse-admin hub:tenant-link`. Mesure prod du 26/11 : 23 espaces vivants, 16 reliés, 0 doublon.

### Le ménage d'essai épargne les espaces reliés (26/11/2026)

À 01:08:29Z, l'étape « Cleanup test tenants » de notre CI (38 préfixes, dont `e2e`) a supprimé `e2ecrmmwwtkl6c`,
`e2edelmwwtid0c` et `e2essosadm4236`, espaces d'essai du Hub et du CRM, en plein essai. Ce qui ressemblait à un import
qui « résout l'espace autrement » (409 à 01:08:43Z) était cet espace supprimé : toutes les actions passent par la même
résolution (`actions.SQLStore.Resolve`). Depuis, `wipe-test-tenants` par préfixe saute tout espace relié à un tenant du
Hub (`hub_linked` dans la réponse) ; nommé dans `tenant_ids`, il part comme avant. Le journal du ménage nomme désormais
le préfixe et les espaces supprimés (`wiped_ids`).
