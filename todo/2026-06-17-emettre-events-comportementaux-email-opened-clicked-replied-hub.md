# [NOTIFUSE] 🟡 P1 — Émettre les events comportementaux `email.opened/clicked/replied` vers le Hub (le réconciliateur est orphelin sans)

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-17 (audit cohérence réconciliateur, agent audit-crossapp)

## TL;DR
Le Hub a livré en prod (Lot 1) un réconciliateur de scoring prospect qui **attend**
des events `email.opened`, `email.clicked`, `email.replied` émis par Notifuse.
**Notifuse ne les émet pas** — ils n'existent même pas comme `VeridianEvent`.
Résultat : 0 row dans `prospect_events`/`prospect_scores` en prod. Backend
orphelin. Ce ticket = câbler l'émission.

## Preuve (vérifiée des deux bouts, 2026-06-17)

### Côté Hub : le récepteur est PRÊT et attend ces 3 events
- `app/api/webhooks/notifuse/route.ts` (voie legacy HMAC, `dispatchLegacyEvent`)
  a un `case 'email.opened' | 'email.clicked' | 'email.replied'` qui appelle
  `ingestProspectEvent(...)`.
- `lib/webhooks/notifuse-handlers.ts` (voie v1.4 Bearer) a aussi les 3 handlers
  `email.opened/clicked/replied`.
- Contrat gravé : `veridian-hub/docs/CONTRAT-HUB.md §7.5` — qui affirme (à tort)
  « C'est par là que le fork Notifuse émet **aujourd'hui** ses
  `email.opened/clicked/replied` » en pointant `internal/service/veridian_webhook_emitter.go`.

### Côté Notifuse : ces events ne sont JAMAIS émis
- Liste exhaustive des constantes `VeridianEvent` (`internal/domain/veridian.go:1009+`,
  `veridian_freeze.go`, `veridian_rotate_transfer.go`) :
  `tenant.{provisioned,suspended,resumed,deleted,soft_deleted,restored,purged,touched,plan_changed,owner_changed,member_added,member_removed,member_role_changed,member_frozen,member_unfrozen,api_key_rotated,activity_threshold_reached,quota_exceeded}`,
  `email.{sent,bounced,complaint}`.
  → **Aucun `email.opened`, `email.clicked`, `email.replied`.**
- Les 21 call-sites réels de `emitter.Emit(...)` (grep `\.Emit(` hors tests/mocks)
  ne couvrent que les events ci-dessus. Aucun n'émet open/click/reply.
- La donnée EXISTE pourtant en interne :
  - `/t/{token}` → `handleEncryptedOpen` → `emailService.OpenEmail` →
    `messageRepo.SetOpened(...)` (DB Notifuse). **Pas d'Emit.**
    (`internal/http/email_handler.go:260` + `internal/service/email_service.go:223`)
  - `/r/{token}` → `handleEncryptedClick` → `emailService.VisitLink` →
    `messageRepo.SetClicked(...)` (DB Notifuse). **Pas d'Emit.**
    (`internal/http/email_handler.go:316` + `internal/service/email_service.go:212`)
  - Reply : tracking inbound présent (`InboundWebhookEvent`,
    `internal/service/demo_service.go` + repo inbound) mais aucun Emit
    comportemental vers le Hub.
  → La donnée open/click/reply reste **captive de Notifuse**, jamais propagée.

## Demande précise (quoi coder, où)

Émettre les 3 events comportementaux via le **`VeridianWebhookEmitter` existant**
(ne PAS créer un nouveau canal — réutiliser l'infra HMAC déjà en prod, voie
legacy que le Hub consomme aujourd'hui).

1. **Déclarer les constantes** dans `internal/domain/veridian.go` (bloc `VeridianEvent`) :
   ```go
   EventEmailOpened  VeridianEvent = "email.opened"
   EventEmailClicked VeridianEvent = "email.clicked"
   EventEmailReplied VeridianEvent = "email.replied"
   ```
2. **Émettre depuis les points de tracking** déjà existants (la donnée y est déjà) :
   - `EmailService.OpenEmail` (`internal/service/email_service.go:223`) : après
     `SetOpened` réussi → `s.emitter.Emit(ctx, EventEmailOpened, workspaceID, data)`.
   - `EmailService.VisitLink` (`:212`) : après `SetClicked` réussi →
     `Emit(ctx, EventEmailClicked, workspaceID, data)`.
   - Tracking reply (inbound webhook event delivered) → `Emit(ctx, EventEmailReplied, ...)`.
   - ⚠️ `EmailService` n'a pas (encore) de champ `emitter` : il faut l'injecter
     (cf comment `veridianWebhookEmitter` est injecté dans les autres services via
     `internal/app/app.go:1117`). Garder le pattern best-effort/noop si Hub non configuré.
3. **Format du `data`** (le Hub lit `data.contact_email`, `data.vid`, `data.occurred_at`,
   et utilise `event_id` comme idempotency_key — cf `CONTRAT-HUB.md §7.5.1/§7.5.2`) :
   ```go
   map[string]interface{}{
     "contact_email": <email du contact du message>,  // ⚠️ CLÉ DE JOINTURE V1 — obligatoire pour scorer
     "message_id":    messageID,
     "occurred_at":   time.Now().UTC().Format(time.RFC3339),
     "link_url":      redirectTo, // pour clicked uniquement
     // "vid": <plus tard, cf ticket vid ci-dessous>
   }
   ```
   → **Le `contact_email` est NON optionnel pour que le scoring marche** : sans lui,
   le Hub ingère l'event pour forensics mais ne déplace AUCUN score (jointure V1 par email).
   Récupérer l'email via `messageRepo` à partir de `messageID`/`workspaceID`.
4. **Bot/timing** : respecter la logique anti-bot déjà en place dans les handlers
   (`IsBotUserAgent`, open/click < 7s ignoré). N'émettre QUE quand `shouldRecord == true`,
   pour ne pas polluer le score avec des opens proxy/bot.

## Impact business (débloque quoi)
Sans ces events, **le tunnel cold de Robert n'a aucun signal d'engagement remonté** :
impossible de savoir quel prospect a ouvert/cliqué/répondu, donc impossible de
prioriser les prospects chauds. Avec, le Hub commence à scorer (open +1, click +5,
reply +20) et le réconciliateur sort de l'état orphelin (0 row → vraies données).

## Dépendances / ordre
- **Indépendant et livrable seul** : le scoring par `contact_email` (jointure V1)
  marche dès que ces 3 events arrivent. Pas besoin du `vid` pour ce lot.
- Le `vid` (clé de jointure forte cold↔web) est un **lot 2 séparé** — voir ticket
  `2026-06-17-propager-vid-deterministe-liens-tracking-hub.md` dans ce même `todo/`.
- Voie de transport : utiliser la **legacy HMAC** existante (le Hub la consomme déjà).
  Migration v1.4 Bearer = chantier distinct, non bloquant.

## Vérif d'acceptation
1. Staging Notifuse : ouvrir un email tracké (`/t/`), cliquer un lien (`/r/`).
2. `docker logs` Notifuse → `veridian webhook: event delivered event_type=email.opened`.
3. Hub staging : `SELECT * FROM hub_app.prospect_events WHERE app='notifuse'` → rows présentes.
4. `SELECT engagement_score, signals FROM hub_app.prospect_scores` → score incrémenté
   (+1 open, +5 click) sur la ligne `(workspace_slug, contact_email)`.
