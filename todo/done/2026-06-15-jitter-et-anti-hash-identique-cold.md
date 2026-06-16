# Jitter temporel + anti-hash identique par provider destinataire (cold outbound)

> **Sévérité** : 🟡 P1
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-15
> **Type** : feature délivrabilité cold — 2 garde-fous indépendants
> **Cadré par** : agent recherche (web + lecture archi), décisions tranchées ci-dessous

---

## TL;DR pour l'agent codeur

Deux garde-fous délivrabilité, **indépendants** (peuvent être codés séparément), tous
deux dans le moule cold existant (fichier `veridian_*.go` flat, zéro patch upstream
hors diff INLINE minimal, cascade `broadcast → infra → workspace`, opt-in strict =
non-régression upstream).

1. **JITTER TEMPOREL** — randomiser l'espacement entre envois pour casser le rythme
   métronomique. Reco tranchée : **jitter ± en pourcentage du pas nominal**, appliqué
   sur le délai de re-planification du throttle minute. Paramétrable par infra. Défaut
   **±30 %**, activé.

2. **ANTI-HASH IDENTIQUE PAR PROVIDER DESTINATAIRE** — empêcher que deux mails au
   contenu **rendu** identique (sujet + corps) partent vers la **même classe de provider
   destinataire**. Reco tranchée : **gate worker durable (option b)**, hash du rendu
   final, clé `(workspace, classe, hash)`, fenêtre glissante. PAS de blocage sec : on
   **re-varie** via re-spin si possible, sinon skip-and-reschedule borné. Couplé au
   linter délivrabilité existant pour l'avertissement amont (option c light).

Les deux respectent le contrat `skip-and-reschedule` des gates existants
(`SetNextRetry` SANS incrément d'attempts, best-effort, no-op sans config).

---

## Contexte archi (déjà lu, ne pas re-explorer)

### Pipeline d'envoi (rappel)

- **Enqueue** (`internal/service/broadcast/{queue_message_sender,message_sender}.go`) :
  le sender compile le template **par destinataire** (`buildQueueEntry` → `CompileTemplate`),
  applique **spintax** (seed = email du contact, `pkg/veridian_spintax`) sur corps +
  sujet, fige le rendu final dans `entry.Payload.Subject` + `entry.Payload.HTMLContent`.
  C'est là que le sender choisit aussi le rate, le pixel, le tracking domain, etc.
- **Worker** (`internal/service/queue/worker.go:processEntry`) : consomme le payload.
  Ordre actuel des gates Veridian, **tous AVANT `MarkAsProcessing`** (un skip ne brûle
  pas d'attempt) :
  1. circuit breaker (upstream)
  2. `veridianProviderClassGate` — throttle minute par classe (`SetNextRetry` + délai)
  3. `veridianDailyCapGate` — cap journalier classe + destinataire (lit `message_history`)
  4. `veridianSendingWindowGate` — fenêtre ouvrable
  5. `veridianPrefilterRecipient` (Lot 7) — skip adresses mortes → échec **permanent**
  6. `MarkAsProcessing` → `rateLimiter.Wait` (throttle émetteur upstream) → `SendEmail`

### Cascade de config (à respecter à l'identique)

`broadcast (payload, posé à l'enqueue par VeridianApplyProviderThrottle)` → `infra
(EmailProvider, R2/Lot5)` → `workspace (WorkspaceSettings)` → rien = no-op strict.
Premier niveau non vide gagne (pas de merge). `EmailProvider` est un **blob JSON**
(colonne `integrations`, affecté par valeur dans Create/UpdateIntegration) → **tout
champ `omitempty` ajouté passe SANS migration ni allowlist**. `WorkspaceSettings`
exige `+1 ligne dans UpdateWorkspace` (allowlist champ-par-champ, sinon l'UI Settings
sauve sans persister — bug vécu pixel 2026-06-11).

### Classification destinataire (à réutiliser, NE PAS dupliquer)

`worker.veridianClassifyRecipient(entry)` = tag `custom_string_5` (option B) sinon MX
réel (Lot 4) sinon suffixe. 11 classes canoniques
(`internal/domain/veridian_provider_class.go`, `VeridianAllProviderClasses`). C'est LA
fonction à appeler pour les deux garde-fous.

### Spintax existant (garantie réelle — important pour le sujet 2)

`pkg/veridian_spintax/ResolveSpintax(input, seed)` : seed = email du contact, choix
**déterministe** par groupe (FNV-1a + splitmix64 sur l'index de groupe). Conséquences
mesurées en lisant le code :

- **Deux destinataires différents → variantes potentiellement différentes**, MAIS rien
  ne garantit qu'elles le soient : avec peu de groupes / peu d'options, l'espace de
  variantes est petit. Ex. 1 groupe `{A|B}` = **2 variantes** seulement → sur 500 envois
  vers gmail, ~250 reçoivent exactement `A`. **Collision massive de rendu garantie.**
- Le seed étant l'email, **un même destinataire re-rendu → même variante** (voulu pour
  l'idempotence), donc le spintax NE protège PAS contre un renvoi au même contact (mais
  ça c'est le cap destinataire qui s'en charge).
- **Conclusion : le spintax SEUL ne garantit pas l'unicité du rendu par provider.** Il
  réduit l'empreinte commune, il ne l'élimine pas. D'où le besoin du sujet 2.

### Linter délivrabilité existant (livré aujourd'hui, 0cf1610e)

`pkg/veridian_deliverability` + endpoint console. Score un **rendu final unique** façon
SpamAssassin. Détecte déjà `SPINTAX_UNRESOLVED` (`{A|B}` qui fuit). **NE mesure PAS** la
diversité spintax (nombre de variantes du template, risque de doublon) — il ne voit
qu'un rendu, pas le template brut. Point d'extension naturel pour l'avertissement amont
(cf. sujet 2, volet linter).

---

# SUJET 1 — JITTER TEMPOREL

## Décision tranchée

**Approche retenue : jitter en POURCENTAGE du pas nominal, appliqué sur le délai de
re-planification du gate throttle minute. Paramétrable par infra. Défaut ±30 %, activé
par défaut sur contexte cold.**

### Pourquoi cette approche (et pas les autres)

Le throttle minute actuel (`veridian_provider_throttle.go`) est un token-bucket
`golang.org/x/time/rate` à burst 1 → espacement **strictement régulier** (ex. 2 mails/min
= 1 mail toutes les 30 s, à la milliseconde près). Quand une classe est saturée, le gate
renvoie un délai = `60/rate` secondes, **constant** → rythme métronomique = tell de
machine (les filtres et les outils de warmup détectent la régularité parfaite).

Le point d'injection le PLUS propre = **le délai retourné par `veridianProviderClassGate`
avant le `SetNextRetry`** (worker.go:303-312). On ne touche PAS le `rate.Limiter`
lui-même (le modifier casserait la garantie de débit moyen et le code upstream
réutilisé). On randomise uniquement le `delay` du skip-and-reschedule. C'est :

- **minimal** : ~10 lignes + 1 helper pur testable, zéro nouvelle dépendance, zéro état.
- **sûr** : le débit MOYEN reste piloté par le token-bucket (le jitter ne fait que
  disperser les re-checks autour du pas nominal, il ne crée pas de sur-débit puisque
  c'est le `Allow()` du limiter qui autorise réellement l'envoi au tick suivant).
- **cohérent** : même endroit, même contrat que les 3 autres gates.

### Pourquoi PAS les alternatives

- ❌ **Délai min/max absolu en secondes** (style preset GMass "10-60 s") : ne se compose
  pas avec le débit par classe déjà configuré (gmail à 0.5/min = 1 mail/2 min ; un
  min/max de 10-60 s serait incohérent avec ce pas). Le **pourcentage** s'adapte
  automatiquement au pas de chaque classe — un seul curseur couvre toutes les cadences.
- ❌ **Randomiser le `rate.Limiter`** : casse la garantie de débit moyen, complexifie un
  code upstream réutilisé. Usine à gaz.
- ❌ **Pause aléatoire longue / "coffee break"** : couvert fonctionnellement par les
  sending windows + le cap journalier. Hors scope (cf. EXCLU).

### Valeurs par défaut (justifiées par la recherche web)

| Paramètre | Défaut | Source / justification |
|---|---|---|
| `enabled` | `true` en contexte cold | Le rythme métronomique est un tell documenté ; tous les outils (GMass, Smartlead, Instantly) randomisent. |
| `jitter_pct` | `0.30` (±30 %) | GMass propose des plages type 10-60 s autour d'un pas (= ~±70 % sur un pas de 35 s) ; la pratique cold recommande de "randomiser la fenêtre" sans exploser la variance. ±30 % disperse visiblement le rythme tout en gardant le débit moyen stable. Conservateur et sûr. |
| `jitter_pct` plage valide | `[0, 0.9]` | Au-delà de 0.9 le délai pourrait approcher 0 (rafale) ou doubler (sous-débit inutile). Clampé. |

**Sémantique exacte** : `delay_final = delay_nominal × (1 + U(−jitter_pct, +jitter_pct))`
où `U` = tirage uniforme. Borné par les mêmes garde-fous que l'existant
(`>= 1s`, `<= veridianMaxProviderClassRetryDelay = 5min`). `math/rand` suffit (pas de
besoin cryptographique ; `crypto/rand` serait du gold-plating).

> ⚠️ Le jitter s'applique aussi, idéalement, au **token-bucket émetteur** upstream
> (`rateLimiter.Wait`, worker.go:404) qui est lui aussi métronomique. MAIS : c'est un
> `Wait` bloquant upstream, le modifier toucherait du code non-veridian et un chemin
> chaud partagé. **Décision : on NE touche PAS le Wait émetteur** dans ce ticket.
> Le throttle par classe (le gate qu'on jitte) est l'étage qui gouverne la cadence cold
> réelle vers chaque provider — c'est suffisant. Si Robert veut jitter l'émetteur plus
> tard, ticket séparé (diff INLINE worker plus lourd).

## Archi proposée (sujet 1)

### Fichier veridian à créer

`internal/service/queue/veridian_jitter.go` :

```go
package queue

// veridianApplyJitter disperse un délai de re-planification autour de sa valeur
// nominale pour casser le rythme métronomique du throttle (tell de machine cold).
// pct ∈ [0, 0.9] : fraction d'amplitude (±). pct<=0 → no-op (retourne delay).
// Borne basse 1s appliquée par l'appelant (inchangée). Pur, déterminisme injecté
// pour le test (rng float64 ∈ [0,1)).
func veridianApplyJitter(delay time.Duration, pct float64, rng func() float64) time.Duration
```

- Helper PUR, `rng` injecté (en prod `rand.Float64`, en test une fonction figée).
- Formule : `factor := 1 + (rng()*2 - 1)*pct` ; `return time.Duration(float64(delay)*factor)`.
- Clamp `pct` à `[0, 0.9]` dedans (défensif).

### Config — champ par infra + workspace + broadcast (cascade)

Ajouter le **jitter pct** au même niveau que les rates. Granularité par infra cohérente
avec R2 (l'IP en warmup porte son propre profil de cadence).

Helper de résolution dans `veridian_jitter.go` (calqué sur `veridianResolveProviderClassRates`) :

```go
func veridianResolveJitterPct(workspace *domain.Workspace, provider *domain.EmailProvider, entry *domain.EmailQueueEntry) float64
```

Cascade `payload → infra → workspace`, premier niveau **défini** gagne. **Subtilité du
zéro** : `0` est une valeur légitime (jitter désactivé explicitement). Pour distinguer
"non configuré" de "0 voulu", utiliser un **pointeur `*float64`** dans les structs
(`omitempty` + `nil` = non configuré → on applique le **défaut cold 0.30** si contexte
cold, sinon 0). C'est le SEUL piège de ce sujet — bien le tester.

> Détection "contexte cold" : réutiliser `domain.VeridianIsColdContext(...)`
> (`veridian_sender_rotation.go`) si accessible côté worker sans dépendance lourde ;
> sinon proxy simple = "des rates par classe sont configurés" (si le throttle classe est
> actif, on est en cold). Trancher au moment du code, le plus léger gagne.

### Diff INLINE (sujet 1) — minimal

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_provider.go` | +1 champ `EmailProvider.VeridianJitterPct *float64` (`json:"veridian_jitter_pct,omitempty"`) — blob JSON, pas de migration |
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianJitterPct *float64` (omitempty) — copié à l'enqueue |
| `internal/domain/workspace.go` | +1 champ `WorkspaceSettings.VeridianJitterPct *float64` (omitempty) |
| `internal/domain/veridian_provider_class.go` | `VeridianApplyProviderThrottle` propage le jitter broadcast → payload (1 ligne, comme la sending window) + helper `VeridianJitterPctFromMetadata` (clé `veridian_jitter_pct`) |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +1 ligne propageant `VeridianJitterPct` |
| `internal/service/queue/worker.go` | au call-site `veridianProviderClassGate` (ligne 303-312) : envelopper le `delay` retourné par `veridianApplyJitter(delay, pct, rand.Float64)` avant `SetNextRetry`. Le `pct` est résolu via `veridianResolveJitterPct`. ~4 lignes. |

> **Note d'implémentation propre** : le plus simple est que `veridianProviderClassGate`
> applique LUI-MÊME le jitter sur son `delay` avant de le retourner (il a déjà
> `workspace`, `provider`, `entry` en main) → **zéro changement au call-site worker, le
> diff INLINE worker.go tombe à 0**. Préférer ça. Le gate devient le seul endroit qui
> connaît le jitter, c'est plus propre que d'éparpiller dans `processEntry`.

---

# SUJET 2 — ANTI-HASH IDENTIQUE PAR PROVIDER DESTINATAIRE

## Citation Robert (à satisfaire)

> "bloquer un envoi parfaitement identique chez un même provider, ces règles doivent
> s'appliquer aux provider destinataire avec la même adresse mail probablement"

Décodage : deux mails au **rendu identique** (même sujet + même corps normalisés = même
hash) partant vers la **même classe de provider destinataire** = signal d'empreinte
commune (fuzzy hashing type Nilsimsa/ssdeep, clustering de n-grams au-dessus d'un seuil
de similarité ~0.75 selon la littérature délivrabilité). Le sujet doit varier aussi.

## Décision tranchée

**Approche retenue : OPTION (c) combinée, mais avec une garantie DURE côté worker
(option b) comme cœur, pas un simple linter.**

1. **Cœur = gate worker durable** : avant l'envoi, le worker calcule un hash du rendu
   final normalisé (sujet + corps). Clé `(workspace_id, classe_provider_destinataire,
   hash)`. Si ce hash a **déjà été envoyé vers cette classe dans la fenêtre glissante**,
   on **re-varie** le contenu (re-spin avec un seed perturbé) ; si la re-variation ne
   produit toujours pas de hash neuf (template sans spintax = aucune variante possible),
   on **skip-and-reschedule borné** ET on logge un warning exploitable. **Jamais de
   perte de mail définitive** sur ce motif (≠ pré-filtre Lot 7 qui, lui, est permanent).
2. **Garde-fou amont = linter** (option a, déjà 80 % là) : étendre le linter
   délivrabilité pour **avertir** quand le template a trop peu de variantes spintax
   (risque de collision de hash) AVANT l'envoi. Guide l'utilisateur sans bloquer.

### Pourquoi cette combinaison (arbitrage explicite)

| Option | Verdict | Raison |
|---|---|---|
| (a) Linter seul (avertit) | **Insuffisant en cœur, retenu en complément** | Un avertissement n'empêche rien : un template sans spintax passera quand même 500 fois le même rendu. Robert veut **bloquer**, pas suggérer. MAIS le linter est le bon endroit pour prévenir EN AMONT (coût zéro à l'envoi). |
| (b) Gate worker durable (hash check) | **RETENU comme cœur** | Seule garantie réelle "pas 2× le même hash vers le même provider". Le coût est maîtrisable (cf. perf ci-dessous). Aligné sur le pattern du daily cap (gate worker + source de vérité durable). |
| (c) Spintax obligatoire + blocage | Partiellement | "Spintax obligatoire" est trop rigide (certains templates courts n'en ont pas besoin si le volume par provider est faible). On garde l'esprit : variété encouragée (linter) + filet dur (gate). |

### Le point de tension : usine à gaz vs garantie

Robert déteste les usines à gaz. Le risque ici = créer une table de hash qui grossit
sans fin + un index + une logique de purge. **Décision pour rester léger** :

- **PAS de nouvelle table dédiée au départ.** La source de vérité est **déjà** là :
  `message_history` contient chaque envoi (sujet + corps loggés ? à vérifier — sinon on
  ajoute une COLONNE hash légère). On **dérive le hash à l'écriture** et on COUNT à la
  lecture, exactement comme le daily cap. Voir le sous-arbitrage stockage ci-dessous.
- **Fenêtre glissante courte** (défaut 24-72 h) : deux mails identiques espacés de
  plusieurs jours ne sont plus une empreinte de campagne. Borne la taille de l'espace de
  recherche. Réutilise le pattern `sent_at >= since` du daily cap.

### Sous-arbitrage : où stocker le hash ?

Deux voies, **trancher après avoir lu `message_history` (schéma + ce qui est loggé)** :

- **Voie A (préférée) — colonne `veridian_content_hash` sur `message_history`** :
  migration additive (1 colonne `bytea`/`char(16)` + 1 index partiel
  `(workspace_id, veridian_content_hash, sent_at)`). Le hash est calculé à l'enqueue ou
  à l'écriture du message_history. La classe N'EST PAS stockée (cohérent avec le daily
  cap) → on filtre par **liste de domaines de la classe** (`VeridianDomainsForClass`,
  déjà utilisé par le cap classe) OU on accepte la même **dégradation gracieuse** pour
  les classes MX (cf. point ci-dessous). Migration **V52** (next dispo), `config.VERSION`
  bump, fixture `manager_test`, index SANS CONCURRENTLY (runner en TX → override
  `migrations-pending.txt`, cf. memory `reference_migration_index_concurrently_tx_trap`).
  COUNT/EXISTS index-only à la lecture, comme le cap destinataire.

- **Voie B (si A trop lourde) — table système légère** `veridian_content_hash_seen`
  `(workspace_id, provider_class, content_hash, sent_at)`, PK composite, purge par
  `sent_at < now - window` (best-effort, dans le cron de cleanup idempotency existant
  `VeridianIdempotencyCleanupService`). Avantage : **stocke la classe directement** → le
  cap MX s'enforce (pas de dégradation). Inconvénient : 2e table à maintenir.

**Reco : commencer par voie A** (réutilise message_history, zéro table neuve, pattern
daily cap éprouvé). Migrer vers B SI/QUAND la dégradation MX devient un problème mesuré.
Documenter le choix dans le CLAUDE.md comme pour le cap.

### Dégradation gracieuse classes MX (cohérence avec le daily cap)

Si voie A : `VeridianDomainsForClass` renvoie une liste vide pour les classes MX
(ovh/ionos/…) → le check par domaine ne s'enforce pas pour elles, EXACTEMENT comme le
cap classe journalier. **Assumé** : le risque d'empreinte est dominant sur
google/microsoft/yahoo (gros providers, gros volumes), qui SONT couverts par suffixe.
Si voie B : pas de dégradation (classe stockée). Trancher selon la voie de stockage.

### Re-variation au lieu de blocage sec (anti-perte de mail)

Quand collision détectée :

1. **Tenter une re-spin** : re-résoudre le spintax avec un seed perturbé
   (`email + ":" + nonce` où nonce = compteur de collision, p.ex. `:r1`, `:r2`). Comme
   `ResolveSpintax` est déterministe sur le seed, changer le seed change la variante.
   Re-calculer le hash. Jusqu'à **N tentatives bornées (défaut 3)**.
2. Si après N tentatives le hash reste connu (template **sans** spintax → 1 seule
   variante possible) : **skip-and-reschedule borné** (`SetNextRetry`, délai court type
   1-5 min, SANS incrément d'attempts) + **log warning** "content collision, template
   has no spintax variety" → exploitable pour alerter l'utilisateur. Borne le nombre de
   reschedules pour éviter une entrée zombie (au-delà, laisser passer en best-effort
   plutôt que bloquer indéfiniment — la réputation prime mais on ne gèle pas la queue).

> ⚠️ **Le re-spin doit se faire au bon endroit.** Le rendu final vit dans le payload
> (`entry.Payload.Subject/HTMLContent`), figé à l'enqueue. Re-spinner DANS le worker
> exige de ré-appliquer spintax sur le payload, ce qui est lourd (le worker n'a pas le
> template brut). **Décision la plus propre** : faire la détection + re-spin **à
> l'ENQUEUE** (dans le sender, là où spintax est déjà appliqué et où on a le template
> brut), PAS dans le worker. Le sender connaît la classe (suffixe au moins) et peut
> consulter une fenêtre récente via le repo. Le worker reste le filet de SÉCURITÉ ultime
> (best-effort, lit le hash, log si collision résiduelle). **Répartition : variété
> garantie à l'enqueue (re-spin), audit/filet au worker.** Cf. "Archi" ci-dessous.

### Le sujet doit varier aussi (demande Robert + reco web)

Le hash porte sur **sujet + corps** normalisés ensemble. Un sujet identique est aussi une
empreinte. Le spintax sujet existe déjà (appliqué côté sender). Le linter doit aussi
avertir si le sujet n'a aucune variante spintax.

### Normalisation du contenu avant hash (éviter les faux négatifs)

Hash = `SHA-256(normalize(subject) + "\x00" + normalize(body))`, tronqué 64-128 bits
suffisant. `normalize` = lowercase + collapse whitespace + strip des parties qui varient
trivialement SANS casser l'empreinte (Liquid déjà résolu à ce stade). **Attention** : ne
PAS normaliser au point de masquer une vraie variation (le but est de détecter
l'identité de CAMPAGNE, pas l'identité stricte byte-à-byte — c'est le bon niveau, c'est
ce que font les filtres fuzzy). Documenter la fonction de normalisation + la tester.

## Valeurs par défaut (sujet 2)

| Paramètre | Défaut | Justification |
|---|---|---|
| `enabled` | `true` en contexte cold | Sans ça, un template sans spintax envoie 500× le même hash. |
| Fenêtre glissante | **72 h** | Au-delà, deux identiques ne forment plus une empreinte de campagne ; borne la recherche. Aligné sur l'ordre de grandeur du daily cap. |
| Max re-spin | **3** | Suffisant pour faire bouger un template avec ≥2 groupes spintax ; au-delà = template trop pauvre, c'est au linter de le signaler. |
| Reschedule borné | **5 min**, max **N reschedules** puis best-effort pass | Ne gèle pas la queue sur un template structurellement non variable. |
| Granularité | **par classe de provider destinataire** (11 classes) | Demande explicite Robert : même hash vers 2 classes ≠ problème ; vers la même classe = problème. |

## Archi proposée (sujet 2)

### Fichiers veridian à créer

- `internal/domain/veridian_content_hash.go` — PUR : `VeridianContentHash(subject, body
  string) string` (normalisation + SHA-256 tronqué) + helpers de config
  (`VeridianAntiHashEnabled`, fenêtre, depuis metadata/settings). Test colocalisé.
- `internal/service/broadcast/veridian_content_dedup.go` — logique d'enqueue :
  étant donné un rendu (subject/body) + classe + repo de lookup fenêtre, décide si
  collision et orchestre la re-spin (rappelle `ResolveSpintax` avec seed perturbé via le
  template brut déjà en main du sender). Injecté dans les deux senders comme le pixel
  resolver / sender rotator (DI optionnelle, nil = no-op upstream).
- `internal/service/queue/veridian_content_hash_gate.go` — FILET worker best-effort :
  `veridianContentHashGate(workspace, provider, entry)` lit le hash du payload (posé à
  l'enqueue), COUNT/EXISTS la fenêtre récente par classe ; collision résiduelle → log
  warning (PAS de blocage dur au worker pour éviter de geler ce que l'enqueue a laissé
  passer ; le worker constate et trace). Placé dans `processEntry` après le pré-filtre,
  avant `MarkAsProcessing`. Best-effort STRICT (erreur DB → pass).
- Repo : +1 méthode `MessageHistoryRepository` (voie A) type
  `ExistsContentHashSince(ctx, workspaceID, hash, domains, exclude, since)` OU repo
  dédié (voie B). Décorateur quota + mock régénérés.
- `internal/migrations/v52.go` (voie A) — colonne + index, idempotent, additif, en TX.

### Lien avec le linter existant (option a, volet avertissement)

`pkg/veridian_deliverability` : ajouter une règle **informative** (poids faible, ou
champ séparé `Warnings`) qui compte les variantes spintax du template **brut** (pas du
rendu) et avertit si `< seuil` (ex. moins de variantes que d'envois prévus vers la plus
grosse classe). ⚠️ Le linter score actuellement un **rendu**, pas le template brut → il
faudra lui passer le template brut OU un `variant_count` calculé par l'appelant. Trancher
au code : le plus simple = l'appelant (sender ou UI) calcule le nombre de variantes via
un helper `pkg/veridian_spintax.CountVariants(template) int` (à ajouter, pur) et le linter
l'affiche. **Garder ce volet léger** — c'est un guide, pas le filet.

### Diff INLINE (sujet 2)

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/domain/email_queue.go` | +1 champ `EmailQueuePayload.VeridianContentHash string` (omitempty) — posé à l'enqueue |
| `internal/domain/email_provider.go` | +champs config anti-hash par infra (enabled `*bool`, window) omitempty — blob JSON, pas de migration |
| `internal/domain/workspace.go` | +champs config anti-hash par workspace (omitempty) |
| `internal/domain/message_history.go` | +1 méthode interface `ExistsContentHashSince` (voie A) |
| `internal/repository/message_history_postgre.go` | +impl EXISTS hash + colonne hash écrite à la création (voie A) |
| `internal/repository/veridian_message_history_decorator.go` | passthrough `ExistsContentHashSince` |
| `internal/service/broadcast/{queue_message_sender,message_sender}.go` | détection collision + re-spin à l'enqueue + pose `Payload.VeridianContentHash` ; +champ dedup + setter DI (comme pixel resolver) |
| `internal/service/broadcast/factory.go` | crée le dedup une fois, l'injecte dans les deux senders |
| `internal/service/queue/worker.go` | +gate `veridianContentHashGate` (filet, log-only) après pré-filtre |
| `internal/service/workspace_service.go` | `UpdateWorkspace` allowlist : +lignes config anti-hash |
| `config/config.go` | `VERSION` bump (voie A) |
| `internal/database/init.go` | colonne + index pour nouveaux workspaces (voie A) |
| `internal/migrations/manager_test.go` | fixture `db_version` bump (voie A) |

---

## DoD (testable, les deux sujets)

### Sujet 1 — jitter

- [ ] `veridianApplyJitter(delay, pct, rng)` PUR + test colocalisé : pct=0 → no-op exact ;
      pct=0.3 + rng=0 → `delay×0.7` ; rng=1 → `delay×1.3` ; rng=0.5 → `delay` ; pct>0.9
      clampé ; résultat jamais négatif.
- [ ] `veridianResolveJitterPct` : cascade payload>infra>workspace ; `nil` (non configuré)
      → défaut cold 0.30 ; `0` explicite → 0 (jitter OFF). **Tester la distinction
      nil/0** (le piège pointeur).
- [ ] Test gate : un délai throttle nominal de 60 s avec pct=0.3 produit des délais
      observés dans `[42s, 78s]` sur N tirages (borne basse 1 s respectée, borne haute
      5 min respectée).
- [ ] Non-régression : sans config jitter ET hors contexte cold → délai inchangé (égal à
      l'upstream actuel).
- [ ] Le débit MOYEN reste piloté par le `rate.Limiter` (test : sur K ticks, le nombre
      d'`Allow()` ne dépasse pas le débit configuré — le jitter ne crée pas de sur-débit).

### Sujet 2 — anti-hash

- [ ] `VeridianContentHash` PUR : même (subject, body) normalisés → même hash ;
      whitespace/casse n'affecte pas ; sujet OU corps différent → hash différent.
- [ ] `pkg/veridian_spintax.CountVariants(template)` PUR : `{A|B}` → 2 ; `{A|B} {C|D}` →
      4 ; nesting compté ; pas de spintax → 1 ; Liquid ignoré.
- [ ] Re-spin à l'enqueue : 2 contacts dont le seed produirait le même rendu → après
      re-spin, hashes distincts (si le template a ≥2 variantes). Template sans variante →
      collision détectée, warning loggé, mail PAS perdu (passe en best-effort après N).
- [ ] Granularité classe : même hash vers `google` ET `microsoft` → AUCUNE collision
      signalée (classes différentes) ; même hash 2× vers `google` dans la fenêtre →
      collision.
- [ ] Gate worker filet : collision résiduelle → log warning, envoi PAS bloqué (best-
      effort) ; erreur DB → pass (best-effort strict).
- [ ] Linter : template à 1 variante avec un gros volume cible → warning "variété
      insuffisante" ; template riche → pas de warning.
- [ ] Migration V52 (voie A) : additive, idempotente, en TX, `config.VERSION` bumpé,
      fixture `manager_test` à jour, `init.go` à jour, override `migrations-pending.txt`
      pour l'index.
- [ ] **E2E staging** (giga, sans mail réel) : provision workspace jetable, broadcast à
      template sans spintax vers 2 contacts même classe → 2e envoi détecté collision
      (warning / reschedule) ; template avec spintax → 2 rendus distincts, pas de
      collision. AUCUN envoi SMTP réel (DRAFT / simulate, cf. garde-fou Robert
      2026-06-11 `--real-send`).

### Transverse

- [ ] `make test-domain test-service test-repo test-http` verts.
- [ ] Pre-push hook vert (1 test colocalisé par fichier veridian créé ; senders modifiés
      → leur test à jour, cf. memory `feedback_shared_worktree_team_commits`).
- [ ] CLAUDE.md Notifuse : 2 nouvelles sections (jitter + anti-hash) avec le tableau des
      diffs INLINE, comme les lots précédents.
- [ ] Commit tier : jitter = 🟡 (config + délai, pas de surface API risquée) → push sans
      `[risk:low]` puis reco. Anti-hash voie A = 🔴 (migration) → E2E lourd staging +
      reco + monitoring avant promo prod (cf. CLAUDE.md §9 + règle E2E lourd team lead).

---

## Ce qui est volontairement EXCLU (cadrage anti-usine-à-gaz)

- ❌ **Jitter sur le token-bucket émetteur upstream** (`rateLimiter.Wait`) : on ne touche
  pas ce code chaud non-veridian. Le jitter sur le throttle par classe suffit pour le
  cold. Ticket séparé si besoin.
- ❌ **Pauses aléatoires longues / "coffee breaks" / micro-batchs** : couvert
  fonctionnellement par les sending windows + caps journaliers. Pas un 5e mécanisme.
- ❌ **Distribution non-uniforme sur la journée** (plus d'envois le matin, etc.) : c'est
  du raffinement de sending window, hors scope ici.
- ❌ **Crypto-random pour le jitter** : `math/rand` suffit, pas un besoin sécu.
- ❌ **Blocage DUR / perte de mail sur collision de hash** : on ne jette JAMAIS un mail
  pour cause de doublon (≠ pré-filtre adresse morte). On re-varie, on retarde borné, ou
  on laisse passer en best-effort avec warning. La réputation ne justifie pas de ne
  jamais contacter un prospect.
- ❌ **Table de hash globale cross-workspace / cross-campagne sans fenêtre** : la fenêtre
  glissante (72 h) est obligatoire ; pas de stockage infini.
- ❌ **Spintax rendu OBLIGATOIRE sur tous les templates** : trop rigide. La variété est
  encouragée (linter) et garantie par le gate quand le volume le justifie, pas imposée.
- ❌ **Réécriture du moteur spintax** : on réutilise `pkg/veridian_spintax` tel quel
  (seed perturbé pour la re-spin, pas de nouveau moteur).
- ❌ **Détection sémantique / similarité fuzzy maison (Nilsimsa/ssdeep)** : on bloque
  l'identité de hash (rendu identique), PAS la quasi-similarité. Reproduire un moteur de
  fuzzy hashing serait l'usine à gaz que Robert refuse. Le hash exact + le spintax + le
  linter couvrent le besoin réel.

---

## Note honnête sur la controverse délivrabilité (à connaître, ne change pas la déc.)

La littérature est partagée : certaines sources (MailReach) affirment que ni Google ni
Microsoft n'ont documenté de "filtre de similarité de template" et que la réputation
d'expéditeur prime sur la variation de contenu. D'autres (Smartlead, Reply, Woodpecker,
audits cités) documentent un clustering par n-grams au-dessus d'un seuil ~0.75 et des
gains mesurés en variant le contenu. **Position retenue pour Veridian** : Robert veut le
garde-fou, le coût est faible, et l'identité de hash strict est le signal le plus net et
le moins discutable (deux mails byte-identiques modulo whitespace vers le même provider
= empreinte triviale). On implémente le filet sur l'IDENTITÉ (pas la similarité floue),
ce qui est défendable quelle que soit la position dans le débat.

**Sources web** :
- Cold email settings / delays — https://www.gmass.co/blog/cold-email-settings/
- Throttle / random delay — https://www.gmass.co/blog/mail-merge-feature-throttle-your-email-campaign/
- Cold email sequences timing 2026 — https://www.allegrow.co/knowledge-base/cold-email-sequences
- Spintax & fuzzy hashing — https://www.smartlead.ai/blog/what-is-spintax
- Spintax deliverability (controverse) — https://www.mailreach.co/blog/spintax-cold-email
- Daily limits / warmup — https://www.smartlead.ai/blog/email-frequency-best-practices-for-cold-emails
