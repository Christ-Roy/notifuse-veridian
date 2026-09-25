# FIX P1 — warmup = cap TOTAL par infra (toutes classes, MX compris) (2026-06-19)

Le warmup progressif (ci-dessus) prétendait plafonner le **volume TOTAL** émis par une
infra (« J1 = 5 max, toutes classes »), mais l'implémentation d'origine **réutilisait le
COUNT-par-classe** (`veridianCountClassForInfra` → `CountSentSinceForDomains*`). Deux bugs
qui rendaient la rampe inutile pour son objectif (audit cohérence cold). Spec :
`todo/done/2026-06-19-audit-warmup-cap-non-enforce-mx-et-total.md` (P1, tier 🔴).

- **Bug 1 — cap PAR CLASSE, pas TOTAL** : le COUNT était filtré par les domaines de la
  classe du destinataire courant → une infra en warmup J1 (cap=5) envoyait `5 × nombre de
  classes` (5 google + 5 microsoft + 5 freemail…), pas 5 au total. Le « volume TOTAL »
  documenté n'était jamais calculé.
- **Bug 2 (plus grave) — bypass TOTAL sur classes MX** : `VeridianDomainsForClass` renvoie
  `[]` pour les 6 classes MX (ovh/ionos/apple_icloud/security_gateway/other_hoster/
  corporate_selfhost) → `COUNT … domain = ANY('{}')` = 0 → jamais ≥ cap → **jamais
  enforcé**. Or la MAJORITÉ des leads B2B cold résolvent en classe MX (cf. cartographie
  providers) → une IP fraîche blastait SANS plafond vers exactement la population à
  protéger. Inverse du but.

- **Voie propre** : le warmup est conceptuellement un cap TOTAL par infra (par DOMAINE
  d'envoi), **indépendant de la classe destinataire** (qui est justement le point faible
  sur MX). Il ne passe donc PLUS par le COUNT-par-classe :
  - **Nouvelle méthode repo** `CountSentSinceForSenderDomain(workspaceID, senderDomain,
    since)` = `COUNT(*) WHERE lower(split_part(veridian_sender_email,'@',2)) = senderDomain
    AND sent_at >= since` — **AUCUN filtre de classe destinataire** (c'est ce qui le rend
    robuste aux MX). Réutilise l'infra V53 (colonne `veridian_sender_email` + index
    partiel). Décorateur quota passthrough + mock régénéré (à la main, mockgen cassé).
  - **Gate** (`veridian_daily_cap.go`) : quand `warmupCap > 0`, branche WARMUP dédiée qui
    compte le TOTAL par domaine émetteur et compare à `warmupCap`, puis **court-circuite**
    (le cap-classe statique n'est PAS aussi évalué — le warmup gouverne le plafond de
    l'infra pendant la rampe). Hors warmup (`warmupCap == 0`), le cap-classe statique
    garde son comportement antérieur EXACT (dégradation MX assumée OK pour lui).
  - **Best-effort inchangé** : erreur COUNT = pass. Sender vide (legacy/pré-V53) = pas
    d'attribution infra → warmup non enforçable (pass documenté).
  - `veridianWarmupClassCap` renommée `veridianWarmupCap` (ce n'est plus un cap *de
    classe*). **Pas de migration, pas de bump VERSION** (la colonne V53 suffit ; le COUNT
    filtre déjà `sent_at` indexé + préfixe sender_email indexé — décision « COUNT live,
    pas d'agrégat », cf. v53.go).

- **Validation E2E ON-PREMISE (tier 🔴)** : `cold-simulate` étendu d'un mode
  `warmup_cap_decision` (`CountSentSinceForSenderDomain(senderDomain) >= warmup_cap`, le
  prédicat EXACT de la branche warmup) + harness `scripts/e2e/cold-warmup-total.sh`
  (workspace jetable vierge → seed 1 envoi google + 1 envoi ovh-MX depuis `infra-a`
  → 3e envoi n'importe quelle classe = `would_be_capped=true` à cap=2 ; infra-b séparée
  reste sous cap ; recoupé par COUNT psql). ZÉRO mail externe. Le gate worker réel
  consomme le même `CountSentSinceForSenderDomain` → le prédicat testé EST le code prod.

⚠️ **Diffs INLINE supplémentaires** (fix warmup total) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/message_history.go` | +1 méthode interface `MessageHistoryRepository.CountSentSinceForSenderDomain` (COUNT TOTAL par domaine émetteur, sans filtre classe destinataire) |
| `internal/repository/message_history_postgre.go` | +impl `CountSentSinceForSenderDomain` (COUNT `lower(split_part(veridian_sender_email,'@',2)) = lower($2)` ; senderDomain vide = 0 sans requête) |
| `internal/repository/veridian_message_history_decorator.go` | +passthrough `CountSentSinceForSenderDomain` (lecture, zéro side-effect quota) |

Fichiers veridian touchés : `internal/service/queue/veridian_daily_cap.go` (branche warmup
= cap TOTAL via `CountSentSinceForSenderDomain` + court-circuit du cap-classe ; helper
renommé `veridianWarmupCap`), `internal/domain/veridian_warmup.go` (doc corrigée : plafond
holistique total, pas par classe), `internal/http/veridian_cold_simulate_handler.go` (mode
`warmup_cap_decision`) + tests colocalisés + mock `mock_message_history_repository.go`
régénéré. Harness `scripts/e2e/cold-warmup-total.sh`. **Pas de migration, pas de bump
`config.VERSION`** (la colonne V53 suffit).

