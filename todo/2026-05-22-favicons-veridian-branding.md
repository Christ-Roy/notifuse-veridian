# Favicons console — encore le logo Notifuse d'origine

> **Sévérité** : 🟢 P2
> **Owner** : agent notifuse-veridian (asset graphique requis)
> **Créé** : 2026-05-22

## Contexte

Repéré pendant le sprint UI polish (team `notifuse-ui-polish`, Task 1 branding).
Le jeu de favicons de la console (`console/public/`) est toujours le logo
**Notifuse d'origine** : un « N » gris dans un carré arrondi avec une pastille
rose. Visible dans l'onglet du navigateur et à l'install PWA.

Vérifié visuellement : `favicon-32x32.png` et `apple-touch-icon.png`
contiennent bien le logo Notifuse, pas le branding Veridian.

## Fichiers concernés (tous dans `console/public/`)

- `favicon.ico`
- `favicon-16x16.png`
- `favicon-32x32.png`
- `apple-touch-icon.png` (180x180)
- `android-chrome-192x192.png`
- `android-chrome-384x384.png`
- `mstile-150x150.png`
- `safari-pinned-tab.svg` (mask-icon, monochrome — couleur déjà `#7763F1` dans `index.html`)
- `icon.png`, `logo.png`, `logo-white.png` (à vérifier aussi — usage à tracer)

## Demande

Régénérer le jeu complet de favicons aux couleurs Veridian (violet de marque
`#7763F1`, cf. `console/src/theme/veridian.ts`). Idéalement décliner le
wordmark / une initiale « V » ou un mono-symbole cohérent avec
`veridian.mail`. C'est un **travail d'asset graphique** (pas du code) — outil
type realfavicongenerator ou export design.

## Hors scope de ce ticket (déjà fait)

- `index.html` : `theme-color`, `msapplication-TileColor`, `mask-icon color`
  déjà à `#7763F1`. `<title>` déjà `Console | Veridian`. `lang="fr"` +
  `meta description` ajoutés au sprint polish.
- `site.webmanifest` : `name` / `short_name` déjà passés à `Veridian Mail`.

Seuls les **fichiers binaires d'icônes** restent à produire.

## Impact

Cosmétique. La console est en `noindex` (app privée), donc zéro impact SEO,
mais résidu de marque visible par les utilisateurs (onglet + PWA).
