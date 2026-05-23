// === Veridian patch — suite E2E « production-build smoke » (ticket 2026-05-22) ===
//
// Pourquoi un fichier de config séparé de `playwright.config.ts` :
//   - Le config principal pointe sur `npm run dev` (Vite dev server, hot
//     reload, modules ESM servis un par un, t`...` résolu à la volée).
//     C'est ce qui a permis aux 225 tests unitaires + aux specs Playwright
//     existantes de PASSER alors que la prod plantait — le bug
//     `manualChunks` ET le bug catalogues Lingui non-extractés ne se
//     manifestent QUE sur le build de production.
//   - Ce config ici lance `npm run build && vite preview` puis exerce
//     l'app exactement comme un vrai utilisateur la verrait en prod
//     (chunks découpés, catalogues compilés, base /console/).
//   - C'est LE filet qui aurait attrapé les 2 incidents 2026-05-22.
//
// Le preview utilise `vite.preview.config.ts` (HTTP plain, sans le cert
// self-signed du dev server) — sinon le healthcheck `webServer.url` de
// Playwright timeout sur « Self-signed certificate detected ».
//
// Specs ciblées : console/e2e-prod-smoke/*.spec.ts
//
// Run local :  cd console && npm run test:prod-smoke
//              cd console && npm run test:prod-smoke:skip-build   (réutilise dist/)
// Run CI    :  job `e2e-console-prod-smoke` dans .github/workflows/veridian-ci.yml

import { defineConfig, devices } from '@playwright/test'

const PREVIEW_PORT = 4173
const PREVIEW_BASE = `http://127.0.0.1:${PREVIEW_PORT}`

export default defineConfig({
  testDir: './e2e-prod-smoke',
  fullyParallel: false, // un seul webServer (vite preview) — sérialiser évite les flakes
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: [['html', { outputFolder: 'playwright-report-prod-smoke' }], ['list']],
  timeout: 60000,
  use: {
    baseURL: PREVIEW_BASE,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure'
  },
  projects: [
    {
      name: 'chromium-prod-build',
      use: { ...devices['Desktop Chrome'] }
    }
  ],
  webServer: {
    // Build PUIS preview — c'est crucial : on ne teste pas le dev server,
    // on teste les artefacts de prod servis statiques.
    // SKIP_BUILD=1 réutilise un dist/ déjà présent (utile en local pour
    // itérer sur les specs sans rebuild à chaque fois).
    command:
      process.env.SKIP_BUILD === '1'
        ? `npx vite preview --config vite.preview.config.ts --port ${PREVIEW_PORT} --host 127.0.0.1 --strictPort`
        : `npm run build && npx vite preview --config vite.preview.config.ts --port ${PREVIEW_PORT} --host 127.0.0.1 --strictPort`,
    url: `${PREVIEW_BASE}/console/`,
    reuseExistingServer: !process.env.CI,
    timeout: 240000,
    stdout: 'pipe',
    stderr: 'pipe'
  }
})
