# Cap journalier par classe KEYÉ PAR INFRA ÉMETTRICE — warm-up multi-domaine (cold outbound, 2026-06-18)

Rend le **cap journalier par classe destinataire** (`veridian_provider_class_daily_cap`)
keyé **PAR INFRA ÉMETTRICE** = couple (domaine émetteur × classe destinataire). Avant,
`CountSentSinceForDomains` comptait au niveau workspace, toutes infras d'envoi
confondues → dès qu'un 2e domaine d'envoi est ajouté (scaling cold multi-domaine), les
deux infras **partagent** le compteur de classe et se marchent dessus, violant la
doctrine warm-up §7.3bis (« 1 infra IP+domaine → 1 classe destinataire = N/jour »). Spec :
ticket `todo/2026-06-18-cap-journalier-par-infra-emettrice-x-classe.md` (P1, tier 🔴).

- **Option A retenue** (la plus fidèle à la doctrine) : compter **par DOMAINE du
  sender** (`lower(split_part(veridian_sender_email,'@',2))`), pas par adresse exacte —
  les N adresses d'un même domaine partagent l'IP/réputation, donc comptent ENSEMBLE.
  La colonne `message_history.veridian_sender_email` existe déjà (V53, peuplée à
  l'envoi, index partiel) → **PAS de nouvelle migration**, juste un COUNT croisant
  domaines-classe ET domaine-émetteur.
- **Pas d'index neuf** (décision « COUNT live, pas d'agrégat », cf. v49.go/v53.go) : le
  COUNT filtre d'abord par `sent_at` (index V49) + le préfixe `veridian_sender_email`
  (index partiel V53) ; volume cold quotidien négligeable. À matérialiser seulement si
  mesuré nécessaire.
- **Cap par DESTINATAIRE inchangé** (`CountSentSinceForContact`) : reste workspace-
  global (anti-harcèlement = ne jamais sur-solliciter une personne, peu importe l'infra).
- **Fallback non-régression** : si l'entrée n'a PAS de FROM exploitable (legacy /
  pré-V53), le gate retombe sur le COUNT workspace-global `CountSentSinceForDomains`
  (comportement strictement antérieur). Best-effort inchangé (erreur COUNT = pass).
- **Cascade config inchangée** (`broadcast → infra (EmailProvider) → workspace`) : le
  cap-classe posé AU NIVEAU INFRA (`EmailProvider.VeridianProviderClassDailyCap`) prend
  alors tout son sens — il plafonne CETTE infra vers la classe.
- **Dégradation MX assumée** (déjà documentée) : `VeridianDomainsForClass` renvoie [] pour
  les classes MX → ce chemin ne les enforce pas ; le throttle minute par classe protège
  le hot path.
- **Preset warm-up réaligné** (`console/src/services/api/workspace.ts`,
  `VERIDIAN_WARMUP_PRESET` + `WARMUP_PRESET` policy preset) : **retrait** de
  `veridian_per_sender_daily_cap` (per-sender individuel = faux modèle réputationnel ; le
  cap-classe par infra couvre la réputation par domaine). Le champ reste supporté backend
  (réglable à la main), il n'est juste plus posé par CE preset. Caps classe=1 (par infra) +
  per-recipient=1 + rates 0.5/min + fenêtre conservés. Tests front colocalisés adaptés.

⚠️ **Diffs INLINE supplémentaires** (cap par classe par infra émettrice) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/message_history.go` | +1 méthode interface `MessageHistoryRepository.CountSentSinceForDomainsAndSenderDomain` (COUNT classe-par-domaines ET `lower(split_part(veridian_sender_email,'@',2)) = senderDomain`) |
| `internal/repository/message_history_postgre.go` | +impl `CountSentSinceForDomainsAndSenderDomain` (clone de `CountSentSinceForDomains` + prédicat domaine émetteur ; senderDomain vide ou domaines vide non-exclude = 0 sans requête) |
| `internal/repository/veridian_message_history_decorator.go` | +passthrough `CountSentSinceForDomainsAndSenderDomain` (lecture, zéro side-effect quota) |

Fichiers veridian touchés : `internal/service/queue/veridian_daily_cap.go`
(`veridianDailyCapGate` : le COUNT de classe passe par le nouveau helper
`veridianCountClassForInfra` qui dérive `senderDomain` de `entry.Payload.FromAddress`
via `veridianEmailDomain`, fallback workspace-global si FROM vide) + tests colocalisés
(`veridian_daily_cap_test.go` : 2 infras cappées indépendamment, alias même domaine
partagent le compteur, legacy sans FROM = fallback global, erreur COUNT = pass).
Front : `console/src/services/api/workspace.ts`,
`console/src/services/cold/sending_policy_presets.ts` (+ tests). Mock
`mock_message_history_repository.go` régénéré (méthode ajoutée à la main, mockgen cassé).
**Pas de migration, pas de `config.VERSION` bump** (la colonne V53 suffit).

**Validation E2E ON-PREMISE (tier 🔴, 2026-06-18)** : prouvée contre la vraie DB
staging via le prédicat EXACT du gate, ZÉRO mail. `cold-simulate.class_cap_decision`
étendu d'un param `sender_domain` (route vers `CountSentSinceForDomainsAndSenderDomain`
quand fourni, fallback `CountSentSinceForDomains` sinon) + harness dédié
`scripts/e2e/cold-cap-par-infra.sh` (workspace jetable vierge → cap google=1 → seed 1
envoi google depuis `infra-a` → infra-a `would_be_capped=true` (compteur 1) / infra-b
`would_be_capped=false` (compteur 0, SÉPARÉ) / global sans `sender_domain`=1 ; recoupé
par 3 COUNT psql directs). Run staging : toutes assertions vertes. Le gate worker
réel consomme le même `CountSentSinceForDomainsAndSenderDomain` → le prédicat testé EST
le code de production.

