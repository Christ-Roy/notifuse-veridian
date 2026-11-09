# notifuse-veridian — fork Veridian de Notifuse

Fork dur du projet [Notifuse](https://github.com/Notifuse/notifuse) (Go 1.26 + Postgres 17
+ React 18 + Vite + Ant Design). Branche de travail : `veridian` (push direct, la CI déploie) ;
jamais de commit Veridian sur `main`. Stack : `README.md`.

Court par design (chargé à chaque session). Le détail d'une brique vit dans
`docs/claude/NN-sujet.md` : **lire la fiche avant de toucher au sujet**, et une nouvelle
brique = une fiche + une ligne d'index.

## Les règles qui ne se discutent pas

1. **Zéro contournement (règle d'or Veridian, gravée Robert 2026-06-10).** Jamais de
   cron bricolé, de store parallèle ou de job maison pour éviter l'API/la DB réelle de
   l'app. Un blocage (accès, credential) se débloque via le lead, il ne se contourne
   pas. Détail : CLAUDE.md racine `veridian-platform`.
2. **Ne jamais patcher un fichier upstream Notifuse.** Tout code Veridian vit dans des
   fichiers `veridian_*.go` au même niveau que l'upstream (code existant). Un handler à
   étendre se wrappe, il ne se modifie pas en place. → `02`
   **Depuis le 29/10/2026 (L0), le code NEUF vit dans `internal/veridian/<module>/`, se câble
   par `internal/app/veridian_wiring.go`, migre par `internal/migrations/veridian/`. Fichiers
   gelés (app.go, worker.go…) : une ligne de branchement max, garde
   `scripts/veridian/frozen-files-check.sh`. → `docs/veridian/MODULARITE.md`**
   **Exception (07/10/2026) : la console React (`console/`) est la nôtre**, plus fusionnée
   depuis l'upstream (Go seulement). Nouvelles pages dans des `veridian_*.tsx`. → `53`
3. **Zéro mail cold réel en test.** Les preuves passent par l'endpoint
   `cold-simulate` (staging-only, 503 hors staging) ou un sink SMTP local
   (`smtp-sink`) — jamais un vrai envoi sans le flag `--real-send` et le GO explicite
   du lead. → `05`, `13`, `14`
4. **La cascade de protection réputation est un empilement de gates dans le worker**
   (pause profil → circuit breaker → exclusion → throttle minute → daily cap → per-sender cap →
   sending window → pré-filtre → anti-hash), résolue `broadcast → infra → workspace →
   défaut`. Retirer ou réordonner un gate sans relire toute la cascade grille un
   domaine d'envoi. Les plafonds se résolvent UNE fois (`veridianResolveCapLimits`),
   partagée avec `VeridianEffectivePlan` (API `emailProfiles.overview`) : ne pas les
   recalculer ailleurs. → `03,04,08,19,21,22,23,25,27,28,29,30,31,32,38,54,56`
5. **`DROP DATABASE` toujours `WITH (FORCE)`, et le record système supprimé AVANT la
   base (record-first).** Sinon la base workspace se recrée toute seule dans la
   seconde (le worker ré-élit un record encore vivant). Staging uniquement ;
   `canary*` et clients réels toujours exclus. → `39`, `40`
6. **Une cellule de la matrice HMAC cross-app ne se modifie jamais sans lire le code
   des deux parties** (Notifuse ET Hub). Le canonical string diverge selon le sens du
   flux pour le même secret — une doc qui contredit le code a déjà coûté une session
   P0 (2026-05-25). → `46`
7. **Promotion prod : l'agent propriétaire de Notifuse tranche, pas Robert** (🟢 CI
   verte / 🟡 smoke staging / 🔴 E2E on-premise / 💀 irréversible = SEUL tier qui demande
   go/stop à Robert). Auto-promotion : `[risk:low]` en 1re ligne du commit, ou
   `workflow_dispatch deploy_prod=true`. Un job rouge d'infra se relance (`gh run rerun --failed`). → `48`
8. **Pricing : générosité maximale gravée par Robert — zéro mur béton, zéro compteur
   visible, zéro limite enforced, y compris sur Free.** Tout enforcement de dimension
   exige une validation explicite contre `PRICING-VERIDIAN.md` (Hub). → `47`
9. **Toute réponse API qui embarque un Workspace passe par la redaction exhaustive
   des credentials.** Ne jamais réintroduire un secret (IMAP, SMTP, FileManager,
   LLM…) en clair dans une réponse HTTP. Seule exception : l'export opérateur SMTP/IMAP,
   HMAC Hub seul, jamais de cache. → `10`, `76`
10. **Migrations en Expand & Contract strict.** Le tag Docker précédent doit tourner
    sur le schéma actuel ; `DROP COLUMN`/`NOT NULL` sur une table peuplée = deux PR
    sur deux déploiements. → `48`
11. **Le rate-limit global de l'API doit rester câblé sur `a.mux`** (défense OWASP
    API4:2023) ; un garde-fou Husky/CI bloque si dé-câblé — ne jamais le contourner.
    → `44`
12. **Tout cross-app (identité, membership, billing) passe par le Hub, jamais
    app→app direct.** Un `hub_user_id` n'est jamais réécrit sur un user existant sans
    audit + migration explicite ; un lien différent est refusé (400), un lien ne s'écrit que
    si le Hub atteste l'adresse. Un utilisateur ne se crée, modifie, supprime qu'au Hub. → `45`, `80`
13. **Toute route `/api/*` a une commande CLI, et la CLI embarquée = son MANIFEST.** Le contrôle
   tourne au pre-push et en CI. → `77`
14. **Le plafond de connexions de l'appli (`DB_MAX_CONNECTIONS`) tient dans le
    `max_connections` de PostgreSQL** : une connexion par workspace, 100 et 100 ont refusé
    des workspaces clients (07/11/2026). `check-nomad-contract.sh` le vérifie. → `81`
15. **Base : pg-saas depuis le 09/11/2026** (`172.26.64.1:15432`, rôle `notifuse`, `PG_SAAS_PASSWORD`). Plus de `notifuse-db` (job purgé, dumps froids sur contabo). → plan nomad-veridian §20.

## Relever plutôt que croire

Commandes de vérité (cluster, dépôt, secrets) : fiche `72`.

## Index des fiches (`docs/claude/`)

**Branche & conventions de code**
- `01` branche et workflow
- `02` convention `veridian_*.go` flat
- `50` conventions clés (résumé exécutable)
- `51` Claude Agent Rules (pas d'auto-attribution)

**Cross-app Hub (contrat, secrets HMAC, pricing)**
- `45` invariants CONTRAT-HUB v1.5
- `46` secrets HMAC cross-app — matrice exhaustive
- `47` pricing — source de vérité

**CI, tests, déploiement**
- `48` constitution CI — règles non négociables (tiers de promotion prod)
- `71` relais SMTP de soumission et certificat géré : configuration dans les réglages système (env = surcharge)
- `72` commandes de vérité (relever plutôt que croire)
- `49` commandes tests (Makefile)
- `41` sync upstream
- `42` déploiement — GitOps Nomad
- `43` tests E2E Veridian

**Sécurité & API**
- `10` redaction exhaustive des credentials workspace API
- `76` export opérateur SMTP/IMAP (HMAC seul, audit, 0600)
- `77` couverture CLI embarquée (chaque route `/api/*` a sa commande, MANIFEST) et distribution du CLI
- `44` rate-limit global de l'API (OWASP API4:2023)
- `61` nœud webhook des automations : garde SSRF (`NewTenantOutboundClient`), secret chiffré, signature HMAC
- `75` lot L4 qualité et sécurité : aperçu e-mail en bac à sable, Liquid des contacts, tests upstream portés, mutants
- `80` identité : Hub source des utilisateurs, `hub_user_id`, lien à usage unique, routes `/api/hub/*`, rebond automatique
- `82` statut d'essai du Hub (stocké, rendu, jamais appliqué) et signal d'activité vers le Hub
- `81` capacité base : connexions par workspace, `max_connections`, incident du 07/11
- `79` CLI utilisateur à parité : profils d'envoi avec sa propre clé, qui peut quoi, faille `tasks.*`, tableau d'audit

**Base de données workspace**
- `39` DROP DATABASE WITH (FORCE) — wipe orphelin + GC
- `40` wipe RECORD-FIRST

**Cold outbound — débit, caps, warmup (protection réputation)**
- `03` throttle par provider destinataire
- `04` plafond journalier d'envoi
- `08` rates + caps par infra d'envoi
- `19` classification destinataire par MX réel
- `21` priorisation follow-up sous contrainte de capacité
- `22` multi-SMTP round-robin par provider destinataire
- `23` fenêtre d'envoi — horaires ouvrables
- `25` jitter temporel du throttle par classe
- `27` exclusion de classes de provider destinataire
- `28` cap journalier par SENDER émetteur — warmup IP
- `29` warmup progressif — rampe auto
- `30` FIX P1 — warmup = cap total par infra
- `31` V55 — réservation atomique des quotas journaliers
- `32` V56 — profils d'envoi multi-intégrations
- `38` cap journalier par classe keyé par infra émettrice

**Cold outbound — contenu & tracking**
- `05` open tracking — pixel par classe
- `11` custom tracking domain aligné au domaine d'envoi
- `15` spintax — variation de contenu anti-empreinte
- `26` anti-hash identique par classe de provider
- `33` pixel d'ouverture par infra

**Cold outbound — IMAP, bounce, réponses**
- `16` intégration IMAP self-service
- `17` bounce-loop NDR IMAP → suppression contact
- `18` stop-on-reply — réponse prospect → exit séquence
- `78` redirection des réponses de prospects par profil (V69)
- `13` E2E lifecycle cold

**Cold outbound — pré-filtrage & preuve anti-cramage**
- `20` pré-filtrage d'envoi — skip adresses invalides
- `14` batterie E2E on-premise — garde-fous anti-cramage de domaine
- `24` vague hygiène/conformité cold

**Console assumée, vérité d'un profil**
- `53` console assumée : inventaire, spécification de la page Profils d'envoi, plan en 5 lots
- `54` vérité d'un profil (lot 2) : `EffectivePlan`, `emailProfiles.overview`, pause, lien IMAP, exclusivité
- `55` page Profils d'envoi (lot 3) : carte, assistant, `emailProfiles.create`
- `56` séparation transactionnel / commercial (lot 4) : `setUsage|pause|resume`, profil réservé, isolation worker, compteurs par type (V61), fusible par profil
- `57` tableau de bord de prospection (lot 5) : `prospection.stats`, dimension analytics `veridian_profile_id`, `transactional_watch`, chauffe par jour de compte
- `58` dépilage équitable de `email_queue` (famine du 10/10) : relances avant J0, tourniquet entre automations, aucune reprise manuelle
- `59` automations observables (conception seule, plan en 7 lots, rien d implémenté)
- `60` capacité d'un profil (lot 6) : somme des capacités par classe destinataire bornée par chauffe et stock, barre empilée console, CLI agent
- `62` « pourquoi ça n'envoie pas » (V62) : raison de report persistée sur `email_queue`, verdicts de gates, journal des décisions, `queue.explain` / `decisions.list`, page File d'envoi
- `69` plafonds et débits PAR DÉFAUT pour les 25 classes, table MX -> classe source unique, échec DNS transitoire non figé, scission de freemail_fr en FAI
- `64` garde réseau partagée (`pkg/netguard`) pour SMTP, IMAP, SparkPost ; exceptions explicites ; garde `prod-residue` de l'E2E prod
- `67` liste blanche IMAP/SMTP `NOTIFUSE_TRUSTED_INTERNAL_HOSTS`, owner-only inviteMember/deleteInvitation, route admin `email-integration.verify`
- `68` self-service owner : audit UI/API/CLI (tableau), boutons d ajout LLM/Firecrawl/Supabase, invitation avec repli local, limites (S3, lists.create 409)
- `65` relances dans le fil du premier mail : réglage `veridian_thread_followups`, In-Reply-To / References, ancre d'expéditeur tenue 7 jours
- `66` une relance ne part pas si le premier mail (J0) n'a pas été livré : échec, rebond dur, refus 5.7.x ou aucun J0 → le contact sort (`first_mail_*`)
- `70` politique d'envoi par classe en données (presets JSON, surcharges par workspace), priorité d'envoi par campagne ou automation, stock par classe (V66, `internal/veridian/policy`)

**Cold outbound — UI console & self-service**
- `06` UI console — section Settings « Cold outreach »
- `07` breakdown contacts par classe de provider
- `09` profils d'envoi Gmail par mot de passe d'application
- `12` UI config cold self-service + câblage API IMAP

**Cold outbound — KPI, dashboard, events**
- `34` events comportementaux cold↔web → Hub
- `35` KPI dashboard cold
- `36` FIX P0 — dashboard 500 `bounce_type` absent
- `37` wrapper veridian analytics — error-shape standardisé
- `52` rendu au dépilage des emails d automation (le modèle courant fait foi)
