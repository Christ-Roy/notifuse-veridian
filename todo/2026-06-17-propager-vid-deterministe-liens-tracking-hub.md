# [NOTIFUSE] 🟢 P2 — Propager le `vid` (ID prospect déterministe) dans les events + liens de tracking

> **Sévérité** : 🟢 P2 (étage 2 — la jointure V1 par `contact_email` marche sans)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-17 (audit cohérence réconciliateur, agent audit-crossapp)

## TL;DR
Le réconciliateur du Hub joint cold↔web par `contact_email` au V1, mais la clé
FORTE prévue est le `vid` (ID prospect déterministe propagé cross-app). Côté
Notifuse, **le `vid` n'existe nulle part** : ni dans le contact, ni dans les liens
`/t/` `/r/`, ni dans les events. Sans lui, un `page.hit` Analytics anonyme ne se
rattachera jamais à un prospect Notifuse. Ce ticket = étage 2, à faire APRÈS le
ticket events comportementaux (`2026-06-17-emettre-events-comportementaux-...`).

## Preuve (vérifiée 2026-06-17)
- Grep `vid`/`VeridianID`/`prospect_id` sur tout `internal/` côté Notifuse :
  **0 résultat réel** (seuls hits = `veridian_idempotency`, faux positifs `valid`).
- Le token de tracking (`pkg/crypto/crypto.go` `EncryptTrackingToken`) encode
  aujourd'hui `messageID\nworkspaceID\nts[\nredirectTo]` (cf parsing dans
  `internal/http/email_handler.go:275` et `:330`). **Pas de slot vid.**
- Le contrat Hub `docs/CONTRAT-HUB.md §7.5.1` marque `vid` comme « ⏳ étage 2 »,
  nullable au V1, et §7.5.4 confirme : *« Le vid déterministe partagé propagé dans
  les liens de tracking Notifuse + capté par Analytics : pas câblé. »*
- Schéma Hub `prospect_events.vid` est nullable, jointure V1 par `contact_email`
  (`lib/prospect/ingest.ts`). Le Hub backfill le `vid` sur le score quand il l'apprend.

## ⚠️ Dépendance amont (pas Notifuse-only)
Le `vid` doit être **généré par le Hub** (source d'identité prospect cross-app —
cf ticket Hub `2026-06-15-reconciliateur-events-cold-web-prospect-scoring.md` :
« Le Hub est la source du vid propagé aux apps »). **Ce mécanisme de génération
n'existe pas encore côté Hub** (voir ticket Hub miroir de l'audit). Notifuse ne
peut propager un vid que si :
1. Le Hub définit comment un `vid` est dérivé/attribué à un prospect (ex : hash
   déterministe de l'email + un sel partagé, ou attribution explicite à l'envoi).
2. Notifuse reçoit/calcule ce `vid` au moment de la composition de l'email cold.

→ **Bloqué tant que le Hub n'a pas tranché la stratégie vid.** Ce ticket trace le
besoin côté émetteur ; ne PAS démarrer avant la décision Hub.

## Demande précise (quand débloqué)
1. **Calculer/recevoir le `vid`** au moment de l'envoi d'un email cold (par contact).
2. **L'embarquer dans le token de tracking** : étendre le plaintext encodé par
   `EncryptTrackingToken` pour inclure le vid (`messageID\nworkspaceID\nts\nvid[\nredirectTo]`),
   et le lire au décodage dans `handleEncryptedOpen`/`handleEncryptedClick`.
3. **L'ajouter au `data` des events** comportementaux émis vers le Hub
   (`data.vid`) — le Hub le lit déjà (`ingestProspectEvent` champ `vid`).
4. **L'exposer dans l'URL de destination** (`/r/` redirect → site client) sous une
   forme captable par Analytics (ex query param `?vid=...`), pour qu'Analytics le
   pose dans son `page.hit`. ⚠️ Coordonner le NOM du param avec le ticket Analytics
   `veridian-analytics/todo/2026-06-17-emettre-page-hit-vid-vers-hub.md`.

## Impact business
Sans vid : la corrélation cold↔web repose sur l'email seul → un visiteur web
anonyme (pas encore identifié par email) ne booste pas le score du prospect.
Avec vid : « ce prospect a cliqué le cold ET visité /audit dans la foulée » →
confiance forte → priorisation CRM. C'est le cœur de la valeur du réconciliateur.

## Dépendances
- ⛔ BLOQUÉ PAR : décision Hub sur la génération/format du vid (ticket Hub).
- Couplé à : ticket Analytics page.hit+vid (nom du query param à aligner).
- Vient APRÈS : ticket Notifuse events comportementaux (qui marche sans vid).
