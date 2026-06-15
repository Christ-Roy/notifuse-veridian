# notifuse-iac — config cold "as files" idempotente

CLI `plan` / `apply` qui applique le manifeste déclaratif
`iac/coldtunnel/workspace.yaml` contre l'API Notifuse, de façon **idempotente**.

> But (Robert 2026-06-15) : « tout poser comme IAC idempotent pour avoir d'un
> coup d'œil notre contenu et l'éditer facilement depuis des fichiers ».

## Pourquoi bash + python3 (et pas un mini-CLI Go)

C'est de la **glue HTTP** : lire un YAML, injecter des secrets `${VAR}`, lire
l'état réel via l'API, diff, upsert. Pas de logique métier. Le pattern auth
owner + idempotence était déjà validé en bash dans `scripts/e2e/tunnel-send.sh`
(la référence d'or réutilisée ici). Un binaire Go ajouterait un cycle de build
pour zéro gain de robustesse sur ce périmètre. `python3` (présent partout dans
le repo) parse YAML/JSON et calcule le diff proprement. shellcheck-clean +
smoke test sans réseau.

## Usage

```bash
# Diff read-only entre déclaré et réel (par défaut: STAGING)
scripts/iac/notifuse-iac.sh plan  --env staging

# Converger l'état réel vers le déclaré (idempotent : re-run = 0 changement)
scripts/iac/notifuse-iac.sh apply --env staging

# PROD : opt-in EXPLICITE (apply prod exige --yes)
scripts/iac/notifuse-iac.sh plan  --env prod
scripts/iac/notifuse-iac.sh apply --env prod --yes

# Manifeste alternatif
scripts/iac/notifuse-iac.sh plan --manifest path/to/other.yaml
```

`plan` est toujours read-only. `apply` exécute, puis **re-plan automatiquement**
pour prouver la convergence (doit afficher « idempotence vérifiée »).

## Ce qui est géré

| Ressource | Endpoint API | Auth | Idempotence (clé stable) |
|---|---|---|---|
| Intégration SMTP d'envoi | `workspaces.{create,update}Integration` | **owner** | `name` = `sending_integrations[].id` |
| Boîte IMAP de retour | `workspaces.{create,update}Integration` | **owner** | `name` = `"Return inbox (cold bounce/reply)"` |
| Settings cold (rates/caps/pixel) | `workspaces.update` | owner (user) | clés `veridian_*` |

Le **workspace** lui-même est supposé déjà provisionné (HMAC Hub). Le CLI le
re-provisionne idempotemment (`created:false`) uniquement pour obtenir une
session owner.

## Secrets — jamais en clair sur disque versionné

Le manifeste ne contient que des références `${VAR}` (ex `${SMTP_AGENCES_PW1}`).
Au runtime, `notifuse-iac.sh` :

1. charge `~/credentials/.all-creds.env` (parsing `KEY=VALUE` robuste, **pas**
   `source` — qui planterait sur une valeur non quotée),
2. résout les `${VAR}` via `render.py` dans un fichier **temp 0600** sous
   `$TMPDIR`, supprimé en fin de run (`trap`),
3. l'`apply` POSTe les valeurs en clair vers l'API ; le `plan` n'affiche que des
   noms de champs (`smtp.host`), jamais de valeur → aucun secret à l'écran.

Un `${VAR}` manquant à l'`apply` = **échec dur** (on ne pousse jamais un
placeholder littéral). `render.py --redact <manifest>` rend une vue masquée
(debug manuel).

## Auth owner (le point dur)

`createIntegration`/`updateIntegration` sont **OWNER-ONLY** (cf
`docs/AGENT-API.md` §1). Le CLI obtient un JWT owner programmatiquement, sans
console :

```
provision (HMAC Hub→Notifuse, idempotent) → auto_login_url (TTL ~60s)
  → fetch HTML → extrait le JWT embarqué (setItem('auth_token', ...))
```

Pattern repris de `scripts/e2e/tunnel-send.sh`. Robustesse : provision + GET sont
`retry` 3× backoff (un hoquet TCP transitoire ne tue pas l'apply), `--max-time`
sur chaque curl.

## Fichiers

| Fichier | Rôle |
|---|---|
| `notifuse-iac.sh` | CLI : secrets, owner session, plan, apply, vérif idempotence |
| `render.py` | YAML → JSON résolu (`${VAR}` injectés ; `--redact` masque) |
| `plan.py` | moteur de diff (déclaré vs réel) → actions create/update/noop |
| `notifuse-iac.test.sh` | smoke test SANS réseau (render + diff + bodies) |

## Tests

```bash
scripts/iac/notifuse-iac.test.sh   # 17 assertions, zéro réseau
shellcheck scripts/iac/*.sh        # clean
```

## Limites connues

- Le contenu **listes / templates / séquences** du manifeste
  (`lists`/`templates`/`sequences`) n'est pas encore appliqué (commenté dans le
  YAML, à câbler quand le contenu réel sera prêt — endpoints `templates.create`
  + `automations.create` + `automations.enroll` déjà dispos côté API).
- Le `sending_window` (heures ouvrables) est déclaré mais l'application dépend
  du Lot WINDOWS (#21) côté worker — pas encore consommé par un endpoint.
- `rate_limit_per_minute` global (upstream, obligatoire `>0`) est posé à `600`
  par le CLI : c'est un plafond large, la vraie limite cold vient des
  `veridian_provider_class_rates` par classe.
