# Carte du poids du fork Notifuse (mesure du 06/11/2026)

**Verdict : ne pas rattraper les 441 commits.** Au-delà de la v39.1, l'amont passe sous licence
BSL, et notre usage (36 workspaces en prod) tombe dans ce qu'elle réserve à une clé payante.
Rattraper jusqu'à la v39.1 reste possible, pour 201 conflits et un bénéfice faible. Assumons un
fork dur, gardé léger, et prenons à l'amont fonctionnalité par fonctionnalité.

L'identité ne pèse presque rien : environ 1 % du diff. Ce qui alourdit le fork, c'est le moteur
de prospection, les tests et les traductions générées.

## 1. L'écart

Base commune : `d8f31f81` du 31/05/2026. Comparaison entre `origin/veridian` (`f7d175db`) et
`upstream/main` (`fb9a92c7`, v42.2 du 05/11/2026).

| | Fichiers | Lignes |
|---|---|---|
| Fork depuis la base (1145 commits) | 1875 | +346 234 / −84 729 |
| Amont depuis la base (441 commits) | 1888 | +415 222 / −57 902 |

Sur les 1875 fichiers du fork : 1355 ajoutés, 376 modifiés, 144 supprimés (dont 127 pour le blog
de la console, `blog_editor` et `blog`).

## 2. Ce qui pèse

Lignes ajoutées, classées par chemin de fichier. Le classement est un script, pas une lecture
fichier par fichier : les frontières entre « identité/SaaS » et « prospection » sont approximatives.

| Famille | Fichiers | Lignes ajoutées | Part |
|---|---:|---:|---:|
| Tests (Go Veridian, Go amont, E2E) | 470 | 100 304 | 29 % |
| Traductions de la console (générées) | 18 | 66 570 | 19 % |
| Documentation et tickets | 256 | 37 941 | 11 % |
| Console : composants `veridian_*` | 178 | 26 141 | 8 % |
| Modules `internal/veridian/*` | 116 | 23 927 | 7 % |
| Go : moteur de prospection (cold) | 117 | 19 812 | 6 % |
| Go : Hub et SaaS (identité, plans, paywall) | 48 | 14 425 | 4 % |
| CI et infra | 79 | 10 854 | 3 % |
| Go : autres fichiers Veridian | 69 | 10 764 | 3 % |
| Console : autres fichiers | 210 | 8 835 | 3 % |
| Divers (fichiers de verrouillage, OpenAPI, etc.) | 34 | 8 570 | 2 % |
| **Go : fichiers amont modifiés** | 95 | 7 674 | 2 % |
| **Console : fichiers amont modifiés** | 116 | 5 064 | 1 % |
| Migrations | 40 | 3 042 | 1 % |
| Mocks | 29 | 2 311 | 1 % |

Les deux lignes en gras sont celles qui créent les conflits de fusion. Le reste vit à côté de
l'amont et ne gêne pas, ou seulement par sa masse.

### Identité

| Périmètre | Lignes Go | Contenu |
|---|---:|---|
| Identité stricte | environ 3 300 (15 fichiers) | rebond et autologin, découverte Hub, membres, invitations, transfert de propriétaire |
| Plans et paywall | environ 4 000 | plan par workspace, gel, suppression logique, obfuscation |
| Reste du contrat Hub | environ 7 200 | provisioning, cycle de vie, tarifs, nettoyage |

Le rebond seul tient en moins de 500 lignes (`veridian_sso_handler.go`,
`veridian_autologin_handler.go`, `veridian_token.go`), plus `IssueMagicLinkForHub`. L'identité n'est
donc pas ce qui rend ce fork lourd.

Le contrat Hub touche quand même le cœur de l'amont en quelques points : `users.hub_user_id` et
ses méthodes dans `internal/domain/user.go` et `internal/repository/user_postgres.go` (+169 lignes),
`CreateAutoLoginSession` dans `user_service.go`, et le câblage.

### Métier

Prospection, boîte unifiée, dossiers, politique d'envoi et réconciliation sont notre produit, et
l'amont n'a pas d'équivalent pour la plupart. Ces briques restent, quel que soit le choix sur le
rattrapage. Pour rappel, l'audit du 28/09 (`AUDIT-FORK-UPSTREAM-2026-09-28.md`) concluait déjà à
« garder » sur le moteur de prospection.

## 3. Ce qui pourrait sortir de l'arbre amont

Le chantier est déjà lancé : `docs/veridian/MODULARITE.md` (lot L0, 29/10) a créé `internal/veridian/`,
un point de câblage unique, un registre de migrations séparé et 8 fichiers gelés, contrôlés par un
détecteur. La marche suivante, sans urgence :

| Candidat | Volume | Gain | Risque |
|---|---:|---|---|
| Les 12 fichiers Go amont les plus patchés | environ 3 800 lignes sur 7 674 | la moitié de la surface de conflit Go | moyen : `message_history_postgre.go` (+855) et `workspace_service.go` (+608) sont couplés |
| Hub et identité vers `internal/veridian/hub` | 14 425 lignes | zéro conflit futur, mais ces fichiers `veridian_*` n'en créent déjà presque pas | faible, gain surtout de lisibilité |
| Traductions générées (66 570 lignes) | 18 fichiers | ne plus versionner du généré éviterait ce bruit de fusion | à mesurer : le build console doit les produire |
| Blog supprimé (127 fichiers) | suppressions | restaurer l'arbre et masquer par la route éviterait une bonne part des 21 conflits « modifié/supprimé » | faible |

Contradiction à trancher dans nos règles : `CLAUDE.md` (règle 2 et fiche `02`) impose des fichiers
`veridian_*.go` à plat « jamais de sous-dossier », alors que `MODULARITE.md` impose
`internal/veridian/<module>/`. Le code récent suit `MODULARITE.md`. Il faudrait aligner `CLAUDE.md`.

**Aucun refactor n'a été fait pour cette carte.**

## 4. Ce que coûte le rattrapage

Fusion simulée avec `git merge-tree` sur l'arbre du fork (mesure, rien n'a été fusionné) :

| Cible | Commits | Conflits | Go prod | Go test | Console | Licence |
|---|---:|---:|---:|---:|---:|---|
| v33.0 | 17 | 57 | 19 | 13 | 22 | AGPL |
| v36.1 | 46 | 98 | 32 | 24 | 31 | AGPL |
| v38.0 | 83 | 175 | 48 | 38 | 75 | AGPL |
| **v39.1** | 93 | 201 | 56 | 48 | 83 | **AGPL, dernière version** |
| v40.0 | 98 | 207 | 58 | 49 | 84 | BSL |
| v41.0 | 106 | 214 | 59 | 52 | 87 | BSL |
| v42.2 | 441 | 247 | 69 | 61 | 98 | BSL |

Ces conflits sont ceux que Git détecte. S'y ajoutent les conflits de sens, que seuls les tests
révèlent :

- **Numéros de migration en collision.** Le fork et l'amont ont chacun leurs propres V33 à V42,
  au contenu différent (13 conflits dans `internal/migrations` pour la seule v39.1). La base de prod
  est à `db_version = 62` : le gestionnaire amont ne rejouerait aucune migration amont de numéro
  inférieur. Il faudrait les réécrire une à une dans `internal/migrations/veridian/`, sans quoi le
  schéma resterait silencieusement incomplet.
- **Cascade d'envoi.** `queue/worker.go` et `message_history_postgre.go` portent la protection de
  réputation. Une fusion ratée peut griller un domaine d'envoi sans qu'aucun test ne rougisse
  (règle 4 du `CLAUDE.md`).
- **Fichiers supprimés** (blog surtout) : 21 conflits « modifié/supprimé » à trancher un par un.

**Estimation, pas mesure** : la v39.1 demande 4 à 8 journées d'agents, soit la résolution, puis le
portage des migrations, la batterie E2E sur staging (tier 🔴), le monitoring et les retours
arrière. Cela mobilise une bonne part du quota d'une semaine, pour aucune fonctionnalité demandée
aujourd'hui. Et il faudrait recommencer, puisque la dette se recrée à chaque version.

### Le piège de licence au-delà de la v39.1

Depuis la v40.0, l'amont est sous Business Source License 1.1 (chaque version repasse en AGPL
quatre ans après sa sortie). L'usage en production n'est permis que sans les « Licensed Features »,
sauf clé payante. Parmi elles :

- **créer plus de trois workspaces sur un déploiement** : nous en avons 36 en prod ;
- la connexion par SSO OpenID Connect ;
- les permissions fines par membre ou clé d'API ;
- les variantes multilingues de modèles ;
- le journal d'audit.

Notre rebond HMAC n'est pas le SSO OpenID de l'amont, mais la limite de trois workspaces nous
concerne de plein fouet. Le prix de la clé n'a pas été relevé. Cette lecture n'est pas un avis
juridique : c'est le texte du fichier `LICENSE` de l'amont, à faire valider si le sujet revient.

Conséquence pratique : **aucun code amont postérieur à la v39.1 ne doit entrer dans le fork**, même
par cherry-pick. Les correctifs de sécurité publiés depuis se lisent dans le changelog et se
réécrivent chez nous. Exemple : la protection SSRF des webhooks de la v42.0, pour laquelle nous
avons déjà `webhook_ssrf_guard.go`.

## 5. Recommandation

| Option | Coût | Effet | Avis |
|---|---|---|---|
| A. Fusionner jusqu'à la v42.2 | 247 conflits, migrations, et une licence payante | suit l'amont | **Non** : contraire à notre usage sans dépense à valider par Robert |
| B. Fusionner jusqu'à la v39.1 | 201 conflits, 4 à 8 journées d'agents (estimation) | gagne Zapier, l'analytique web et des correctifs | Non pour l'instant : aucun besoin ne le justifie |
| C. Fork dur assumé | presque rien | on prend à l'amont ≤ v39.1 au cas par cas, et on réécrit ce qui vient après | **Oui** |

Si Robert retient C :

1. Retirer de `docs/claude/41-sync-upstream.md` la procédure `git merge main`, qui n'est plus
   praticable, et y écrire la frontière v39.1.
2. Veiller chaque version amont par son changelog (sécurité d'abord), sans tâche planifiée nouvelle.
3. Poursuivre `MODULARITE.md` quand on touche ces fichiers, sans chantier dédié.

Ce qui ferait changer d'avis : un besoin client précis sur une fonctionnalité de la v33 à la v39,
ou le chiffrage d'une clé BSL qui rendrait l'option A rentable.

## 6. Pour rejouer ces mesures

```bash
git fetch upstream main --tags
git rev-list --left-right --count origin/veridian...upstream/main
git merge-tree --write-tree origin/veridian v39.1 | grep -c '^CONFLICT'
git diff --numstat --no-renames "$(git merge-base origin/veridian upstream/main)" origin/veridian
git show upstream/main:LICENSE | sed -n 1,60p
```
