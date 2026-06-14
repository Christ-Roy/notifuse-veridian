# [NOTIFUSE] 🟡 P1 — Cold outreach SANS lien/header unsubscribe (façon Lemlist)

> **Sévérité** : 🟡 P1 (délivrabilité cold)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-14 — extrait du ticket
> `2026-06-14-classification-mx-table-patterns-option-A.md` (le Lot 4
> classification MX a été livré séparément ; ce sous-lot est isolé ici).

## Décision (actée Robert 2026-06-14)
Le cold outreach Veridian assume **AUCUN lien/header unsubscribe** sur les envois
cold (comme Lemlist/Instantly) : le cold B2B se présente comme du 1-to-1
personnel ; un footer "se désabonner" + header `List-Unsubscribe` = signal
"mailing de masse" → tue la délivrabilité et le côté personnel. Risque légal B2B
assumé en connaissance de cause par Robert.

## État du code (déjà vérifié dans le ticket source — c'est FAISABLE proprement)
- Footer `{{unsubscribe_url}}` : présent UNIQUEMENT dans les templates de démo
  (`demo_service.go`), jamais imposé. ✓
- Header RFC-8058 `List-Unsubscribe` : ajouté SEULEMENT si
  `EmailOptions.ListUnsubscribeURL` non-vide (ses_service.go, mailgun_service.go,
  sparkpost_service.go). ✓
- Le broadcast remplit `ListUnsubscribeURL` SEULEMENT si
  `data["oneclick_unsubscribe_url"]` non-vide
  (`queue_message_sender.go:420-421`, `message_sender.go:360-361`). ✓

## À faire (petit, ciblé tunnel cold)
En **contexte tunnel** (même détection que le pixel par classe : tag
`custom_string_5` OU config cold présente), NE PAS générer
`oneclick_unsubscribe_url` → donc pas de header `List-Unsubscribe`, pas de footer.
Gater côté **fichier veridian_*** (ne PAS patcher le compilateur upstream), même
pattern que `VeridianResolveOpenPixel`.

## DoD
- [ ] Contexte tunnel détecté → `oneclick_unsubscribe_url` non généré (header + footer absents)
- [ ] Hors tunnel → comportement upstream inchangé (broadcasts marketing GARDENT leur unsubscribe — non-régression)
- [ ] Tests : tunnel → `ListUnsubscribeURL` vide ; hors tunnel → inchangé
- [ ] Fichier veridian dédié (pas de patch compilateur upstream)
