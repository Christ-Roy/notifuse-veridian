# Sync upstream Notifuse v30.2 → v32.0 — cherry-pick chirurgical

> **Sévérité** : 🟡 P2 (rattrapage upstream, ~10 jours de retard)
> **Owner** : agent Notifuse
> **Créé** : 2026-05-23
> **Approche** : pas de merge sauvage. **Cherry-pick par release**, idée
> business comprise, code adapté à nos custom `veridian_*` quand
> nécessaire. Décidé par Robert 2026-05-23.

---

## Contexte

Notre fork tourne sur l'upstream Notifuse **~v30.1** (notre `veridian`
contient le tag `saas-v1.0.3` du 2026-05-12 = release upstream v30.1).
Upstream a publié **4 releases** depuis : v30.2, v30.3, v31.0, v32.0
(dernière le 2026-05-22).

**10 commits seulement** à traiter (pas 591 — la divergence cumulée
depuis la création du fork est trompeuse ; les sync précédents ont déjà
intégré la masse).

---

## Pourquoi NE PAS faire `git merge upstream/main`

- Risque de conflit sur ~20 fichiers upstream qu'on a patchés
  (`WorkspaceLayout`, `SettingsSidebar`, `AnalyticsPage`, `SetupWizard`,
  `AcceptInvitationPage`, `App.tsx`, `index.html`, `vite.config.ts`,
  `client.ts`, `index.css`…) — risque de casser le wordmark
  `veridian.mail`, l'i18n extract, le code-splitting fixé, l'intercepteur
  401 d'AcceptInvitation, etc.
- Pas de tri sur ce qui vaut le coup : on importerait aussi de la
  cosmétique inutile ou des refactors qui collidaient avec notre custom.
- Pas de doc sur ce qu'on a effectivement intégré → personne ne sait
  plus quel patch upstream tourne en prod.

→ Pattern propre : `git cherry-pick <commit>` ou recopie chirurgicale,
release par release, avec une note dans ce ticket pour chaque commit
**pris**, **adapté**, ou **skipped (et pourquoi)**.

---

## Inventaire complet — 10 commits, 4 releases

### v30.2 (2026-05-13, 5 commits)

| Commit | Sujet | Reco initiale |
|---|---|---|
| `eec68b93` | openai + deps | 🟢 PRENDRE — bumps deps OpenAI + transitives |
| `2118c6c2` | soft bounce threshold | 🟢 PRENDRE — feature email delivery utile |
| `9d1b5a96` | preview email subjects with compile endpoint | 🟢 PRENDRE — UX email builder |
| `d50c83a6` | open api | 🟡 ÉVALUER — feature OpenAPI publique, intéressant si on veut exposer l'API à des clients |
| `8aaae0e1` | open api json | 🟡 ÉVALUER — suit d50c83a6 |

### v30.3 (2026-05-14, 1 commit)

| Commit | Sujet | Reco initiale |
|---|---|---|
| `57a42d69` | fix utm params | 🟢 PRENDRE — bug fix, sûrement low risk |

### v31.0 (2026-05-19, 3 commits)

| Commit | Sujet | Reco initiale |
|---|---|---|
| `f5f144f7` | pool ping | 🟢 PRENDRE — fix DB connection pool, important |
| `eb1ead06` | triggers loop | 🟡 ÉVALUER — automation, risque de toucher du code qu'on n'a pas patché mais qu'on utilise |
| `f9741fc8` | cleaning | 🟡 ÉVALUER — vague, lire le diff avant |

### v32.0 (2026-05-22, 1 commit)

| Commit | Sujet | Reco initiale |
|---|---|---|
| `7a42651d` | translate system emails with user language | 🟢 PRENDRE — i18n cross-app, aligné avec nos propres efforts i18n du 22-05 |

---

## Procédure de traitement

Pour CHAQUE commit, dans l'ordre v30.2 → v30.3 → v31.0 → v32.0 :

1. **Lire le diff complet** :
   ```bash
   git show --stat <sha>
   git show <sha>
   ```
2. **Comprendre l'idée business** : qu'est-ce que ce commit apporte
   réellement ? (feature, bug fix, perf, sécu, doc, refactor neutre…)
3. **Repérer les fichiers touchés** : sont-ils des fichiers qu'on a
   patchés côté Veridian ? (cf. liste fichiers à risque ci-dessous)
4. **Décider** :
   - **PRENDRE tel quel** : `git cherry-pick <sha>`. Si conflit
     trivial → résoudre. Si conflit non trivial → passer à 4b.
   - **ADAPTER chirurgicalement** : `git show <sha>` → comprendre
     l'intention → réimplémenter sur notre code en gardant nos custom
     `veridian_*` et nos patches. Commit avec un message explicite
     `feat(upstream-v30.X): <intention> — adapted from <sha>`.
   - **SKIP** : noter dans ce ticket pourquoi (incompatible avec notre
     archi, déjà couvert par un `veridian_*`, refactor sans valeur,
     etc.). Ne PAS faire de cherry-pick.
5. **Build + tests + smoke** entre chaque release :
   ```bash
   cd console && npm run build && npx vitest run
   go build ./... && go test ./...
   ```
6. **Documenter dans ce ticket** : remplir le tableau « État final »
   ci-dessous avec PRIS / ADAPTÉ / SKIP + justification courte.

---

## Fichiers à risque (déjà patchés Veridian)

Si un commit upstream touche un de ces fichiers, **adapter
chirurgicalement** plutôt que cherry-pick brut :

- `console/src/App.tsx` (thème Veridian, Inter)
- `console/src/index.css` (boilerplate purgé)
- `console/index.html` (wordmark splash, branding)
- `console/vite.config.ts` (manualChunks — ATTENTION incident
  manualChunks 2026-05-22)
- `console/src/services/api/client.ts` (allowlist 401
  PUBLIC_TOKEN_ENDPOINTS)
- `console/src/layouts/WorkspaceLayout.tsx` (wordmark sidebar, dropdown
  d'aide Veridian)
- `console/src/pages/SignInPage.tsx` (wordmark)
- `console/src/pages/SetupWizard.tsx` (Steps + Result + branding)
- `console/src/pages/AcceptInvitationPage.tsx` (Result error,
  fix interceptor 401)
- `console/src/pages/AnalyticsPage.tsx` (Result 404 + CTA)
- `console/src/components/settings/SettingsSidebar.tsx` (item Plan
  Veridian)
- Toute la suite `veridian_*.tsx` / `veridian_*.go` (custom, ne sera
  jamais touchée par upstream)

---

## Validation finale obligatoire

Une fois les 10 commits traités :

- [ ] `cd console && npm run build` vert (lingui + tsc + vite)
- [ ] `cd console && npx vitest run` — 225 tests verts
- [ ] `go build ./... && go test ./internal/...` vert
- [ ] `scripts/ci/check-console-build.sh` vert (le garde-fou attrape
      les régressions manualChunks et i18n)
- [ ] Smoke staging post-deploy : `/console/signin` boote
      (wordmark `veridian.mail` présent), `/console/workspace/.../settings/plan`
      affiche « Plan » (pas un hash i18n)
- [ ] `config.VERSION` : bump à `42.0` après merge complet (notre
      numérotation interne, pas upstream)

---

## État final (à remplir au fil de l'eau)

| Release | Commit | Décision | Note |
|---|---|---|---|
| v30.2 | eec68b93 | ⬜ | (à traiter) |
| v30.2 | 2118c6c2 | ⬜ | (à traiter) |
| v30.2 | 9d1b5a96 | ⬜ | (à traiter) |
| v30.2 | d50c83a6 | ⬜ | (à traiter) |
| v30.2 | 8aaae0e1 | ⬜ | (à traiter) |
| v30.3 | 57a42d69 | ⬜ | (à traiter) |
| v31.0 | f5f144f7 | ⬜ | (à traiter) |
| v31.0 | eb1ead06 | ⬜ | (à traiter) |
| v31.0 | f9741fc8 | ⬜ | (à traiter) |
| v32.0 | 7a42651d | ⬜ | (à traiter) |

---

## Effort estimé

- Lecture + décision par commit : ~3-5 min × 10 = 30-50 min
- Cherry-pick + résolution conflits triviaux : 15-30 min
- Build + tests + smoke entre releases : ~20 min total
- **Total : 1h-1h30** (vs 1 journée pour un merge sauvage à résoudre)

À faire dans une **session dédiée** (pas en fin de soirée après 8h de
polish UI + 2 incidents prod résolus).

---

## Référence

- Procédure standard de sync dans `CLAUDE.md` Notifuse : section "Sync
  upstream" (`git pull upstream main` + `git merge main`). **CE ticket
  remplace cette procédure** par le cherry-pick chirurgical décidé
  2026-05-23.
- Si demain un nouveau commit upstream apparaît : compléter le tableau,
  pas créer un nouveau ticket.
- Pattern `veridian_*.{go,tsx}` flat (CLAUDE.md Notifuse) :
  upstream ne touche jamais ces fichiers, ils sont safe.
