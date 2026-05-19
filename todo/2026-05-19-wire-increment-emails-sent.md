# 2026-05-19 — Wire `planRepo.IncrementEmailsSent` dans le pipeline d'envoi mail

> **Sévérité** : 🟠 P1 fonctionnel (bug latent, masqué)
> **Effort** : M (4-6h)
> **Découvert pendant** : session attaque tickets contrat Hub v1.3 (chantier webhooks-manquants)

## Contexte

`internal/repository/veridian_plan_postgres.go:201` définit `IncrementEmailsSent(ctx, workspaceID, delta)` mais **personne ne l'appelle dans le code** :

```bash
$ grep -rn "IncrementEmailsSent" --include="*.go" /home/brunon5/Bureau/veridian-platform/notifuse-veridian/ | grep -v "_test.go\|mocks"
internal/repository/veridian_plan_postgres.go:199:// (commentaire)
internal/repository/veridian_plan_postgres.go:201:func ...
internal/domain/veridian.go:148:    IncrementEmailsSent(...)  // interface
```

**Conséquences en prod aujourd'hui** :

1. `veridian_plan.emails_sent_this_month` reste à `0` pour TOUS les tenants
2. Le paywall middleware (`internal/http/middleware/veridian_paywall.go`) check `IsBlocked()` qui inclut `emails_sent_this_month >= monthly_email_quota` → **ne bloque jamais sur le quota**
3. `UsageSummaryResponse.MessagesSent30d` est toujours `0` (puisque c'est un proxy sur `emails_sent_this_month` pour MVP)
4. Le ticket `tenant.quota_exceeded` (sec. 7.1) ne peut pas émettre — pas de crossing détectable
5. Le rate-limiting Veridian est **désarmé** côté Notifuse — n'importe quel tenant peut envoyer ∞ emails sans déclenchement

## Pipeline d'envoi mail à patcher

Le `MessageHistoryRepository.Create` est appelé une fois par email envoyé. 4 callsites :

- `internal/service/transactional_service.go:874`
- `internal/service/demo_service.go:1745`
- `internal/service/broadcast_service.go:1079`
- `internal/service/broadcast/message_sender.go:650` (worker async — envois mass)

## Options d'implémentation

### Option 1 (recommandée) — Décorateur sur `MessageHistoryRepository`

Créer `internal/repository/veridian_message_history_decorator.go` qui wrap le `messageHistoryRepo` upstream et appelle `planRepo.IncrementEmailsSent` après chaque `Create` réussi.

```go
type veridianMessageHistoryDecorator struct {
    upstream domain.MessageHistoryRepository
    planRepo domain.VeridianPlanRepository
}

func (d *veridianMessageHistoryDecorator) Create(ctx, wsID, secret, msg) error {
    if err := d.upstream.Create(ctx, wsID, secret, msg); err != nil {
        return err
    }
    // Best-effort : log mais ne bloque pas l'envoi.
    if err := d.planRepo.IncrementEmailsSent(ctx, wsID, 1); err != nil && d.logger != nil {
        d.logger.Warn("veridian increment quota failed", ...)
    }
    return nil
}
```

Wirer dans `internal/app/app.go` en remplaçant l'injection actuelle de `messageHistoryRepo` par le décorateur.

**Avantages** :
- Pas de patch upstream Notifuse (convention `veridian_*` respectée)
- Testable en isolation avec gomock
- Best-effort par design (échec increment ne bloque pas l'envoi)

**Risques** :
- Race sur le counter (concurrent envois mass). `UPDATE veridian_plan SET emails_sent_this_month = emails_sent_this_month + $2` est atomique côté Postgres donc OK pour la simple addition. Le check `IsBlocked()` reste susceptible au TOCTOU mais c'est acceptable pour un quota (overspend marginal de quelques mails sur burst concurrent).

### Option 2 — Hook dans `EmailService.SendEmail`

Ajouter un callback `OnEmailSent` dans `EmailService` que le code Veridian peut brancher.

**Inconvénient** : patch upstream minimal mais quand même une exception à la règle "ne JAMAIS patcher directement un fichier upstream".

### Option 3 — Worker dédié sync

Cron qui scan `message_history` chaque 60s et sync le compteur.

**Inconvénient** : latence quota, coût DB, double source de vérité.

## Tests à ajouter

1. Décorateur unit test : mock upstream + planRepo, vérifier que `IncrementEmailsSent` est appelé exactement 1 fois après chaque `Create` réussi, 0 fois après une erreur upstream.
2. Test intégration : provision tenant, envoyer N mails, vérifier que `emails_sent_this_month == N`.
3. Test paywall : provision tenant free (500 quota), envoyer 501 mails, vérifier que le 501e est bloqué 402 par le paywall middleware.

## Lien tickets

- Débloque `2026-05-19-webhooks-manquants.md` (pour émission `tenant.quota_exceeded`)
- Bug latent sécu (paywall désarmé) → priorité montée P1 fonctionnel
