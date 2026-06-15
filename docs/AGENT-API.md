# Notifuse — Guide API pour agents (AI-first)

> But : piloter Notifuse de bout en bout **par API**, sans la console.
> Ce guide est un livre de recettes. La référence complète des schémas est dans
> `openapi/openapi.yaml` (bundle `openapi.json`, `make openapi-bundle`).

Toutes les routes sont **RPC-style** : `POST /api/<resource>.<verb>` (quelques
lectures sont en `GET` avec query params). Réponses et corps en JSON.

---

## 1. Authentification

Une seule mécanique : header `Authorization: Bearer <token>`. Deux types de
token, tous deux signés et validés par le même middleware (`RequireAuth`) :

| Type | Quand l'utiliser | Comment l'obtenir |
|---|---|---|
| **JWT user** | Sessions console, agents qui agissent « comme un humain owner » | Login console (magic link) |
| **JWT api_key** | Intégrations machine | Émis par la console (Settings → API keys) ; rattaché à **un seul** workspace |

Toute requête porte aussi le `workspace_id` **dans le body** (ou la query pour
les GET) — l'auth donne l'identité, `workspace_id` cible le tenant.

### Permissions (⚠️ à connaître avant d'écrire un agent)

- La plupart des écritures exigent une permission de ressource
  (`automations:write`, `contacts:read`, …) vérifiée côté service.
- **`workspaces.createIntegration` et `workspaces.updateIntegration` sont
  OWNER-ONLY** : seul un membre de rôle `owner` peut les appeler (un api_key ou
  un membre non-owner reçoit `401 user is not an owner of the workspace`). C'est
  par là que passe TOUTE la config d'envoi (SMTP, IMAP, rates/caps/tracking cold).
  → un agent qui configure le cold doit utiliser un **JWT d'un owner**.

Codes d'erreur : `400` validation, `401` non authentifié / non-owner, `403`
permission de ressource manquante, `404`/`500` selon le cas. Corps :
`{"error":"<message>"}`.

---

## 2. Envoyer un email transactionnel avec un template + variables

C'est « appliquer un template par API ». Le template est rendu (Liquid) avec
`data`, puis envoyé au contact.

```bash
curl -X POST https://notifuse.app.veridian.site/api/transactional.send \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{
    "workspace_id": "ws_demo",
    "notification": {
      "id": "welcome_email",            // ID de la transactional notification
      "contact": { "email": "alice@example.com" },
      "channels": ["email"],
      "data": { "user_name": "Alice", "activation_link": "https://…" },
      "email_options": {
        "subject": "Bienvenue, {{ user_name }} !"   // override Liquid optionnel
      }
    }
  }'
```

Réponse : `{"message_id":"…","success":true}`.

**Tester un template** (rendu + envoi à une seule adresse, sans notification
configurée) : `POST /api/transactional.testTemplate` avec `template_id`,
`integration_id`, `sender_id`, `recipient_email` (+ `email_options` optionnel).

**Juste rendre un template sans l'envoyer** (preview, debug) :
`POST /api/templates.compile` → renvoie le HTML compilé.

### 2.1 Gérer les transactional notifications (CRUD)

Une **transactional notification** = un binding réutilisable (id + nom +
templates par canal + tracking) que `transactional.send` déclenche ensuite via
son `id`. Tout est pilotable par API (permission de ressource côté service).

**Créer** (`POST /api/transactional.create`) → `201 {"notification": {…}}` :

```bash
curl -X POST …/api/transactional.create -H "Authorization: Bearer $TOKEN" -d '{
  "workspace_id": "ws_demo",
  "notification": {
    "id": "welcome_email",                       // requis, sert d'id de trigger
    "name": "Welcome email",                     // requis
    "description": "Sent right after signup.",
    "channels": {                                // requis, ≥1 canal (email seul supporté)
      "email": { "template_id": "tpl_welcome" }
    },
    "tracking_settings": { "enable_tracking": true },
    "metadata": { "category": "lifecycle" }
  }
}'
```

**Mettre à jour** (`POST /api/transactional.update`) → `200 {"notification": {…}}`.
Au moins un champ parmi `name`/`description`/`channels`/`metadata` requis dans
`updates` :

```bash
curl -X POST …/api/transactional.update -H "Authorization: Bearer $TOKEN" -d '{
  "workspace_id": "ws_demo",
  "id": "welcome_email",
  "updates": { "channels": { "email": { "template_id": "tpl_welcome_v2" } } }
}'
```

**Supprimer** (soft-delete) (`POST /api/transactional.delete`) →
`200 {"success": true}` :

```bash
curl -X POST …/api/transactional.delete -H "Authorization: Bearer $TOKEN" \
  -d '{"workspace_id":"ws_demo","id":"welcome_email"}'
```

Erreurs : `400` (id/name/channels manquants ou `at least one field must be
updated`, ou `invalid template`), `404 Notification not found` (update/delete).
**Lecture** : `GET /api/transactional.list?workspace_id=…` et
`GET /api/transactional.get?workspace_id=…&id=…`.

---

## 3. Créer une séquence (automation) et y enrôler des contacts

Une automation = un workflow de **nodes** (étapes). Cycle de vie :
`draft` → (activate) → `live` → (pause) → `paused`.

### 3.1 Créer le workflow (`draft`)

```bash
curl -X POST …/api/automations.create -H "Authorization: Bearer $TOKEN" -d '{
  "workspace_id": "ws_demo",
  "automation": {
    "id": "7c3e2b1a-1234-4abc-9def-0123456789ab",   // UUID, max 36 chars
    "name": "Cold 2-step",
    "status": "draft",
    "list_id": "list_main",                          // requis si nodes email
    "trigger": { "event_kind": "contact.created", "frequency": "once" },
    "root_node_id": "n_email1",
    "nodes": [
      { "id": "n_email1", "type": "email",
        "config": { "template_id": "tpl_step1" },
        "next_node_id": "n_delay" },
      { "id": "n_delay", "type": "delay",
        "config": { "duration": 3, "unit": "days" },
        "next_node_id": "n_email2" },
      { "id": "n_email2", "type": "email",
        "config": { "template_id": "tpl_step2" } }
    ]
  }
}'
```

Types de node (`config` varie selon `type`) : `email` (`template_id`),
`delay` (`duration`+`unit`), `branch`, `filter`, `add_to_list`,
`remove_from_list`, `ab_test`, `webhook` (`url`), `list_status_branch`.
Schémas détaillés : `components/schemas/automation.yaml`.

`trigger.event_kind` ∈ {`contact.created`, `contact.updated`, `list.subscribed`,
`segment.joined`, `email.opened`, `custom_event`, …}. `frequency` = `once`
(1ère occurrence, dédupliquée) ou `every_time`.

### 3.2 Activer (installe le trigger, status → `live`)

```bash
curl -X POST …/api/automations.activate -H "Authorization: Bearer $TOKEN" \
  -d '{"workspace_id":"ws_demo","automation_id":"7c3e2b1a-…"}'
```

### 3.3 Enrôler des contacts **par API** (le maillon AI-first)

Une fois l'automation `live`, pousser des contacts au node d'entrée SANS
attendre que l'événement trigger se produise :

```bash
curl -X POST …/api/automations.enroll -H "Authorization: Bearer $TOKEN" -d '{
  "workspace_id": "ws_demo",
  "automation_id": "7c3e2b1a-…",
  "contact_emails": ["alice@example.com", "bob@acme.io"]
}'
```

Réponse :
```json
{ "enrolled": 1, "skipped": 1, "failed": 0,
  "results": [
    {"email":"alice@example.com","status":"enrolled"},
    {"email":"bob@acme.io","status":"already_active"}
  ] }
```

- **Idempotent** : un contact déjà `active` dans l'automation → `already_active`
  (pas de doublon). Un contact qui a `completed`/`exited` peut être ré-enrôlé.
- L'automation **doit être `live`** (sinon `400 automation is not live`).
- Best-effort par contact : un email en erreur n'interrompt pas le lot.
- Réutilise le chemin d'enrôlement natif (fonction SQL `automation_enroll_contact`,
  la même que le trigger) → contact_automation au root node, stat `enrolled++`,
  log d'exécution, timeline `automation.start`. Permission `automations:write`.

> Note cold outreach : pour une cadence cold, l'**exit automatique** sur réponse
> (stop-on-reply) et sur bounce est déjà câblé côté worker (Lots 2/3) — pas
> besoin de le piloter par API. On enrôle, le moteur sort les contacts qui
> répondent ou bouncent.

### 3.4 Inspecter / piloter

- `GET /api/automations.list?workspace_id=ws_demo&status=live`
- `GET /api/automations.get?workspace_id=ws_demo&id=7c3e2b1a-…`
- `GET /api/automations.nodeExecutions?workspace_id=ws_demo&automation_id=…&email=alice@example.com`
  → état du contact + log node par node (debug d'une cadence).
- `POST /api/automations.pause` / `POST /api/automations.update` /
  `POST /api/automations.delete` (même body `{workspace_id, automation_id}`
  pour pause/delete).

---

## 4. Configurer le cold outreach par API

> Toute la config cold se pose **par intégration d'envoi (`EmailProvider`)** ou
> **au niveau workspace settings**. Cascade à l'envoi (du plus spécifique au plus
> général) : `broadcast.metadata` → `EmailProvider` (infra) → `workspace
> settings`. Le premier niveau non vide gagne.
>
> ⚠️ `createIntegration`/`updateIntegration` sont **OWNER-ONLY** (cf. §1).

### 4.1 Dimensionner : combien de contacts par classe de provider ?

```bash
curl -X POST …/api/veridian/contacts.providerBreakdown -H "Authorization: Bearer $TOKEN" \
  -d '{"workspace_id":"ws_demo","list_id":"list_main"}'
# → {"breakdown":{"google":120,"microsoft":88,"yahoo_aol":12,"freemail_fr":30,"corporate":250},"total":500}
```

Permission `contacts:read`. Sert à choisir les débits/plafonds par classe.

### 4.2 Rates + caps + tracking domain PAR INFRA (sur l'EmailProvider)

`updateIntegration` renvoie le provider **complet** (sinon on écrase
senders/rate). Les 4 champs cold vivent sur `provider` :

```bash
curl -X POST …/api/workspaces.updateIntegration -H "Authorization: Bearer $OWNER_TOKEN" -d '{
  "workspace_id": "ws_demo",
  "id": "<integration_id>",
  "name": "Cold relay agences-veridian.fr",
  "type": "email",
  "provider": {
    "kind": "smtp",
    "senders": [ … ],          // conserver l'existant
    "smtp": { … },
    "veridian_provider_class_rates":      { "google": 0.5, "microsoft": 0.5, "freemail_fr": 5 },
    "veridian_provider_class_daily_cap":  { "google": 100, "microsoft": 100 },
    "veridian_per_recipient_daily_cap":   1,
    "veridian_tracking_domain":           "track.agences-veridian.fr"
  }
}'
```

- `*_rates` : emails/**minute** par classe (`0.5` = 1 mail / 2 min). Vide = pas de throttle.
- `*_daily_cap` : envois/**jour** par classe ; `per_recipient_daily_cap` = max/jour vers une même adresse. `0`/absent = illimité.
- `tracking_domain` : sous-domaine de tracking aligné au domaine d'envoi (réputation).

Mêmes clés posables au **niveau workspace** via `POST /api/workspaces.update`
(champs `veridian_provider_class_rates`, `veridian_provider_class_daily_cap`,
`veridian_per_recipient_daily_cap`, `veridian_open_pixel_by_class`).

### 4.3 Pixel d'ouverture par classe

`veridian_open_pixel_by_class` (map classe→bool) sur `workspaces.update` :
typiquement pixel OFF sur `google`/`microsoft`, ON sur petits providers. Le
tracking de clics reste actif partout.

### 4.4 Boîte IMAP de retour (bounce-loop + stop-on-reply)

Crée une intégration `type:"imap"` (OWNER-ONLY). Le `password` clair n'est
jamais re-renvoyé en lecture ; à l'update, l'omettre conserve l'existant.

```bash
curl -X POST …/api/workspaces.createIntegration -H "Authorization: Bearer $OWNER_TOKEN" -d '{
  "workspace_id": "ws_demo",
  "name": "Return inbox",
  "type": "imap",
  "imap_settings": {
    "host": "imap.example.com", "port": 993, "use_tls": true,
    "username": "bounce@agences-veridian.fr", "password": "…",
    "folder": "INBOX", "polling_interval_seconds": 60
  }
}'
```

Notifuse poll cette boîte, détecte les NDR (→ suppression contact bounced) et
les réponses de prospects (→ exit de la cadence). Aucun script externe.

---

## 5. Briques de base (contacts, listes, broadcasts)

- **Contacts** : `contacts.upsert`, `contacts.list`, `contacts.count`,
  `contacts.getByEmail`, `contacts.import` (batch), `contacts.delete`.
- **Listes** : `contactLists.updateStatus`, `lists.subscribe`.
- **Broadcasts** (newsletter one-shot ≠ automation) : `broadcasts.create`,
  `.schedule`, `.pause`, `.resume`, `.cancel`, `.sendToIndividual`, … Les rates/
  caps cold se posent aussi sur `broadcast.metadata`.
- **Templates** : `templates.create/update/list/get/compile/delete`.

Schémas et exemples complets : `openapi/openapi.yaml`.

---

## 6. Ce qui n'est PAS (encore) pilotable par API

- **Génération/rotation d'une api_key** : se fait dans la console (Settings →
  API keys), pas d'endpoint public. Un agent doit recevoir un token déjà émis.
- **Désinscription cold** : volontairement absente (choix produit cold outreach).
