import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './specs',
  // 180s : certains tests attendent le cache TTL paywall (60s) entre suspend
  // et envoi pour valider le 402, donc 90s est trop court.
  timeout: 180_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // Pas de retry en CI : les tests sont sequentiels, un retry refait tout
  // depuis zero et sature le pool DB. Mieux vaut fix les flakys que de retry.
  retries: 0,
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
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
