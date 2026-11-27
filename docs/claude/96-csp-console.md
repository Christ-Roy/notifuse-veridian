# 96 — CSP de la console (VER-265)

Modèle Veridian : `veridian-platform/securite/CSP-MODELE.md` (relevé sur Academy). Console et centre de notifications =
SPA statiques (Vite) : `script-src 'self'` sans `'unsafe-inline'` ni nonce.

- Code : `internal/http/middleware/veridian_csp.go` (`VeridianCSPPolicy`), posé par `VeridianSecurityHeadersMiddleware` sur
  toute réponse hors `/api/*` (les routes de données et le corps de mail gardent leur propre CSP).
- Mode : `VERIDIAN_CSP_MODE` dans `deploy/notifuse.nomad.hcl` : `report` (défaut, Report-Only), `enforce`, `off`. Basculer
  = changer la variable, aucun build.
- Rapports : `POST /api/veridian/csp-report` (report-uri et report-to), journal `csp_violation` (directive, ressource
  bloquée et page réduites à origine + chemin, ni query ni extrait). Lire : `obs search csp_violation --since 1h`.
- Besoins mesurés (26/11) : Monaco (`@monaco-editor/react`, workers `blob:`) servi par la console depuis le 27/11 (VER-265 :
  `monaco-editor` 0.55.1, même version que le CDN, `min/vs` copié en `public/monaco/vs` par `vite.config.ts`, chargeur pointé par
  `src/lib/veridian_monaco.ts`) ; `cdn.jsdelivr.net` est sorti de la politique (il sert n'importe quel paquet npm : contournement).
  `csp-parcours.mjs` échoue si une requête part vers un CDN. Styles inline
  (Ant Design), images, polices et `connect-src` en `https:` (aperçus de mails, S3 choisi par le client). Le rempart est
  script-src.
- `console/index.html` : plus aucun script inline (l'ancien `document.write` de config.js, inutile : config.js est
  `no-store`). Garde : `console/src/veridian_index_csp.test.ts`.
- Passage en enforce : après un vrai parcours de la console relu sans violation inattendue, et un contrôle positif
  (un `onerror` injecté doit être bloqué et rapporté).
- **Incident du 26/11/2026, 15:21:24Z à 15:24:45Z** : passé en enforce, la connexion par le Hub est tombée (3 min, repli
  `report` par le script serveur de déploiement rejoué depuis le bastion). `/veridian/auto-login` est une page rendue par
  le Go dont le script inline pose le jeton : bloqué. Les rapports `script-src-elem` étaient dans le journal, mal lus ; le
  parcours relevait les violations par page et perdait celles de la page d'atterrissage. Corrigé : la page pose sa propre
  CSP (`VeridianSetPageCSP`, `default-src 'none'`, script par nonce neuf), le parcours relève au niveau du contexte.
- **Règles** : toute page HTML rendue par le Go avec un script déclare sa CSP par `VeridianSetPageCSP` + nonce. Avant
  enforce : (1) `obs search csp_violation --since 24h` lu EN ENTIER, groupé par document, zéro hors contrôle positif ;
  (2) `tests/e2e-veridian/csp-parcours.mjs` vert, connexion comprise. Repli immédiat : `VERIDIAN_CSP_MODE=report`.
- **Rapports navigateur** : Chrome envoie par l'API Reporting (`report-to`), hors de la page et en différé, invisible pour
  Playwright ; Firefox par `report-uri` (requête visible). Chaîne prouvée le 26/11 par un rapport Firefox réel reçu
  (favicon de l'auto-login, `img-src` ajouté depuis). `csp-parcours.mjs` : `NAVIGATEUR=firefox`, `ATTENTE_RAPPORTS=<s>`.
- **Enforce le 26/11/2026 (2e passage)** : nonce de l'auto-login en prod, parcours Chrome vert connexion comprise
  (12 étapes, 0 violation, Monaco chargé), journal relu sur 24 h (seuls une sonde et le favicon).
- **Sans jsdelivr en prod (27/11/2026, c4163c5a, VER-265)** : servi `script-src 'self'` seul ; `/console/monaco/vs/loader.js`
  200 depuis la console. `scripts/e2e/csp-parcours-prod.py robertbrunon` (lien SSO à usage unique, `lourd --navigateur`,
  lecture seule, `EXIGER_MONACO=1`) : connexion par le Hub, 11 pages, éditeur MJML ouvert par « Créer un modèle » en mode
  Code puis Annuler (34 gabarits relus inchangés), Monaco chargé par 16 requêtes locales et 0 vers un CDN, 0 violation,
  `onerror` injecté bloqué et rapporté. Les gabarits de l'espace Veridian sont tous en texte brut : sans la voie
  « Code (MJML) », le parcours n'ouvrait aucun Monaco et ne prouvait rien (vu le même jour, code 1 sous `EXIGER_MONACO=1`).

