# Notifuse Veridian — environnement de dev rapide

> Cycle de feedback **2-3s** au lieu de 10 min de CI. Pour itérer sur des fixes
> Veridian quand on n'a pas envie d'attendre staging→prod après chaque commit.

## Quick start (1 fois)

```bash
make dev-bootstrap     # check tools + génère .env + injecte secrets Hub
make dev-up            # démarre Postgres test (:5433) + Mailpit (:1025/:8025)
make dev               # Air hot-reload sur le serveur Go
```

Ouvre un 2e terminal pour tester :
```bash
curl http://localhost:8080/healthz
```

À la fin :
```bash
make dev-stop          # stop Postgres + Mailpit + Tailscale
```

## Quoi tourne où

| Service | URL locale | Rôle |
|---|---|---|
| Notifuse API + console | `http://localhost:8080` | Le serveur Go avec Air hot-reload |
| Postgres test | `localhost:5433` (user `notifuse_test` / pw `test_password`) | DB système + workspaces |
| Mailpit SMTP | `localhost:1025` | Reception emails pour test |
| Mailpit UI | `http://localhost:8025` | Voir les emails reçus en local |

## Hot reload Air

`.air.toml` (déjà présent) déclenche un rebuild à chaque save dans `internal/`, `pkg/`, `cmd/`. Rebuild en ~1s. Le serveur redémarre auto.

Pour exclure des fichiers du watch, éditer `.air.toml` clé `exclude_regex`.

## Tunnel HTTPS public (Tailscale Funnel)

Si tu veux tester ton Notifuse local depuis une autre machine, ou contre le Hub déployé :

```bash
make dev-tailscale     # expose http://localhost:8080 en https://mail.tail-net.ts.net
```

Tu obtiens une URL stable HTTPS avec un certif valide. Tant que ta machine est en ligne, l'URL fonctionne.

Pour stop : `make dev-stop` ou `tailscale serve reset`.

## Tester les endpoints HMAC Hub

`.env` contient déjà `HUB_API_SECRET` (récupéré depuis `~/credentials/.all-creds.env`). Tu peux donc signer des requêtes HMAC contre ton localhost :

```bash
SECRET=$(grep "^HUB_API_SECRET=" .env | cut -d= -f2-)
TENANT="local-test-$(date +%s)"
TS=$(($(date +%s) * 1000))
BODY="{\"tenant_id\":\"$TENANT\",\"owner_email\":\"alice@local.test\",\"plan\":\"free\"}"
SIG=$(printf '%s' "${TS}.${BODY}" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $NF}')

curl -X POST http://localhost:8080/api/tenants/provision \
  -H "X-Veridian-Timestamp: $TS" \
  -H "X-Veridian-Hub-Signature: $SIG" \
  -H "Content-Type: application/json" \
  -d "$BODY"
```

## Workflow typique pour fixer un bug Veridian

1. Reproduire le bug en local : `curl ... http://localhost:8080/...`
2. Éditer le code → Air rebuild auto → re-curl pour valider
3. Lancer les tests unitaires colocalisés : `go test ./internal/service/ -run TestVeridianService_XXX -v`
4. Une fois OK en local + tests unitaires verts : commit + push → CI confirme

**Économie estimée** : ~10 min × N itérations dans un debugging session.

## Reset DB locale

```bash
make dev-stop
docker volume rm notifuse-veridian_postgres_test_data    # ou nom du volume
make dev-up
```

## Debug Mailpit

UI : http://localhost:8025

Tous les emails envoyés par Notifuse (magic links, transactional, etc.) atterrissent là sans partir vers Brevo/SES. Tu peux inspecter le HTML/text, headers, content multipart.

## Limites connues

- **Le Hub local n'est pas démarré** par cette commande. Si tu veux tester le flow complet Hub→Notifuse, soit pointer ton Hub local vers `http://localhost:8080` (via env `NOTIFUSE_URL`), soit utiliser `make dev-tailscale` pour exposer Notifuse et pointer un Hub déployé dessus.
- **Pas de console UI build** (le hot-reload est backend only). Pour reload la console TypeScript : `cd console && npm run dev` dans un terminal séparé (port 5173 par défaut).
- **Pas de Twenty/Supabase local** — uniquement Notifuse + dépendances directes.

## Status check

```bash
make dev-status
```

Affiche : containers Docker, Air process, Tailscale serve, healthcheck local.
