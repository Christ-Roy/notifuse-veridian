# Aligner le masquage JSON des secrets : Supabase / Firecrawl / LLM

> **Sévérité** : 🟢 P2 (cohérence sécu, pas une fuite de plaintext)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-24
> **Trouvé par** : audit CLI parité (axe intégrations tous types)

## Constat (vérifié dans le code 2026-06-24)

Deux types d'intégration masquent leurs secrets en sortie JSON via `MarshalJSON` :
- `internal/domain/email_provider_smtp.go` → `MarshalJSON` présent ✅
- `internal/domain/veridian_imap_integration.go` → `MarshalJSON` présent ✅

Trois types ne l'ont PAS :
- `internal/domain/supabase_integration.go` → ❌
- `internal/domain/firecrawl_integration.go` → ❌
- `internal/domain/llm.go` / `llm_provider*.go` → ❌

## Nuance importante (ce n'est PAS une fuite de plaintext)

Contrairement à ce qui a pu être remonté brut, les 3 types **ne renvoient pas le
secret en clair** : le champ clair (`api_key`, `signature_key`) est `omitempty`
et vidé avant réponse (commentaire explicite Supabase : *"cleared before API
responses"*). Ce qui transite dans `workspaces.get` côté owner = la version
**chiffrée** (`encrypted_api_key`, `encrypted_signature_key`). Donc :

- **SMTP/IMAP** : `MarshalJSON` masque AUSSI le ciphertext (rien ne sort).
- **Supabase/Firecrawl/LLM** : le ciphertext sort dans la réponse owner.

C'est une **asymétrie de traitement**, pas une fuite de clé exploitable (le
ciphertext est inutile sans la passphrase serveur `SecretKey`). D'où 🟢 et non 🔴.

## Demande

Décider de la doctrine et l'appliquer uniformément aux 5 types :
- **Option A (recommandée)** : ajouter `MarshalJSON` à Supabase/Firecrawl/LLM qui
  masque aussi le ciphertext (`encrypted_*` → `"***"` ou omis), pour aligner sur
  SMTP/IMAP. Defense-in-depth : un ciphertext en réponse API ne sert à rien de
  légitime côté client.
- **Option B** : assumer que le ciphertext peut sortir (il est inerte) et au
  contraire RETIRER le masquage agressif de SMTP/IMAP — moins souhaitable.

Reco : Option A. Fichiers veridian dédiés si possible (`MarshalJSON` est une
méthode de domaine — vérifier si on peut l'ajouter sans patcher upstream ; ces 3
structs sont upstream → si patch upstream nécessaire, créer un wrapper veridian
ou documenter le diff INLINE dans CLAUDE.md comme les autres).

## Test

Unit : marshal d'une intégration de chaque type → assert que `encrypted_*`
n'apparaît pas (ou est masqué) dans le JSON. + E2E owner `workspaces.get` sur un
workspace ayant les 3 types → vérifier le masquage réel.

## Hors scope

Le CLI `notifuse` ne fait que refléter le comportement serveur — rien à changer
côté CLI. Ce ticket est du code repo (domain layer).
