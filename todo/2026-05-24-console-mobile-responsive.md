# Console Veridian Mail — Version mobile utilisable

> **Sévérité** : 🟡 P1 — la console est actuellement quasi-inutilisable sur mobile/tablette
> **Owner** : agent Notifuse (front)
> **Créé** : 2026-05-24 (audit responsive post-session N)

## Constat (audit 2026-05-24)

```
Tailwind breakpoints utilisés dans tout console/src/ :
  6× md:grid-cols-, 3× md:col-span-, 2× sm:px-, 1× sm:w-full, 1× sm:rounded-lg,
  1× sm:mx-auto, 1× sm:max-w-, 1× lg:px-
  → 16 occurrences au TOTAL sur des dizaines de milliers de lignes UI

Composants Antd avec props responsive (xs/sm/md/lg) : 6 fichiers seulement

Media queries CSS : 4 occurrences
  - console/src/index.css:123 — @media (max-width:600px) pour .ant-alert
    (livré aujourd'hui par moi, fix bandeau Email Provider Required)
  - blog_editor/styles/editor.css × 2 — pour l'éditeur blog interne
  - blog_editor/toolbars/floating-toolbar.css — toolbar éditeur
```

**Résultat** : tout le reste de la console (dashboard, broadcasts, templates,
contacts, segments, automations, team settings, integrations, billing, plan,
analytics) **n'a pas de responsive**. Pris dans un viewport <600px :

- Sidebar Antd ne se replie pas en hamburger → coupe le contenu principal
- Tables Antd avec 6+ colonnes → scroll horizontal forcé, illisible
- Modales/Drawers Antd ouvertes en 80% width → débordent + boutons coupés
- Forms à 2 colonnes (`Form layout="horizontal"`) → labels écrasés
- KPI cards dashboard en `Row gutter` non-responsive → empilement aléatoire

## Demande

Rendre la console **utilisable** (pas forcément belle) sur mobile/tablette.
Niveau de service cible :
- ≥ 320px (iPhone SE 1ère gen) : pas crash, navigation possible, lecture des
  données possible même en scroll
- ≥ 768px (iPad portrait) : layout propre, pas de scroll horizontal forcé

## Travail à faire

### Lot 1 — Layout global (CRITIQUE)
- [ ] `WorkspaceLayout.tsx` : sidebar repliable en drawer hamburger sous 768px
- [ ] Topbar : compacter actions (regrouper sous menu kebab sous 480px)
- [ ] Footer Veridian : déjà OK (`veridian_brand_footer.tsx` minimal)

### Lot 2 — Pages list (broadcasts, templates, contacts, segments)
- [ ] Tables → cards stack sous 768px (Antd `Card` ou pattern row-as-card)
- [ ] Actions colonne → menu kebab par ligne sous 768px
- [ ] Filtres en haut → drawer/accordion sous 768px

### Lot 3 — Forms & drawers
- [ ] `Form layout="horizontal"` → `layout="vertical"` sous 768px
- [ ] Drawers : `width="100%"` sous 480px (au lieu du défaut 378-512px)
- [ ] Modales : `width="100vw"` + `style={{maxWidth:'100vw'}}` sous 480px

### Lot 4 — Dashboard & analytics
- [ ] KPI Row → 1 col mobile, 2 col tablette, 4 col desktop
- [ ] Graphs `recharts` : `ResponsiveContainer` partout (vérifier qu'ils y sont)
- [ ] Tableaux de stats → swipe horizontal si > 4 colonnes (Antd `scroll={{x: true}}`)

### Lot 5 — Editor email (visual mode)
- [ ] Le visual editor MJML est complexe — viable mobile = read-only mode ?
- [ ] Au minimum : afficher message "Mode mobile : preview uniquement, édition
      désactivée, repassez sur desktop"

### Lot 6 — i18n & accessibilité
- [ ] `lingui:extract` après ajout des chaînes (incident 2026-05-22 — hash en prod)
- [ ] Tester avec un screen reader mobile (VoiceOver iOS, TalkBack Android)
- [ ] Tap targets ≥ 44×44 px (WCAG 2.5.5)

## Comment tester

```bash
# Local dev server :
cd console && npm run dev
# Puis Chrome DevTools → Toggle device toolbar → iPhone SE, iPhone 14 Pro,
# iPad Mini, iPad Pro 12.9".

# Spec E2E à ajouter (Lot 7) :
tests/e2e-veridian/specs/responsive-console.spec.ts
- viewport({width: 320, height: 568}) → page boote, sidebar accessible
- viewport({width: 768, height: 1024}) → forms vertical, drawer full-width
- viewport({width: 1280, height: 800}) → layout desktop standard

Devrait tourner via le job CI `e2e-console — production-build smoke` (Agent D
2026-05-23).
```

## Pourquoi maintenant

- **Stripe live** depuis ~hier. Premier client qui s'inscrit depuis mobile
  ne peut PAS finaliser son setup. Friction de conversion massive.
- Mobile = 40-60% du trafic SaaS en 2026 (selon segment B2B).
- Coût relatif faible : Tailwind + Antd ont déjà le système responsive, c'est
  surtout du câblage (ajouter `xs={24} md={12}` aux `Col`, etc.).

## Estimation

- Lot 1 : 1 jour (refactor WorkspaceLayout)
- Lots 2-3 : 2 jours
- Lot 4 : 1 jour
- Lot 5 : 4h (mode preview-only mobile)
- Lot 6-7 : 1 jour (lingui + tests E2E)

**Total : ~5-6 jours dev** pour atteindre "utilisable mobile".

## Dépendances

Aucune. Tout est dans `console/src/`, pas de coordination cross-app.

## Définition de done

- [ ] Toutes les pages bootent sans crash en viewport 320×568
- [ ] Spec E2E `responsive-console.spec.ts` verte (3 viewports testés)
- [ ] Test manuel sur vrai iPhone + vrai Android (Robert)
- [ ] Pas de régression desktop (spec existante `boot-smoke.spec.ts` toujours verte)
- [ ] CLAUDE.md mis à jour : note breakpoints standard (`sm:640px md:768px
      lg:1024px xl:1280px 2xl:1536px`)
