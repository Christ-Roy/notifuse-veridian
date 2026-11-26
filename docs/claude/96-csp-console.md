# 96 — CSP de la console (VER-265)

Modèle Veridian : `veridian-platform/securite/CSP-MODELE.md` (relevé sur Academy). Console et centre de notifications =
SPA statiques (Vite) : `script-src 'self'` sans `'unsafe-inline'` ni nonce.

- Code : `internal/http/middleware/veridian_csp.go` (`VeridianCSPPolicy`), posé par `VeridianSecurityHeadersMiddleware` sur
  toute réponse hors `/api/*` (les routes de données et le corps de mail gardent leur propre CSP).
- Mode : `VERIDIAN_CSP_MODE` dans `deploy/notifuse.nomad.hcl` : `report` (défaut, Report-Only), `enforce`, `off`. Basculer
  = changer la variable, aucun build.
- Rapports : `POST /api/veridian/csp-report` (report-uri et report-to), journal `csp_violation` (directive, ressource
  bloquée et page réduites à origine + chemin, ni query ni extrait). Lire : `obs search csp_violation --since 1h`.
- Besoins mesurés (26/11) : `cdn.jsdelivr.net` (Monaco chargé par `@monaco-editor/react`, workers `blob:`), styles inline
  (Ant Design), images, polices et `connect-src` en `https:` (aperçus de mails, S3 choisi par le client). Le rempart est
  script-src.
- `console/index.html` : plus aucun script inline (l'ancien `document.write` de config.js, inutile : config.js est
  `no-store`). Garde : `console/src/veridian_index_csp.test.ts`.
- Passage en enforce : après un vrai parcours de la console relu sans violation inattendue, et un contrôle positif
  (un `onerror` injecté doit être bloqué et rapporté).
- **Passage en enforce le 26/11/2026** après le parcours `tests/e2e-veridian/csp-parcours.mjs` (node02, `lourd --navigateur`,
  lien SSO par `--secrets`) : 10 pages + éditeur de modèle jusqu'au Monaco des données de test, 0 violation ; le contrôle
  positif (`onerror` injecté) rapporté (`script-src-attr`, lu dans `csp_violation`). Rejouer après tout ajout de dépendance
  externe à la console. Repli immédiat : `VERIDIAN_CSP_MODE=report` dans le job.
