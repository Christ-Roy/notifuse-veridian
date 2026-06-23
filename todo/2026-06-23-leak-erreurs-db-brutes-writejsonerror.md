# Leak d'erreurs internes brutes au client sur les 500 (err.Error() → WriteJSONError)

> **Sévérité** : 🟢 P2 (HMAC/admin only, caller de confiance ; leak = noms de tables/schéma DB, PAS de credentials)
> **Owner** : agent notifuse
> **Créé** : 2026-06-23
> **Trouvé par** : HUNT axe 2 (réponses & contrats API)

## ⚠️ STATUT (2026-06-23) — partiellement livré par accroc de coordination

Le sweep d'une PARTIE de ces sites a été poussé sur `veridian` (staging) en commit
**`fdbad2cf`** AVANT que la décision "Option B / ne pas coder maintenant" du lead
n'arrive (messages croisés). `fdbad2cf` couvre **24 sites / 8 handlers** (les
handlers hors-provisioning + une partie de veridian_handler.go), avec 11 tests
durcis, contract-safe, hooks verts. **Décision lead à appliquer** : soit REVERT de
`fdbad2cf` (et ce ticket couvre TOUT le sweep, 57 sites, au calme), soit KEEP de
`fdbad2cf` (et ce ticket ne couvre plus que le RESTE = ~33 sites, dont le gros de
`veridian_handler.go`). Voir section "Reste à faire" selon le choix.

## Contexte

Les handlers Veridian renvoient l'erreur INTERNE brute (`err.Error()`,
potentiellement une erreur Postgres `pq: relation "X" does not exist` /
`pq: too many connections` / schéma DB) au client sur le **fallthrough 500
non-classifié** :

```go
WriteJSONErrorCode(w, ErrCodeInternalError, err.Error(), http.StatusInternalServerError, nil)
```

Recoupé par le lead : **57 sites `err.Error()` exposés au total** (tous status
confondus), dont **27 dans `veridian_handler.go`**. ⚠️ NUANCE IMPORTANTE : tous ces
57 ne sont PAS à fixer. Il faut distinguer 2 familles :
- **Fallthrough 500 non-classifié** → l'erreur résiduelle (potentiellement DB brute)
  = LE LEAK à fermer. C'est ce que cible le fix. (Dans `veridian_handler.go` : 16 sites,
  tous traités par `fdbad2cf`.)
- **Cas TYPÉS 4xx** (sentinels → 400/409/422 avec `err.Error()`) = message contractuel
  LÉGITIME, **NE PAS toucher** (le Hub parse certains). (Dans `veridian_handler.go` :
  7 sites restants — 1×400, 6×409 Conflict type plan_locked/owner_mismatch — à LAISSER.)

Donc le périmètre réel À FIXER ≈ les **fallthroughs 500 uniquement** (pas les 57 bruts).
C'est une CONVENTION du repo : seul le fallthrough 500 leak l'erreur résiduelle.

Le pattern `%w` traverse les wraps → `pq:` ressort jusqu'au client (confirmé par secu).

## Sévérité réelle : FAIBLE

- 100% des endpoints concernés sont **HMAC Hub** ou **admin/staging** → caller = le
  Hub de confiance, PAS l'internet public.
- Le leak = noms de tables / schéma / état infra, **JAMAIS de credentials**.
- Pas d'urgence. Mais incohérent : le repo a DÉJÀ fermé ce pattern ailleurs
  (P0 dashboard 2026-06-17, wrapper analytics 2026-06-18, et les handlers récents
  engagement-by-class / reply-stats / deliverability le font bien). Ces sites sont
  les **retardataires** d'une convention déjà adoptée.

## Le fix (mécanique, sûr, contract-safe)

Pour chaque fallthrough 500 :
1. **Garder le log** de l'erreur brute côté serveur (`h.logError(op, err, fields)` est
   déjà présent juste au-dessus dans la quasi-totalité des sites ; à AJOUTER là où il
   manque — ex. `grant_unlimited` ne loggait pas → zéro perte de debuggabilité).
2. **Renvoyer un message GÉNÉRIQUE** au client (const `veridianGenericInternalError`
   = "internal error", ou message fixe par endpoint type "failed to sync member").
3. **Conserver le code machine** `internal_error` (le client Hub en a besoin).

### Contrat Hub vérifié SAFE (R5, lecture des 2 côtés)

`veridian-hub/lib/notifuse/client.ts:240-251` : le client lit `errorBody.error` comme
message d'AFFICHAGE avec un **fallback générique**, passe `errorBody` complet (le
`code` reste dispo), et branche sa logique sur `response.status` + `code`, **JAMAIS
sur le texte de `error`**. → remplacer le message 500 de brut→générique ne casse rien.

### NE PAS toucher (cas typés, message contractuel)

- `user_not_in_app` (400, parsé EXACTEMENT par le Hub `bounce-apps.ts:228`)
- `plan_locked` / `tenant_not_found` / `owner_mismatch` / `cannot_remove_owner` …
- `ErrFrozenMemberRepoNotConfigured` (503, le message EST le sentinel = safe)
- `cold-simulate` (endpoint test staging-only, verbeux VOULU)

## Fichiers concernés (57 sites)

- `internal/http/veridian_handler.go` — **27 sites** (provisioning/update-plan/
  suspend/resume/delete/soft-delete/restore/touch/usage/status/limits/list/attach-*/
  wipe…). C'est le gros morceau, mérite une relecture dédiée + test par chemin.
- `internal/http/veridian_freeze_handler.go`, `veridian_rotate_transfer_handler.go`,
  `veridian_sso_handler.go`, `veridian_discovery_handler.go`, `veridian_magic_handler.go`,
  `veridian_grant_unlimited_handler.go`, `veridian_membership_handler.go`,
  `veridian_orphan_db_gc_handler.go`, `veridian_test_tenants_stats_handler.go` — le reste.
  (Plusieurs DÉJÀ traités par `fdbad2cf` si on le garde.)

## Tests (discipline Husky "Nuclear" 1-pour-1)

Tout handler modifié exige son `_test.go` co-localisé modifié avec un test qui
EXERCE la route (`httptest.NewRequest`). Pattern de test à appliquer (déjà fait dans
`fdbad2cf` pour les sites traités) : mock service renvoie une vraie erreur DB
(`errors.New("pq: ...")` / `"db connection lost"`), puis :
```go
assert.NotContains(t, rec.Body.String(), "pq:")          // leak fermé
assert.Contains(t, rec.Body.String(), "internal_error")  // code machine exposé
```
Ces tests sont MEANINGFUL et durables (ils cassent si le leak revient).

## Reste à faire (selon décision lead sur fdbad2cf)

- **Si REVERT `fdbad2cf`** : sweeper les 57 sites en 1 passe propre (helper +
  logError partout + 1 test no-leak par handler), relecture dédiée de
  `veridian_handler.go`.
- **Si KEEP `fdbad2cf`** : sweeper le RESTE (~33 sites, surtout les 27 de
  `veridian_handler.go` + ceux non couverts), même méthode.

Aucun bloquant prod. Pure défense en profondeur + cohérence API.
