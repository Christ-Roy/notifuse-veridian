# Tests E2E Veridian

`tests/e2e-veridian/` (Playwright). Tag `@prod-safe` pour les tests
read-only autorisés à tourner sur prod.


## Staging stable avant l'E2E (27/11/2026)

`scripts/ci/staging-stable.sh` ouvre l'E2E de staging : 6 lectures saines CONSÉCUTIVES, sans suivre de redirection (SHA servi,
`setup.status` en JSON, une route `/api` qui répond en JSON et pas en 404, `/console/` en 200 avec `/config.js`), une toutes les
5 s, bornées à 3 min. Un seul échec remet le compteur à zéro. Pourquoi : le run 45912109689 démarrait l'E2E 37 s après la fin du
déploiement, pendant que l'ancienne allocation se vidait. Une seule lecture du bon SHA passait, puis « Failed to fetch » et
« redirect count exceeded » (infra, journaux Sablier sans expiration ni réveil). Épreuve : `scripts/ci/test-staging-stable.sh`
(faux serveur Node : redirection, bascule, sain), jouée avant la garde ; vue rouge sur la remise à zéro retirée.
