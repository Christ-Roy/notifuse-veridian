# Couverture CLI embarquée et distribution (2026-11-03)

## Le défaut trouvé

Le CLI distribué à /agent/notifuse (tar.gz, `internal/http/agent_cli_embed.go`) contenait
`notifuse_common.py` qui **importe `notifuse_mail`**, mais le tar ne livrait que
`notifuse` et `notifuse_common.py`. Depuis le commit `2186a5fc` (29/10/2026, [risk:low]),
`build_parser` levait `ModuleNotFoundError` : **toute commande du CLI distribué échouait**.
Mesure : `curl https://notifuse.app.veridian.site/agent/notifuse`, extraction, puis
`python3 notifuse_common.py __routes` → `No module named 'notifuse_mail'`.

Correction : `notifuse_mail.py` est embarqué (go:embed, tar, `sync-agent-cli.sh`, MANIFEST).
`TestAgentHandler_*` vérifie que le tar le contient.

## Le contrôle qui manquait

`bin/coverage_test.sh` (skill) mesurait la couverture mais n'était lancé par aucun hook
du dépôt. Le pre-push fait i18n, frozen-files et `gardes`. Nouveau contrôle DANS le dépôt :

- `scripts/veridian/cli-coverage-check.sh` (wrapper) + `cli_coverage_check.py`.
- Routes : littérales (`mux.Handle("[MÉTHODE ]/api/…")`), helpers (`both("/api/…")` sur un
  chemin-variable), concaténations (`"/api/veridian/"+string(kind)+".move"` avec la boucle
  résolue sur les constantes du dépôt). Un argument non résolu = **non abouti**, jamais
  silencieux. Commentaires Go pleine ligne ignorés.
- Contrôles : (1) chaque route `/api/*` a sa commande dans la CLI embarquée (`__routes`) ;
  (2) MANIFEST intègre ; (3) la CLI s'exécute avec les SEULS fichiers du MANIFEST ;
  (4) identique à la source de vérité si présente (`NOTIFUSE_CLI_SOURCE`, défaut skill).
- Rc : 0 vert, 1 rouge (bloque), 2 non abouti (prévient). Autotest : `--selftest`.
- Exemptions : `scripts/veridian/cli-route-exemptions.txt` (route + raison). Une exemption
  périmée fait échouer. Une seule aujourd'hui : `/api/workspaces.generateMagicLink`, retiré
  de la CLI le 19/10/2026 pour raison de sécurité (fiche 68).
- Branché : `.husky/pre-push` juste avant `gardes`, et step dans le job
  « Constitution CI §1 » (runner auto-hébergé, Loi 7).

## Preuves (rouge puis vert)

- Route fictive `POST /api/veridian/zz-fictif-preuve` sans commande : check rc 1,
  `.husky/pre-push` rc 1 avant `gardes`. Route retirée : rc 0.
- Dérive à la main de la copie embarquée : rc 1 (hash ≠ MANIFEST). Revert : rc 0.
- Source divergente (`NOTIFUSE_CLI_SOURCE` modifié) : rc 1.
- État « mail non distribué » simulé : rc 1 (CLI non exécutable).
- Routes récentes présentes : `emailProfiles.usage`, `admin/email-integration.export`,
  `admin/email-integration.verify`, `mail.threads.list`, `mail.messages.reply`,
  `policy.get`, `segments.move`, `folders.move`. Total : 246 routes Go, 245 commandes + 1 exemption.

## Consommateurs de la CLI (mesures, 03/11/2026)

- `~/bin/notifuse`, `~/bin/notifuse-admin` : liens symboliques vers la source (alignés).
- `/agent/notifuse` servi par prod : CASSÉ jusqu'à la correction (ci-dessus).
- Copies dans d'autres checkouts (`nv-*`, `wt-*`, `.worktrees`) : 10 différentes de la
  source, 1 identique. Elles ne sont pas déployées ; elles se remettent à jour au rebase.
- `acquisition-emailing` et `all-cron` (hors archive) : 4 commandes référencées
  (`contactLists:updateStatus`, `contacts:import`, `env`, `profiles:overview`), toutes présentes.
- `SKILL.md` (source) : 66 commandes citées, 0 absente. `agentskill/SKILL.md` embarqué : 21, 0 absente.
- `docs/AGENT-API.md` : 21 routes citées, 0 absente du routeur. 12 copies identiques.
  **Écart** : 51 commandes utilisateur sur 123 ne sont mentionnées nulle part dans
  `AGENT-API.md` (ex. `folders:*`, `policy:*`, `analytics:query`). À documenter, non traité ici.

## Dettes

- Le pre-push lit `core.hooksPath` = `notifuse-veridian/.husky` (dépôt principal, 201 commits
  en retard, hook différent de `origin/veridian`). Le hook des worktrees n'est donc **pas**
  celui du dépôt. Corriger = `git config core.hooksPath .husky` (relatif) : décision de Robert.
- Une route fictive dans une fonction helper non littérale est attrapée seulement si le
  fichier contient aussi un appel `helper("/api/…")` ; un chemin entièrement variable hors
  de tout motif reconnu passe en non abouti (rc 2), pas en vert.
