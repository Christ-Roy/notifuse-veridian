# Fuite du password IMAP en clair dans la réponse `workspaces.get`

> **Sévérité** : 🟡 P1 (fuite de secret, mais limitée aux membres du workspace)
> **Owner** : agent notifuse
> **Créé** : 2026-06-15
> **Découvert par** : agent IAC (validation E2E staging du CLI plan/apply cold)

## Contexte

En validant le CLI IAC `scripts/iac/notifuse-iac.sh` contre staging, j'ai lu
l'état réel du workspace `coldtunnel` via `GET /api/workspaces.get?id=...` avec
un JWT owner. La réponse JSON contient l'intégration IMAP de retour **avec son
`imap_settings.password` EN CLAIR** :

```json
"imap_settings": { "host":"imap.larksuite.com", "port":993,
  "username":"robert.brunon@veridian.site", "password":"<EN CLAIR>", ... }
```

Le password SMTP, lui, n'apparaît PAS dans la même réponse (asymétrie).

## Cause racine

- `internal/domain/veridian_imap_integration.go` : `IMAPSettings.Password` a le
  json tag `password,omitempty` (champ runtime, non persisté).
- `internal/domain/workspace.go:269-275` (`Integration.AfterLoad`, type IMAP,
  fork Veridian) : déchiffre `EncryptedPassword` → remplit `Password` (clair)
  pour le poller runtime.
- `WorkspaceService.GetWorkspace` → `repo.GetByID` → `workspace.AfterLoad` →
  l'objet déchiffré (Password clair rempli) est renvoyé **tel quel** par
  `WorkspaceHandler.handleGet` (`internal/http/workspace_handler.go`) sans
  masquage. Le `password,omitempty` est non-vide → il sort dans le JSON.

Le SMTP échappe à la fuite parce que son chemin de sérialisation re-masque le
password (à confirmer côté `EmailProvider`/handler) — l'IMAP n'a jamais reçu ce
masquage car ajouté par le fork sans répliquer la précaution upstream.

## Impact

- N'importe quel **membre** du workspace (pas juste owner) qui appelle
  `workspaces.get` reçoit le password IMAP en clair. Pour le tunnel cold, c'est
  le mot de passe de la boîte Lark `robert.brunon@veridian.site`.
- Surface : réponse API + tout cache navigateur/console qui logge la réponse.

## Demande (fix propre, hors scope sprint IAC)

Masquer `imap_settings.password` (et re-vérifier le SMTP par symétrie) dans la
réponse `workspaces.get` — ou plus généralement dans tout chemin de
sérialisation sortant d'un `Workspace`. Deux pistes :

1. **`MarshalJSON` sur `IMAPSettings`** qui omet toujours `Password` (clair) et
   `EncryptedPassword` à la sérialisation sortante — le runtime poller accède au
   champ Go directement, pas via JSON. C'est le fix le plus robuste (vaut pour
   tous les endpoints, pas juste `workspaces.get`).
2. Ou un `BeforeMarshal`/sanitize explicite dans `handleGet` qui vide les
   secrets (moins robuste : à répéter sur chaque handler qui renvoie un
   workspace).

Vérifier au passage que le SMTP est bien masqué partout (le test E2E suggère que
oui, mais le confirmer pour ne pas avoir de régression symétrique).

## Tier / promotion

🔴 HAUT (touche la sérialisation de secrets sur une route API). Push sans
`[risk:low]`, reco écrite + smoke (`curl workspaces.get` → vérifier
`password` ABSENT du JSON IMAP **et** SMTP) avant promo prod. Test colocalisé à
ajouter sur le `MarshalJSON`/sanitize choisi.

## Non bloquant pour le CLI IAC

Le CLI plan/apply fonctionne et est idempotent — cette fuite est un défaut
préexistant de l'API, indépendant du CLI. Signalé ici pour traçabilité.
