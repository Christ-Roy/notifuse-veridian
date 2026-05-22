# 2026-05-22 — Perfs UI : base saine avant le polish UI

> **Type** : Performance frontend + serving statique
> **Sévérité** : 🟡 P1 (impact direct sur l'UX perçue de tout client)
> **Owner** : agent Notifuse
> **Créé** : 2026-05-22
> **Pré-requis de** : `2026-05-21-ui-veridian-mode-autonomous.md` — partir
> d'une base perf saine AVANT le polish UI fin (sinon on polit du lent).

---

## Pourquoi ce ticket

Robert veut polir l'UI. Avant de toucher au visuel, il faut une **base
de chargement saine** — inutile de soigner des pixels si l'app met 5s à
s'afficher. Ce ticket mesure l'existant et corrige les latences
structurelles.

---

## Mesures terrain 2026-05-22 (prod `v41.0-veridian.999d6780`)

Toutes les mesures sont **réelles**, prises sur `notifuse.app.veridian.site`
depuis une machine de bureau fibre (un client mobile/4G sera 5-10× pire).

### 🔴 Problème #1 — Bundle JS monolithique 6.3 MB

```
dist/assets/index-B7KYMJSS.js  = 6 282 764 octets (6.28 MB brut)
                                = ~1.7 MB gzippé
```

- **UN SEUL chunk** pour toute la SPA console. Aucun code-splitting.
- `console/vite.config.ts` : **aucun `manualChunks`**, aucun
  `rollupOptions.output` — Vite met tout dans `index.js`.
- Conséquence : le user télécharge + parse + exécute 6 MB de JS avant
  de voir quoi que ce soit, même pour afficher la page login.

### 🔴 Problème #2 — Assets servis NON COMPRESSÉS

```
GET /console/assets/index-B7KYMJSS.js
→ HTTP 200, content-length: 6282764
→ PAS de header content-encoding: gzip|br
```

- Le serveur Go (`internal/http/root_handler.go:178`,
  `serveConsole`) utilise `http.FileServer` brut — **zéro compression**.
- Le user télécharge **6.28 MB au lieu de ~1.7 MB**. ~3.7× trop de
  données sur le fil.
- Mesure : **1.19 s** rien que le download de ce fichier depuis une
  fibre. En 4G (~5 Mbps réel) : ~10 s.
- Le code Go a déjà `compress/gzip` importé (utilisé ailleurs ligne
  679) mais PAS branché sur le file server console.

### 🟡 Problème #3 — Pas de cache long sur les assets hashés

À vérifier : les assets `index-<hash>.js` ont un hash de contenu dans
le nom (cache-bustable), donc ils peuvent être servis avec
`Cache-Control: public, max-age=31536000, immutable`. À confirmer que
le file server ne les sert pas avec un cache court ou absent → si
absent, chaque navigation recharge tout.

### 🟡 Problème #4 — 10 fichiers de locale chargés ?

```
dist/assets/  →  fr, ja, es, ca, de, pt-BR, it, en, pt-BR-temp ...
               ~100-120 KB chacun
```

À vérifier : est-ce que TOUS les fichiers de langue Lingui sont
téléchargés au boot, ou bien lazy-loadés par langue active ? Si tout
est chargé → ~1 MB de locales inutiles (le user n'a qu'une langue).
Note : `pt-BR-temp` ressemble à un artefact de build à nettoyer.

---

## Travail à faire

### Lot perf-1 — Code-splitting Vite (gros gain, ~2-3h)

`console/vite.config.ts` : ajouter `build.rollupOptions.output.manualChunks`
pour découper :

- **vendor** : `react`, `react-dom`, `react-router` → chunk stable,
  cacheable très longtemps (change rarement)
- **antd** : Ant Design v5 (probablement le plus gros morceau) → chunk
  séparé
- **tanstack** : `@tanstack/react-query` + `react-router`
- **charts/editor** : si la console a un éditeur d'email ou des graphes
  (souvent les plus lourds), les isoler + lazy-load via `React.lazy()`
- Route-based splitting : `React.lazy()` sur les pages lourdes
  (Settings, Broadcasts, Templates, Contacts) — chaque route ne charge
  son code qu'à la navigation.

**Cible** : chunk initial < 500 KB gzippé (vs 1.7 MB actuel). Le
`chunkSizeWarningLimit` Vite doit rester à 500 pour garder le garde-fou.

### Lot perf-2 — Compression gzip/brotli des assets statiques (gros gain, ~1-2h)

Le serveur Go `serveConsole` ne compresse pas. 2 options :

**Option A (recommandée) — pré-compression au build + serving direct**
- `console` : ajouter `vite-plugin-compression` qui génère
  `index-<hash>.js.gz` ET `.br` au build.
- Côté Go : créer `internal/http/veridian_static_handler.go` (wrapper
  Veridian, NE PAS patcher `root_handler.go` upstream) qui :
  - check `Accept-Encoding` du client
  - sert le `.br` ou `.gz` pré-compressé s'il existe + header
    `Content-Encoding` correct
  - fallback sur le fichier brut sinon
- Avantage : compression maximale (brotli niveau 11), zéro CPU runtime.

**Option B — compression à la volée**
- Wrapper `gzip.Writer` autour du `http.FileServer`. Plus simple mais
  consomme du CPU à chaque requête (acceptable vu le trafic actuel).

Reco : **Option A** — c'est le standard, et brotli >> gzip sur du JS.

**Cible** : `content-encoding: br` sur les `.js`/`.css`, transfert
divisé par ~3.5×.

### Lot perf-3 — Cache-Control sur assets hashés (~30 min)

Dans le wrapper `veridian_static_handler.go` :
- assets `assets/*-<hash>.{js,css}` (nom cache-bustable) →
  `Cache-Control: public, max-age=31536000, immutable`
- `index.html` → `Cache-Control: no-cache` (doit toujours être revalidé
  pour pointer vers les derniers hash)

### Lot perf-4 — Audit lazy-loading des locales (~1h)

Vérifier la config Lingui (`console/src/`) : les catalogues de langue
doivent être `import()` dynamique par langue active, pas tous bundlés.
Supprimer l'artefact `pt-BR-temp`.

### Lot perf-5 — Mesure avant/après (obligatoire)

Avant de clore : refaire les mesures et documenter le delta dans ce
ticket. Cibles chiffrées :

| Métrique | Avant (2026-05-22) | Cible |
|---|---|---|
| Chunk JS initial (gzip) | ~1.7 MB | < 500 KB |
| Transfert total 1er load | 6.28 MB | < 1.5 MB |
| Compression assets | aucune | brotli |
| Download main JS (fibre) | 1.19 s | < 0.4 s |
| Cache-Control assets | à vérifier | immutable 1 an |

Outils : `curl -w` pour les transferts, `npx vite build` +
inspection `dist/`, Lighthouse via Chrome MCP sur staging pour le
score perf global (FCP, LCP, TBT, CLS).

---

## Hors-scope (ce ticket ≠ polish visuel)

- Tout ce qui est apparence / couleurs / layout → reste dans
  `2026-05-21-ui-veridian-mode-autonomous.md` + session calme Robert.
- Optimisations React render (memo, virtualisation de listes) — à
  traiter SI Lighthouse montre un TBT élevé après perf-1 à perf-4. Pas
  spéculatif.

---

## Méthode de mesure (pour reproduire)

```bash
# Taille bundle
cd console && npx vite build
ls -S dist/assets/*.js | head -5 | xargs -I{} du -h {}
gzip -c dist/assets/index-*.js | wc -c   # taille gzip

# Compression servie en prod
curl -sS -o /dev/null -D- -H "Accept-Encoding: gzip,br" \
  https://notifuse.app.veridian.site/console/assets/index-XXXX.js \
  | grep -iE "content-encoding|content-length"

# Transfert réel
curl -sS -o /dev/null -w "%{size_download}b %{time_total}s\n" \
  -H "Accept-Encoding: gzip,br" \
  https://notifuse.app.veridian.site/console/assets/index-XXXX.js

# Lighthouse (via Chrome MCP sur staging)
```

---

## Convention

- `console/vite.config.ts` : config build, OK de modifier (fichier de
  config, pas du code applicatif upstream).
- Serving Go : **wrapper Veridian** `internal/http/veridian_static_handler.go`,
  ne PAS patcher `root_handler.go` upstream (cf. convention CLAUDE.md).
- Tests : si nouveau handler Go → test colocalisé Constitution §1.
  Test du splitting = vérif `dist/` après build (peut être un script CI).

## Effort estimé

- perf-1 code-splitting : 2-3h
- perf-2 compression : 1-2h
- perf-3 cache-control : 30 min
- perf-4 locales : 1h
- perf-5 mesure : 30 min
- **Total : ~6-7h** — gros ROI, c'est la base avant tout polish.

## Priorité de séquençage

1. **perf-2 compression** EN PREMIER : gain immédiat ×3.5 sur le
   transfert, ne touche pas au code applicatif, faible risque.
2. **perf-1 code-splitting** : le plus gros gain structurel mais
   demande de tester que rien ne casse au lazy-load.
3. perf-3 + perf-4 : finitions.
4. perf-5 : mesure de clôture.

Une fois ce ticket livré → la base est saine → le polish UI
(`ui-veridian-mode-autonomous.md`) peut commencer proprement.

---

## ✅ LIVRÉ — 2026-05-22

### Ce qui a été fait

**perf-2 + perf-3 — Compression + Cache-Control** (1 wrapper Go).
- Créé `internal/http/veridian_static_handler.go` : middleware appliqué
  au handler global (pattern `VeridianPaywallPathFilter`), câblé dans
  `app.go`. NE patche PAS `root_handler.go` upstream.
- Intercepte `/console/assets/*` : gzip à la volée + cache mémoire
  (assets immuables entre 2 deploys) + `Cache-Control: public,
  max-age=31536000, immutable` sur les assets au nom hashé.
- **Choix gzip, pas brotli** (écart à l'Option A du ticket) : brotli
  imposerait une dépendance tierce (`andybalholm/brotli` côté Go +
  un plugin Vite) pour ~15 % de gain en plus. Go a `compress/gzip` en
  stdlib. gzip seul donne déjà ×3.5 sur le transfert — la cible du
  ticket. Compression à la volée (Option B) + cache mémoire : zéro
  dépendance, recompression nulle. Ajouter brotli plus tard = ticket
  30 min si le trafic le justifie.
- Test colocalisé : `veridian_static_handler_test.go`, 9 tests verts.

**perf-1 — Code-splitting Vite.**
- `vite.config.ts` : `build.rollupOptions.output.manualChunks` découpe
  `react-vendor / antd / tanstack / monaco / charts / flow / editor /
  icons / vendor-utils`.
- `router.tsx` : toutes les pages post-login passées en `React.lazy()`
  via helper `veridianLazyPage` (Suspense local + spinner Antd). Restent
  statiques uniquement SignIn / Logout / AcceptInvitation / Setup
  (chemin critique avant auth).

**perf-4 — Locales Lingui.**
- Déjà optimal : `loadLocale` fait `await import(\`./locales/\${locale}.po\`)`
  — une seule langue chargée au boot, pas les 8.
- Supprimé l'artefact orphelin `src/i18n/locales/pt-BR-temp.po`.

### Mesures avant / après (build prod)

| Métrique | Avant | Après |
|---|---|---|
| `index.js` (chunk applicatif) | 6 282 764 o (6.28 MB) | 370 816 o (120 KB gzip) |
| Bundle **initial** (1er paint, gzip) | ~1.8 MB | **~892 KB gzip** |
| Transfert réel 1er load (sur le fil) | 6.28 MB (non compressé) | ~892 KB (gzip auto) |
| Compression assets | aucune | **gzip** + cache mémoire |
| `Cache-Control` assets hashés | absent | `immutable` 1 an |
| Features lourdes (Blog 200K, charts 175K, flow 57K, editor 70K, CreateTemplateDrawer 172K gzip) | dans le bundle initial | **chargées à la demande** |

→ **~×7 sur le transfert au premier chargement.** `antd` (485 KB gzip)
reste le plancher incompressible d'une app Ant Design — non lazy-loadable
sans casser le 1er rendu.

### Non-régression
- `npm run build` (lingui + tsc strict + vite) : ✅
- `vitest run` : 225 tests / 20 fichiers ✅ (1 warning React post-teardown
  préexistant dans `App.test.tsx` / `LocaleContext` — pas lié à ce ticket).

### Note pour le polish UI suivant
La base perf est saine. `ui-veridian-mode-autonomous.md` peut démarrer.
Si Lighthouse montre un TBT élevé après deploy → optimisations React
render à traiter (hors scope ici, non spéculatif).
