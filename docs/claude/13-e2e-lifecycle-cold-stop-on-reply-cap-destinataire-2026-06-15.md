# E2E lifecycle cold — stop-on-reply + cap destinataire (2026-06-15)

Valide en E2E DÉROULÉ deux comportements cold que seul l'unitaire couvrait :
réponse reçue → contact `replied` → sortie de séquence (Lot 3/9) ; et 2e envoi
vers une adresse déjà au quota du jour → bloqué par le cap destinataire (V49).

Problème : ni `VeridianReplyService.ProcessInboundMessage` (appelé QUE par le
poller IMAP) ni le gate `veridianDailyCapGate` (worker, lit `message_history`
peuplé QUE par un vrai envoi SMTP) ne sont atteignables par HTTP. Et CONTRAINTE
Robert : zéro mail réel. Voie propre retenue (pas de mock mensonger, pas de
contournement) : **endpoint de test staging-only** `POST /api/veridian/admin/cold-simulate`
(HMAC Hub, 503 hors staging — même garde-fou que `test-tenants-stats`) qui frappe
le VRAI code métier sans transport IMAP ni SMTP.

- **Fichier veridian** : `internal/http/veridian_cold_simulate_handler.go` (+ test
  colocalisé). 6 modes (3 d'origine ci-dessous + 3 ajoutés par la batterie garde-fous
  2026-06-17 : `class_cap_decision`, `per_sender_cap_decision`, `sending_window_decision`) :
  - `inbound_reply` : seed un `message_history` "sent" puis passe un
    `VeridianIMAPMessage` (In-Reply-To = id de l'envoi) au VRAI `ProcessInboundMessage`
    → match fort Message-ID → signal `replied` + exit automations. Renvoie `has_replied`.
  - `seed_sent` : pose N (≤50) entrées `message_history` "sent" via le VRAI repo
    `Create` (secret key workspace = `workspace.Settings.SecretKey`, comme le worker).
  - `daily_cap_decision` : renvoie le COUNT réel (`CountSentSinceForContact` depuis
    minuit UTC) + `would_be_capped` = `count >= cap`, le **prédicat exact** du gate.
- **DI** : `VeridianHandler.SetColdSimulate(replyService, messageHistoryRepo, workspaceRepo, environment)`
  câblé dans `app.go` (staging-only). `coldSimulate` nil = 503.
- **Spec** : `tests/e2e-veridian/specs/cold-lifecycle.spec.ts` — `@prod-safe`
  (route montée + gate staging) + `@cold` mutation staging (provision jetable →
  réponse simulée → `replied` + event timeline `email.replied` ; cap : sous cap →
  pass, seed 1 envoi, au quota → bloqué, relèvement cap → repass). AUCUN mail réel.
- **Non-couvrable en E2E pur** (assumé honnêtement) : l'**exit ACTIF des automations**
  (passage `ContactAutomation` à `exited`/`replied`) n'est pas observé en E2E faute
  d'endpoint d'enrôlement de contact en automation (l'enrôlement passe par un trigger
  SQL, pas d'API). Couvert unitairement (`veridian_reply_service_test.go`). L'E2E
  valide le SIGNAL `replied` (source de vérité que le gate Lot 9 consomme) + l'event
  timeline. Le throttle MINUTE n'est pas re-testé ici (token-bucket RAM, déjà unitaire).

⚠️ **Diffs INLINE supplémentaires** (câblage API IMAP self-service) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/workspace.go` | +1 champ `CreateIntegrationRequest.IMAPSettings` + `UpdateIntegrationRequest.IMAPSettings` (`*IMAPSettings`, `json:"imap_settings,omitempty"`) ; +`case IntegrationTypeIMAP` dans `CreateIntegrationRequest.Validate` (le `default:` rejetait IMAP) + branche IMAP dans `UpdateIntegrationRequest.Validate` |
| `internal/service/workspace_service.go` | +`case IntegrationTypeIMAP` dans `CreateIntegration` (assigne `IMAPSettings`) ET `UpdateIntegration` (préserve `EncryptedPassword` si password clair non re-fourni) |

