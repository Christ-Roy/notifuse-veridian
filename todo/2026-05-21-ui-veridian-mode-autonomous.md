# 2026-05-21 — UI Veridian-mode : travail autonome agent + session calme staging

> **Type** : Ticket UI à exécuter en autonomie par l'agent (sans Robert)
> **Sévérité** : 🟡 P1 (impact UX direct, mais pas bloquant business)
> **Owner** : agent Notifuse, en autonomie complète
> **Suivi conjoint avec Robert** : session calme dédiée sur staging quand l'agent aura shippé la base, **sans sub-agents**, juste hot-reload + UX live
> **Lien parent** : `todo/review_hot_reload_with_robert.md` (catalogue exhaustif)
> **Créé** : 2026-05-21 (post-pivot pricing + sprint sections §17-22)

---

## 📊 STATUT — mis à jour 2026-05-22

**La partie autonome est LIVRÉE. Reste : la session calme avec Robert.**

- ✅ **Lot UI-2** (intercepteur 402, toast auto-login, bandeau soft-delete,
  badge plan_source) — livré commit `fd487136` (2026-05-21). Composants
  `veridian_402_interceptor`, `veridian_welcome_toast`,
  `veridian_soft_delete_banner`, `veridian_plan_source_badge` créés +
  câblés + tests Vitest.
- ✅ **Lot UI-3** (co-brand : header lien retour Veridian, footer "Powered
  by") — livré même commit. `veridian_brand_header_link`,
  `veridian_brand_footer`.
- ✅ **Lot UI-1** (purge compteurs/limites visibles) — **rien à purger**.
  Audit `console/src/` 2026-05-22 : aucun compteur de quota de plan,
  aucune progress bar de plan, aucun menu grisé "🔒 Pro". Les seuls
  `remaining` du code sont des compteurs d'envoi de broadcast en cours
  ("X sent, Y remaining" pendant une campagne) — info opérationnelle
  légitime, hors interdit du pivot pricing. Critère grep du ticket :
  satisfait.

⏳ **Hors-scope autonome — RESTE À FAIRE avec Robert** : la session calme
hot-reload sur staging (polish fin : typo, espacements, couleurs Veridian,
refonte signin/onboarding, modal paywall design avancé, white-label
Business+, i18n complet, responsive). Voir §"Hors-scope autonome" plus
bas. **Ce ticket reste pending pour cette session** — c'est la seule
raison pour laquelle il n'est pas archivé.

Pré-requis posé 2026-05-22 : ticket perf `2026-05-22-perf-ui-baseline-saine.md`
livré (bundle 6.28 MB → ~892 KB gzip) → la base est saine pour le polish.

---

## Contexte

Le ticket `review_hot_reload_with_robert.md` recense 22 sections backend
nécessitant un polish UI. Total estimé : **12-18h de travail UI**. Robert
ne veut pas tout faire d'un coup, encore moins en hot-reload synchrone
(c'est trop pour 1 session).

**Décision Robert 2026-05-21** : l'agent fait **le nécessaire seul**, en
autonomie complète. Les passes "jolies" / fines viendront **plus tard** en
session calme sur staging, **sans sub-agents**, juste l'agent + Robert,
hot-reload, tranches en live.

---

## Périmètre autonome (à exécuter par l'agent SANS Robert)

Le but n'est pas de polish au pixel. Le but est de **purger les violations
de CLAUDE.md** (compteurs/limites visibles interdits par le pivot pricing)
et de **câbler le strict minimum fonctionnel** sur les flows backend
shippés mais sans UI.

### Lot UI-1 — PIVOT PRICING : purge des compteurs/limites visibles 🔴🔴🔴

**Priorité #1.** Le pivot 2026-05-21 (`PRICING-VERIDIAN.md`) interdit :

- ❌ Compteurs "il vous reste X mails / contacts / seats / domaines"
- ❌ Menus grisés "🔒 Pro" / pop-ups "passez Pro pour faire ça"
- ❌ Toute limite enforced sur features (contacts / OAuth / seats /
  automation / historique / custom domains / A/B testing)
- ❌ Bandeau trial AVANT phase 3 (J+2 post-5 mails — orchestré côté Hub)

**Travail à faire en autonomie** :

1. **Audit `console/src/` page-par-page** (scripté ou manuel) — lister
   tous les fichiers `.tsx` qui contiennent : `quota`, `limit`, `max_`,
   `remaining`, `progress`, `usage`, `Pro plan`, `Upgrade`, `unlimited`.
2. **Widget quota Dashboard** : afficher `∞ Illimité` quand `quota === -1`
   au lieu de progress bar. Pas de %. Pas de "X / Y mails utilisés".
3. **Pages Contacts / Templates / Broadcasts / Sequences** : retirer toute
   mention de compteur restant. Si upstream Notifuse les affiche, override
   via wrapper `veridian_*.tsx` qui retourne `null` ou texte neutre.
4. **Settings → Plan** : afficher "Plan Free — accès illimité" pour les
   Free. Aucune feature list grisée. Pas de comparatif "Pro vs Free".
5. **Aucune feature gate UI** sur A/B testing (déjà reverté backend
   lot 4a 2026-05-21). Si menu / bouton grisé → débloquer.

**Effort estimé** : 2-4h (audit 1h + fix 1-3h selon ce qui est trouvé).

**Critère de complétion** : `grep -rE "quota|remaining|/[0-9]+ used"
console/src/ | grep -v node_modules | grep -v _test` ne ressort plus rien
de visible côté client. Au pire des cas neutre ("Plan actif").

### Lot UI-2 — Câblage minimal des flows backend sans UI

Pour chaque feature backend shippée sans UI, câbler **le strict minimum**
fonctionnel (pas le polish) :

1. **§5 — Intercepteur axios 402** (1h)
   - Mapper réponse `402 {error: "Payment required: <reason>",
     tenant_status: "..."}` vers modal Ant Design clair.
   - Copy minimaliste : "Compte suspendu — gérez votre abonnement [→
     Veridian]". CTA → `https://app.veridian.site/dashboard`.
   - Pas de design fin, juste un modal qui marche.

2. **§9 — Toast bienvenue auto-login + page token expiré** (1h)
   - Au mount, si `?token=` dans URL et serveur OK : toast Ant Design
     "Bienvenue, <email>".
   - Si serveur renvoie 401 : afficher `<Result status="403">` "Lien de
     connexion expiré — demandez un nouveau lien depuis Veridian"
     [→ app.veridian.site]. Pas de fallback signin upstream confus.

3. **§3 — Bandeau soft-delete simple** (1h, sans obfuscation — §21 reste
   pending backend)
   - Au boot console, si `GET /api/tenants/:id/status` renvoie
     `deleted_at != null` : afficher `<Alert>` Ant Design persistant top
     de page : "Votre compte est supprimé — restauration possible jusqu'au
     <purge_eligible_at>. [Restaurer →]" CTA → `restore_url` du Hub.

4. **§7 — Badge plan_source dans Settings → Plan** (30min)
   - Si `plan_source === "lifetime_partner"` → badge "Lifetime — accès
     offert par Veridian"
   - Si `plan_source === "lifetime_site_vitrine"` → "Lifetime — inclus
     avec votre site vitrine"
   - Si `plan_source === "internal"` → "Compte interne Veridian" (visible
     uniquement pour Robert + équipe — détecter via email pattern ?)
   - Si `plan_source === "manual"` → "Plan admin manuel"
   - Sinon (stripe ou vide) → afficher upstream normal.

**Effort total Lot UI-2** : ~3.5h.

### Lot UI-3 — Branding / co-brand léger 🔴 (§1)

**Co-brand minimal, pas full white-label** (white-label = Business+
seulement, cf. §22).

1. **Header console** : ajouter un lien discret "← Retour au dashboard
   Veridian" (icône + texte) à gauche du logo Notifuse. Si user en mode
   `veridian-managed`, visible. Sinon caché.
2. **Footer** : ajouter "Powered by Veridian — [Manage subscription]" en
   small grey. Lien → `app.veridian.site`.
3. **Page signin (mode veridian-managed)** : déjà OK (CreateWorkspacePage
   intercepte). Vérifier juste le branding final.

**Effort** : ~2h.

---

## Hors-scope autonome (gardé pour session calme avec Robert)

Ces points demandent **vraiment** un œil UX humain en hot-reload — pas
question de les faire seul :

- **Polish fin du widget quota / badges** : choix typographie, espacements,
  couleurs spécifiques au branding Veridian (vs Ant Design default)
- **Refonte des écrans signin / onboarding** : ergonomie, copy, ordre
  d'apparition
- **Modal paywall design avancé** : illustrations, ton de voix, CTA
  alternatif "Exporter avant suppression"
- **White-label full Business+** : reskin profond pour les clients qui
  paient pour ça (§22 backend feature `FeatureWhiteLabel`)
- **i18n complet** : mapping error_code → messages traduits FR/EN (§6)
- **Animations / transitions** : tout ce qui demande un sens du polish
- **Mobile / responsive** : tester sur différentes tailles, ajuster

→ Session "calme sans sub-agents" Robert + agent, hot-reload sur staging,
on tranche en live.

---

## Exigences techniques pour l'agent autonome

### Convention de code

- Convention `veridian_*.tsx` flat pour les nouveaux composants custom
  (mirror du backend Go `veridian_*.go`). Ex : `veridian_quota_widget.tsx`,
  `veridian_paywall_modal.tsx`, `veridian_brand_footer.tsx`.
- **Ne jamais patcher** un fichier upstream `console/src/*.tsx` du fork
  Notifuse — créer un wrapper Veridian qui override.
- Tests colocalisés OBLIGATOIRES (Constitution §1 — Vitest pour le front).
  À chaque fichier `veridian_*.tsx` créé/modifié, son
  `veridian_*.test.tsx` doit suivre.

### Stack

- React 18 + TypeScript strict + Vite + Ant Design v5
- TanStack Query / Router (déjà en place)
- Lingui i18n (`useLingui()` + `` t`...` `` — utiliser le format existant
  pour les copies user-facing, **anglais par défaut** comme upstream)
- pas de nouvelle dépendance npm sans accord explicite

### Validation

1. `cd console && npm run build` doit passer (TypeScript strict)
2. `cd console && npm test` doit passer (Vitest)
3. Lighthouse / accessibilité non bloquant pour cette passe (session
   calme avec Robert)
4. Vérification manuelle staging : `cd console && npm run dev` pointé sur
   `https://notifuse.staging.veridian.site` + screenshots clés avant/après
   joints au commit message

### Critère de fin (autonome)

L'agent a fini quand :

- [ ] **Lot UI-1** : audit + purge des compteurs/limites violant CLAUDE.md
  pivot pricing. Critère grep ci-dessus.
- [ ] **Lot UI-2** : intercepteur 402 + toast auto-login + bandeau
  soft-delete + badge plan_source câblés.
- [ ] **Lot UI-3** : header lien retour Veridian + footer "Powered by".
- [ ] Build TypeScript clean.
- [ ] Tests Vitest verts (au moins 1 test colocalisé par nouveau
  composant `veridian_*.tsx`).
- [ ] Staging testé manuellement, screenshots du dashboard avec
  utilisateur Free / Pro / lifetime joints au commit.
- [ ] Update du ticket parent `review_hot_reload_with_robert.md` —
  sections §1, §3, §4, §5, §7, §9, §17, §22 marquées
  `✅ POLISHED autonome — 2026-05-XX` avec note "à valider en session
  calme avec Robert".

---

## Plan d'attaque agent

```
Session autonome 1 (~3h)
  └─ Audit grep + lot UI-1 (purge compteurs)
     └─ Commit : feat(ui): purge compteurs/limites — pivot pricing [risk:medium]

Session autonome 2 (~3h)
  └─ Lot UI-2 (câblages minimaux)
     └─ Commit : feat(ui): intercepteur 402 + toast login + bandeau soft-delete + badge plan_source [risk:low]

Session autonome 3 (~2h)
  └─ Lot UI-3 (co-brand)
     └─ Commit : feat(ui): co-brand minimal Veridian (header retour + footer) [risk:low]

Puis : Robert valide en session calme staging, polish fin en hot-reload.
```

L'agent peut tout faire en 1 session si dispo, en 3 commits séparés
risk-marker `[risk:low]` ou `[risk:medium]`.

---

## Notes

- **Pas de sub-agents** sur la session calme finale avec Robert. C'est un
  travail UX humain, pas du dispatch.
- L'agent autonome peut utiliser tous les outils habituels (Read/Edit/
  Bash, mais pas spawn d'autres agents pour cette UI).
- Si l'agent bloque sur un choix UX clair (ex : "footer en gris #ccc ou
  #999 ?"), **ne pas demander** — choisir et noter dans le commit message
  pour que Robert tranche en session calme.
- Le ticket parent `review_hot_reload_with_robert.md` reste la source de
  vérité du backlog UI à long terme.
