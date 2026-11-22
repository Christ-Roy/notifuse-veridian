# Bounce-loop NDR IMAP → suppression contact (cold outbound, 2026-06-15)

Ferme la boucle de bounce du cold outbound : le relai SMTP Postfix self-hosted
renvoie les NDR (Non-Delivery Reports) ASYNCHRONES dans la boîte du Return-Path
du domaine d'envoi. Notifuse ne les voit pas via un webhook provider (il n'y en
a pas). Ce lot 2 consomme l'IMAP du Lot 1 (consumer enregistré sur le poller),
détecte les NDR et alimente la chaîne de suppression EXISTANTE — **rien n'est
réinventé**. Spec : ticket `todo/2026-06-14-bounce-loop-postfix-suppression-cold.md`.

- **Chaîne réutilisée** (zéro duplication de la suppression) :
  `NDR brut` → `veridian_ndr.Parse` → `domain.SMTPWebhookPayload{Event:"bounce"}`
  → `InboundWebhookEventService.ProcessWebhook` (EXISTANT) → `processSMTPWebhook`
  → `ClassifyBounce` → `MarkEmailsAsBounced` → `contact_lists.status='bounced'`
  → plus jamais renvoyé.
- **Parseur NDR** : `pkg/veridian_ndr/parser.go` (`Parse(raw, from, subject) Result`).
  Robuste : MIME structuré RFC 3464 (`multipart/report; report-type=delivery-status`,
  part `message/delivery-status` → `Status`, `Final-Recipient`, `Diagnostic-Code`)
  PUIS heuristiques de repli (From MAILER-DAEMON/postmaster, sujets NDR multi-langue,
  scan code DSN 5.x.x/4.x.x + adresse). Un message non-NDR (vraie réponse de
  prospect, auto-reply OOO) → `Result{IsNDR:false}`, jamais d'erreur (le Lot 3
  stop-on-reply le traite). Sévérité dérivée du code DSN : 5.x.x = hard, 4.x.x =
  soft. Fixtures de test : Postfix, Gmail, Outlook/Exchange, text/plain, garbage
  MIME, vraie réponse, auto-reply.
- **Consumer** : `internal/service/veridian_bounce_consumer.go`
  (`VeridianBounceConsumer` implémente `domain.VeridianIMAPConsumer`, `Name()` =
  "bounce-loop"). `OnNewMessage` résout l'intégration d'ENVOI SMTP du workspace
  (`GetIntegrationsByType(email)` filtré sur `Kind==smtp`) — car `ProcessWebhook`
  route sur `EmailProvider.Kind`, et le poller donne l'ID de la boîte IMAP de
  RÉCEPTION, pas celui du provider d'envoi. Best-effort de bout en bout (erreur
  loggée + retournée, le poller marque vu quoi qu'il arrive). **Idempotence
  métier** garantie en aval : `MarkEmailsAsBounced` est idempotent (UPDATE ...
  WHERE status NOT IN ('complained','bounced')) → rejouer le même NDR ne supprime
  qu'une fois.
- **Enregistrement** : `app.go` crée le consumer et appelle
  `veridianIMAPPoller.RegisterConsumer(...)` au point d'ancrage prévu par Lot 1
  (ce qui ACTIVE le poller — no-op tant qu'aucun consumer).
- **Pré-filtrage (lot A/B du ticket)** : la suppression empêchait DÉJÀ le
  ré-envoi via `GetContactsForBroadcast` / `CountContactsForBroadcast`
  (`cl.status <> 'bounced'/'complained'`), MAIS ces clauses étaient gatées par le
  flag optionnel `audience.ExcludeUnsubscribed` → un broadcast sans ce flag
  réincluait les bounced. **Corrigé** : `bounced`/`complained` (statuts terminaux)
  sont désormais TOUJOURS exclus, indépendamment du flag (qui ne gate plus que
  `unsubscribed`). Réputation #1 : on ne renvoie jamais à une adresse morte.
- **Classification hard/soft SMTP** : `processSMTPWebhook` force `BounceType="Bounce"`
  (libellé mort pour la classif). `ClassifyBounce` (cas SMTP) enrichi pour lire le
  **code DSN** dans `Subtype` (BounceCategory) puis `Diagnostic` : 5.x.x → Hard
  (suppression immédiate), 4.x.x → SoftCount (seuil). Sans ce diff, un hard bounce
  cold n'aurait supprimé qu'après 5 occurrences.

⚠️ **Diffs INLINE supplémentaires** (bounce-loop) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/bounce_classification.go` | +helper `classifyDSNCode` (regex code DSN enrichi → Hard/Soft) + cas `EmailProviderKindSMTP` enrichi : à défaut de `hardbounce`/`softbounce`, classe par code DSN du subtype puis du diagnostic. Additif, ne change aucun mapping existant. |
| `internal/repository/contact_postgres.go` | `GetContactsForBroadcast` + `CountContactsForBroadcast` : clauses `status <> 'bounced'` et `<> 'complained'` SORTIES du `if ExcludeUnsubscribed` (toujours appliquées). L'ordre des `$N` change (Bounced, Complained, puis Unsubscribed conditionnel) → `WithArgs` des tests réordonnés. |
| `internal/app/app.go` | +création `VeridianBounceConsumer` + `veridianIMAPPoller.RegisterConsumer(...)` au point d'ancrage Lot 1 (active le poller). |

Fichiers veridian dédiés : `pkg/veridian_ndr/{parser.go,parser_test.go}`,
`internal/service/veridian_bounce_consumer{,_test}.go`.


## Depuis le 22/11/2026 (VER-220) : la boîte unifiée est la seule source

Constat en prod (robertbrunon, 30 jours) : le consommateur « bounce-loop » du poller historique
n'avait lu qu'un message de la boîte des rapports du relais agences et aucun de celle du relais
nord, et n'avait journalisé aucun « bounce processed », pendant que la boîte unifiée (L1) rangeait
101 rapports définitifs. Résultat : 126 envois rebondis restés « partis », relances continuées (la
porte de la fiche 66 lit l'effet du rebond sur le J0, qui n'était jamais posé).

- **Source unique** : `internal/veridian/mailbox` remet chaque rapport rangé (pas les copies, pas
  les relectures, pas les rebonds hors portée d'une boîte « replies ») à `mailbox.BounceSink`.
- **Applicateur** : `internal/veridian/bounces` (`Applier`), branché par le module mailbox via
  `bounces.FromHost`. Il passe le rebond par la chaîne EXISTANTE (`ProcessWebhook`, interne :
  classement `ClassifyBounce`, `bounced_at`/`bounce_type`, listes en « bounced » pour un dur), puis
  **sort le contact de ses inscriptions `active` et `sending`** sur un rebond dur (`bounce_hard`) ou
  un refus de politique (`bounce_policy`), étape et date gardées. Temporaire (5.2.2, 4.x) : compté
  par la chaîne, aucune sortie. Un refus de politique ne supprime toujours pas le contact de ses
  listes (décision du 06/10).
- **Ancien consommateur débranché** (`veridian_wiring.go`) : le garder compterait deux fois chaque
  rebond temporaire. Le consommateur stop-on-reply garde le poller actif.
- **Garde finale du worker** (`veridian_automation_send_guard.go`) : relit l'inscription
  (`SetVeridianEnrollmentReader`, branché par le module mailbox) ; une inscription terminée
  (sortie, terminée, en échec) retire la ligne avant le relais. Lecture en échec : laisse passer.
- **Rattrapage** : le réconciliateur (invariant 1, « rebond non appliqué ») juge chaque rapport par
  la même règle et propose `replay_bounces`, qui rejoue les rapports sans effet dans la chaîne.
- **« Relay access denied » = adresse morte** (décision du 23/11/2026) : un refus 5.7.x « Relay access
  denied » est classé Hard (`domain.veridianIsRelayAccessDenied`) : le serveur du destinataire
  n'héberge pas le domaine. Le 5.4.1 garde la règle d'octobre, un 4.x reste temporaire. Rapports
  passés rejoués le 23/11 (35 contacts, sauvegarde `~/sauvegardes-manuelles/notifuse-ver220-rebonds-*/relay-access-denied/`).
