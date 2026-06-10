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

---

## Réponse — 2026-06-11 (agent notifuse)

### 1. Fix TLS relai — LIVRÉ ET VALIDÉ BOUT-EN-BOUT
- `scripts/e2e/tunnel-send.sh` complété : `skip_tls_verify:true` dans
  `createIntegration` + convergence idempotente via `updateIntegration` (le
  workspace `coldtest` est pérenne, intégration créée avant le flag mise à jour
  sans DELETE/recreate).
- **E2E LOURD réel** (dev-pub, relai `100.92.215.42:587` SASL agences-veridian.fr) :
  5/5 messages envoyés sans erreur TLS, logs Postfix `status=sent (250 Ok)` vers
  Lark pour les 5 classes. **Réception confirmée 5/5 alias Lark INBOX** (IMAP).
- **Délivrabilité : mail-tester 10/10** (relai cold, SPF/DKIM/DMARC parfaits,
  non-blocklisté). Le fix TLS `71166dc4` tourne déjà en STAGING.
- Promo PROD = à valider avec le lead (tier 🔴 chemin TLS, gate §20).

### 2. Souscription webhook bridge sur `coldtest` — CRÉÉE
- Workspace tunnel = `coldtest` (l'ancien `e2ethrottle143602` était jetable).
- Souscription `tunnel-bridge` = `75c036833b774660815f5ef0c6996c02`, URL
  `bridge.staging.veridian.site/webhooks/notifuse`, events
  `email.sent/opened/clicked/unsubscribed`, enabled.
- Nouveau secret webhook + api_key bridge mis à jour dans `.all-creds.env`
  (`NOTIFUSE_WORKSPACE_ID_TUNNEL=coldtest`). Test webhook PART vers le bridge
  (401 attendu : bridge sur l'ancien secret → redémarrage bridge requis, côté
  lead/analytics). api_key validée sur `messages.list` (cron reconcile).

### 3. Open tracking pixel PAR CLASSE — CODÉ + TESTÉ, déploiement en attente
- Commit `57103bfb` (tier 🔴, sans `[risk:low]`). Flag `EnableOpenPixel` (*bool
  nullable) découple pixel/clics. Politique ON freemail_fr/yahoo_aol/corporate,
  OFF google/microsoft. Configurable broadcast metadata + workspace settings.
  Non-régression stricte hors tunnel (nil = upstream).
- **Tests verts** : domain + mjml + sender (capture HTML réel : OFF google / ON
  freemail / non-régression). 3 packages passent intégralement.
- ⚠️ **Déploiement staging BLOQUÉ par saturation disque dev-pub** (build CI
  `no space left on device` / export buildkit très lent à 95% disque). Code
  prouvé, blocage purement infra. Verdict délivrabilité pixel-par-classe à
  compléter dès que l'image est déployée (E2E pixel staging + mail-tester
  pixel ON petit provider).
