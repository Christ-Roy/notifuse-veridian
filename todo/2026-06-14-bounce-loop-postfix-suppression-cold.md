# [NOTIFUSE/POSTFIX] 🔴 P0 — Boucle bounce Postfix→Notifuse + ne jamais renvoyer à une adresse morte

> **Sévérité** : 🔴 P0 réputation (taux de bounce élevé = blacklist)
> **Owner** : agent notifuse-veridian (+ coordination skill postfix / repo prospection)
> **Créé** : 2026-06-14 par Robert. Cold outreach via SMTP Postfix self-hosted.
> Robert : *"je ne veux pas renvoyer des mails à une adresse qui n'en reçoit pas"*.

## Le problème

Le tunnel cold envoie via **SMTP Postfix self-hosted** (PAS Brevo — Brevo c'est pour
le SaaS transactionnel ; le cold = relai Postfix). Or les bounces ne reviennent pas
à Notifuse comme avec SES/Mailgun (qui ont des webhooks providers). Deux types :
1. **Bounce synchrone** (`550 user unknown` pendant la transaction SMTP) → Postfix le
   voit, Notifuse PEUT le capter sur le code retour (smtp_service). Cas minoritaire.
2. **Bounce asynchrone** (NDR / non-remise envoyé PLUS TARD au Return-Path) → atterrit
   dans la boîte du domaine d'envoi Postfix, **Notifuse ne le voit PAS**. **Majorité
   des bounces.** → on continue à envoyer à des adresses mortes → réputation grillée.

## Ce qui existe DÉJÀ (bonne nouvelle, paths)

- **Notifuse accepte déjà un webhook bounce SMTP** : `inbound_webhook_event_service.go:85`
  `case domain.EmailProviderKindSMTP: events, err = s.processSMTPWebhook(integration.ID, rawPayload)`.
  Endpoint : `POST /webhooks/email?provider=smtp&workspace_id={id}&integration_id={id}`
  (`inbound_webhook_event_handler.go:49-50`). ➜ Le CANAL D'ENTRÉE EXISTE. Reste à lui
  POSTer les NDR + vérifier/ajuster le format que `processSMTPWebhook` attend (À LIRE).
- **Suppression contact câblée** : statut `bounced` → contact suppressé et le reste
  ("bounced and complained contacts must stay suppressed", `contact_list.go:32`). Une
  fois le bounce remonté, Notifuse ne renvoie plus. ✓
- **Classification bounce** : `ClassifyBounce` (`bounce_classification.go`) gère le cas
  SMTP (`hardbounce → Hard, otherwise SoftCount`). ✓
- **Pré-filtrage à la source** : `odh-scrape-db.email_verification` a déjà `result`
  (valid 22.5k / **invalid 9.5k / no_mx 1.7k / catch_all 2.4k / unknown 11.9k**) +
  `smtp_code`. ➜ ~25k/48k adresses à risque détectables AVANT envoi.

## Travail à faire

### Lot A — Boucle bounce Postfix → Notifuse (le coeur)
1. Lire `processSMTPWebhook` (format payload attendu : champs recipient, bounce type,
   diagnostic, message-id).
2. Outil côté Postfix (skill `postfix`) qui **lit la boîte de bounce / Return-Path**
   (les NDR), parse l'adresse morte + le code DSN (5.x.x hard, 4.x.x soft), et **POST
   vers `/webhooks/email?provider=smtp&workspace_id=&integration_id=`** au format attendu.
   → mécanisme : soit Postfix pipe les bounces vers un script (transport `bounce`),
   soit un script lit l'IMAP de la boîte bounce périodiquement (mais PAS un cron
   bricolé qui contourne — un vrai consumer propre). À designer avec le skill postfix.
3. Notifuse reçoit → classe → suppresse le contact → plus jamais d'envoi à cette adresse.
4. ⚠️ Return-Path : s'assurer que les envois cold posent un Return-Path qui revient
   sur une boîte qu'on lit (VERP idéalement, pour mapper bounce→message exact).

### Lot B — Pré-filtrage AVANT envoi (réduire les bounces à la source)
- À l'import des leads dans Notifuse, exclure / ne pas envoyer aux adresses dont
  `email_verification.result IN ('invalid','no_mx')`. `unknown`/`catch_all` = prudence
  (débit bas ou exclusion selon risque). Exploiter le pipeline `email_verification`
  existant (scaler sur extracted_v2). Lien : doc PROVIDERS-DESTINATAIRES-CARTOGRAPHIE.md.

### Lot C — ⚠️ MAILINBLACK (cas particulier, attention)
Mailinblack (`*.mailinblack.com` en MX) n'est PAS un bounce classique : c'est un
**anti-spam à challenge-réponse**. Quand tu envoies, il peut :
- répondre par un mail de **demande de confirmation** ("cliquez ce lien pour prouver
  que vous êtes humain") AVANT de délivrer au vrai destinataire.
- Donc un envoi vers Mailinblack qui ne reçoit pas de bounce N'EST PAS forcément délivré
  — il est peut-être en attente de validation du challenge.
→ Robert : prévoir un **outil qui lit le mail de réponse (le challenge) et déclenche
le lien de validation (curl ou autre) pour faire passer le mail**. C'est jouable mais
délicat (chaque anti-spam a son format). Démarrer par : DÉTECTER Mailinblack (MX) →
classe `security_gateway` (cf ticket classification MX Option A) → débit ultra-prudent,
et lire les réponses de challenge pour les valider automatiquement. Identifier aussi les
AUTRES passerelles à challenge (Vade, Hornetsecurity, Proofpoint...) — voir si elles ont
le même pattern de validation. Lot exploratoire, à chiffrer après A+B.

## DoD
- [ ] Lot A : NDR Postfix → POST webhook smtp Notifuse → contact suppressé (test bout-en-bout sink)
- [ ] Return-Path/VERP câblé sur les envois cold (mapping bounce→contact)
- [ ] Lot B : pré-filtrage import sur email_verification.result (invalid/no_mx exclus)
- [ ] Lot C : Mailinblack détecté (MX→security_gateway) + POC lecture/validation challenge
- [ ] Jamais de renvoi à une adresse `bounced` (vérifié — déjà câblé, confirmer en E2E)
- [ ] Mesure : taux de bounce avant/après sur un envoi test

## Liens
- Classification MX (security_gateway pour Mailinblack/Vade...) : `todo/2026-06-14-classification-mx-table-patterns-option-A.md`
- Cartographie + email_verification : `docs/PROVIDERS-DESTINATAIRES-CARTOGRAPHIE.md`
- Skill Postfix : `~/.claude/skills/postfix/SKILL.md`
