# notifuse-veridian — fork Veridian de Notifuse

Fork du projet [Notifuse](https://github.com/Notifuse/notifuse) (Go 1.25 + Postgres 17
+ React 18 + Vite + Ant Design). Branche de travail : `veridian` (push direct OK, CI
obligatoire) ; sync upstream depuis `main` (`upstream/main`), jamais de commit Veridian
direct sur `main`. Tech stack upstream : voir `README.md`, pas repris ici.

Ce fichier reste COURT et se charge à chaque session. Le détail de chaque brique (le
diff exact, les fichiers touchés, l'historique du piège) vit dans `docs/claude/`, un
fichier par sujet. **Lire la fiche avant de toucher au sujet.** Un nouveau piège ou une
nouvelle brique s'écrit dans une nouvelle fiche `docs/claude/NN-sujet.md`, avec une
ligne dans l'index ci-dessous — jamais un pavé de plus ici.

## Les règles qui ne se discutent pas

1. **Zéro contournement (règle d'or Veridian, gravée Robert 2026-06-10).** Jamais de
   cron bricolé, de store parallèle ou de job maison pour éviter l'API/la DB réelle de
   l'app. Un blocage (accès, credential) se débloque via le lead, il ne se contourne
   pas. Détail : CLAUDE.md racine `veridian-platform`.
2. **Ne jamais patcher un fichier upstream Notifuse.** Tout code Veridian vit dans des
   fichiers `veridian_*.go` au même niveau que l'upstream (flat, jamais de
   sous-dossier). Un handler à étendre se wrappe, il ne se modifie pas en place. → `02`
3. **Zéro mail cold réel en test.** Les preuves passent par l'endpoint
   `cold-simulate` (staging-only, 503 hors staging) ou un sink SMTP local
   (`smtp-sink`) — jamais un vrai envoi sans le flag `--real-send` et le GO explicite
   du lead. → `05`, `13`, `14`
4. **La cascade de protection réputation est un empilement de gates dans le worker**
   (circuit breaker → exclusion → throttle minute → daily cap → per-sender cap →
   sending window → pré-filtre → anti-hash), résolue `broadcast → infra → workspace →
   défaut`. Retirer ou réordonner un gate sans relire toute la cascade grille un
   domaine d'envoi. → `03,04,08,19,21,22,23,25,27,28,29,30,31,32,38`
5. **`DROP DATABASE` toujours `WITH (FORCE)`, et le record système supprimé AVANT la
   base (record-first).** Sinon la base workspace se recrée toute seule dans la
   seconde (le worker ré-élit un record encore vivant). Staging uniquement ;
   `canary*` et clients réels toujours exclus. → `39`, `40`
6. **Une cellule de la matrice HMAC cross-app ne se modifie jamais sans lire le code
   des deux parties** (Notifuse ET Hub). Le canonical string diverge selon le sens du
   flux pour le même secret — une doc qui contredit le code a déjà coûté une session
   P0 (2026-05-25). → `46`
7. **Promotion prod : c'est l'agent propriétaire de Notifuse qui tranche, pas
   Robert**, selon le niveau de preuve atteint (🟢 CI verte / 🟡 smoke staging / 🔴
   E2E on-premise réel / 💀 destructif-irréversible = SEUL tier où on demande
   go/stop à Robert). → `48`
8. **Pricing : générosité maximale gravée par Robert — zéro mur béton, zéro compteur
   visible, zéro limite enforced, y compris sur Free.** Tout enforcement de dimension
   exige une validation explicite contre `PRICING-VERIDIAN.md` (Hub). → `47`
9. **Toute réponse API qui embarque un Workspace passe par la redaction exhaustive
   des credentials.** Ne jamais réintroduire un secret (IMAP, SMTP, FileManager,
   LLM…) en clair dans une réponse HTTP. → `10`
10. **Migrations en Expand & Contract strict.** Le tag Docker précédent doit tourner
    sur le schéma actuel ; `DROP COLUMN`/`NOT NULL` sur une table peuplée = deux PR
    sur deux déploiements. → `48`
11. **Le rate-limit global de l'API doit rester câblé sur `a.mux`** (défense OWASP
    API4:2023) ; un garde-fou Husky/CI bloque si dé-câblé — ne jamais le contourner.
    → `44`
12. **Tout cross-app (identité, membership, billing) passe par le Hub, jamais
    app→app direct.** Un `hub_user_id` n'est jamais réécrit sur un user existant sans
    audit + migration explicite. → `45`

## Relever plutôt que croire

```bash
# état réel du cluster Nomad + du job notifuse (prod/staging)
ssh bastion nomad-v state
ssh bastion 'nomad job status notifuse'

# la dette de tests non couverts (baseline transitoire, cible 0)
wc -l tests-pending.txt

# le mapping test colocalisé est-il vert sur le HEAD actuel ?
BASE_REF=origin/veridian scripts/ci/check-test-mapping.sh

# tous les fichiers custom Veridian (convention flat veridian_*.go)
git ls-files | grep -E 'veridian_|veridian\.go' | wc -l

# la vraie valeur des secrets HMAC/infra (jamais dans les .env locaux)
ssh bastion 'nomad var get nomad/jobs/notifuse'
```

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
- `49` commandes tests (Makefile)
- `41` sync upstream
- `42` déploiement — GitOps Nomad
- `43` tests E2E Veridian

**Sécurité & API**
- `10` redaction exhaustive des credentials workspace API
- `44` rate-limit global de l'API (OWASP API4:2023)

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
- `13` E2E lifecycle cold

**Cold outbound — pré-filtrage & preuve anti-cramage**
- `20` pré-filtrage d'envoi — skip adresses invalides
- `14` batterie E2E on-premise — garde-fous anti-cramage de domaine
- `24` vague hygiène/conformité cold

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
