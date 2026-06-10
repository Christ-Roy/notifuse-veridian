# Notifuse — throttle par provider destinataire (cold mailing)

> **Sévérité** : 🔵 P3 — chantier éloigné, à NE PAS prioriser avant les P0/P1/P2 en cours.
> **Owner** : agent Notifuse
> **Créé** : 2026-06-02
> **Demandeur** : agent tunnel-de-vente (via Robert)
> **Statut** : 📥 déposé — spec posée, pas encore planifié
> **Tier risque** (CI Hub §20 transposé) : 🔴 HAUT (touche le worker d'envoi = cœur du moteur). E2E lourd obligatoire avant promo main.

---

## 0. TL;DR pour l'agent qui prend le ticket

Le moteur Notifuse sait throttler **par intégration émettrice** (ce SMTP / ce
compte SES, en emails/minute global). Il ne sait **PAS** throttler **par provider
du destinataire** (ralentir vers Gmail mais foncer vers les domaines corporate).

Le tunnel de vente outbound (cold mailing ultra-qualifié, batch ~1000) **en a
besoin** : la délivrabilité et les limites de débit diffèrent radicalement par
boîte réceptrice. Sans ça, le batch part en spam et tout le tunnel en aval
(page audit → scoring → appel) devient inutile.

**Le dev = ajouter un second étage de rate-limiting keyé par classe de provider
destinataire, en amont du `Wait` existant, sans casser le throttle émetteur.**
Point d'extension localisé : `internal/service/queue/worker.go:298-311`.

C'est P3 : on pose la spec maintenant pour ne pas la reperdre, mais l'agent
Notifuse a des priorités plus proches. À sortir du frigo quand le tunnel
outbound passe en exécution.

---

## 1. Contexte business

Robert monte un **tunnel de vente outbound** (repo `veridian-tunnel-de-vente`,
voir `docs/api-refs/SYNTHESE-FAISABILITE.md` §2). Le flux :

```
Batch ~1000 leads ultra-qualifiés (DB Prospection, segmentés PAR provider)
  → cold mail Notifuse (throttle PAR provider destinataire ← CE TICKET)
  → page audit perso no-index (veridian-site)
  → scoring comportemental (Analytics)
  → appel commercial priorisé par score (Twenty)
```

Le throttle par provider est le **chemin critique** de tout le tunnel : c'est la
seule brique sans laquelle rien d'autre ne peut démarrer en sécurité. Les leads
sont segmentés en amont par provider côté Prospection (`gmail.com`,
boîtes MS grand public, FAI, corporate) précisément pour pouvoir leur appliquer
des cadences d'envoi distinctes ici.

**Pourquoi c'est vital** : un même domaine d'envoi qui balance 1000 mails en
rafale vers Gmail se fait classer spam quasi instantanément (Gmail = seuils de
réputation agressifs, throttling côté MX). Vers des domaines corporate dispersés
(chacun son MX, peu de volume par domaine), on peut envoyer beaucoup plus vite
sans risque. D'où le besoin de **cadences différenciées par classe de boîte**.

---

## 2. État réel du code (audité 2026-05-31, lecture seule)

> Source : `veridian-tunnel-de-vente/docs/api-refs/notifuse/VERIDIAN-OVERRIDES.md`
> §"Throttle / vitesse d'envoi". Recopié ici pour autosuffisance du ticket.

### Ce qui existe — throttle par intégration émettrice

- Seul réglage de débit : `EmailProvider.RateLimitPerMinute int`
  — `internal/domain/email_provider.go:65`. **Obligatoire et > 0**
  (validation `email_provider.go:76`). Débit unique par intégration, **aucune
  dimension destinataire**.
- Enforcement : un `golang.org/x/time/rate` limiter **keyé par `integrationID`** —
  `internal/service/queue/rate_limiter.go` →
  `IntegrationRateLimiter.GetOrCreateLimiter(integrationID, ratePerMinute)`.
  (Commentaire ligne 10-11 : *"manages rate limits per integration. Each
  integration has its own rate limiter based on its configured
  RateLimitPerMinute"*.)
- Le worker attend sur ce limiter avant chaque mail :
  `internal/service/queue/worker.go:298-304` →
  ```go
  ratePerMinute := integration.EmailProvider.RateLimitPerMinute
  w.rateLimiter.Wait(ctx, entry.IntegrationID, ratePerMinute)
  ```
  La clé est `entry.IntegrationID`, **jamais** le domaine/provider du destinataire.
- Cross-intégration : `getMinEmailRateLimit` (`worker.go:511-526`) prend le **min**
  des `RateLimitPerMinute` — toujours côté émetteur.
- Broadcast : `internal/service/broadcast/config.go:24` `DefaultRateLimit` (défaut
  25/min), appliqué via `message_sender.go:227 enforceRateLimit(...)`.

### Ce qui N'existe PAS

- `grep -rinE "recipientDomain|per.?domain|mailbox.?provider|destination.?domain|gmail|outlook"`
  dans `internal/` + `pkg/` → **aucune** notion de throttle par domaine/provider
  destinataire. Les seules occurrences "gmail/outlook" :
  - liste disposable (`pkg/disposable_emails/`),
  - `mail_provider_choice` V48 (choix **sender**, pas throttle),
  - champ SparkPost `MailboxProvider` (`email_provider_sparkpost.go:41`) =
    dimension **de reporting SparkPost native** (leur API), pas un knob de débit.

### Note connexe (déjà en place, ne PAS refaire)

Le fork skip un recipient qui renvoie 429 sans crasher le batch (commit
`030dd0b0` "wrapper rate-limit handler", classification retryable 429/5xx dans
`pkg/emailerror/*.go`). C'est de la **résilience d'envoi réactive**, pas du
throttle **proactif** — c'est complémentaire à ce ticket, pas un substitut.

---

## 3. Ce qu'il faut construire

### 3.1 Modèle de données — classification provider destinataire

Besoin d'une fonction qui, à partir de l'email du destinataire, renvoie une
**classe de provider** (le bucket de throttle). Proposition de classes (à
challenger) :

| Classe | Critère de détection | Pourquoi un bucket dédié |
|---|---|---|
| `google` | domaine Gmail/Googlemail **ou** MX Google Workspace | seuils réputation Gmail très agressifs |
| `microsoft` | Outlook/Hotmail/Live grand public **ou** MX M365 | throttling Outlook + filtrage SmartScreen |
| `yahoo_aol` | yahoo/aol/… | politique d'envoi propre |
| `freemail_fr` | orange/free/sfr/laposte… (FAI FR) | volumétrie + filtrage FR |
| `corporate` | tout le reste (MX privé/Proofpoint/Mimecast…) | dispersé, tolérance plus haute |

> ⚠️ **Détection domaine ≠ détection provider réel.** `contact@boitepro.fr`
> peut être hébergé chez Google Workspace ou M365 — invisible sans **lookup MX**.
> Décider du niveau de finesse :
> - **V1 simple** : classification **par suffixe de domaine connu** uniquement
>   (table statique gmail.com → google, etc.), `corporate` par défaut. Zéro I/O,
>   zéro latence. Suffisant pour la majorité du batch (les freemails dominent en
>   B2C ; en B2B corporate, le bucket `corporate` global est déjà une bonne
>   approximation).
> - **V2 enrichie** : lookup MX (avec cache TTL) pour reclasser les domaines
>   corporate vers `google`/`microsoft` selon leur MX réel. Plus juste mais ajoute
>   I/O réseau dans le chemin d'envoi → à mettre **hors du chemin chaud** (résolu
>   au moment du build de la queue, pas par mail).
>
> **Reco : livrer V1, garder V2 en option.** La segmentation par provider est
> **déjà faite en amont côté Prospection** (cf. ticket Prospection à venir) — si
> le batch arrive déjà tagué par provider, Notifuse peut même consommer ce tag au
> lieu de re-dériver (cf. §3.4 option B).

### 3.2 Second étage de rate-limiting (le cœur du dev)

Ajouter un limiter **par classe de provider destinataire**, en **amont** du
`Wait` émetteur existant. Le mail doit franchir **les deux** limiters :

```
mail à envoyer
  → Wait(providerClassLimiter[class], rateForClass)   ← NOUVEAU
  → Wait(integrationLimiter[integrationID], ratePerMinute)  ← EXISTANT, inchangé
  → send
```

- **Réutiliser le pattern existant** : cloner la mécanique de
  `IntegrationRateLimiter` (`rate_limiter.go`) en un `ProviderClassRateLimiter`
  keyé par `class` (string). Même `golang.org/x/time/rate`, même
  `GetOrCreateLimiter`. **Ne pas réinventer**, ne pas introduire une lib de
  rate-limiting tierce.
- **Point d'insertion** : `worker.go:298-311`, juste avant le `Wait` actuel.
  Dériver `class := classifyProvider(entry.ContactEmail)` puis
  `w.providerRateLimiter.Wait(ctx, class, rateForClass(class, cfg))`.
- **Unité de débit** : aligner sur l'existant = **emails/minute** par classe (pas
  par jour, pour rester cohérent avec `RateLimitPerMinute` ; le "X/jour" demandé
  par Robert se traduit en /minute : 1000/jour ≈ 0.7/min, à exprimer en /min ou
  introduire une fenêtre jour — voir §3.3).
- **Garde-fou anti-deadlock** : deux `Wait` séquentiels sur le même `ctx` — vérifier
  qu'un limiter saturé ne bloque pas indéfiniment le worker pool (timeout/ctx
  cancel déjà géré dans le `Wait` existant, le reproduire).

### 3.3 Config — où règle-t-on les débits par classe ?

Décisions à trancher (proposer une reco, ne pas bloquer dessus) :

- **Granularité** : global au workspace ? par broadcast ? par campagne cold ?
  → Reco : **par broadcast** (champ optionnel sur le broadcast, fallback config
  workspace), pour aligner sur `DefaultRateLimit` qui est déjà par-broadcast.
- **Forme** : map `{class: rate_per_minute}` dans le payload broadcast +
  défauts en config (`broadcast/config.go`). Ex :
  `{"google": 5, "microsoft": 8, "corporate": 30}`.
- **Fenêtre jour vs minute** : si Robert veut vraiment des plafonds **journaliers**
  (warm-up "200/jour vers Gmail puis +50/jour"), `golang.org/x/time/rate` (fenêtre
  glissante par seconde) ne suffit pas pour un quota dur sur 24h. Soit on convertit
  en /min (approximation acceptable pour du throttle continu), soit on ajoute un
  **compteur persistant journalier par classe** (table + reset minuit). → Reco :
  **V1 en /minute** (suffit pour lisser), **quota journalier dur = V2** si le
  warm-up programmatique le réclame.

### 3.4 Deux chemins d'implémentation — choisir

**Option A — Notifuse dérive lui-même la classe** (autonome) :
Notifuse classe chaque destinataire à l'envoi (table de suffixes V1, MX V2).
Aucune dépendance amont. Plus de code ici, mais Notifuse reste self-contained.

**Option B — Notifuse consomme un tag provider posé en amont** (couplé) :
Le batch importé porte déjà un champ contact (ex `custom_string_1 = provider_class`)
posé par l'export Prospection. Notifuse lit ce tag au lieu de dériver.
Moins de code throttle, mais dépend du contrat d'import + ne marche que pour les
contacts taggués (fallback dérivation pour le reste).

> **Reco : A pour la robustesse** (Notifuse ne doit pas dépendre d'un tag externe
> pour ne pas spammer), **avec B en court-circuit** si le tag est présent
> (`if entry.providerClass != "" { use it } else { classify() }`). Le meilleur des
> deux : autonome par défaut, optimisé si l'amont coopère.

---

## 4. Tests exigés (tier 🔴 HAUT = E2E lourd obligatoire)

- **Unitaires** :
  - `classifyProvider()` : gmail.com→google, outlook.com→microsoft,
    orange.fr→freemail_fr, boitepro.fr→corporate, casse/espaces/`+alias`,
    domaine vide/invalide → fallback `corporate` sans panic.
  - `ProviderClassRateLimiter` : N mails vers `google` respectent le débit classe ;
    mails vers classes différentes ne se bloquent pas mutuellement.
- **Intégration worker** : un batch mixte (gmail + corporate) avec
  `{google: 2/min, corporate: 60/min}` → vérifier que les gmail sont étalés et les
  corporate passent vite, **et** que le throttle émetteur reste appliqué par-dessus
  (les deux étages composent, pas l'un OU l'autre).
- **Non-régression** : sans config de classe (map vide / absente), comportement
  **strictement identique** à aujourd'hui (throttle émetteur seul). Ce ticket ne
  doit JAMAIS dégrader l'envoi transactionnel/broadcast existant.
- **E2E staging** : broadcast réel sur staging avec quelques destinataires test
  de classes différentes, vérifier l'étalement dans les logs worker + aucun crash
  batch sur 429 (la résilience `030dd0b0` doit continuer de fonctionner).

---

## 5. Garde-fous & non-négociables

- **Discipline fork** : aucun patch sur fichier upstream. Le nouveau limiter et la
  classification vont dans des fichiers `veridian_*` OU dans les fichiers worker
  existants si c'est inévitable — **dans ce cas, documenter le diff vs upstream**
  (le fork suit upstream v30.1, cf. `.veridian-fork-marker`) car ça compliquera le
  prochain merge upstream. Préférer l'extension par injection à la modif inline.
- **Le throttle émetteur existant reste intact** : on AJOUTE un étage, on ne
  remplace rien. `IntegrationRateLimiter` ne bouge pas.
- **Zéro I/O réseau dans le chemin chaud** pour la V1 (pas de lookup MX par mail).
  Si V2 (MX), résolution au build de queue + cache, jamais par envoi.
- **Pas de plafond visible / mur** : cohérent avec la philo Veridian — le throttle
  est invisible côté produit, c'est de la délivrabilité, pas une limite de plan.

---

## 6. Dépendances & coordination

- **Amont (Prospection)** : la segmentation par provider est faite côté Prospection
  (ticket à venir dans `veridian-prospection/todo/`). Si l'option B (tag consommé)
  est retenue, le **contrat du champ** (`provider_class` + valeurs canoniques) doit
  être figé entre les deux agents. → coordonner via Robert avant de coder l'option B.
- **Aval (Twenty timeline)** : indépendant de ce ticket. La sync events
  Notifuse→Twenty passe par les **webhooks abonnés système A** (`email.*`), déjà
  dispo upstream (cf. `VERIDIAN-OVERRIDES.md` §Webhooks A). Pas un prérequis ici.
- **Relai d'envoi cold** : décision ouverte (Postfix self-hosted dédié vs domaine
  warm-up — ticket `veridian-tunnel-de-vente/todo/2026-05-31-archi-tunnel-outbound.md`).
  Notifuse ne pilote que le débit/minute par intégration ; le warm-up IP/réputation
  est délégué au relai SMTP en amont (skill `postfix`). **Hors scope de ce ticket**,
  mais le throttle par classe est ce qui rend le warm-up exploitable côté Notifuse.

---

## 7. Definition of Done

- [ ] `classifyProvider(email) → class` (V1 table de suffixes), testé.
- [ ] `ProviderClassRateLimiter` (clone du pattern integration), testé.
- [ ] Insertion du second `Wait` dans `worker.go` (~ligne 298), les deux étages
      composent, non-régression prouvée quand config classe absente.
- [ ] Config débits par classe (par broadcast + défauts workspace).
- [ ] Court-circuit option B (consomme `provider_class` si présent sur le contact).
- [ ] Suite de tests §4 verte (unitaires + intégration + E2E staging).
- [ ] Diff vs upstream documenté si fichier worker touché inline.
- [ ] Ticket source mis à jour côté tunnel
      (`veridian-tunnel-de-vente/docs/api-refs/SYNTHESE-FAISABILITE.md` §2 passe de ❌ à ✅).

---

## 8. Références code (pour ne pas re-fouiller)

| Quoi | Fichier:ligne |
|---|---|
| Champ débit émetteur | `internal/domain/email_provider.go:65` (+ validation `:76`) |
| Limiter par intégration | `internal/service/queue/rate_limiter.go` (`GetOrCreateLimiter`) |
| **Point d'insertion worker** | `internal/service/queue/worker.go:298-311` |
| Min cross-intégration | `internal/service/queue/worker.go:511-526` (`getMinEmailRateLimit`) |
| Débit par défaut broadcast | `internal/service/broadcast/config.go:24` (`DefaultRateLimit`) |
| Enforce côté broadcast | `internal/service/broadcast/message_sender.go:227` |
| Résilience 429 (déjà là) | commit `030dd0b0`, `pkg/emailerror/*.go` |
| Synthèse tunnel (vue d'ensemble) | `veridian-tunnel-de-vente/docs/api-refs/SYNTHESE-FAISABILITE.md` §2 |
| DIFF fork complet | `veridian-tunnel-de-vente/docs/api-refs/notifuse/VERIDIAN-OVERRIDES.md` |
