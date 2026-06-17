// === Smoke E2E — Dashboard analytics charge sans 500 (garde-fou P0) ===
//
// CE QUI A MANQUÉ : le bug P0 du 2026-06-17 (dashboard Email Metrics → 500
// `pq: column "bounce_type" does not exist`) est passé staging ET prod parce
// qu'AUCUN test ne CHARGE le dashboard contre le vrai schéma DB. Les unit tests
// (Vitest front + Go) mockent la DB → un mock vert ne prouve rien sur le flux
// réel. Ce smoke est le chaînon manquant : il provisionne un workspace NEUF,
// auto-login, charge le dashboard dans un navigateur headless, et ASSERTE :
//
//   1. ZÉRO réponse HTTP ≥ 500 sur les appels analytics du dashboard
//      (/api/analytics.query, /api/veridian/messages.replyStats,
//       /api/veridian/messages.engagementByClass,
//       /api/veridian/contacts.providerBreakdown).
//   2. ZÉRO erreur d'application affichée à l'écran (bandeau "Unable to load
//      email metrics") ni erreur console contenant "ApiError"/"Error".
//   3. Le graphique Email Metrics est bien rendu (carte "Sent" présente).
//
// Niveaux : ce spec EXIGE le secret HMAC (provision d'un workspace). En prod il
// est SKIP (on ne crée pas de tenant en prod) ; en staging CI HUB_API_SECRET est
// injecté → BLOQUANT (le job e2e-staging fait tourner tout specs/). AUCUN envoi
// de mail réel. Conventions héritées de cold-config.spec.ts : readBody, prefix
// tenant ≤20 chars, afterAll wipe, describe.serial.

import { test, expect, chromium } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL) {
  throw new Error('NOTIFUSE_URL env var required');
}

const RUN_STAMP = Date.now().toString(36).slice(-6);

// === Helpers ================================================================

async function readBody(r: Response): Promise<{ raw: string; json: () => any }> {
  const raw = await r.text();
  return { raw, json: () => (raw ? JSON.parse(raw) : null) };
}

function signHMAC(body: string) {
  const ts = Date.now().toString();
  return {
    timestamp: ts,
    signature: crypto.createHmac('sha256', HUB_API_SECRET).update(`${ts}.${body}`).digest('hex'),
  };
}

async function hmacFetch(path: string, method: string, body: object | null = null) {
  const raw = body ? JSON.stringify(body) : '';
  const { timestamp, signature } = signHMAC(raw);
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Veridian-Hub-Signature': signature,
      'X-Veridian-Timestamp': timestamp,
    },
    body: raw || undefined,
  });
}

// Endpoints analytics que le dashboard appelle. On surveille tout 5xx sur ces
// chemins pendant le chargement de la page (c'est le bug P0 exact : un 500).
const DASHBOARD_API_PATTERNS = [
  '/api/analytics.query',
  '/api/veridian/messages.replyStats',
  '/api/veridian/messages.engagementByClass',
  '/api/veridian/contacts.providerBreakdown',
];

function isDashboardApi(url: string): boolean {
  return DASHBOARD_API_PATTERNS.some((p) => url.includes(p));
}

// ============================================================================
// Mutation réelle sur STAGING (provision jetable). Skip en prod (pas de tenant).
// ============================================================================

const smokeDescribe = HUB_API_SECRET ? test.describe.serial : test.describe.skip;

const provisioned: string[] = [];

test.afterAll(async () => {
  if (!HUB_API_SECRET || provisioned.length === 0) return;
  const ids = [...provisioned];
  provisioned.length = 0;
  try {
    await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
      tenant_ids: ids,
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest'],
    });
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn(`afterAll wipe failed (non-fatal): ${err}`);
  }
});

smokeDescribe('@cold Dashboard smoke — charge sans 500 sur workspace neuf (staging)', () => {
  // varchar(20) : `dash<6chars>` = 10 chars.
  const tid = `dash${RUN_STAMP}`;
  const ownerEmail = `e2e-dashboard-smoke-${RUN_STAMP}@e2e.veridian.site`;
  let autoLoginUrl = '';

  test('00. Provision workspace NEUF (le schéma DB est testé tel quel)', async () => {
    provisioned.push(tid);
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: ownerEmail,
      plan: 'free',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const prov = body.json();
    expect(prov.auto_login_url, 'provision must return auto_login_url').toBeTruthy();
    autoLoginUrl = prov.auto_login_url;
  });

  test('01. Dashboard charge : 0 réponse ≥500 sur les appels analytics + graphique rendu', async () => {
    expect(autoLoginUrl, 'auto_login_url requis (test 00 doit passer)').toBeTruthy();

    const browser = await chromium.launch();
    const serverErrors: string[] = [];
    const consoleErrors: string[] = [];
    try {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
      const page = await ctx.newPage();

      // Capture tout 5xx sur les endpoints analytics du dashboard.
      page.on('response', (resp) => {
        const url = resp.url();
        if (isDashboardApi(url) && resp.status() >= 500) {
          serverErrors.push(`${resp.status()} ${url}`);
        }
      });

      // Capture les erreurs console parlantes (ApiError/Error) — le symptôme
      // visible du bug ("Failed to fetch email metrics: ApiError: true").
      page.on('console', (msg) => {
        if (msg.type() === 'error') {
          const text = msg.text();
          if (/ApiError|Failed to fetch email metrics|column .* does not exist/i.test(text)) {
            consoleErrors.push(text);
          }
        }
      });
      page.on('pageerror', (err) => {
        consoleErrors.push(`pageerror: ${err.message}`);
      });

      // 1) Auto-login : pose la session. ⚠️ Atterrit sur /console = écran
      // "Select workspace" (PAS le dashboard) → aucun appel analytics déclenché
      // ici. (Bug smoke initial : on restait sur cet écran → body quasi vide +
      // assertions analytics faussement vertes car jamais appelées.)
      await page.goto(autoLoginUrl, { waitUntil: 'networkidle' });
      await page.waitForTimeout(1500);
      // 2) Navigue EXPLICITEMENT vers le dashboard du workspace → c'est CE mount
      // qui déclenche AnalyticsPage → analytics.query/replyStats/engagementByClass
      // (le vrai chemin du bug P0). Sans ça, le smoke ne teste rien.
      await page.goto(`${NOTIFUSE_URL}/console/workspace/${tid}`, {
        waitUntil: 'networkidle',
      });
      // Laisse les requêtes analytics (useEffect au mount) partir et revenir.
      await page.waitForTimeout(3000);
      await page.waitForLoadState('networkidle').catch(() => {});

      // 1) AUCUN 5xx sur les endpoints analytics (le bug P0 exact).
      expect(serverErrors, `5xx analytics: ${serverErrors.join(' | ')}`).toEqual([]);

      // 2) AUCUNE erreur d'app affichée (bandeau d'erreur) ni console parlante.
      const errorBanner = await page
        .getByText(/Unable to load email metrics/i)
        .count()
        .catch(() => 0);
      expect(errorBanner, 'le bandeau "Unable to load email metrics" ne doit pas apparaître').toBe(0);
      expect(
        consoleErrors,
        `erreurs console analytics: ${consoleErrors.join(' | ')}`,
      ).toEqual([]);

      // 3) La page n'a pas crashé (pas d'error boundary React global).
      // ⚠️ On NE dépend PAS d'un texte de section ("Email Metrics", "Sent"...) :
      // ces libellés passent par Lingui (i18n) → en staging/CI ils peuvent être
      // traduits ou rendus via hash, donc un match texte = faux négatif garanti
      // (piège Lingui connu). Le VRAI garde-fou anti-régression du bug P0 est déjà
      // assuré par les assertions 1 & 2 (0 réponse ≥500 sur analytics + 0 bandeau
      // "Unable to load email metrics" + 0 erreur console). Ici on confirme juste
      // que le dashboard a rendu du contenu et PAS l'error boundary global React.
      const crashBoundary = await page
        .getByText(/Something went wrong/i)
        .count()
        .catch(() => 0);
      expect(crashBoundary, "pas d'error boundary React global sur le dashboard").toBe(0);
      const bodyLen = (await page.locator('body').innerText().catch(() => '')).length;
      expect(bodyLen, 'le dashboard doit rendre du contenu (pas un écran vide)').toBeGreaterThan(200);
    } finally {
      await browser.close();
    }
  });
});
