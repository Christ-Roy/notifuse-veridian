# 85 — Réservoirs : import massif, activation par lots, outils MCP (24/11/2026)

Demande d'Arnaud Capitaine relayée par Robert : alimenter la prospection depuis ChatGPT (3 795 prospects), sans geste manuel et
sans toucher à la limite de 50 mails par jour. Code : `internal/veridian/reservoir/`, câblé par `internal/app/veridian_mod_reservoir.go`.
Migration V76 (lots nommés). Outils MCP : `internal/veridian/mcp/tools_reservoir.go`.

## Les trois routes
| Route | CLI | MCP | Fait |
|---|---|---|---|
| `POST /api/veridian/contacts.intake` | `contacts:intake` | `contacts_import` | 1000 contacts par appel, dédoublonnés, exclusions, listes, `dry_run` |
| `GET /api/veridian/reservoir.summary` | `reservoir:status` | `reservoirs_status` | par liste : activables, et pourquoi pas les autres |
| `POST /api/veridian/reservoir.activate` | `reservoir:activate` | `reservoir_activate` | un lot entre dans une séquence active |

## Règles
- **Dédoublonnage** : adresse toujours ; SIREN (`custom_json_N.siren` ou `external_id`) et domaine par défaut. Une messagerie ou une
  plateforme partagée (classes d'envoi connues + `sharedExtra`) n'est jamais une clé. Dans un lot, le premier gagne.
- **Exclusions valables dans tout l'espace** : désinscrit, plainte, rebond dur (`contact_lists` ou `message_history`). Un rebond
  temporaire ou de politique ne bloque pas. Une exclusion s'applique même sans dédoublonnage.
- **Fiche existante intacte par défaut** (`existing=attach`) ; `update` complète `custom_json_1` sans l'écraser et garde `created_at`.
- **Un import ne démarre jamais une séquence sans accord** : liste qui déclenche une séquence active, ou séquence « contact créé » :
  409 `would_trigger`, rien n'est écrit, sauf `allow_triggers`. Le déclencheur de gbc (`list.subscribed`) rend ce cas ordinaire.
- **Activation** : écarte bloqués, ayant répondu, déjà contactés, déjà en séquence. Pose d'abord la liste de la séquence (la garde
  d'envoi jette `contact_not_on_list`), puis n'inscrit directement que ceux que le déclencheur n'a pas déjà inscrits. Refuse si la
  liste démarre aussi une AUTRE séquence. Meilleur score d'abord (`custom_number_1`).
- **Rejouable** (le Hub coupe un programme à 45 s) : un import rejoué ne crée, ne modifie ni n'inscrit rien de plus ; une activation
  par liste ou segment exige un `batch_id` pour l'être (sinon le rejeu ferait entrer le lot SUIVANT). Choix et inscription sous un
  même verrou : lots simultanés sans doublon.
- **Aucun outil MCP ne relève un plafond** ni ne touche profils, intégrations, réglages : test `TestCatalog_AucunOutilNeToucheAuxPlafonds`.
- Tout outil qui peut faire partir un mail est `destructiveHint` (`contacts_import`, `contacts_upsert` et `segment_rebuild` compris : ils peuvent démarrer une séquence).

## Appel depuis un programme (code mode du Hub)
En-tête `X-Veridian-MCP-Appel: programme` : réponse entière jusqu'à 1 Mo et `structuredContent` (au-delà, erreur franche, jamais un
JSON coupé). Il n'ouvre aucun droit. Corps accepté par `/api/mcp` : 2 Mo. Sans lui : 30 000 caractères, et le Hub coupe à 16 000.
`automations_list`, `segments_list` et `sending_quota` sont réduits (les réponses brutes de gbc dépassaient la coupe du Hub).
