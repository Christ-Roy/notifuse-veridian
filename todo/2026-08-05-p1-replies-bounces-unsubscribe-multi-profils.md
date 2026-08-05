# P1 - Provenance replies/bounces et suppression finale multi-profils

> **Sévérité** : 🟠 P1, conformité et réputation

## Problèmes

- Les replies peuvent être corrélées par Message-ID/contact, mais l'historique ne conserve pas le profil d'envoi.
- Le bounce consumer choisit la première intégration SMTP du workspace : en multi-profils, cette provenance est arbitraire.
- Le poller sait parcourir plusieurs intégrations IMAP, sans association formelle entre inbox de réponse et profil d'envoi.
- Le recheck final reply/statut de liste avant SMTP est limité aux automations. Un broadcast retardé par une fenêtre ou un quota peut envoyer après un bounce, unsubscribe ou reply intervenu depuis l'enqueue.

## Correction exigée

- Propager `integration_id` depuis queue vers history, événements reply/bounce et analytics.
- Résoudre un bounce par Message-ID/envelope/history ; supprimer le fallback « première SMTP ». Une absence de provenance doit être explicite, jamais inventée.
- Modéliser l'association profil d'envoi ↔ inbox IMAP/reply, avec fallback workspace documenté pour legacy.
- Exécuter le gate final de suppression pour tout message marketing juste avant réservation quota/SMTP : replied, unsubscribed, bounced, complained, liste inactive/supprimée.
- Garder les suppressions globales au workspace lorsque c'est la politique, tout en attribuant la cause et le profil source.
- Rendre idempotents les événements doublons et prévenir les boucles bounce auto-générées.

## Tests obligatoires

- Enqueue, puis unsubscribe/reply/bounce pendant fenêtre fermée : zéro message au sink après réouverture.
- Bounce profil B : événement et historique attribués à B, jamais au premier profil A.
- Deux IMAP, messages simultanés et doublons : une seule transition métier, aucune contamination de profil.
- Profil supprimé après historique : analytics et suppression restent interprétables.

## Definition of Done

Aucun message marketing différé ne part vers un contact devenu supprimé entre enqueue et SMTP. Toute réponse ou bounce est attribuable au profil réel ou explicitement marquée inconnue.
