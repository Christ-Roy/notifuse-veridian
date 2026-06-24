# CLI `notifuse` — parité TOTALE console + tout configurable, IA-first

> **Sévérité** : 🟡 P1 (demande Robert 2026-06-24, suite du CLI V1 livré)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-24
> **Dépend de** : CLI V1 livré (`~/.claude/skills/notifuse-cli/bin/notifuse`, 70+ cmds)

## Demande Robert
« Pouvoir TOUT faire : intégration Supabase, pousser un template, tout configurer
avec les settings. Tout faisable pour un tenant via CLI avec sa clé API. » +
« propre, bonne UX, **pas interactif car c'est d'abord IA-first**. »

## Principe d'archi (validé Robert)
Objectif : un tenant pilote 100% de SON workspace par CLI. Implémentation SANS
modif sécu app : le CLI résout le BON niveau d'auth de façon TRANSPARENTE :
- api_key → broadcasts/templates/contacts/automations/transac/webhooks/reads.
- JWT owner auto (provision→auto_login→token, déjà câblé) → ops OWNER-ONLY
  (createIntegration Supabase/SMTP/IMAP, settings sensibles).
- HMAC Hub → provisioning/admin.
On NE lève PAS le owner-only serveur (clé volée ne doit pas reconfigurer les
secrets d'envoi). Le CLI fait le pont. (Robert: "JWT owner auto transparent".)

## UX (IA-first, gravé)
- 100% NON-INTERACTIF : zéro prompt, tout en flags/args/stdin. Scriptable d'un coup.
- Sortie JSON structurée, exit codes, --json/--quiet. Idempotent quand possible.
- Help clair, erreurs lisibles (pas de stacktrace), noms cohérents <resource>:<verbe>.

## Delta à combler (vérifié 2026-06-24)
1. **Intégrations TOUS types** : Notifuse a 5 types (Email/SMTP, IMAP, Supabase,
   Firecrawl, LLM). CLI couvre SMTP+IMAP. AJOUTER create/update/delete pour
   Supabase (url+service_role_key), Firecrawl (clé), LLM (provider+clé). Owner-only.
2. **Settings COMPLETS** : WorkspaceSettings ~30+ champs (file_manager/S3, blog,
   branding/cover, tracking, custom_endpoint, timezone, lang, secret_key...). CLI
   ne pilote que cold. AJOUTER settings:get (dump) + settings:set/update couvrant
   tous les champs de l'allowlist UpdateWorkspace. Owner-only.
3. **Push template RÉEL** : AJOUTER templates:push --file template.mjml --name X
   [--test-data d.json] = create OR update idempotent avec contenu réel depuis
   fichier + compile + validation + test optionnel (gated --real-send).
4. **Parité console=CLI** : auditer chaque écran/action console (console/src/),
   garantir une commande CLI équivalente, livrer une TABLE DE PARITÉ dans SKILL.md,
   combler les trous. Couvrir : Settings (toutes sections), Integrations, Templates,
   Broadcasts, Contacts/Lists, Automations, Webhooks, Analytics, Team, API keys.

## DoD
- 5 types d'intégration créables/éditables par CLI.
- settings:get + settings:set/update sur tous les champs éditables.
- templates:push --file (MJML/HTML fichier, idempotent, IA-first).
- Table de parité console↔CLI dans SKILL.md, trous comblés.
- 100% non-interactif, JSON, exit codes, help propre. JWT owner auto transparent.
- Testé staging réel (provision→exercer→wipe), ZÉRO mail réel.

## Fichiers
~/.claude/skills/notifuse-cli/bin/notifuse + SKILL.md. Lecture seule: console/src/,
internal/domain/workspace.go (allowlist UpdateWorkspace), openapi.json.
