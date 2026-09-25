# V56 — profils d'envoi multi-intégrations et quota exact (2026-08-05)

- `WorkspaceSettings.veridian_marketing_email_provider_ids` configure un pool
  ordonné d'intégrations email complètes. La queue choisit par hash stable
  workspace + classe destinataire + message : allocation et restart convergent
  vers le même profil, puis l'`integration_id` est figé dans l'entrée.
- `EmailProvider.veridian_profile_daily_cap` est un plafond total par profil,
  toutes classes et senders confondus. Gmail prend 30/j par défaut et refuse
  toute valeur supérieure à 50.
- Le worker réserve atomiquement le quota `profile` dans le ledger V55 avec
  l'IntegrationID exact. `message_history.veridian_profile_id` attribue les
  acceptations au profil exact; migration V56 additive et indexée.
- `GET|POST /api/veridian/emailProfiles.usage` renvoie `used`/`remaining` depuis
  le compteur atomique (autorité quota), plus `accepted_used` et
  `accepted_by_provider_class` depuis l'historique. Une issue SMTP ambiguë peut
  donc produire `used > accepted_used` sans mentir sur la capacité restante.
  Le jour de politique est explicitement UTC et les classes inconnues restent
  sous la clé `unclassified`, jamais reclassées artificiellement `corporate`.
- Le cache OAuth Google inclut un digest opaque du refresh token : deux comptes
  partageant le même client OAuth ne partagent jamais un access token, même si
  leur username est vide. Aucun secret n'apparaît dans la clé ou les logs.
- Lifecycle fail-safe : une entrée déjà en queue n'est jamais reroutée si son
  profil atteint son cap (elle attend le jour suivant). Une suppression prend
  un verrou partagé sur `email_queue` et refuse en `409` toute intégration encore
  référencée par une entrée `pending` ou `processing`; les enqueue concurrents
  prennent le verrou conflictuel puis revalident l'intégration avant insertion.

