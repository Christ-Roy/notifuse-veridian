# 54. Vérité d'un profil d'envoi (lot 2 du plan 53)

Date : 08/10/2026. Base : `origin/veridian` au commit `6772691f`. Plan d'origine : fiche 53, section 7, lot 2.

## Ce que ça pose

1. **`VeridianEffectivePlan`** (`internal/service/queue/veridian_effective_plan.go`) : une seule fonction qui dit ce que le worker appliquera à un profil maintenant. Elle ne recalcule rien : les plafonds viennent de `veridianResolveCapLimits` (`veridian_gate_limits.go`), la même résolution que lisent la porte de plafond journalier (`veridianDailyCapGate`) et la réservation atomique (`veridianReserveDailyQuota`) ; le débit par classe de `veridianEffectiveClassRate` (porte de débit) ; la fenêtre de `veridianResolveSendingWindow` ; le facteur du fusible de `VeridianComputeReputationStatus` (qui rejoue `veridianEvaluateCouple`). Types de sortie : `internal/domain/veridian_effective_plan.go`.
2. **Test de parité** (`veridian_effective_plan_test.go`) : pour chaque scénario (chauffe, plafond profil, par expéditeur, par classe, plainte, pause, classe exclue, fenêtre), le test appelle `veridianSelectSendableIntegration` + `veridianReserveDailyQuota` (ce que `processEntry` exécute avant SMTP) et `VeridianEffectivePlan` sur les mêmes compteurs, et exige la même réponse par classe. Éprouvé par mutation : pause ignorée dans le worker, ralentissement ignoré dans la résolution, fenêtre ignorée dans le plan, chacune fait rougir le test.
3. **`GET|POST /api/veridian/emailProfiles.overview`** : tous les profils email du workspace (commerciaux et transactionnels), avec `plan`, type (`smtp`, `gmail_app_password`, `gmail_oauth`, ou le nom du fournisseur API), `verified_at`, `senders`, `usage`, `in_rotation`, `paused`, `return_inbox`. `global_inboxes` = IMAP liés à aucun profil. `usage_conflicts` = profils qui violent l'exclusivité. Aucun secret. Permission : `message_history:read`, donc accessible avec une clé API scopée en lecture. Compteurs du jour (UTC) : `message_history` (mêmes filtres que les COUNT des portes) et `veridian_daily_quota_counters`.
4. **Lien profil vers IMAP** : `veridian_return_imap_integration_id` sur le profil (`omitempty`, aucune migration). Doit désigner une intégration `imap` du même workspace (400 sinon) ; supprimé automatiquement si l'IMAP est supprimé. **Aucun effet sur l'envoi ni sur la détection des réponses et des rejets** (restée globale au workspace). Un IMAP peut être lié à plusieurs profils. Les IMAP liés à aucun profil sont exposés comme boîtes de retour globales.
5. **Pause** : `veridian_paused`. `veridianSelectSendableIntegration` saute un profil en pause (ni « atteignable », ni circuit ouvert) : bascule sur les autres membres du pool, ou l'entrée attend une heure sans échouer. Une relance de séquence dont l'ancre est en pause bascule tout de suite (la pause est une décision de l'opérateur, pas un plafond du jour). Les entrées en file sont conservées ; la levée de la pause réveille la file (`UpdateIntegration` appelle `WakePendingByIntegration`). Le transactionnel n'est pas concerné (il n'a aucune porte).
6. **Exclusivité commercial / transactionnel** : `Workspace.ValidateVeridianUsageExclusivity`, appelée par `UpdateWorkspace` seulement quand l'écriture change le pool ou le profil transactionnel (400 `ValidationError`). Un workspace qui viole déjà la règle reste modifiable pour tout le reste ; l'overview le liste dans `usage_conflicts`. Le singleton historique `marketing_email_provider_id` n'est pas contraint (upstream l'utilise pour les deux).

## Définitions du plan

- `daily_cap_today` : le plus bas des plafonds de porte (`warmup`, `profile_cap`, `per_sender` x nombre d'adresses, `class_cap` = somme des plafonds si TOUTES les classes sont plafonnées). `null` = aucun plafond. `limiting_gate` nomme la porte (à égalité : chauffe, profil, expéditeur, classes).
- `remaining_today` / `remaining_gate` : ce qu'il reste à la porte qui se vide en premier (peut différer de `limiting_gate`).
- La chauffe ne court-circuite PAS le plafond de classe : la porte du worker le court-circuite, mais la réservation atomique pose les deux compteurs. Le plan applique les deux (c'est ce qui bloque réellement).
- Compteurs : le plus haut de l'historique accepté et du compteur atomique (un résultat SMTP ambigu consomme la capacité sans être compté accepté).
- `classes[].sendable_now` : le worker enverrait vers cette classe maintenant. `blocked_by` : première raison dans l'ordre des portes. Profil transactionnel : `applicable=false`, volume du jour seulement.
- Plafond par expéditeur : bloque une classe quand TOUTES les adresses du profil sont au plafond (la rotation peut en choisir une autre).

## CLI

`notifuse profiles:overview <ws> [--json]` (clé scopée), `notifuse-admin profiles:link-imap|pause|resume`. `notifuse config` ajoute `profils_verite_serveur` ; l'ancien port Python (`_effective_caps_for_integration`) ne sert plus qu'au contrôle de file figée. Le CLI embarqué (`internal/http/agentcli/`) se resynchronise avec `scripts/veridian/sync-agent-cli.sh`.

Bug corrigé : `notifuse-admin magic-link` laissait un membre api_key `magiclink…` à chaque appel. Clé éphémère révoquée en `finally`, restes de plus de 10 minutes balayés.

## Pas fait ici (lots suivants)

Page console « Profils d'envoi » (lot 3), `emailProfiles.setUsage` / `create` (lot 4), persistance du dernier poll IMAP (santé de la boîte), réputation par `veridian_profile_id` plutôt que par domaine, extension de `emailProfiles.usage` (l'overview couvre déjà les profils hors pool).
