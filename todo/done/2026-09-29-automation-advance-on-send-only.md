# [NOTIFUSE] 🔴 P0 -- un node email avancait le contact des l ENQUEUE, pas sur envoi reel

> **Severite** : 🔴 P0 -- un contact pouvait recevoir une relance J+4 sans avoir jamais
> recu le J0 (classe exclue, blocklist, cap, ou echec SMTP rejouaient quand meme le node
> comme "completed").
> **Cree** : 2026-09-29, incident robertbrunon (workspace cold outreach). Le fork Notifuse
> avait aussi un bug voisin sur `sent_at` (corrige v58, commit 86a0c829) : la migration a
> remis `sent_at` a NULL sur les messages jamais reellement envoyes, mais les contacts
> concernes avaient deja avance dans leur automation (current_node_id = j4) sans jamais
> avoir recu J0. Ce ticket corrige la CLASSE du defaut, pas seulement les donnees.
> **Owner** : agent notifuse-veridian.

## Le probleme

`EmailNodeExecutor.Execute` (internal/service/automation_node_executor.go) mettait le
message en file (`email_queue`) puis retournait immediatement `Status: Active,
NextNodeID: <node suivant>` -- le contact avancait dans l automation des l ENQUEUE,
independamment du sort reel de l envoi (gere de facon totalement asynchrone par
`EmailQueueWorker`, internal/service/queue/worker.go). Un gate qui REJETTE (classe
exclue, blocklist, adresse invalide) ou qui REPORTE (cap journalier, warmup, fenetre
d envoi) laissait quand meme filer le contact vers le node de relance.

## Le fix

- Nouveau statut `domain.ContactAutomationStatusSending` ("sending") : le contact
  PARQUE sur son node email (current_node_id inchange), exclu du scheduler
  (`GetScheduledContactAutomations` ne selectionne que `status='active'`).
- `EmailNodeExecutor.Execute` retourne desormais `NextNodeID: <lui-meme>, Status:
  Sending` au lieu d avancer.
- `AutomationExecutor.HandleEmailSent` / `HandleEmailFailed` (nouvelles methodes),
  branchees comme callbacks du `EmailQueueWorker` (`SetCallbacks`, cf. app.go) :
  - envoi confirme (SMTP accepte) -> avance au node suivant (ou complete
    l automation si terminal) ;
  - rejet definitif (`isPermanent=true` : classe exclue, blocklist, pre-filtre,
    ou retries SMTP epuises) -> sort de l automation avec la raison exacte en
    `exit_reason`, jamais de relance ;
  - report (cap/warmup/fenetre -- aucun callback tant que ce n est pas terminal)
    -> aucun changement, le contact reste parque, la file retente seule
    (backoff/reschedule deja natif au niveau `email_queue`).
- `EmailSentCallback`/`EmailFailedCallback` (worker.go) etendus avec `contactEmail`
  pour retrouver le `contact_automation` parque sans lookup supplementaire.

## Tests

- 8 tests existants de `automation_node_executor_test.go` (chemin succes du node
  email) mis a jour : `NextNodeID` pointe desormais sur le node lui-meme, `Status`
  vaut `Sending`.
- 8 tests neufs dans `automation_executor_test.go` :
  `TestAutomationExecutor_Execute_EmailNode_ParksUntilDeliveryConfirmed` (repro RED
  du bug : sans le fix, le contact aurait avance au node "j4" ; avec le fix il reste
  parque), `HandleEmailSent_{AdvancesParkedContact,CompletesWhenNoNextNode,
  NoopWhenContactNotParked,NoopForNonAutomationSource}`,
  `HandleEmailFailed_{ExitsOnPermanentRejection,NoopWhileRetrying,
  NoopForNonAutomationSource}`.
- Suite complete `go test ./...` verte (go vet compris).

## Deploiement

Push sur `veridian` -> CI (`veridian-ci.yml`) -> staging + e2e-staging verts ->
promotion prod par `workflow_dispatch deploy_prod=true` (pas de marker
`[risk:low]` : changement de semantique sur le moteur d automation partage par
tous les tenants, promotion explicite plutot qu auto-promote).

## Suite (hors ce ticket)

Une fois en prod : reset natif des 4 automations robertbrunon (delete + recreation
IaC identique + reinscription J0 de tous les membres actifs sauf les 5 vrais
destinataires confirmes) -- desormais sans risque de ré-avancer un contact pendant
la fenetre live, le tick du scheduler fait sortir ou reporter, jamais avancer.
