# Notifuse — open tracking (pixel ouverture) focus petits providers + déployer fix TLS

> **Sévérité** : 🟡 P1 (V1 DoD)
> **Owner** : agent notifuse (OPUS)
> **Créé** : 2026-06-10 (par lead tunnel-de-vente)
> **DoD** : `../veridian-tunnel-de-vente/docs/DEFINITION-OF-DONE-V1.md` §1.3

## Contexte
La V1 veut le MAXIMUM d'events sur chaque prospect, dont l'**ouverture du mail**
(`email.opened`). Le pixel d'ouverture dégrade la délivrabilité chez les gros
providers — **décision Robert : on focus PETITS PROVIDERS d'abord** (cohérent
avec le throttle par classe qui attaque aussi par les petits FAI).

## À faire
1. **Déployer le fix TLS déjà codé** (`skip_tls_verify`, commit `71166dc4`) :
   merge staging → valider → prod. C'est ce qui permet l'envoi via le relai réel
   (réception réelle dans les alias Lark), au-delà du sink E2E.
2. **Open tracking activable PAR CLASSE de provider** :
   - ON pour `freemail_fr` / petits FAI / `yahoo_aol` (peu sensibles au pixel) ;
   - OFF par défaut pour `google` / `microsoft` (réputation sensible) — réactivable
     plus tard en data-driven si les tests de délivrabilité montrent que ça passe.
   - Le pixel d'ouverture émet `email.opened` → webhook → bridge → timeline Twenty.
3. **Mesurer la délivrabilité** : pour chaque classe avec pixel ON, vérifier via
   mail-tester + réception réelle que le score ne s'effondre pas. Documenter le
   verdict par classe (data-driven, pas a priori).
4. **email.opened dans la chaîne d'events** : le bridge doit le mapper en timeline
   Twenty (`email.opened`) et l'intégrer au scoring (poids faible — une ouverture
   < un clic).

## Garde-fous
- OPUS. Zéro contournement.
- Jamais Gmail/Outlook froid en test : alias Lark @veridian.site + mail-tester.
- Relai agences-veridian.fr, jamais le domaine principal.
- Le throttle par provider (déjà livré) reste la sécurité anti-spam.
