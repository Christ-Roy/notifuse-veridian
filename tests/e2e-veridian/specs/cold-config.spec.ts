// === Giga E2E — Config cold outreach self-service (Lot 8) ===
//
// Couvre la config cold que Robert / un non-dev règle depuis la console SANS
// curl (Settings → Cold outreach) :
//
//   - IMAP self-service (Lot 1/8)         : POST/UPDATE intégration type "imap"
//   - Custom tracking domain (Lot 5/8)    : EmailProvider.veridian_tracking_domain
//   - Breakdown contacts par classe (R1)  : /api/veridian/contacts.providerBreakdown
//
// Deux niveaux :
//   1. @prod-safe  : routes montées + rejets d'auth (zéro side-effect, tourne en prod)
//   2. mutation    : contre STAGING réel — provision un workspace jetable via HMAC,
//      configure via l'API authentifiée (Bearer api_key owner), recharge, vérifie
//      la persistance. AUCUN envoi de mail réel (consigne stricte Robert : les
//      alias test routent vers sa boîte perso).
//
// Conventions héritées de la suite mega : readBody (stream lisible 1×), prefix
// tenant ≤20 chars varchar(20), afterAll wipe (describe.serial), retries en CI.

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

function signHMAC(body: string, tsOverride?: string) {
  const ts = tsOverride ?? Date.now().toString();
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

async function bearerFetch(path: string, jwt: string, method = 'GET', body: object | null = null) {
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${jwt}`,
    },
    body: body ? JSON.stringify(body) : undefined,
  });
}

// Connecte l'OWNER du workspace et retourne son JWT de session.
//
// IMPORTANT : l'api_key Bearer renvoyée par le provision N'EST PAS owner (rôle
// distinct) → les ops owner-only comme createIntegration/updateIntegration la
// rejettent en 403 "user is not an owner". Pour configurer une intégration
// (IMAP, tracking domain) il faut le JWT d'un USER owner. On l'obtient en
// suivant l'auto_login_url dans un vrai navigateur : la console échange le token
// HMAC self-contained contre une session et stocke le JWT dans
// localStorage.auth_token. On le récupère pour signer les appels API owner.
async function loginAsOwner(autoLoginUrl: string): Promise<string> {
  const browser = await chromium.launch();
  try {
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
    const page = await ctx.newPage();
    await page.goto(autoLoginUrl, { waitUntil: 'networkidle' });
    await page.waitForTimeout(2500);
    const jwt = await page.evaluate(() => localStorage.getItem('auth_token'));
    if (!jwt) throw new Error('auto-login did not yield an auth_token');
    return jwt;
  } finally {
    await browser.close();
  }
}

// ============================================================================
// NIVEAU 1 — @prod-safe : routes cold montées + rejets d'auth (read-only)
// ============================================================================

test.describe('@prod-safe Cold outreach — routes montées + auth', () => {
  test('@prod-safe breakdown contacts : POST monté + rejet sans auth (pas 404 catchall)', async () => {
    // Piège catchall root_handler (incident 2026-05-25) : si la route n'est pas
    // montée pour cette méthode, on tombe sur la SPA (HTML 200). On doit voir un
    // rejet d'auth JSON, jamais un 200 HTML ni un 404.
    const r = await fetch(`${NOTIFUSE_URL}/api/veridian/contacts.providerBreakdown?workspace_id=nope`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}',
    });
    expect(r.status).not.toBe(404);
    expect(r.status).not.toBe(200); // pas de SPA servie par le catchall
    expect([400, 401, 403]).toContain(r.status);
  });

  test('@prod-safe breakdown contacts : GET monté aussi (piège catchall sur méthode)', async () => {
    // POST ET GET doivent être routés explicitement (Go 1.22 mux par méthode).
    const r = await fetch(
      `${NOTIFUSE_URL}/api/veridian/contacts.providerBreakdown?workspace_id=nope`,
      { method: 'GET' },
    );
    expect(r.status).not.toBe(404);
    expect(r.status).not.toBe(200);
    expect([400, 401, 403]).toContain(r.status);
  });

  test('@prod-safe createIntegration monté + rejet sans auth', async () => {
    // L'endpoint self-service IMAP/email passe par workspaces.createIntegration.
    const r = await fetch(`${NOTIFUSE_URL}/api/workspaces.createIntegration`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}',
    });
    expect(r.status).not.toBe(404);
    expect([400, 401]).toContain(r.status);
  });

  test('@prod-safe updateIntegration monté + rejet sans auth', async () => {
    const r = await fetch(`${NOTIFUSE_URL}/api/workspaces.updateIntegration`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}',
    });
    expect(r.status).not.toBe(404);
    expect([400, 401]).toContain(r.status);
  });
});

// ============================================================================
// NIVEAU 2 — Mutation réelle sur STAGING (provision jetable, zéro mail envoyé)
// ============================================================================

// Ces tests exigent le secret HMAC pour provisionner. En prod ils sont skip
// (on ne crée pas de tenant en prod) ; en staging CI HUB_API_SECRET est injecté.
const mutationDescribe = HUB_API_SECRET ? test.describe.serial : test.describe.skip;

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

mutationDescribe('@cold Cold outreach config — persistance réelle (staging)', () => {
  // varchar(20) : `l8c<6chars>` = 9 chars.
  const tid = `l8c${RUN_STAMP}`;
  const ownerEmail = `e2e-lot8-cold-${RUN_STAMP}@e2e.veridian.site`;
  // JWT de l'USER owner (pas l'api_key, qui n'est pas owner — cf loginAsOwner).
  let jwt = '';

  test('00. Provision workspace jetable + login owner (JWT)', async () => {
    provisioned.push(tid);
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: ownerEmail,
      plan: 'free',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const prov = body.json();
    expect(prov.api_key).toBeTruthy();
    expect(prov.auto_login_url, 'provision must return auto_login_url').toBeTruthy();
    // Échange l'auto-login contre le JWT owner (les ops integration sont owner-only).
    jwt = await loginAsOwner(prov.auto_login_url);
    expect(jwt.length).toBeGreaterThan(20);
  });

  test('01. IMAP self-service : create owner → persisté, chiffré au repos', async () => {
    const create = await bearerFetch('/api/workspaces.createIntegration', jwt, 'POST', {
      workspace_id: tid,
      name: 'Cold reply inbox',
      type: 'imap',
      imap_settings: {
        host: 'imap.example.com',
        port: 993,
        username: 'returns@example.com',
        password: 's3cret-e2e',
        use_tls: true,
        folder: 'INBOX',
        polling_interval_seconds: 120,
      },
    });
    const cb = await readBody(create);
    expect(cb.raw).not.toContain('<!doctype'); // pas de catchall SPA
    // 201 Created (création d'intégration).
    expect([200, 201]).toContain(create.status);

    // Recharge le workspace et vérifie la persistance.
    const get = await bearerFetch(`/api/workspaces.get?id=${tid}`, jwt);
    const gb = await readBody(get);
    expect(get.status, gb.raw).toBe(200);
    const ws = gb.json().workspace;
    const imap = (ws.integrations || []).find((i: any) => i.type === 'imap');
    expect(imap, 'imap integration persisted').toBeTruthy();
    expect(imap.imap_settings.host).toBe('imap.example.com');
    expect(imap.imap_settings.username).toBe('returns@example.com');
    expect(imap.imap_settings.folder).toBe('INBOX');
    // Chiffré au repos : encrypted_password posé (le clair n'est jamais stocké en
    // DB). NB : workspaces.get est owner-only et renvoie aussi le clair déchiffré
    // pour le propriétaire — comportement upstream identique pour SMTP/SES.
    expect(imap.imap_settings.encrypted_password, 'password chiffré au repos').toBeTruthy();
  });

  test('02. IMAP self-service : update folder sans password → folder changé, creds préservés', async () => {
    const get0 = await bearerFetch(`/api/workspaces.get?id=${tid}`, jwt);
    const ws0 = (await readBody(get0)).json().workspace;
    const imap0 = (ws0.integrations || []).find((i: any) => i.type === 'imap');
    expect(imap0).toBeTruthy();
    const encBefore = imap0.imap_settings.encrypted_password;

    const upd = await bearerFetch('/api/workspaces.updateIntegration', jwt, 'POST', {
      workspace_id: tid,
      integration_id: imap0.id,
      name: 'Cold reply inbox',
      imap_settings: {
        host: 'imap.example.com',
        port: 993,
        username: 'returns@example.com',
        use_tls: true,
        folder: 'Bounces',
        // password absent => ne change pas (le backend préserve encrypted_password)
      },
    });
    const ub = await readBody(upd);
    expect(upd.status, ub.raw).toBe(200);

    const get1 = await bearerFetch(`/api/workspaces.get?id=${tid}`, jwt);
    const ws1 = (await readBody(get1)).json().workspace;
    const imap1 = (ws1.integrations || []).find((i: any) => i.type === 'imap');
    expect(imap1.imap_settings.folder).toBe('Bounces');
    expect(imap1.imap_settings.username).toBe('returns@example.com');
    // Le password chiffré est préservé (pas re-saisi → encrypted_password inchangé).
    expect(imap1.imap_settings.encrypted_password).toBe(encBefore);
  });

  test('03. Custom tracking domain : posé sur l’EmailProvider → persisté, senders conservés', async () => {
    // Crée une infra d'envoi SMTP minimale.
    const createEmail = await bearerFetch('/api/workspaces.createIntegration', jwt, 'POST', {
      workspace_id: tid,
      name: 'Cold relay',
      type: 'email',
      provider: {
        kind: 'smtp',
        rate_limit_per_minute: 25,
        smtp: { host: 'smtp.example.com', port: 587, username: 'u', password: 'p', use_tls: true },
        senders: [{ id: 's1', email: 'hello@agences-veridian.fr', name: 'Veridian', is_default: true }],
      },
    });
    const ceb = await readBody(createEmail);
    expect([200, 201]).toContain(createEmail.status);
    const emailIntegrationId = ceb.json().integration_id;
    expect(emailIntegrationId).toBeTruthy();

    // Pose le tracking domain (renvoie le provider COMPLET, comme l'UI).
    const upd = await bearerFetch('/api/workspaces.updateIntegration', jwt, 'POST', {
      workspace_id: tid,
      integration_id: emailIntegrationId,
      name: 'Cold relay',
      provider: {
        kind: 'smtp',
        rate_limit_per_minute: 25,
        smtp: { host: 'smtp.example.com', port: 587, username: 'u', use_tls: true },
        senders: [{ id: 's1', email: 'hello@agences-veridian.fr', name: 'Veridian', is_default: true }],
        veridian_tracking_domain: 'track.agences-veridian.fr',
      },
    });
    const ub = await readBody(upd);
    expect(upd.status, ub.raw).toBe(200);

    const get = await bearerFetch(`/api/workspaces.get?id=${tid}`, jwt);
    const ws = (await readBody(get)).json().workspace;
    const email = (ws.integrations || []).find((i: any) => i.id === emailIntegrationId);
    expect(email.email_provider.veridian_tracking_domain).toBe('track.agences-veridian.fr');
    // Senders conservés (on renvoie le provider entier, pas un patch partiel).
    expect(email.email_provider.senders).toHaveLength(1);
    expect(email.email_provider.senders[0].email).toBe('hello@agences-veridian.fr');
  });

  test('04. Breakdown contacts par classe : JSON cohérent (5 classes canoniques + total)', async () => {
    // Importe quelques contacts répartis sur des providers connus pour vérifier
    // la classification par suffixe (Google/Microsoft/freemail_fr/corporate).
    const importRes = await bearerFetch('/api/contacts.import', jwt, 'POST', {
      workspace_id: tid,
      contacts: [
        { email: `e2e-lot8-a-${RUN_STAMP}@gmail.com` },
        { email: `e2e-lot8-b-${RUN_STAMP}@outlook.com` },
        { email: `e2e-lot8-c-${RUN_STAMP}@orange.fr` },
        { email: `e2e-lot8-d-${RUN_STAMP}@some-unknown-corp-${RUN_STAMP}.com` },
      ],
    });
    const ib = await readBody(importRes);
    expect(importRes.status, ib.raw).toBe(200);

    const r = await bearerFetch(
      `/api/veridian/contacts.providerBreakdown?workspace_id=${tid}`,
      jwt,
    );
    const rb = await readBody(r);
    expect(rb.raw).not.toContain('<!doctype'); // pas de catchall
    expect(r.status, rb.raw).toBe(200);
    const data = rb.json();
    // 5 classes canoniques toujours présentes.
    for (const c of ['google', 'microsoft', 'yahoo_aol', 'freemail_fr', 'corporate']) {
      expect(typeof data.breakdown[c]).toBe('number');
    }
    expect(typeof data.total).toBe('number');
    // Les 4 contacts importés se retrouvent dans le total (au moins).
    expect(data.total).toBeGreaterThanOrEqual(4);
    // gmail → google, outlook → microsoft, orange.fr → freemail_fr.
    expect(data.breakdown.google).toBeGreaterThanOrEqual(1);
    expect(data.breakdown.microsoft).toBeGreaterThanOrEqual(1);
    expect(data.breakdown.freemail_fr).toBeGreaterThanOrEqual(1);
  });
});
