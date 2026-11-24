# 88 — L'organisation vient du Hub (VER-252, 25/11/2026) et l'abonnement aux réponses (F3, VER-247)

Robert, 24/11 : « une seule configuration sur le Hub […] pareil pour Notifuse ». Code : `internal/veridian/organisation/`, migration V79.

## `POST /api/hub/organisations/sync` (HMAC Hub)
`{hub_tenant_id, version, nom, type, marque {nom_affiche, logo_url, logo_sombre_url, favicon_url, couleur_primaire, couleur_accent}, modules, membres
[{hub_user_id, email, role}], reglages}`. Champs inconnus ignorés. 200 `{applied, reason?, workspace_id, version, membres_ajoutes, membres_refuses}`.
- **Version monotone par espace** : plus ancienne = ignorée (`stale`, rien n'est fait) ; même version = `unchanged`, rien de réécrit, les membres manquants
  sont rattrapés. 404 `espace_non_relie` ; 400 `invalid_payload`.
- Appliqué : nom de l'espace (`nom_affiche`, sinon `nom`) et `settings.logo_url`, par une écriture ciblée. **Une valeur absente ne vide rien.**
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
Contrat de l'événement : `docs/veridian/BOITE-UNIFIEE.md`. Lien d'une conversation : `/console/workspace/<ws>/inbox?message=<data.message_id>&onglet=conversation&nature=toutes`.

Épreuve : `scripts/e2e/organisation-du-hub.py` (staging par défaut, console lue dans Chromium ; `--prod` : couleur seule).
CLI opérateur : `hub:organisation-sync`, `hub:organisation-version`, `hub:abonnement`, `organisation:get`.
