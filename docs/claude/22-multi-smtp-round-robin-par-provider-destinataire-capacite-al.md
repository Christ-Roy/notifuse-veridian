# Multi-SMTP round-robin par provider destinataire + capacité alignée (2026-06-15)

Répartit les envois cold sur TOUS les senders d'une infra (les 3 boîtes
`agences-veridian.fr` p.ex.), en **round-robin keyé par CLASSE de provider
destinataire** : chaque classe (`google`/`microsoft`/…) a son propre curseur, on
ne martèle pas le même couple (sender → provider) → préservation réputation
IP/domaine. Spec : ticket `todo/2026-06-15-...` (Lot ENVOI). + **alignement de
capacité** : le débit global de l'infra = `RateLimitPerMinute · N senders` (les N
boîtes envoient en parallèle, chacune porte sa part).

- **Sélection à l'ENQUEUE** : le sender est figé dans le payload
  (`buildQueueEntry` → `FromAddress`/`FromName`). C'est là (et dans le sender
  direct `SendToRecipient`) que la rotation remplace `GetSender`. Le worker ne
  choisit PAS le sender (il consomme le payload).
- **Fichiers veridian** : `internal/domain/veridian_sender_rotation.go`
  (`VeridianSenderRotator` thread-safe, curseur `integrationID|classe` ;
  `EmailProvider.VeridianSelectSender` respecte le SenderID explicite du template
  puis round-robin si >1 sender ; `VeridianActiveSenderCount` /
  `VeridianEffectiveRateLimit` ; `VeridianIsColdContext` centralise la détection
  tunnel — tag contact OU config broadcast OU config workspace) +
  `internal/service/broadcast/veridian_sender_rotation.go` (`veridianResolveSender` :
  détection cold via workspace mémoïsé du pixel resolver, classification par
  suffixe — pas de lookup MX, la précision MX reste réservée au throttle/cap).
- **OPT-IN strict** : rotation active uniquement si `len(senders) > 1` ET contexte
  cold. Sinon `GetSender` upstream figé (non-régression). rotator nil = upstream.
- **Rotator partagé** par la factory (`f.veridianSenderRotator`), injecté dans les
  deux senders → curseurs persistants entre batchs.

