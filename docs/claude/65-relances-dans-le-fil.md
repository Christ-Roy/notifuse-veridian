# Relances dans le fil du premier mail (12/10/2026)

Demande de Robert : les relances d'une séquence (J+4, J+10) doivent arriver chez le prospect
dans la même conversation que le premier mail, depuis la même adresse.

## Mesure (12/10, 2 510 copies Gmail du dossier `Agences Veridian/Envoyé`)

- `In-Reply-To` présent sur 2 mails sur 2 510, `References` sur 2 : le fil n'existait pas.
- L'objet d'une relance n'avait aucun rapport avec celui du premier mail.
- Sur les 332 relances parties le 12/10, 151 venaient d'une autre adresse que le premier mail.
  Cause : l'ancre de séquence (`veridianSequenceAnchor`) lâchait l'expéditeur d'origine quand il
  n'avait « rien envoyé depuis 48 h ». La fenêtre d'envoi est fermée du vendredi 19 h au lundi
  8 h (61 h) : tous les lundis matin, toutes les relances dues basculaient sur le pool libre.
  La limite est maintenant de 7 jours.

## Mécanisme

Réglage du workspace `veridian_thread_followups` (booléen, absent ou faux = comportement
upstream). Posé avec `settings:set <ws> veridian_thread_followups true`.

Le worker (`processEntry`) appelle `veridianApplyThreading` après le rendu au dépilage et le choix
du profil, pour toute entrée d'automation :

1. lecture des mails déjà partis (`sent_at` posé) vers ce contact par CETTE automation, dans
   `message_history`, du plus ancien au plus récent ;
2. `In-Reply-To` = Message-ID du plus récent ; `References` = la chaîne complète ;
3. objet = `Re: <objet du premier mail>` (jamais `Re: Re:`) ;
4. le Message-ID d'un mail déjà parti se recalcule à l'identique par
   `domain.VeridianRFCMessageID(message_history.id, veridian_sender_email)` : aucune colonne en plus,
   aucune migration, les séquences déjà en cours sont couvertes ;
5. l'objet du premier mail est lu dans `message_data.metadata.veridian_subject` (consigné à chaque
   envoi d'automation depuis ce correctif). Pour un premier mail parti avant, le modèle de ce premier
   mail est re-rendu pour le contact (les objets sont du Liquid déterministe, sans spintax) ;
6. même expéditeur : garanti par l'ancre de séquence (même profil d'envoi). Un écart est tracé en
   `Warn` (`Thread follow-up: sender differs...`), il ne bloque pas.

Premier mail d'une séquence : rien ne change. Historique illisible : le mail part sans fil.

Transport : `EmailOptions.InReplyTo` et `EmailOptions.References` sont posés en en-têtes par le SMTP
(`smtp_service.go`). Les autres transports les ignorent.

## Fichiers

`internal/service/queue/veridian_thread_followup.go` (+ test), `queue/worker.go`,
`queue/veridian_pool_failover.go` (constante 7 jours), `service/smtp_service.go`,
`service/veridian_send_message_id.go`, `domain/veridian_message_id.go`, `domain/email_provider.go`,
`domain/workspace.go`, `service/workspace_service.go`.
