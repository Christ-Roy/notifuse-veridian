# P0 - Contrat multi-profils Gmail, rotation, quota atomique et historique

> **Sévérité** : 🔴 P0, surface d'envoi
> **Bloque** : activation de plusieurs profils Gmail personnels

## Problème

Le modèle actuel n'active qu'une intégration marketing. La rotation existante concerne les senders internes d'une intégration, pas plusieurs comptes Gmail. Le cap historique par sender est un `COUNT` non atomique et `message_history` ne conserve pas l'intégration.

Sous plusieurs workers, un curseur RAM par allocation et un contrôle `COUNT` peuvent dépasser un plafond. Après redémarrage, la distribution repart de zéro. Après succès, la suppression de la queue efface la seule attribution d'intégration durable.

## Briques à conserver

- `EmailQueueEntry.IntegrationID` et le rechargement du provider par le worker.
- Claim queue multi-worker et transaction de réservation V55.
- Fenêtre, throttle, sender selection et circuit breaker déjà résolus au niveau provider.
- Fallback legacy `marketing_email_provider_id` pour une migration progressive.

Le ticket étend ces primitives ; il ne demande pas de réécrire le pipeline d'envoi.

## Implémentation exigée

1. Ajouter `WorkspaceSettings.VeridianMarketingEmailProviderIDs []string` / `veridian_marketing_email_provider_ids`.
   - IDs uniques, non vides, présents dans le workspace, intégrations email utilisables.
   - tableau non vide prioritaire ; fallback vers `marketing_email_provider_id` pour les workspaces legacy.
   - create/update/delete, API, types console et UI cohérents.
2. Choisir l'intégration au moment de construire chaque entrée de queue.
   - Répartition juste entre profils éligibles.
   - La sélection résiste aux allocations concurrentes et aux redémarrages : hash stable/rendezvous ou curseur DB atomique, pas un compteur process-local.
   - Écarter un profil fermé, désactivé ou déjà au quota sans concentrer toute la charge sur le suivant.
   - Écrire définitivement l'ID choisi dans `EmailQueueEntry.IntegrationID` ; le worker ne reroute jamais silencieusement.
   - Garder l'algorithme indépendant du mode d'authentification. Cela prépare OAuth mais ne le certifie pas opérationnel.
3. Ajouter `EmailProvider.VeridianProfileDailyCap int` / `veridian_profile_daily_cap`.
   - Gmail personnel : défaut effectif et UI = 30/jour.
   - validation backend et UI : `1..50`; aucune valeur, métadonnée ou API ne peut contourner le maximum 50.
   - autres providers : politique explicite, pas de faux défaut Gmail appliqué par détection fragile du nom.
4. Étendre le ledger atomique V55.
   - quota kind `profile` ; clé incluant `integration_id`, indépendamment du domaine/sender email.
   - réservation transactionnelle au dernier gate DB avant SMTP.
   - release seulement si aucune acceptation SMTP ambiguë n'a eu lieu, comme V55.
   - migrations additive et rollback documenté ; test de concurrence PostgreSQL réel avec plusieurs goroutines/connexions.
5. Ajouter `message_history.integration_id` (ou nom de colonne de profil explicitement versionné), rempli à chaque upsert.
   - index pour les agrégations workspace + integration + sent_at.
   - dashboard/history indiquent le profil ; conserver un snapshot d'adresse/libellé si la suppression d'intégration doit rester possible.
6. Cycle de vie.
   - Interdire la suppression d'un profil référencé par des entrées pending/processing, ou proposer une opération explicite pause + drain/remap.
   - Les changements de fenêtre/cap affectent l'exécution future du profil figé ; ils ne changent jamais son ID.

## Tests obligatoires

- Unitaires : tableau/fallback, IDs étrangers/dupliqués, défaut 30, rejet 0/51/valeurs négatives, sélection juste.
- Concurrence : deux workers réservent simultanément un cap 3, jamais plus de 3 succès pour le profil.
- Restart : la distribution ne repart pas durablement sur le premier profil.
- Migration : version suivante à V55, idempotence, `migrations-pending`, manager tests et schéma frais.
- UI : création de deux profils, activation/désactivation multiple, cap 30 visible, 51 refusé, legacy migré sans perte.
- Staging : `scripts/e2e/gmail-multi-profile-sink.sh`, sink loopback uniquement.

## Definition of Done

- Deux profils actifs, six destinataires : écart de distribution au plus égal à 1 tant que les deux sont éligibles.
- Un profil fermé ne part pas ; sa queue conserve son integration ID ; il repart à l'ouverture sans reroutage.
- Cap 3 par profil avec deux workers : trois historiques maximum par ID, même sous course.
- Chaque historique, log structuré et métrique d'envoi est attribuable au profil exact.
- Aucun test ne contacte Gmail, un MX ou le relai sortant.
