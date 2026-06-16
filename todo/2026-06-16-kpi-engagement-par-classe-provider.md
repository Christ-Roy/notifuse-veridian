# KPI manquant : engagement (sent/delivered/bounced/opened) par CLASSE de provider destinataire

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse
> **Créé** : 2026-06-16
> **Axe audit** : KPI / Dashboard (audit cohérence post-sprint cold)

## Contexte

Tout le moteur cold est keyé par **classe de provider destinataire**
(`google` / `microsoft` / `yahoo_aol` / `freemail_fr` / `corporate` + 6 classes MX
ovh/ionos/apple_icloud/security_gateway/other_hoster/corporate_selfhost) :
throttle minute par classe, daily-cap par classe, pixel d'ouverture par classe,
round-robin sender par classe. Le pilotage de réputation cold se fait classe par
classe (on warm-up Google différemment d'OVH). **Or le dashboard mails est
totalement aveugle à la classe** : il agrège tous les providers ensemble.

Le SEUL endroit où la classe apparaît en UI = la carte « X contacts » du breakdown
R1 dans Settings → Cold outreach (compte de CONTACTS par classe, pour dimensionner
le throttle). Ce n'est PAS un KPI d'ENVOI/engagement : ça ne dit pas combien on a
envoyé/délivré/bounce/ouvert/répondu PAR classe sur une période. Sans ça, impossible
de voir « je grille Microsoft » (bounce rate Microsoft qui monte) avant que la
réputation soit cuite.

### Preuve du trou (code lu des deux bouts)

**Backend — ce qui existe** :
- Classification destinataire : `internal/domain/veridian_provider_class.go`
  (`ClassifyProviderClass`, suffixe) + `internal/domain/veridian_provider_class_mx.go`
  (MX réel, 11 classes, `VeridianAllProviderClasses()`).
- Breakdown CONTACTS par classe : `internal/http/veridian_contact_breakdown_handler.go`
  → `POST/GET /api/veridian/contacts.providerBreakdown` → compte les **contacts**
  (projection `email, custom_string_5` puis classification Go). PAS les envois.
- `message_history` (schéma analytics `internal/domain/analytics.go`) porte
  `sent_at/delivered_at/bounced_at/opened_at/clicked_at` + la dimension
  `contact_email`. **MAIS aucune dimension `provider_class`** : la classe n'est
  PAS stockée sur `message_history` (décision Lot 4 : classe dérivée à la lecture,
  pas matérialisée — cf. CLAUDE.md « dégradation gracieuse MX »). Donc le moteur
  analytics générique ne peut pas grouper par classe via une dimension SQL.

**Front — ce qui manque** :
- `console/src/components/analytics/EmailMetricsChart.tsx` : un `Segmented`
  All / Broadcasts / Transactional, puis 8 cartes globales. **Aucun découpage par
  classe de provider.**
- Aucun composant front ne montre un tableau « classe × {sent,delivered,bounced,opened} ».

## Demande précise

Ajouter au dashboard un **tableau (ou des barres) « engagement par classe de
provider destinataire »** sur la fenêtre de dates : pour chaque classe, le compte
`sent / delivered / bounced / opened / clicked` (+ reply si le ticket reply est
livré). Permet de repérer la classe qui se dégrade.

### Chemin recommandé

La classe n'est PAS sur `message_history` → deux options, trancher par le lead :

**Option A (recommandée, pas de migration) — agrégation Go par domaine** :
- Nouvel endpoint `POST/GET /api/veridian/messages.engagementByClass`
  (`internal/http/veridian_engagement_by_class_handler.go`, modèle =
  `veridian_contact_breakdown_handler.go`, auth JWT + `contacts:read`).
- Repo : `SELECT lower(split_part(contact_email,'@',2)) AS domain,
  COUNT(*) FILTER (WHERE sent_at IS NOT NULL) AS sent, ... FROM message_history
  WHERE created_at BETWEEN $1 AND $2 GROUP BY 1`. Le service mappe chaque domaine
  → classe via la table de domaines `veridian_provider_class.go` (réutiliser, NE PAS
  dupliquer en CASE SQL — même posture que le breakdown R1) et agrège par classe en
  Go. ⚠️ Les classes MX (ovh/ionos/…) ne sont dérivables que par MX, pas par suffixe
  → elles tomberont en `corporate` dans cet agrégat suffixe-only (même dégradation
  gracieuse que le daily-cap classe ; documenter, c'est acceptable pour un dashboard).
- Front : composant `console/src/components/analytics/veridian_engagement_by_class.tsx`
  (tableau Ant Design : 1 ligne par classe, colonnes sent/delivered/bounce%/open%),
  monté dans `AnalyticsDashboard.tsx` sous `EmailMetricsChart`.

**Option B (exact mais coûteux) — matérialiser la classe** :
- Ajouter colonne `veridian_provider_class` à `message_history` (migration additive,
  posée à l'enqueue via `EmailQueuePayload`) + dimension analytics `provider_class`
  dans le schéma `message_history` (`internal/domain/analytics.go`). Le dashboard
  pourrait alors grouper nativement. PLUS lourd (migration tier 🔴 + backfill) — à
  ne faire QUE si l'option A (suffixe) s'avère insuffisante en pratique.
  Le lead avait déjà tranché « matérialiser SI/quand mesuré nécessaire » (cf.
  CLAUDE.md Lot 4 + v49.go) → rester en option A par défaut.

## Fichiers concernés (exacts)

| Rôle | Fichier |
|---|---|
| Classification (réutiliser) | `internal/domain/veridian_provider_class.go`, `internal/domain/veridian_provider_class_mx.go` |
| Schéma analytics (constat) | `internal/domain/analytics.go` (pas de dimension classe sur message_history) |
| Modèle endpoint | `internal/http/veridian_contact_breakdown_handler.go`, `internal/service/veridian_contact_breakdown_service.go`, `internal/repository/veridian_contact_breakdown_postgres.go` |
| Handler à créer | `internal/http/veridian_engagement_by_class_handler.go` (+ test) |
| Service à créer | `internal/service/veridian_engagement_by_class_service.go` (+ test) |
| Repo à créer | `internal/repository/veridian_engagement_by_class_postgres.go` (+ test) |
| Câblage | `internal/app/app.go` (bloc « R1 breakdown contacts ») |
| Front | `console/src/components/analytics/veridian_engagement_by_class.tsx` (créer), `console/src/components/analytics/AnalyticsDashboard.tsx` (monter) |

## Impact tunnel de vente

Le pilotage cold se fait par classe (réputation IP/domaine vis-à-vis de Google vs
Microsoft vs petits FAI). Sans vue d'engagement par classe, Robert ne peut pas
détecter une classe qui se dégrade (bounce qui grimpe sur Microsoft = signal d'arrêt
AVANT de griller le domaine). C'est le tableau de bord opérationnel du warm-up.
Moins urgent que le reply (qui est nommé explicitement), mais directement utile pour
gérer la délivrabilité au quotidien.

## Notes pour l'implémenteur

- Tier 🟡 en option A (lecture seule, additif). Option B = tier 🔴 (migration).
- Réutiliser STRICTEMENT la table de domaines de `veridian_provider_class.go` côté
  service Go ; ne jamais re-coder le mapping en SQL (source de vérité unique).
- Si le ticket reply (`2026-06-16-kpi-reply-rate-dashboard.md`) est livré, ajouter
  une colonne reply au tableau par classe (join `veridian_contact_reply` par domaine).
