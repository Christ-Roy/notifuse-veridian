# Constitution CI — règles non négociables

Standard de référence : `../CI-ARCHITECTURE.md` (racine `veridian-platform/`).
Adaptations Go pour ce repo :

1. **1 fichier critique = 1 test colocalisé.** Scopes :
   `internal/{http,service,repository,domain}/**/*.go`. Convention Go :
   `foo.go` ↔ `foo_test.go` au même niveau. Zéro exception.
2. **Pre-push hook bloquant** via `.husky/pre-push` →
   `scripts/ci/check-test-mapping.sh`. Setup : `make setup-hooks`.
3. **Jamais `--no-verify`.** Si le hook bloque : fix le test ou ajoute à
   `tests-pending.txt` (dette tracée).
4. **Règle 1-pour-1 stricte** : chaque nouvelle func exportée dans un fichier
   critique = au moins un nouveau `TestXxx` dans le test colocalisé.
5. **`tests-pending.txt`** : baseline transitoire, cible 0 sous 90 jours. Toute
   ligne retirée doit s'accompagner du test.
6. **`test-coverage-map.yaml`** : si un fichier est légitimement couvert
   ailleurs, déclarer avec `reason:` explicite + `covered_by`.
7. **Exception upstream sync** : auteur `@notifuse.com` bypasse le mapping
   pour les fichiers **non-veridian_***. Les `veridian_*.go` restent sous
   discipline stricte.
8. **Skip docs** : `paths-ignore: ['**.md', 'docs/**', 'runbooks/**', 'plans/**']`.
9. **Promotion prod — L'AGENT TRANCHE (gravé Robert 2026-06-16).**

   **C'est l'agent propriétaire de Notifuse qui décide de la promo prod, pas
   Robert.** Robert délègue le résultat : il ne valide PAS chaque push, il
   n'attend PAS au bout d'une notif Telegram. L'agent juge le risque, lance les
   tests qu'il faut, promeut quand c'est vert, rend compte après. Robert
   intervient seulement (a) en veto explicite (`stop`/`rollback`/`freeze`), ou
   (b) sur le tier 💀 destructif-irréversible (cf. ci-dessous).

   **Le critère de promo n'est PAS un marker, c'est le NIVEAU DE PREUVE atteint.**
   L'agent classe son lot par risque et choisit la batterie de tests en
   conséquence — les unit tests de la CI sont le PLANCHER, jamais le plafond :

   | Tier | Exemples | Preuve EXIGÉE avant promo (par l'agent) | Promo |
   |---|---|---|---|
   | 🟢 BAS | doc, todo, tests-only, refactor sans surface API | CI verte (unit + mapping) | Marker `[risk:low]` → auto-promote |
   | 🟡 MOYEN | UI, route non-auth, bump dep patch | CI verte + smoke ciblé staging (curl/Chrome sur la surface touchée) | Agent promote (`workflow_dispatch deploy_prod=true`) après smoke OK |
   | 🔴 HAUT | envoi mail core, throttle/pixel cold, HMAC, lib partagée, gros bump dep (CVE), sync upstream | CI verte + **TEST ON-PREMISE staging** = E2E réel sur le vrai système (DB/SMTP staging, sink local si besoin), état vérifié à la main, pas un mock | Agent promote après E2E on-premise vert + monitoring 10 min post-deploy |
   | 💀 CRITIQUE | DROP COLUMN, rotation secret prod, suppression tenant prod, migration destructive | **SEUL tier où l'agent demande go/stop à Robert** | Robert tranche |

   **Test on-premise = complément OBLIGATOIRE des unit tests sur le tier 🔴.**
   La CI ne lance que du Vitest/Go-test qui mocke SMTP/DB/apps downstream — un
   mock vert ne prouve RIEN sur le flux réel (incident `pk_test_fake` 2026-05-23,
   bug pixel-workspace `workspace=nil` 2026-06-13 : tests unit verts, flux réel
   cassé). Donc pour tout lot 🔴, AVANT de promouvoir, l'agent DOIT dérouler le
   vrai flux sur staging (le vrai worker, la vraie DB staging, le vrai SMTP — ou
   le sink local `aiosmtpd` pour le cold, cf. `todo/2026-06-13-e2e-tunnel-validation-sink-local.md`),
   lire l'état réel (queue, message_history, HTML émis), et ne promouvoir que
   sur preuve observée. Pas d'E2E on-premise vert = pas de promo 🔴.

   **Mécanique de promo** :
   - 🟢 `[risk:low]` dans le subject (1ère ligne) → `deploy-prod` auto.
   - 🟡/🔴 → push SANS `[risk:low]` (staging only), puis l'agent promeut
     lui-même via `gh workflow run veridian-ci.yml -f deploy_prod=true` une fois
     sa batterie de preuve verte. PAS d'attente d'un GO Robert (sauf tier 💀).
   - Stop-gap `[skip-prod]` / `[wip]` : bloquent toujours la prod (WIP en cours).
   - ⚠️ Piège connu (mémoire `feedback_risk_low_doc_commit_auto_promote_trap`) :
     ne JAMAIS finir une vague par un commit doc `[risk:low]` si du tier 🟡/🔴
     non encore validé est dans le même push range — le head-commit `[risk:low]`
     déclenche l'auto-promote de TOUT le lot. Valider d'abord, archiver/documenter
     `[risk:low]` après.
   - Toujours valable : si `Dockerfile`, `go.mod`, `docker-compose.yml`,
     `internal/migrations/**` modifiés → staging vert + e2e-staging vert exigés
     avant toute prod (implicite via `needs`).

   **Fluidité du sprint (gravé 2026-06-16)** : l'objectif est un flow CONTINU,
   pas une file d'attente de validations. L'agent enchaîne coder → push staging
   → preuve (selon tier) → promo → monitoring → ticket suivant, SANS s'arrêter
   demander « je promeus ? » entre chaque. Une vague de team se termine par UNE
   promo groupée des lots mûrs + UN récap, pas par N demandes de GO. Le seul
   point d'arrêt est le tier 💀 ou un veto Robert.
10. **Deploy via Nomad SSH-bastion** (Dokploy décommissionné 2026-07-10, canon
    prospection) : `scripts/ci/nomad-ssh-deploy.sh <env> <tag>` → SSH bastion →
    `nomad job run -var image_tag=<tag>`. Secrets CI : `NOMAD_DEPLOY_SSH_KEY` +
    `NOMAD_BASTION_HOST` + `NOMAD_BASTION_USER`. Le token ne quitte pas le bastion.
11. **Rollback prod auto** sur e2e-prod fail : `nomad job revert notifuse <version-1>`
    via SSH-bastion → wait `/api/setup.status` → Telegram alert.
12. **Migrations Expand & Contract obligatoire**. Le tag Docker précédent doit
    tourner sur le schéma actuel. Versions majeures (V6, V7…) additives ;
    DROP COLUMN / NOT NULL sur table peuplée = 2 PRs sur 2 deploys.
13. **Findings GitHub Security tab** : Trivy + gitleaks SARIF via
    `upload-sarif@v3`. Pas de `.trivyignore` sans VEX écrit.
14. **Renovate auto-merge** (à activer) : patch + minor + CVE auto-mergés si
    CI verte. Major → review humaine.

### Commandes CI utiles

```bash
make setup-hooks                                 # one-time, installe pre-push
BASE_REF=HEAD scripts/ci/check-test-mapping.sh   # test local working tree
BASE_REF=origin/veridian scripts/ci/check-test-mapping.sh  # comme pre-push
wc -l tests-pending.txt                          # voir la dette
git ls-files | grep -E 'veridian_|veridian\.go'  # lister fichiers custom
```

---

