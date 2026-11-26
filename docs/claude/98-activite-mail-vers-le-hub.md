# 98. Activité Mail vers le Hub, par lots (CONTRAT-HUB §5.25, 26/11/2026)

**But (Robert, 26/11)** : chaque fiche du CRM a un dossier « Mail », une ligne par message (envoyé, remis, ouvert, cliqué, rejeté,
désinscrit, reçu), jamais le corps. Notifuse pousse l'activité au Hub par lots signés ; le Hub filtre (personnes déjà au CRM, une
réponse humaine crée la personne) et range par l'outil CRM `app_evenements_mail`. Le contrat fait foi : `veridian-hub/docs/CONTRAT-HUB.md` §5.25.

Pourquoi pas les webhooks `email.*` de l'upstream : un déclencheur, une livraison et UNE requête par événement. Une campagne de
5 000 mails ferait 15 000 requêtes signées en quelques minutes.

## Chemin

    message_history (envoi, remise, ouverture, clic, rejet, désinscription)  ─┐  déclencheurs V86
    veridian_mail_message (réponse humaine ou auto, rejet reçu)              ─┴─> veridian_activite_outbox (base de l'espace)
                                                                                  │ worker de l'app (capacité « activite »)
                                                   POST <HUB_BASE_URL>/api/evenements/notifuse/lot, Standard Webhooks

- **Code** : `internal/veridian/activite/` (événement, lots, envoi, worker, métriques, clics), schéma dans `activitesql/` (paquet
  feuille), branchement `internal/app/veridian_mod_activite.go`, filtre des clics dans `veridian_wiring.go`. Migration V86 (workspace).
- **Déclencheurs** : une ligne par transition NULL → date (`sent_at`, `delivered_at`, `opened_at`, `clicked_at`, `bounced_at`,
  `unsubscribed_at`), canal `email` seul ; un reçu `reply_human`, `reply_auto`, `bounce_hard`, `bounce_soft`, hors copie Sieve et hors second
  rangement du même Message-ID. **Rien n'entre si l'espace n'a pas l'abonnement du Hub** (`veridianhubevenements`, actif). Unicité
  `(source, ref_id, event)` : une ouverture et un clic par message, dédoublonnés à l'entrée. Jamais bloquants (erreur avalée, `WARNING`).
- **La file ne porte que des références** ; objet, adresses, nom, fil et Message-ID sont lus à l'envoi (rendu rangé dans la boîte
  unifiée, sinon `message_history` et `VeridianRFCMessageID`). Le corps n'est jamais lu. Les clés d'un événement sont figées par test.
- **Natures** : envoi d'une campagne `campaign`, d'une automation `sequence`, sinon `transactional` (mails écrits depuis la boîte
  compris). Un `message.bounced` ou `unsubscribed` garde la nature du message touché ; un rejet REÇU (rapport) est `message.received`,
  nature `bounce`, `bounce_type` hard ou soft. `other` et `dsn` ne partent pas.
- **Clic** : l'hôte seul (`HostOf` : ni chemin, ni paramètres, ni port, ni identifiants). Les routes de suivi de l'upstream (`/r/`,
  `/visit`) ne sont pas touchées : `activite.WrapClicks` lit le lien comme elles, après leur redirection, et pose l'hôte sur la ligne
  `message.clicked` encore en file. Un clic écarté par l'anti-bot n'a pas de ligne.

## Worker (pas une tâche planifiée)

Passage toutes les 10 s. Un espace part dès 200 événements prêts, sinon toutes les 30 s ; un événement n'est prêt que 10 s après son
entrée (capture de l'envoyé, hôte du clic). Lots de 200 événements ET 1 Mo au plus (`Split`), 25 lots par espace et par passage.
`webhook-id` = `lot_id`, horodatage pris à l'envoi, secret de l'abonnement de l'espace (jamais le secret global HMAC). L'adresse est
`HubEventsURL(HUB_BASE_URL)` + `/lot` ; un abonnement qui pointe ailleurs (modifié à l'écran) n'est jamais suivi.

| Réponse | Effet |
|---|---|
| 2xx | lignes closes `accepte` ; acceptés, doublons et ignorés comptés |
| 400, 409, 413 | lot abandonné (`refuse`), journal `error`, jamais rejoué |
| 401, 403 | lignes gardées, journal `error`, pause 30 s à 15 min ; un nouveau secret (abonnement resynchronisé) relance aussitôt |
| 429, 5xx, 404, réseau | lignes gardées, attente 30 s, 1, 2, 4… 30 min, `Retry-After` respecté ; au-delà d'une heure sans succès, `error` toutes les 10 min |

**Bornes** (toutes les 10 min par espace) : un événement de plus de 7 jours est clos `perime` (le Hub l'ignorerait) ; au-delà de
100 000 en attente, les plus vieux sont clos `debordement` (journal `error`) ; les lignes closes partent après 2 jours, 50 000 gardées au plus.

## Réglages (job `deploy/notifuse*.nomad.hcl`)

| Variable | Rôle |
|---|---|
| `VERIDIAN_ACTIVITE_ENABLED` | `true` = envoi. Éteint : la file se remplit et se borne, rien ne part. **Éteint en prod et staging tant que le Hub ne sert pas `/lot`.** |
| `VERIDIAN_ACTIVITE_METRICS_PORT` | écouteur des métriques (9464, port Nomad `activite`, tailnet seul, service `notifuse-activite`) |
| `VERIDIAN_ACTIVITE_ALERT_AFTER` | retard qui déclenche l'alerte (défaut 15 min) |

## Métriques et alerte

`GET /metrics` sur l'écouteur à part. **Pourquoi un port de plus** : le 8081 est servi par Traefik au public, et l'exportateur
OpenCensus de l'upstream (`TRACING_PROMETHEUS_PORT`, déjà 9464) n'existe qu'avec `TRACING_ENABLED`, qui allume aussi les traces et les
vues HTTP par route (séries sans fin, palier gratuit de Grafana Cloud). Neuf séries, aucune étiquette par espace :
`veridian_activite_enabled`, `_pending_events`, `_oldest_pending_seconds`, `_workspaces_pending`, `_workspaces_failing`, `_lag_alert`,
`_last_pass_timestamp_seconds`, `_events_total{resultat}`, `_lots_total{resultat}`. Le détail d'un espace est dans les journaux
(`workspace_id`), jamais une adresse ni un secret.
Dans l'app, sans Prometheus : `error` « retard de la file vers le Hub » toutes les 10 min tant que l'envoi est allumé et que le plus vieil
événement attend plus que le seuil, puis « retard résorbé ».
Le scrape et la règle d'alerte vivent dans `nomad-veridian/jobs/infra/mon-prometheus.nomad.hcl` (session infra).

## Coût mesuré (26/11/2026, PostgreSQL 17, `TestMesureCoutDeclencheurs`, `VERIDIAN_ACTIVITE_MESURE=1`)

| 10 000 envois | sans déclencheur | déclencheur, sans abonnement | déclencheur, abonnement |
|---|---|---|---|
| une seule instruction | 79 ms | 539 ms | 1 972 ms |
| ouverture des 10 000 | 110 ms | 563 ms | 2 264 ms |
| une instruction par envoi (comme en vrai, chronométré dans la base) | 203 ms | 867 ms | 2 428 ms |

Soit, par envoi réel : **+0,07 ms sans abonnement, +0,22 ms avec**, contre une session SMTP de plusieurs centaines de ms. Pas nul : la
fonction plpgsql est appelée même sans abonnement. Essayé et écarté : un `WHEN` qui appelle une fonction SQL (2,2 s, pire). Pour un vrai
zéro, il faudrait poser ou retirer le déclencheur avec l'abonnement (DDL sur la table la plus écrite) : non fait, le gain ne le vaut pas.
Pour mémoire, l'upstream porte déjà deux déclencheurs plpgsql par ligne sur cette table.

## Épreuves

Tests `internal/veridian/activite` (PostgreSQL par `testpg`), chacun vu échouer sur une mutation le 26/11 : lot de 201 coupé, 1 Mo
respecté, 400 non rejoué, 5xx rejoué après `Retry-After` puis attente croissante, 401 rejoué au nouveau secret, doublon d'ouverture
écarté, URL réduite à l'hôte, aucun corps, abonnement éteint ou d'un client sans effet, adresse détournée non suivie, bornes tenues.
Signature : vecteur officiel Standard Webhooks, bibliothèque de référence et portage de `veridian-hub/lib/evenements/signature.ts` ; la
routine du Hub elle-même accepte le vecteur (exécutée par `tsx` le 26/11).
