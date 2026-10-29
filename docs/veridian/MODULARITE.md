# Modularité Veridian : règles, fichiers gelés, ajouter une capacité

Lot L0 (29/10/2026). Design d'ensemble : `PLATEFORME-IA-FIRST.md` (section 3.1). Ce document est la version exécutable.

## 1. Les règles

1. **Le code Veridian neuf vit dans `internal/veridian/<module>/` (ou `pkg/veridian_<nom>/`)**, jamais dans un fichier upstream, hors une ligne de branchement. (Les anciens `veridian_*.go` plats restent où ils sont ; on ne les déplace pas sans raison.)
2. **Les fichiers gelés** (section 2) ne reçoivent plus de logique Veridian. Maximum **une** ligne de logique ajoutée par fichier et par diff : l'appel vers `veridian_wiring.go`. Les suppressions, commentaires, lignes vides et fermetures de bloc ne comptent pas.
3. **Un seul point de câblage** : `internal/app/veridian_wiring.go`. `app.go` l'appelle par `WireVeridian(a)` (fin d'`InitHandlers`) et par trois crochets d'étape (`veridianInitRepositories`, `veridianInitServices`, `veridianWrapHandler`) ; l'état Veridian de l'`App` est la struct embarquée `veridianState`.
4. **Migrations Veridian dans un registre séparé** : `internal/migrations/veridian/`, numéros réservés par lot, expand puis contract, suivi par numéro appliqué (section 4).
5. **Console** : composants `veridian_*`, aucune édition d'un composant upstream hors une ligne d'intégration.
6. **Toute fonction exposée en API l'est aussi en CLI** (`notifuse`), avec la même sémantique, et documentée dans l'OpenAPI.
7. **i18n** : `.po` et `.js` générés, jamais édités à la main.

## 2. Fichiers gelés

Liste de référence : `scripts/veridian/frozen-files.tsv` (le détecteur la lit, ce tableau la reflète).

| Fichier | Pourquoi |
|---|---|
| `internal/app/app.go` | racine de composition, 2 647 lignes avant L0 (283 occurrences Veridian), 1 400 après |
| `internal/domain/workspace.go` | modèle upstream, touché par chaque fusion |
| `internal/service/workspace_service.go` | idem, très couplé (inscription) |
| `internal/domain/email_provider.go` | idem |
| `internal/repository/message_history_postgre.go` | idem |
| `internal/service/queue/worker.go` | cascade d'envoi, couplé (priorité) |
| `console/src/services/api/workspace.ts` | client API upstream |
| `internal/migrations/v32.go` | migration upstream, jamais retouchée |

Ce qui reste de Veridian dans `app.go` après L0, volontairement : le rate-limit global et les en-têtes de sécurité de `Start()` (garde-fou `check-rate-limit-coverage.sh`, ordre avec le traçage) ; les appels aux constructeurs upstream dont un argument est Veridian (`NewGuardedSparkPostService`, `SchedulerEndpoint()`, client SSRF du flux de données). Les extraire demande de changer une signature upstream : jalon 3.

## 3. Ajouter une capacité

Exemple : le module `mailbox`.

1. **Le code** : `internal/veridian/mailbox/` (`package mailbox`), avec ses tests dans le même dossier.
2. **L'enregistrement**, dans le paquet, à l'import :

   ```go
   func init() {
       platform.Register(platform.Capability{
           Name: "mailbox",
           Wire: func(h platform.Host) error {
               // routes : h.Mux().Handle("/api/veridian/mail.list", h.RequireAuth()(handler))
               // consommateur IMAP : h.IMAPPoller().RegisterConsumer(c)
               return nil
           },
           Start: func(ctx context.Context, h platform.Host) error { /* boucle de fond, s'arrête à ctx.Done() */ return nil },
           Stop:  func(ctx context.Context) error { return nil },
       })
   }
   ```

   `Wire` tourne une fois, après les migrations Veridian, quand dépôts, services, `mux` et poller existent ; une erreur ou un panic empêche le démarrage (le staging E2E l'attrape). `Start` tourne quand le serveur HTTP est créé ; `Stop` à l'arrêt.
3. **Le branchement** : un fichier `internal/app/veridian_mod_mailbox.go` qui ne contient que `import _ "github.com/Notifuse/notifuse/internal/veridian/mailbox"`. Un fichier par capacité : aucun conflit de fusion entre lots.
4. **Ce que le module voit** : l'interface `platform.Host` (config, logger, base système, mux, authentification, dépôts, services, poller, worker, contexte d'arrêt). Il manque un accesseur : on l'ajoute à `Host` et à `veridianHost` dans `veridian_wiring.go`, pas dans `app.go`.
5. **Une migration** si le module a un schéma (section 4).
6. **CLI et OpenAPI** pour chaque route (règle 6).

## 4. Migrations Veridian

Paquet `internal/migrations/veridian`. Pourquoi pas le gestionnaire upstream : il suit un scalaire (`settings.db_version`) et ne rejoue jamais un numéro inférieur. Si V65 livre avant V63, V63 ne tournerait jamais en production. Ici chaque migration est suivie **par numéro** dans la table `veridian_migrations` de chaque base (système et workspace), indépendamment de `config.VERSION`. Le runner joue au démarrage les numéros manquants dans l'ordre ; un workspace créé ensuite reçoit toutes les migrations à sa création (`database/veridian_hooks.go`), donc **rien à ajouter à `database/init.go`** et **aucun bump de `config.VERSION`**.

Numéros réservés : V63 = L1, V64 = L1b, V65 = L2, V66 = L3, V67 = L4. Au-delà, main ajoute la ligne dans `Reserved` (`spec.go`).

```go
// internal/migrations/veridian/v65_dossiers.go
func init() {
    Register(Spec{
        Number: 65, Lot: "L2", Name: "dossiers",
        Workspace: []string{
            `CREATE TABLE IF NOT EXISTS veridian_folder (id VARCHAR(36) PRIMARY KEY, ...)`,
            `CREATE INDEX IF NOT EXISTS idx_veridian_folder_parent ON veridian_folder(parent_id)`,
        },
    })
}
```

Règles vérifiées à l'enregistrement (`CheckExpandOnly`, panic au démarrage et en test) : `CREATE TABLE/INDEX` et `ADD COLUMN` avec `IF NOT EXISTS` ; colonne `NOT NULL` ajoutée avec `DEFAULT` ; `INSERT` avec `ON CONFLICT` ; interdits : `DROP`, `TRUNCATE`, `RENAME`, `DELETE FROM`, `SET NOT NULL`, changement de type, `ADD CONSTRAINT`, `CONCURRENTLY` (le runner joue chaque migration dans une transaction). Une fonction Go (`SystemFunc`, `WorkspaceFunc`) n'est pas analysable : même discipline, revue obligatoire.

**Contract** : une étape destructive est une migration à part, d'un numéro attribué par main, avec `Contract: true` et `ContractOf: <numéro de l'expand>`, livrée seulement quand le tag précédent n'est plus en service. Le contrôle `check-migration-safety.sh` ne couvre que `internal/migrations/v*.go` : pour ce registre, c'est `CheckExpandOnly` qui garde.

Échec d'un workspace (base orpheline ou injoignable) : journalisé en erreur, rejoué au démarrage suivant, sans empêcher les autres ni le démarrage. Échec sur la base système : le démarrage échoue, comme pour le gestionnaire upstream.

## 5. Le détecteur de fichiers gelés

`scripts/veridian/frozen-files-check.sh` compare `BASE_REF...HEAD` (défaut `origin/veridian`) pour chaque fichier de `frozen-files.tsv`. Sorties : 0 conforme, 1 logique ajoutée au-delà du branchement, 2 non abouti (base introuvable, fichier gelé disparu : la liste a dérivé).

- Branché dans `.husky/pre-push` (local), dans `scripts/ci/controles.tsv` (palier rapide) et dans le job « Constitution CI §1 » de `veridian-ci.yml` (runner auto-hébergé).
- `--selftest` éprouve le détecteur sur un dépôt jetable (accepte un branchement, échoue sur trois lignes, sur un bloc `if`, accepte les commentaires et les suppressions, signale un fichier gelé absent). Il tourne en CI avant le contrôle réel.
- **Dérogation** : trailer `Frozen-Override: <raison>` dans un commit de la plage (synchronisation upstream, déplacement de câblage), ou `FROZEN_OVERRIDE="<raison>"`. Elle passe et s'affiche. L'utiliser pour contourner une règle gênante plutôt que pour un cas prévu est un défaut à signaler.

## 6. Tester

- Go : sur node02, conteneur éphémère `veridian-gotest-api*`, sous le verrou commun (`flock /tmp/claude-1000/gotest.lock`). Cibler les paquets touchés.
- Un test jamais vu échouer est du maquillage : mutation volontaire avant de croire un test.
