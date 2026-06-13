# VeridianPricingSync : fetch failed — DNS `hub.veridian.site` non résolu depuis le container prod

> **RÉSOLU 2026-06-13** — fix host Hub fantôme hub.veridian.site -> app.veridian.site (commit 600b7001). pricing sync dérive désormais de HUB_BASE_URL (= app.veridian.site en prod). Garde-fou TestDefaultHubPricingURL_UsesPublicHost.

> **Sévérité** : 🟡 P2 (dégradation gracieuse en place, mais le cache pricing ne se rafraîchit JAMAIS)
> **Owner** : agent notifuse (+ infra si réseau)
> **Créé** : 2026-06-11 (par lead tunnel-de-vente, signalé par l'agent notifuse pendant le monitoring post-deploy `75cde46b`)

## Symptôme
Logs worker prod (récurrent, préexistant au deploy TLS+pixel) :
`VeridianPricingSync: fetch failed` — DNS `hub.veridian.site` non résolu
depuis le container `notifuse-prod`. Le service dégrade gracieusement
(« keeping previous cache ») donc rien ne casse, MAIS le cache pricing
(TTL 1h prévu par l'ADR shared/) ne se rafraîchit jamais : un changement
de grille côté Hub ne se propagerait pas à Notifuse.

## Pistes
1. Vérifier l'URL configurée dans l'ENV du compose Notifuse prod : le Hub
   public vit sur `app.veridian.site` (l'endpoint pricing =
   `GET /api/pricing/plans`). `hub.veridian.site` n'existe peut-être tout
   simplement pas en DNS public → ENV obsolète à corriger.
2. Si l'URL est censée être interne (réseau docker/Tailscale), vérifier la
   résolution depuis le container (`docker exec … getent hosts`).
3. Après fix : vérifier dans les logs que le sync passe + que le cache se
   met à jour (et ajouter un check obs si pertinent).

## Garde-fou
Ne pas toucher au fallback gracieux — c'est lui qui protège l'envoi.
