# Incohérence UX : la carte KPI "Replies" ignore le filtre All/Broadcasts/Transactional

> **Sévérité** : 🔵 P3 (UX mineure, signalé R7bis par agent reply-kpi)
> **Owner** : agent notifuse
> **Créé** : 2026-06-17
> **Axe** : Dashboard mails / cohérence KPI

## Contexte (trouvaille hors scope, déposée plutôt qu'ignorée)

En livrant le KPI reply rate (ticket `2026-06-16-kpi-reply-rate-dashboard.md`,
SHA `dec1df0d`), j'ai posé une 9e carte "Replies" dans
`console/src/components/analytics/EmailMetricsChart.tsx`. Les 8 cartes upstream
(Sent/Delivered/Opens/Clicks/Bounced/Complaints/Unsub/Failed) respectent le
`Segmented` `messageTypeFilter` (All / Broadcasts / Transactional) : la query
analytics ajoute un filtre `broadcast_id set/notSet`.

**La carte Replies, elle, NE respecte PAS ce filtre** : le signal reply vient de
la table `veridian_contact_reply` (stop-on-reply Lot 3), qui est **contact-level**
(PK `contact_email`, pas de `broadcast_id`). On ne peut donc pas, en l'état,
ventiler les réponses entre broadcasts et transactionnels. La carte affiche
toujours le total sur la fenêtre temporelle, quel que soit le segment choisi.

Ce n'est pas un bug de mon lot (la donnée n'a structurellement pas le lien), mais
c'est une **incohérence visuelle** : un utilisateur qui bascule sur "Broadcasts"
voit 7/7 cartes bouger sauf Replies qui reste figée → confusion possible.

## Options (pour l'agent qui prend le ticket)

1. **Tooltip explicite** (le moins invasif) : ajouter dans le tooltip de la carte
   Replies une note "toutes campagnes confondues (le signal réponse n'est pas
   rattaché à un envoi spécifique)". ~5 min, zéro backend.
2. **Griser/masquer la carte** quand `messageTypeFilter !== 'all'` : honnête mais
   prive du KPI #1 sur la vue segmentée.
3. **Rattacher la réponse à l'envoi** (gros) : le stop-on-reply pose déjà
   `matched_message_id` (match fort Message-ID) sur `veridian_contact_reply`.
   On POURRAIT joindre `message_history` via ce `matched_message_id` pour dériver
   `broadcast_id` et ventiler — mais le fallback faible (match par From, sans
   `matched_message_id`) resterait non ventilable. Migration éventuelle + jointure.
   À ne faire QUE si Robert veut le reply rate par campagne (probable à terme).

## Reco

Option 1 (tooltip) en attendant un besoin métier de reply rate PAR campagne, qui
justifierait l'option 3. Pas d'arbitrage business urgent : P3.
