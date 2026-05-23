// === Veridian patch — suite E2E « production-build smoke » (ticket 2026-05-22) ===
//
// Vérifie que TOUS les chunks JS/CSS chargés au boot du build de prod
// répondent en 200 et ne sont pas vides. C'est une vérification réseau
// (équivalent runtime de `check-console-build.sh` qui fait l'analyse
// statique du dist/).
//
// Pourquoi double-check au runtime alors que `check-console-build.sh`
// scanne déjà le dist/ ? Parce que :
//   - `check-console-build.sh` vérifie le DISQUE (les fichiers existent
//     et ne sont pas vides).
//   - Cette spec vérifie le RÉSEAU (le serveur de prod les sert bien,
//     aucun MIME-type loupé, aucun chemin de base /console/ cassé, aucun
//     content-encoding mauvais qui ferait que le navigateur reçoit du
//     vide).
//
// Les deux checks sont complémentaires.

import { test, expect } from '@playwright/test'
import {
  mockConfigJs,
  defaultApiStub,
  trackConsoleErrors,
  waitForAppMount
} from './helpers'

test.describe('Production build — intégrité chunks au runtime', () => {
  test.beforeEach(async ({ page }) => {
    await mockConfigJs(page)
    await defaultApiStub(page)
  })

  test('aucun chunk JS/CSS du build ne 404 ou ne renvoie un body vide', async ({ page }) => {
    const errors = trackConsoleErrors(page)

    // Capture toutes les réponses pour /console/assets/*
    interface AssetResponse {
      url: string
      status: number
      bodySize: number
      contentType: string
    }
    const assetResponses: AssetResponse[] = []

    page.on('response', async (response) => {
      const url = response.url()
      if (!url.includes('/console/assets/')) return
      if (!/\.(js|css)(\?|$)/.test(url)) return
      try {
        const body = await response.body()
        assetResponses.push({
          url,
          status: response.status(),
          bodySize: body.length,
          contentType: response.headers()['content-type'] ?? ''
        })
      } catch {
        // certaines réponses (cache hit, redirect) ne sont pas lisibles —
        // on ignore, le check status() suffit.
        assetResponses.push({
          url,
          status: response.status(),
          bodySize: -1,
          contentType: response.headers()['content-type'] ?? ''
        })
      }
    })

    await page.goto('/console/signin', { waitUntil: 'networkidle' })
    await waitForAppMount(page)

    // Au moins UN chunk JS doit avoir été chargé (sinon on n'a rien testé)
    const jsChunks = assetResponses.filter((r) => /\.js(\?|$)/.test(r.url))
    expect(jsChunks.length, 'Aucun chunk JS chargé — preview serveur cassé ?').toBeGreaterThan(0)

    // Aucun chunk en erreur HTTP
    const broken = assetResponses.filter((r) => r.status >= 400)
    expect(
      broken,
      `Chunks en erreur HTTP au chargement de /console/signin :\n${broken
        .map((b) => `  ${b.status} ${b.url}`)
        .join('\n')}`
    ).toEqual([])

    // Aucun chunk JS avec body de taille 0 (le bug manualChunks pouvait
    // générer ce cas). On ignore les réponses dont on n'a pas pu lire le
    // body (bodySize: -1).
    const empty = assetResponses.filter(
      (r) => /\.js(\?|$)/.test(r.url) && r.bodySize === 0
    )
    expect(
      empty,
      `Chunks JS vides (bodySize=0) — manualChunks mal découpé ?\n${empty
        .map((e) => `  ${e.url}`)
        .join('\n')}`
    ).toEqual([])

    // Pas d'erreur JS uncaught (filet de sécurité — chunk cassé =
    // souvent pageerror).
    expect(
      errors,
      `Erreurs JS uncaught pendant le chargement des chunks :\n${errors.join('\n---\n')}`
    ).toEqual([])
  })

  test('le chunk react-vendor est atomique (pas de chunk antd/tanstack séparé)', async ({
    page
  }) => {
    // Pendant un boot, on log tous les chunks JS chargés. Si un chunk
    // `tanstack-*.js` ou `antd-*.js` apparaît à côté de `react-vendor-*.js`,
    // c'est que manualChunks a re-divergé → potentiel crash createContext.
    //
    // Cette spec double `check-console-build.sh` au runtime — c'est la
    // ceinture de sécurité côté navigateur.

    const jsUrls: string[] = []
    page.on('response', (response) => {
      const url = response.url()
      if (url.includes('/console/assets/') && /\.js(\?|$)/.test(url)) {
        jsUrls.push(url)
      }
    })

    await page.goto('/console/signin', { waitUntil: 'networkidle' })
    await waitForAppMount(page)

    const filenames = jsUrls.map((u) => u.split('/').pop() ?? '')

    // S'il existe un chunk « react-vendor », alors aucun autre chunk ne
    // doit s'appeler antd-*, tanstack-*, ant-design-*, rc-*, fortawesome-*.
    const hasReactVendor = filenames.some((f) => /^react-vendor-/.test(f))
    if (hasReactVendor) {
      const forbidden = filenames.filter((f) =>
        /^(antd|tanstack|ant-design|rc-|fortawesome)-/.test(f)
      )
      expect(
        forbidden,
        `Chunks séparés du react-vendor détectés (incident #1 risque de revenir) :\n${forbidden.join(
          '\n'
        )}`
      ).toEqual([])
    }
  })
})
