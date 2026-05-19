# 2026-05-19 — Format d'erreur standardisé

> **Spec** : `../CONTRAT-HUB.md` §5.10
> **Sévérité** : 🟡 P2
> **Effort** : M (2-8h)

## Contexte

Aujourd'hui `WriteJSONError` (`internal/http/utils.go:11`) retourne :
```json
{"error": "tenant_id required"}
```

Contrat §5.10 exige :
```json
{
  "error": "invalid_payload",     // code machine-readable
  "message": "tenant_id required", // humain
  "details": {"field": "tenant_id"} // optionnel
}
```

## Codes obligatoires (énumération §5.10)

- `invalid_payload`, `unauthorized`, `forbidden`, `tenant_not_found`, `tenant_soft_deleted`, `owner_mismatch`, `quota_exceeded`, `plan_not_found`, `plan_locked`, `api_key_multi_workspace`, `idempotency_key_mismatch`, `purge_not_eligible`, `internal_error`

## Travail

1. **Type `ErrorResponse`** dans `internal/http/utils.go` :
   ```go
   type ErrorResponse struct {
       Error   string                 `json:"error"`
       Message string                 `json:"message"`
       Details map[string]interface{} `json:"details,omitempty"`
   }
   ```

2. **Helper `WriteJSONErrorCode(w, code, message, status, details...)`** — backward-compat avec `WriteJSONError`.

3. **Remapping des handlers** : tous les `WriteJSONError(w, "...", http.Status...)` du repo passent au nouveau format. Concerne ~30 callsites dans `internal/http/`.

4. **Tests** : updater les assertions E2E (`tests/e2e-veridian/specs/*.spec.ts`) qui matchent `body.error == "message"` → désormais `body.error == "code"` + `body.message == "message"`.

## Risque

P3 sur la migration : peut casser des consumers qui parsent l'ancien format. Vérifier côté Hub si on parse `error` (le champ change de sémantique : "message lisible" → "code machine").

**Option safe** : garder l'ancien champ `error` = message lisible + ajouter `error_code` à côté. Moins propre contractuellement mais zéro breaking change.

**Décision Robert** : on shipe le format strict §5.10 (breaking) ou option safe (additive) ? Reco : **additive d'abord, strict une fois le Hub aligné**.
