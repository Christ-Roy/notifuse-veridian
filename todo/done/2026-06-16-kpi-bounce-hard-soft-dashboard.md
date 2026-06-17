# KPI manquant : bounce HARD vs SOFT non distingué au dashboard (donnée déjà en DB)

> **Sévérité** : 🟢 P2
> **Owner** : agent notifuse
> **Créé** : 2026-06-16
> **Axe audit** : KPI / Dashboard (audit cohérence post-sprint cold)

## Contexte

Le bounce-loop cold (Lot 2, 2026-06-15) classe finement les bounces **hard vs soft**
via le code DSN du NDR Postfix (`internal/domain/bounce_classification.go`,
`classifyDSNCode` : 5.x.x → Hard, 4.x.x → Soft). En cold outreach la distinction est
critique : un **hard bounce** = adresse morte (réputation grillée immédiatement, on
supprime) ; un **soft bounce** = transitoire (boîte pleine, greylisting). Piloter une
campagne cold sans séparer les deux, c'est confondre « ma liste est pourrie » (hard)
et « le serveur distant rate-limite » (soft).

**La donnée est stockée** (`message_history.bounce_type`, colonne existante) **mais le
dashboard agrège tout dans un seul compteur `count_bounced`.** C'est un trou quasi
gratuit à combler : la colonne existe, il manque juste les mesures analytics + les
cartes front.

### Preuve du trou (code lu des deux bouts)

**Backend — la donnée existe** :
- Colonne `message_history.bounce_type VARCHAR(100)` (`internal/database/init.go:242`),
  alimentée par la classif bounce. Le trigger timeline la propage déjà
  (`internal/database/init.go:692`).
- Classification hard/soft : `internal/domain/bounce_classification.go` (helper
  `classifyDSNCode`, enrichi Lot 2 pour le SMTP/cold).

**Backend — le schéma analytics ne l'expose pas finement** :
- `internal/domain/analytics.go`, schéma `message_history` : une seule mesure bounce,
  `count_bounced` (`{SQL: "bounced_at IS NOT NULL"}`). **Pas de `count_bounced_hard`,
  pas de `count_bounced_soft`, et `bounce_type` n'est PAS déclaré en dimension** (les
  dimensions listées s'arrêtent à `created_at/sent_at/contact_email/broadcast_id/
  channel/template_id/external_id/transactional_notification_id`).

**Front — affichage agrégé** :
- `console/src/components/analytics/EmailMetricsChart.tsx` : 1 seule carte
  « Bounced » (`count_bounced`). Pas de split hard/soft.

## Demande précise

Distinguer hard vs soft bounce dans le dashboard. **Aucune migration nécessaire** :
la colonne `bounce_type` existe déjà, il suffit d'ajouter des mesures au schéma
analytics et des cartes au front.

### Chemin recommandé (le plus léger des 3 tickets KPI)

**1. Backend — ajouter 2 mesures au schéma analytics** dans
   `internal/domain/analytics.go`, schéma `message_history`, à côté de `count_bounced` :
   ```go
   "count_bounced_hard": {
       Type: "count", Title: "Hard Bounces", SQL: "*",
       Filters: []analytics.MeasureFilter{
           {SQL: "bounced_at IS NOT NULL"},
           {SQL: "bounce_type ILIKE 'hard%'"},   // ⚠️ confirmer la valeur exacte écrite par classifyDSNCode (grep les valeurs posées dans bounce_classification.go / le processeur webhook SMTP)
       },
   },
   "count_bounced_soft": {
       Type: "count", Title: "Soft Bounces", SQL: "*",
       Filters: []analytics.MeasureFilter{
           {SQL: "bounced_at IS NOT NULL"},
           {SQL: "bounce_type ILIKE 'soft%'"},
       },
   },
   ```
   ⚠️ **AVANT de coder le filtre** : grepper la valeur EXACTE écrite dans
   `bounce_type` (chaîne « Hard »/« Soft »/« hardbounce »/code DSN ?) côté
   `internal/service/*` (processeur webhook SMTP / `MarkEmailsAsBounced`) — ne PAS
   deviner le littéral. C'est `internal/domain/analytics.go` qui est upstream-pur :
   l'ajout de mesures est une extension INLINE → re-vérifier au prochain sync upstream
   (documenter dans le tableau « Diffs INLINE » du CLAUDE.md Notifuse).
   `internal/domain/analytics.go` n'a PAS de test colocalisé veridian aujourd'hui
   (c'est une map de données) ; si le check-test-mapping bloque, c'est couvert par
   le test du repo analytics — déclarer via `test-coverage-map.yaml` avec `reason:`.

**2. Front — split la carte Bounced** dans
   `console/src/components/analytics/EmailMetricsChart.tsx` :
   - Ajouter `count_bounced_hard` / `count_bounced_soft` aux `measures` des queries
     (`buildQuery` + `buildStatsQuery`) et à `visibleLines`.
   - Remplacer la carte « Bounced » par deux cartes (« Hard » rouge / « Soft »
     orange) ou garder « Bounced » total + tooltip détaillant hard/soft. Au choix
     UI, mais la distinction hard/soft DOIT être lisible.
   - ⚠️ Lingui : libellés littéraux `` t`...` `` dans le composant (piège extraction
     documenté).

## Fichiers concernés (exacts)

| Rôle | Fichier |
|---|---|
| Donnée (colonne) | `internal/database/init.go:242` (`bounce_type`) |
| Classif hard/soft | `internal/domain/bounce_classification.go` |
| Valeur exacte posée | grep dans `internal/service/` (webhook SMTP / MarkEmailsAsBounced) avant de coder le filtre |
| Mesures analytics (étendre) | `internal/domain/analytics.go` (schéma `message_history`) |
| Front | `console/src/components/analytics/EmailMetricsChart.tsx` |

## Impact tunnel de vente

Hard bounce = liste à nettoyer / sourcing à revoir (réputation immédiate). Soft
bounce = ajuster le rythme/warm-up. Les confondre fausse le diagnostic de
délivrabilité. Gain élevé pour un coût faible (pas de migration, colonne déjà là).
Sévérité 🟢 car c'est un raffinement d'un KPI déjà présent (bounced), pas un KPI
totalement absent comme le reply.

## Notes pour l'implémenteur

- Tier 🟢 BAS (extension de mesures analytics + cartes front, additif, pas de
  surface API auth, pas de migration) → éligible `[risk:low]` SI et seulement si la
  validation du rendu réel est faite (memory `feedback_skip_prod_pour_valider_UI`).
- Quick win possible : ce ticket peut être groupé avec le ticket reply dans la même
  vague UI dashboard (même composant `EmailMetricsChart.tsx`).

---

## ✅ LIVRÉ — 2026-06-17 (agent dashboard-kpi)

**Prémisse du ticket corrigée** : `message_history.bounce_type` EXISTE mais
n'était JAMAIS écrite (le chemin bounce ne posait que `bounced_at` + `status_info`).
« +2 mesures analytics » seul aurait donné un KPI mort (toujours 0). Voie propre
R0, zéro migration : on ÉCRIT désormais le label typé.

- `internal/domain/veridian_bounce_type.go` (+test) : `VeridianBounceTypeLabel`
  (`HardBounce`/`SoftBounce`) dérivé de `domain.ClassifyBounce`.
- `MessageEventUpdate.BounceType *string` (message_history.go) écrit sur
  `bounce_type` pour le groupe Bounced dans `SetStatusesIfNotSet`
  (message_history_postgre.go, COALESCE idempotent). Renseigné au cas
  `BounceClassificationHard` dans `inbound_webhook_event_service.go`.
- 2 mesures analytics `count_bounced_hard`/`_soft` (analytics.go, ILIKE 'hard%'/'soft%').
- Front : carte Bounced = TOTAL, split hard/soft au tooltip (EmailMetricsChart.tsx).
- Seuls les HARD posent bounced_at → count_bounced_hard fidèle, soft ~0 sur ce
  flux (assumé : un soft transitoire n'est pas terminal, poser bounced_at
  déclencherait à tort la suppression contact + webhook). Documenté.
- Tests repo (SetStatusesIfNotSet) mis à jour à la nouvelle forme SQL + 1 test neuf
  bounce_type typé. Build + tests verts.
