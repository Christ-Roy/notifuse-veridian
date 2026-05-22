# Bandeau "Email Provider Required" — texte cassé verticalement (Broadcasts)

> **Sévérité** : 🟢 P2
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-05-22

## Contexte

Repéré pendant le sprint UI polish (team `notifuse-ui-polish`) par l'agent
reviewer, en audit visuel à 523px de large (viewport mobile/étroit).

Sur la page **Broadcasts**, le bandeau d'alerte "Email Provider Required"
(`.ant-alert`) rend son texte **cassé verticalement — environ 1 lettre par
ligne**. L'`.ant-alert` mesure ~162px de haut alors que le message devrait
tenir sur 1-2 lignes ; le conteneur du message (`.ant-alert-message` ou
équivalent) a un `width: 0` calculé.

## Cause probable

Répartition flex défaillante dans le bandeau : le bloc texte n'a pas de
`flex: 1` / `min-width` et se fait écraser à largeur nulle quand l'icône +
l'action prennent toute la place. Classique sur viewport étroit.

## Demande

Corriger le layout du bandeau d'alerte "Email Provider Required" sur la page
Broadcasts : le bloc message doit garder une largeur exploitable (`flex: 1`
ou `min-width` sur le conteneur de texte), texte lisible sur toutes les
largeurs y compris ≤ 523px.

## Pour situer

Composant à identifier côté `console/src/` — chercher le wording exact
"Email Provider Required" dans les composants de la page Broadcasts.

## Impact

Cosmétique mais visible et moche sur viewport étroit. Hors scope du sprint
polish (4 tâches : branding, skeleton, Result, steps) — déposé ici pour
traitement séparé.
