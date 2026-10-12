# 66. Une relance ne part pas si le premier mail n'a pas été livré (12/10/2026)

Demande de Robert (12/10) : une relance « Re: » sans premier mail est absurde pour le prospect et coûte en
réputation d'expéditeur.

## Mesure

Lundi 12/10, 9 relances J+4 sur 539 sont parties vers des contacts dont le J0 avait été **refusé par le serveur
destinataire** (`bounce_type = PolicyBounce`, 5.7.x : rspamd, relais refusé, SPF). Deux prospects ont répondu
« je n'ai rien reçu » (Vergers de Montdragon). Aucun des 9 n'était un rebond dur ni un échec d'envoi : le serveur
avait accepté le mail à l'envoi (`sent_at` posé), puis renvoyé un refus asynchrone.

## Règle (`queue/veridian_first_mail_gate.go`, appelée au début de `processEntry`)

1. L'entrée est une **relance** si son nœud (ou, pour une entrée posée avant V62 sans nœud, le nœud email du même
   modèle) est atteignable depuis un autre nœud email du graphe (`veridianEntryIsFollowUp`). Au moindre doute : pas une
   relance, donc jamais supprimée. Le premier mail lui-même n'a pas de J0 et n'est pas concerné.
2. Le **J0** est le plus ancien message de la même automation vers ce contact (`message_history`, hors entrée
   courante, hors SMS et transactionnel). Lecture par `GetByContact`, qui lit désormais `bounce_type` (nouveau champ
   `MessageHistory.BounceType`, lecture seule).
3. **Non livré** : `failed_at` posé (`first_mail_failed`) · `PolicyBounce` (`first_mail_policy_refused`) ·
   `HardBounce`, ou `bounced_at` posé sans libellé mou (`first_mail_bounced`) · `sent_at` vide
   (`first_mail_not_sent`) · aucun J0 (`first_mail_missing`).
4. **Pas une sortie** : un rebond MOU (`SoftBounce`, classement de `domain.ClassifyBounce` : 4.x.x, 5.2.2 boîte pleine,
   5.4.x routage) ou un report temporaire. Le mail a été accepté, la relance part. Le 5.4.1 « Recipient address
   rejected » d'Exchange Online est un rebond DUR depuis `22359286` : il sort.
5. **Sortie** : ligne de file supprimée, exécuteur prévenu (`onEmailFailed` permanent : le contact sort avec la raison en
   `exit_reason`), décision `exited` dans le journal de la fiche 62 (`logs:decisions --reason first_mail_policy_refused`).
6. Historique illisible : la relance ne part pas et le contact ne sort pas, nouvelle tentative dans la minute (échec
   fermé, comme la garde finale). Automation en pause : on ne touche à rien.

La porte est placée AVANT les portes de débit : une relance condamnée sort au premier dépilage, même fenêtre fermée
(le dimanche), au lieu d'attendre le lundi 08h. Les entrées `paused` des automations en pause ne sont pas dépilées et
ne sont donc pas touchées.

## La neuvième relance sans « Re: » (après le déploiement de 18h06 Paris)

Relance à `inscriptions@autransstillmagic.com`, 18h11:48, la seule sans « Re: » parmi les 9 envoyées après le déploiement. Ce n'est
PAS un cas « J0 non livré » (J0 parti le 08/10, accepté, sans rebond). Mesuré :
- le Message-ID et l'expéditeur du J0 ont été retrouvés (le failover de pool a bien ramené la relance sur le profil de J0 :
  l'ancre fonctionne, donc `GetByContact` lit bien le J0) ;
- aucun `Warn` « Thread follow-up » dans Loki, donc ni historique illisible ni objet de J0 introuvable ;
- la relance a été créée et envoyée dans la même seconde (16:11:47Z, 4 min 41 s après le démarrage du conteneur) ; les 8 suivantes
  (18h16 à 18h41) étaient en file depuis 09h14 et portent « Re: ».
La seule porte de sortie silencieuse de `veridianApplyThreading` qui reste est le réglage `veridian_thread_followups` lu faux sur
l'espace de travail à cet instant. Le réglage porte `updated_at = 15:59:35Z`, avant le conteneur : non expliqué, non prouvé. Aucun
changement de code n'est donc apporté pour ce cas ; la relance suivante de ce type (entrée créée et envoyée dans la même seconde)
est à vérifier au prochain lot.

## Fichiers

`queue/veridian_first_mail_gate.go` (+ test), `queue/worker.go` (appel), `domain/veridian_send_decision.go`
(raisons `first_mail_*`), `domain/message_history.go` (`BounceType`), `repository/message_history_postgre.go`
(`GetByContact` lit `bounce_type`).

## Pièges

- Les tests du dépôt simulent la base : la requête `GetByContact` a été relue en SQL réel sur la production (lecture
  seule) avant d'être crue.
- `veridianAllowAutomationSend` (tests de pool) déclare un repli `GetByContact` vide EN DERNIER : un test qui attend un
  historique précis déclare le sien d'abord, en `AnyTimes()`.
