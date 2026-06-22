# Audit cold — warmup progressif : cap non enforcé sur classes MX + cap PAR CLASSE au lieu de TOTAL infra

> **Sévérité** : 🟡 P1 (réputation IP en warm-up — le scénario que la feature existe pour protéger)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-19
> **Source** : audit de cohérence cold (axe 1 cascades + axe 5 classification MX)

## Contexte

Le warmup progressif (2026-06-17, `internal/domain/veridian_warmup.go` +
`internal/service/queue/veridian_daily_cap.go`) est censé, d'après sa propre
doc (`veridian_warmup.go:22-25` et CLAUDE.md) :

> « Le cap warmup, quand il est actif sur une infra, PRIME sur le cap-classe
> statique et s'applique UNIFORMÉMENT à toutes les classes. Un warmup IP
> **plafonne le volume TOTAL émis par l'IP**, pas une classe en particulier. »

C'est l'intention correcte d'un warm-up Lemlist/Instantly : J1 = 5 mails MAX,
toutes classes confondues.

## Bug 1 — le cap warmup n'est PAS un cap TOTAL, c'est un cap PAR CLASSE

`veridian_daily_cap.go:157-178` : quand `warmupCap > 0`, le code applique
`classCap = warmupCap` puis compte via
`veridianCountClassForInfra(... VeridianDomainsForClass(class) ...)`.

Le COUNT est donc **filtré par les domaines de LA classe du destinataire
courant**. Conséquence : le cap de 5 s'applique **indépendamment à chaque
classe**. Une infra en warmup J1 (cap=5) peut envoyer :

- 5 vers google + 5 vers microsoft + 5 vers freemail_fr + … = **5 × nombre de
  classes**, pas 5 au total.

Le COUNT pour la classe `google` ne voit jamais les envois `microsoft`. Le
« volume TOTAL de l'IP » documenté n'est jamais calculé.

**Repro** (mentale, confirmable en E2E sink) : infra avec
`VeridianWarmupSchedule=[5,...]`, `StartedAt=now`. Enrôler 6 contacts gmail +
6 contacts outlook sur la même infra. Attendu (doc) : 5 partent, 7 reschedulés.
Réel : 5 gmail + 5 outlook = 10 partent.

## Bug 2 (plus grave) — cap warmup totalement bypassé sur les classes MX

`VeridianDomainsForClass(class)` renvoie une **liste VIDE** pour les 6 classes
MX (`ovh`, `ionos`, `apple_icloud`, `security_gateway`, `other_hoster`,
`corporate_selfhost`) — c'est la « dégradation gracieuse MX » documentée et
assumée pour le cap-classe STATIQUE (`veridian_provider_class.go:198-208`).

Pour le cap STATIQUE c'est acceptable : l'opérateur pose des caps par classe
explicitement et sait que les classes MX ne sont pas couvertes (le throttle
minute, lui, protège le hot path).

Pour le WARMUP c'est un trou : l'opérateur attend un plafond IP HOLISTIQUE
(« J1 = 5 max, point »), et le code **n'enforce RIEN** dès que le destinataire
résout en classe MX :

- `VeridianDomainsForClass("ovh")` → `([], false)`
- `CountSentSinceForDomainsAndSenderDomain(..., domains=[], ...)` → COUNT sur
  un `WHERE domain = ANY('{}')` → **0** → `0 >= warmupCap` toujours faux → jamais reschedulé.

Or la cartographie réelle des leads cold (memory
`reference_providers_destinataires_cartographie`) montre que la **majorité du
B2B** est hébergée Google Workspace / M365 / OVH résolus PAR MX → classés
`other_hoster` / `corporate_selfhost` / `security_gateway`, **pas** par suffixe.
Donc une IP fraîche en warmup blaste **sans aucun plafond journalier** vers
exactement la population que le warmup doit protéger. C'est l'inverse du but.

Le `veridianPerSenderCapGate` ne rattrape pas : il keye sur l'adresse FROM
EXACTE (`CountSentSinceForSender`), c'est un levier opt-in distinct qui ne back
pas la rampe warmup (et le preset warmup pose `per_sender=20`, pas la valeur de
la rampe).

## Fichiers concernés

- `internal/service/queue/veridian_daily_cap.go:100-110` (`veridianWarmupClassCap`)
- `internal/service/queue/veridian_daily_cap.go:157-179` (application warmup dans le gate)
- `internal/service/queue/veridian_daily_cap.go:184-204` (`veridianCountClassForInfra`)
- `internal/domain/veridian_provider_class.go:209-227` (`VeridianDomainsForClass` vide sur MX)

## Fix proposé (voie propre, pas de contournement)

Le warmup est conceptuellement un cap TOTAL par infra (par DOMAINE d'envoi),
**indépendant de la classe destinataire**. Il ne doit donc PAS passer par le
COUNT-par-classe-par-domaines (qui est par construction par-classe et aveugle
au MX).

1. Ajouter un COUNT TOTAL par domaine d'envoi du jour :
   `CountSentSinceForSenderDomain(ctx, workspaceID, senderDomain, since)` =
   `SELECT COUNT(*) WHERE lower(split_part(veridian_sender_email,'@',2)) = $1
   AND sent_at >= $2`. (L'index partiel `(veridian_sender_email, sent_at)` de
   V53 couvre déjà ; un `split_part` côté lecture est OK sur le volume cold,
   sinon matérialiser le sender_domain — décision lead.)
2. Dans le gate, quand `warmupCap > 0` : court-circuiter la branche cap-classe
   et tester `CountSentSinceForSenderDomain(senderDomain) >= warmupCap`
   (vraiment TOTAL, toutes classes, MX compris). Fallback workspace-global si
   FROM absent (legacy), comme `veridianCountClassForInfra`.
3. Garder la branche cap-classe statique pour le cas `warmupCap == 0` inchangée.
4. Tests : warmup cap=5, 6 gmail + 6 ovh-MX → 5 partent au TOTAL (peu importe la
   répartition de classes), 7 reschedulés. + non-régression : warmup off =
   comportement actuel.

## Impact / risque

- **Réversible**, additif (nouvelle méthode repo + 1 branche dans le gate).
- Tier 🔴 (touche le cap d'envoi core + nouveau COUNT) → test on-premise staging
  sink obligatoire avant promo prod (rejouer `scripts/e2e/cold-garde-fous.sh`
  étendu d'un gate warmup-total).
- **Best-effort préservé** : erreur COUNT = pass (jamais de blocage d'envoi).

## Note doc

Si on garde volontairement un cap warmup PAR CLASSE (décision lead possible),
alors **corriger la doc** (`veridian_warmup.go:22-25` + CLAUDE.md) qui affirme
« volume TOTAL de l'IP » — aujourd'hui la doc ment sur le comportement. Mais le
trou MX (Bug 2) reste à corriger dans tous les cas : une rampe warmup qui
n'enforce rien sur la majorité MX des leads cold est inutile pour son objectif.

## ⚠️ STATUT 2026-06-22 — fix livré sur staging, PROUVÉ, PAS EN PROD

- Fix `a39c9f15` (warmup = cap TOTAL par infra) sur `veridian`/staging. PAS en prod (3bbc1cce).
- **Preuve on-premise FAITE 2026-06-21** (par le lead, pas l'agent qui avait archivé à tort) :
  workspace jetable + seed 1 envoi `gmail` + 1 envoi `ovh` (classe MX) depuis le MÊME
  domaine émetteur `warmsender.fr` → `warmup_cap_decision` (cap=2) renvoie
  `sent_today=2` (TOTAL toutes classes, MX compris) + `would_be_capped=true`. ✅
  Avant le fix, l'envoi `ovh` MX n'était pas compté → warmup inopérant sur les vraies cibles B2B.
- **Reste** : promouvoir en prod via le ticket maître `2026-06-22-PROMO-PROD-lot-fixes-session.md`.
