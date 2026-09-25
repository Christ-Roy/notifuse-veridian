# KPI dashboard cold — engagement par classe + bounce hard/soft + reply tooltip (2026-06-16/17)

Trois KPI du dashboard mails (`console/src/components/analytics/`), pour piloter
la délivrabilité cold sans curl. Tickets `todo/done/2026-06-16-kpi-engagement-par-classe-provider.md`,
`...2026-06-16-kpi-bounce-hard-soft-dashboard.md`, `...2026-06-17-reply-kpi-ignore-message-type-filter.md`.

- **Engagement PAR CLASSE de provider destinataire** (🟡, endpoint neuf) :
  `POST/GET /api/veridian/messages.engagementByClass` agrège
  sent/delivered/bounced/opened/clicked par classe sur la fenêtre du dashboard.
  La classe n'est PAS une dimension de `message_history` (Lot 4) → le repo agrège
  par DOMAINE en SQL (`COUNT(*) FILTER`, indexé created_at), le service mappe
  domaine → classe en Go via `veridian_provider_class.go` (zéro CASE SQL, **pas de
  migration**). ⚠️ Classification par SUFFIXE (pas MX) → classes MX (ovh/ionos/…)
  tombent en `corporate` (même dégradation gracieuse que le breakdown R1 / le
  daily-cap classe ; documentée dans le tooltip du tableau). Auth JWT console +
  `contacts:read` (gardien service, comme breakdown R1 / reply stats). Fichiers
  veridian flat : `internal/domain/veridian_engagement_by_class.go`,
  `internal/repository/veridian_engagement_by_class_postgres.go`,
  `internal/service/veridian_engagement_by_class_service.go`,
  `internal/http/veridian_engagement_by_class_handler.go` (+ tests + mocks),
  câblé dans `app.go`. Front :
  `console/src/components/analytics/veridian_engagement_by_class.tsx` (tableau Ant
  Design, bounce >5% en rouge) + `console/src/services/api/veridian_engagement_by_class.ts`,
  monté dans `AnalyticsDashboard.tsx` sous `EmailMetricsChart`.
- **Bounce HARD vs SOFT** (🟢) : la colonne `message_history.bounce_type` EXISTAIT
  (v8) mais n'était **JAMAIS écrite** (le chemin bounce ne posait que `bounced_at`
  + `status_info`). Donc « +2 mesures analytics » seul = KPI mort (toujours 0). Voie
  propre (R0, pas de faux KPI, **pas de migration**) : on ÉCRIT le label typé
  `HardBounce`/`SoftBounce` dans `bounce_type` au moment du bounce, dérivé de
  `domain.ClassifyBounce` (helper veridian `VeridianBounceTypeLabel` dans
  `internal/domain/veridian_bounce_type.go`). ⚠️ Seuls les HARD posent `bounced_at`
  sur message_history (un soft transitoire n'est PAS terminal — poser `bounced_at`
  déclencherait les triggers `contact_lists.status='bounced'` + webhook = faux) →
  `count_bounced_hard` est fidèle, `count_bounced_soft` reste ~0 sur ce flux
  (assumé). Front : carte Bounced reste le TOTAL, split hard/soft révélé au tooltip.
- **Reply KPI ignore le filtre** (🔵, ~tooltip) : la carte Replies (livrée v53) est
  contact-level (`veridian_contact_reply`, pas rattaché à un envoi) → ne respecte
  PAS le Segmented All/Broadcasts/Transactional. Fix = tooltip explicite (option 1
  du ticket), zéro backend.

⚠️ **Diffs INLINE supplémentaires** (KPI dashboard cold) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/analytics.go` | +2 mesures schéma `message_history` : `count_bounced_hard` (`bounce_type ILIKE 'hard%'`), `count_bounced_soft` (`'soft%'`). Map de données, pas de func → pas de test colocalisé requis. |
| `internal/domain/message_history.go` | +1 champ `MessageEventUpdate.BounceType *string` (écrit sur `bounce_type` pour le groupe Bounced uniquement) |
| `internal/repository/message_history_postgre.go` | `SetStatusesIfNotSet` : sur le groupe Bounced, 4e colonne VALUES `bounce_type` + `bounce_type = COALESCE(message_history.bounce_type, updates.bounce_type)` (idempotent re-dispatch). Autres groupes : forme 3-colonnes inchangée. |
| `internal/service/inbound_webhook_event_service.go` | cas `BounceClassificationHard` : `MessageEventUpdate.BounceType = VeridianBounceTypeLabel(class)` (`"HardBounce"`). |

Fichiers veridian dédiés : `internal/domain/veridian_bounce_type.go` (+test),
`internal/domain/veridian_engagement_by_class.go` (+test),
`internal/repository/veridian_engagement_by_class_postgres.go` (+test),
`internal/service/veridian_engagement_by_class_service.go` (+test),
`internal/http/veridian_engagement_by_class_handler.go` (+test) + mocks
`mock_veridian_engagement_by_class_{repository,service}.go`. Front :
`console/src/components/analytics/{veridian_engagement_by_class.tsx,EmailMetricsChart.tsx,AnalyticsDashboard.tsx}`,
`console/src/services/api/veridian_engagement_by_class.ts`.

