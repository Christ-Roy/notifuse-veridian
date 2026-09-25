# Classification destinataire par MX RÉEL (Option A, Lot 4, 2026-06-14)

Le throttle/cap par classe était **aveugle** : la classe dérivait du **suffixe**
de domaine → ~70% des leads B2B jetés en `corporate` alors qu'ils sont hébergés
Google Workspace / M365 / OVH → on tapait Google/Microsoft à plein régime sans
throttle = réputation grillée. Le fix résout le **MX réel** du domaine (le DNS
dit où le mail atterrit) et mappe le **hostname MX** à une classe via une **table
de patterns versionnée**. Spec : ticket
`todo/2026-06-14-classification-mx-table-patterns-option-A.md`.

- **11 classes** (5 historiques + 6 MX, `VeridianAllProviderClasses()`) :
  `google` · `microsoft` · `yahoo_aol` · `freemail_fr` · `corporate` (suffixe
  inconnu AVANT MX, rétrocompat) **+** `ovh` · `ionos` · `apple_icloud` ·
  `security_gateway` (anti-spam pro → débit ultra-prudent) · `other_hoster`
  (infomaniak/gandi/zoho/proton…) · `corporate_selfhost` (MX inconnu, fallback).
  Toutes acceptées par `IsValidProviderClass` (un seul set = source de vérité
  pour throttle/cap/pixel/breakdown). Les 5 historiques inchangées (non-régression).
- **Hot path rapide** : `ClassifyProviderClass(email)` reste PURE (suffixe seul,
  zéro I/O — call-sites historiques inchangés). La couche MX vit dans
  `internal/domain/veridian_provider_class_mx.go` (`VeridianMXClassifier`) :
  suffixe connu → classe directe SANS lookup ; suffixe inconnu → MX caché.
  Le worker classe via `veridianClassifyRecipient(entry)` (tag amont prime, sinon
  MX) dans les DEUX gates (throttle minute + daily cap).
- **Resolver** : `MXResolver` (interface DI, mockable en test). Prod =
  `net.Resolver` forcé sur **8.8.8.8 / 1.1.1.1** (le resolver local conteneur est
  instable). Timeout court **2s**, best-effort STRICT : échec/timeout/NXDOMAIN/
  pattern inconnu → `corporate_selfhost`, **jamais de blocage d'envoi**.
- **Cache** : **in-memory** domaine→classe, TTL **7 jours** (MX changent rarement),
  thread-safe (RWMutex). PAS de table DB → **PAS de migration, `config.VERSION`
  NON bumpé** : les MX sont une donnée d'infra GLOBALE (pas par workspace), une
  table par workspace serait fausse ; le worker est long-lived ; le
  pré-remplissage massif (7,8M) se fait via le tag contact `custom_string_5`
  (override option B, posé à l'import par Prospection → ticket séparé) qui
  court-circuite tout lookup. Cache partagé inter-process = à matérialiser SI/quand
  mesuré nécessaire, pas avant.
- **Table de patterns MX→classe** : `veridianMXPatternTable` dans
  `veridian_provider_class_mx.go`, dérivée de la VRAIE data (email_verification
  48k + prospection prod 286k, cf. `docs/PROVIDERS-DESTINATAIRES-CARTOGRAPHIE.md`).
  Match **case-insensitive** sur **suffixe** du hostname MX. Ordre significatif :
  **gateways anti-spam testées EN PREMIER** (elles frontent un MX d'entreprise).
  Pour l'étendre : ajouter une ligne (suffixe lowercase OBSERVÉ en data, pas deviné).
- **Daily cap par classe — dégradation gracieuse documentée** : `VeridianDomainsForClass`
  renvoie une liste VIDE pour les classes MX (un domaine custom n'est rangé là que
  par son MX, NON stocké en DB) → le COUNT-par-domaine du cap-CLASSE ne s'enforce
  PAS pour ovh/ionos/… Le **throttle par MINUTE** (clé `integrationID|classe`),
  lui, protège pleinement la réputation sur le hot path. Si le cap-classe doit
  s'enforcer sur les classes MX → matérialiser la classe sur `message_history`
  (colonne+index), pas de COUNT par domaines (décision lead, cf. v49.go).
- **Fichiers veridian** : `internal/domain/veridian_provider_class_mx.go` (+test).
  Extensions de `veridian_provider_class.go` (constantes classes MX, set étendu,
  `VeridianAllProviderClasses`, helpers `veridianDomainFromEmail`/`classifyBySuffix`).
  Breakdown + pixel + UI étendus aux 11 classes.
- **UI** : 11 classes dans `console/src/services/api/workspace.ts`
  (`VERIDIAN_PROVIDER_CLASSES`, `VERIDIAN_DEFAULT_OPEN_PIXEL`) + libellés littéraux
  (piège Lingui : pas de `t` hors composant) dans `veridian_cold_outreach_settings.tsx`
  et `veridian_broadcast_rates_info.tsx`.

⚠️ **Diffs INLINE supplémentaires** (classification MX) :

| Fichier upstream | Diff Veridian |
|---|---|
| `internal/service/queue/worker.go` | +champ `providerMXClassifier *domain.VeridianMXClassifier` + init constructeur (`domain.NewVeridianMXClassifier(nil)`) ; les gates classent via `w.veridianClassifyRecipient(entry)` (MX) au lieu de `domain.ClassifyProviderClass` |

