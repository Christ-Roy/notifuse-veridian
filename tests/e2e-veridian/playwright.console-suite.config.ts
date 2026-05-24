// === Veridian patch — config Playwright suite E2E console (staging réel) ===
//
// Suite COMPLÉMENTAIRE de la suite e2e-staging principale :
//   - principale (playwright.config.ts, testDir=./specs) : tests HMAC,
//     chaos, contrat Hub… purement backend. BLOQUANTE en CI staging.
//   - console-suite (cette config, testDir=./console-suite) : tests
//     navigateur sur staging réel — boot, nav, auth UI, responsive.
//     INFORMATIVE pour démarrer (workflow CI séparé non-bloquant).
//
// On garde 2 configs distinctes pour pouvoir :
//   1. Ne PAS faire fail la promo prod sur un flaky d'écran tant que la
//      suite n'est pas mûre.
//   2. Avoir un timeout adapté (les tests UI doivent attendre lazy chunks,
//      hydration, antd, etc. — plus lent que du HMAC pur).
//   3. Pouvoir trace+screenshot tous les tests (utile pour drill-down).
//
// Quand la suite sera stable, on la branchera en `needs:` de deploy-prod
// dans veridian-ci.yml (cf. ticket suite-e2e-playwright-console §5
// "Quand la suite est stable et fiable → la passer bloquante sur staging").

import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './console-suite',
  // 240s : les tests UI peuvent attendre auto-login redirect (10s) + lazy
  // chunks (5-10s par écran) + hydration Antd. 4min couvre largement.
  timeout: 240_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // 2 retries en CI : un flaky réseau peut tomber sur lazy chunk download.
  // workers=1 garantit qu'un seul test tourne — pas de saturation pool DB.
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: process.env.CI
    ? ([
        ['line'],
        ['html', { open: 'never', outputFolder: 'playwright-report-console-suite' }],
        ['json', { outputFile: 'playwright-results-console-suite.json' }]
      ] as const)
    : 'list',
  use: {
    baseURL: process.env.NOTIFUSE_URL || 'https://notifuse.staging.veridian.site',
    // Trace + screenshot TOUJOURS (vs retain-on-failure de la suite principale) :
    // les bugs UI sont visuels, on veut pouvoir replayer pour comprendre même
    // un test qui passe (vérifier qu'on a vu le bon écran, pas un blank).
    trace: 'on',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    // Headless par défaut (CI). HEADED=1 pour debug local.
    headless: process.env.HEADED !== '1',
    // Viewport par défaut desktop. Les tests responsive override via test.use.
    viewport: { width: 1440, height: 900 }
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] }
    }
  ]
})
