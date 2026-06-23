# GenerateMagicLink — vérifier le membership (defense-in-depth)

> **Sévérité** : 🟢 P3 (durcissement, PAS un trou exploitable)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-23 (trouvé en audit HUNT axe 1 sécu API)

## Contexte
`workspaces.generateMagicLink` (`internal/http/veridian_magic_handler.go`) émet un magic
link pour un `userEmail` en vérifiant seulement son existence globale (`users`), PAS son
membership au workspace ciblé.

## Pourquoi ce N'EST PAS exploitable (vérifié)
- `workspaceID` verrouillé à celui de l'API key caller (inféré, pas du body).
- Un userEmail non-membre ne gagne pas d'accès : l'autorisation reste gouvernée par
  `user_workspaces` à l'usage. Pas d'élévation cross-workspace.
→ Pas de fix urgent. Defense-in-depth manquant.

## Fix proposé (à froid, test soigné)
Vérifier le membership de userEmail au workspaceID avant d'émettre. Non-membre → 403.
⚠️ Test non-régression OBLIGATOIRE sur le flow magic-link cross-app (Hub→Notifuse,
provisioning + invitation) : owner provisionné OK, non-membre 403, membre invité OK.

## Fichiers
- `internal/http/veridian_magic_handler.go` + test colocalisé

## Note audit
Audit sécu HUNT axe 1 (2026-06-23) : 22 handlers tous authentifiés, zéro IDOR, zéro
injection, HMAC exemplaire, CORS strict. Seul durcissement résiduel.
