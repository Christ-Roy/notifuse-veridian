# CLI `notifuse` unifié — piloter l'app 100% par API (modèle CLI analytics)

> **Sévérité** : 🟡 P1 (demande Robert 2026-06-24 — outil de pilotage/test)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-24

## Demande Robert (verbatim)

« Je veux un CLI pour gérer complètement l'app notifuse, pouvoir faire des tests
dry-run pour être sûr que tout marche, etc. Il faut pouvoir tout piloter par API
comme le nouveau CLI veridian analytics. »

## Le modèle de référence : CLI `analytics`

`~/.claude/skills/analytics-provision/bin/analytics` (Python, 1 exécutable, 31KB) :
- Commandes verbe-style : `doctor`, `provision`, `verify` (DRY-RUN sans pollution),
  `status`, `keys:list/provision/revoke`.
- Credentials lus depuis `~/credentials/.all-creds.env`, jamais hardcodés.
- Défaut PROD, `--env staging` bascule. `doctor` = self-test (engine up + clé valide).
- `verify` = DRY-RUN : event synthétique qui ne touche jamais les compteurs réels.

## Matière existante côté Notifuse (à réutiliser, pas réinventer)

- `openapi.json` / `openapi/openapi.yaml` : **54 endpoints** RPC-style documentés.
- `docs/AGENT-API.md` : guide AI-first (auth Bearer JWT user/api_key, workspace_id
  dans le body, permissions par ressource, recettes endpoint par endpoint).
- `scripts/iac/notifuse-iac.sh` (+ plan.py/render.py) : plan/apply pour la config
  cold (intégrations, rates, caps). À absorber ou wrapper dans le CLI.
- Auth : `Authorization: Bearer <JWT user | JWT api_key>`. Provision tenant =
  HMAC Hub (`NOTIFUSE_HUB_API_SECRET`) → `auto_login_url` → JWT owner.

## Périmètre cible du CLI `notifuse`

- **doctor** : self-test (API up + clé HMAC/JWT valide, round-trip).
- **provision / wipe** : tenant jetable (HMAC Hub) — déjà rodé en smoke E2E.
- **verify / dry-run** : LE cœur de la demande. Tester qu'un flux marche SANS
  envoyer de vrai mail ni polluer (réutiliser le pattern cold-simulate/sink +
  les modes de test staging-only). Ex : « est-ce qu'un broadcast partirait bien
  avec cette config ? » → simulation, pas d'envoi.
- **broadcasts / templates / contacts / lists / automations** : CRUD + send-test.
- **integrations** : config cold (rates/caps/exclusion/window/pixel par infra) —
  absorber notifuse-iac.sh.
- **breakdown / analytics / status** : lecture d'état consolidé d'un workspace.
- **keys** : list/provision/revoke api_key.

## Arbitrages à trancher avec Robert (avant de coder)

1. **Langage** : Python (comme `analytics`, cohérence) vs Go (comme l'app). → Python
   recommandé (cohérence avec le CLI de référence + itération rapide).
2. **Emplacement** : skill `~/.claude/skills/notifuse-cli/bin/notifuse` (comme
   analytics-provision) vs `scripts/iac/` du repo. → skill recommandé (c'est un
   outil de pilotage agent, pas du code applicatif).
3. **Scope V1** : tout (54 endpoints) d'un coup vs MVP (doctor + provision + verify
   dry-run + broadcasts + integrations cold) puis itération.

## Définition of done

- 1 exécutable `notifuse`, credentials depuis .all-creds.env, défaut prod + --env staging.
- `doctor` + `verify`/dry-run fonctionnels (le cœur : tester sans casser/polluer).
- Couvre le flux cold complet (provision → config infra → template → broadcast dry-run).
- README + intégration skill. Testé contre staging réel.
