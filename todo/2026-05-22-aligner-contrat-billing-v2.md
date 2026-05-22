# [NOTIFUSE] Aligner le consumer `update-plan` sur CONTRAT-BILLING v2

> **Type** : Mise en conformité contractuelle billing cross-app
> **Sévérité** : 🟡 P1
> **Owner** : agent Notifuse
> **Créé** : 2026-05-22 par l'agent Hub
> **Réfère** : `veridian-hub/docs/CONTRAT-BILLING.md` v2.0 (À VENIR)
> **Bloqué par** : la rédaction de `CONTRAT-BILLING.md` côté Hub
>   (ticket `veridian-hub/todo/2026-05-22-extraire-contrat-billing.md`)

---

## ⚠️ TIMING — ne code pas à l'aveugle

Le contrat `CONTRAT-BILLING.md` v2.0 **n'est pas encore rédigé** au moment où
ce ticket est déposé. L'agent Hub le rédige en parallèle.

**Ce ticket te donne le contexte pour PRÉPARER ton terrain** (lire ton code,
identifier les écarts) mais **NE COMMENCE PAS l'implémentation tant que
`CONTRAT-BILLING.md` v2.0 n'est pas mergé sur `veridian-hub` main.** Le
payload exact `update-plan` v2 sera figé dans ce contrat — coder avant = se
faire re-corriger après.

Vérifie l'existence du contrat avant d'attaquer :
`cat ../veridian-hub/docs/CONTRAT-BILLING.md | head -5` — s'il existe et est
en v2.0, tu peux y aller.

---

## Contexte

Le Hub découpe son contrat monolithique. La partie billing devient
`CONTRAT-BILLING.md`, scopé aux **apps commerciales** : Notifuse +
Prospection. Notifuse est concerné à plein titre (app SaaS payante).

Le contrat v2 va graver des invariants que ton consumer `update-plan` doit
respecter. Aujourd'hui ton endpoint `POST /api/tenants/update-plan` existe
(livré v1.2, cf CONTRAT-HUB §5.2) mais il a probablement été codé contre une
version antérieure, moins stricte, du payload.

## Écarts probables à auditer (avant même le contrat figé)

Lis ton handler `update-plan` actuel et vérifie :

1. **Versioning** — ton handler lit-il un champ `contract_version` ? Le v2
   exige : rejeter 400 si `contract_version` major inconnu. Aujourd'hui tu
   acceptes probablement n'importe quel payload.

2. **Enum `plan` fermé** — ton handler valide-t-il que `plan` ∈
   {free, pro, business, enterprise} ? Le v2 exige rejet 400 si hors enum.

3. **`plan_source` enum** — le v2 fige : `stripe | stripe_trial |
   grant_manual | downgrade_auto`. Tu dois distinguer `stripe_trial` de
   `stripe` (UI "essai gratuit" vs "abonné payant", pas de facture sur un
   trial). Tu as déjà `plan_source` (cf CONTRAT-HUB §3.3) — vérifier que les
   4 valeurs sont gérées.

4. **Idempotence** — ton handler dédoublonne-t-il sur `idempotency_key` ?
   Replay du même update-plan = no-op + 200.

5. **Plan offert immune** — un tenant `plan_source=grant_manual` (ex:
   lifetime, internal) ne doit PAS être downgradé par un `update-plan
   plan_source=stripe`. Tu l'as déjà via `plan_source` — confirmer.

6. **Fail-open** — INTERDIT : un cron Notifuse qui downgrade un tenant
   "parce que pas de heartbeat Hub". Si le Hub est down, Notifuse garde le
   tenant dans son dernier état connu. Vérifier qu'aucun mécanisme de ce
   genre n'existe.

## Endpoints concernés côté Notifuse

- `POST /api/tenants/update-plan` — le consumer principal à durcir
- `POST /api/webhooks/...` émetteur vers Hub : le signal
  `activity_threshold_reached` (5e mail → trial) — déjà livré, vérifier qu'il
  reste conforme au contrat v2 §3.7
- Éventuel nouvel endpoint si le v2 demande à Notifuse d'exposer un
  `billing-state` lisible — à confirmer selon ce que le contrat tranche

## Ce que ce ticket NE demande PAS

- Tu ne touches pas à Stripe directement (Notifuse ne reçoit JAMAIS de
  webhook Stripe, n'appelle JAMAIS l'API Stripe en écriture — c'est gravé
  dans le contrat v2 §2)
- Tu ne gères pas le dunning (cycle suspend/relance) — c'est le Hub

## Definition of Done

- [ ] `CONTRAT-BILLING.md` v2.0 lu (attendre qu'il existe)
- [ ] Handler `update-plan` audité contre les 6 écarts ci-dessus
- [ ] Versioning `contract_version` géré (rejet 400 si major inconnu)
- [ ] Enum `plan` + `plan_source` fermés et validés
- [ ] Idempotence `idempotency_key` confirmée
- [ ] Fail-open vérifié (aucun cron downgrade-by-timeout)
- [ ] Tests de conformité (un par invariant)
- [ ] Réponse `## Réponse — YYYY-MM-DD` dans ce fichier + archivage done/

## Réponse attendue

Sous `## Réponse — YYYY-MM-DD`, lister les écarts trouvés + les corrections
faites. Prévenir Robert si un invariant du contrat est impossible/coûteux
côté Notifuse (arbitrage).
