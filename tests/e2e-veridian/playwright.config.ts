import { defineConfig, devices } from '@playwright/test';

const chromeExecutablePath = process.env.PLAYWRIGHT_CHROME_EXECUTABLE_PATH;
const disableVideo = process.env.PLAYWRIGHT_DISABLE_VIDEO === '1';

export default defineConfig({
  testDir: './specs',
  // Filet anti-accumulation : GC des bases de test orphelines en fin de run
  // (un test crashé saute son afterAll → tenant+base restent → pool DB sature →
  // chaos-provisioning/anti-regression flakent). Best-effort, staging-only.
  // cf. todo/2026-06-20-e2e-sature-staging-provisioning-pic.md
  globalTeardown: './global-teardown.ts',
  // 180s : certains tests attendent le cache TTL paywall (60s) entre suspend
  // et envoi pour valider le 402, donc 90s est trop court.
  timeout: 180_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // En CI : 2 retries pour absorber les flakys réseau/timing sans saturer
  // le pool DB (workers: 1 garantit qu'un seul test tourne à la fois —
  // le retry rejoue 1 test isolé, pas la suite entière).
  // En local : 0 (pas de retry, on veut voir l'échec immédiatement).
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  // En CI : reporter line (lisible dans logs streames) + html (drill-down
  // post-mortem via artifact uploade) + json (parsing programmatique futur).
  // En dev local : list (interactif).
  reporter: process.env.CI
    ? ([
        ['line'],
        ['html', { open: 'never', outputFolder: 'playwright-report' }],
        ['json', { outputFile: 'playwright-results.json' }],
      ] as const)
    : 'list',
  use: {
    baseURL: process.env.NOTIFUSE_URL || 'http://localhost:8080',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: disableVideo ? 'off' : 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: chromeExecutablePath ? { executablePath: chromeExecutablePath } : undefined,
      },
    },
  ],
});
