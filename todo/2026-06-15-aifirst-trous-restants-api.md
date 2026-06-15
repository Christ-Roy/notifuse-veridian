# [NOTIFUSE] 🟡 P2 — Trous restants pour un pilotage 100% AI-first par API

> **Sévérité** : 🟡 P2 (complétude AI-first — pas bloquant, le socle est livré)
> **Owner** : agent notifuse-veridian
> **Créé** : 2026-06-15. Signalés honnêtement par l'agent aifirst-api après livraison
> de `automations.enroll` + OpenAPI complet + AGENT-API.md (SHAs 5bb38d8f + 6abf2e38).

## Contexte
Notifuse est désormais largement pilotable par API (automations CRUD+enroll, templates
+compile, transactional.send/testTemplate, config cold, breakdown, OpenAPI à jour, guide
AGENT-API.md). Il reste 3 trous pour qu'un agent pilote TOUT sans intervention humaine.

## Trou 1 — 🟡 Pas de génération/rotation d'API key par API (le plus impactant)
Aujourd'hui un agent doit recevoir un token DÉJÀ émis (généré via console Settings → API keys
par un humain). Pour un pilotage 100% autonome (un agent qui provisionne + se donne un token +
orchestre), il manque un endpoint owner-only de génération/rotation d'api_key.
- ⚠️ Sensible (sécu) : un tel endpoint doit être OWNER-ONLY strict + audit + rotation propre.
- Note : le provision HMAC (`/api/tenants/provision`) renvoie DÉJÀ une api_key à la création —
  donc le cas "nouveau tenant" est couvert. Le trou = rotation/régénération d'un tenant existant
  sans passer par la console.
- DoD : endpoint `apiKeys.create`/`apiKeys.rotate` owner-only + test + doc OpenAPI + AGENT-API.

## Trou 2 — 🟢 transactional.create/update absents de l'OpenAPI (doc-only)
Les routes EXISTENT (`internal/http/transactional_handler.go` : transactional.create/update/delete)
mais ne sont pas documentées dans l'OpenAPI (aifirst-api est resté fidèle au déjà-documenté).
- DoD : ajouter transactional.create/update/delete aux chunks OpenAPI + AGENT-API. Commit doc-only `[risk:low]`.

## Trou 3 — Désinscription cold : absente PAR CHOIX produit (pas un trou)
Pas de List-Unsubscribe sur le cold (décision Robert, façon Lemlist, assumé — cf ticket
2026-06-14-cold-no-unsubscribe). Mentionné pour mémoire, RIEN à faire.

## Priorisation
Trou 1 (rotation api_key) = le seul vrai manque pour l'autonomie agent, mais sensible (sécu) →
à faire proprement, pas dans la précipitation. Trou 2 = trivial doc-only. Trou 3 = no-op.
