import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

// Scenario complet de saasification Notifuse :
// 1. Hub (HMAC) → POST /api/tenants/provision → workspace + owner reel + API key + magic link
// 2. User clique magic link → connecte sur la console Notifuse
// 3. Hub (API key tenant) → POST /api/workspaces.generateMagicLink → nouvelle session OK
// 4. Hub (HMAC) → POST /api/tenants/suspend → tentative envoi → 402
// 5. Hub (HMAC) → POST /api/tenants/resume → envoi OK
// 6. Hub (HMAC) → DELETE /api/tenants/:id → soft delete
//
// Le test ne s'appuie sur AUCUN setup wizard (lance via env vars + ROOT_EMAIL configured at boot).

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

function signHMAC(body: string): { timestamp: string; signature: string } {
  const timestamp = Date.now().toString();
  const signature = crypto
    .createHmac('sha256', HUB_API_SECRET)
    .update(`${timestamp}.${body}`)
    .digest('hex');
  return { timestamp, signature };
}

async function hmacFetch(path: string, method: string, body: object | null = null) {
  const rawBody = body ? JSON.stringify(body) : '';
  const { timestamp, signature } = signHMAC(rawBody);
  const res = await fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Veridian-Hub-Signature': signature,
      'X-Veridian-Timestamp': timestamp,
    },
    body: rawBody || undefined,
  });
  return res;
}

// Génère un tenant_id unique pour ce run (préfixe e2e + timestamp)
const tenantId = `e2e${Date.now().toString(36).slice(-8)}`;
const ownerEmail = `${tenantId}@e2e.veridian.test`;

let provisioningResponse: any;

test.describe.serial('Notifuse saasification end-to-end', () => {
  test('1. Provision tenant via HMAC', async () => {
    const res = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
      plan: 'pro',
    });
    const body = await res.text();
    expect(res.status, body).toBe(200);
    provisioningResponse = JSON.parse(body);
    expect(provisioningResponse.workspace_id).toBe(tenantId);
    expect(provisioningResponse.owner_user_id).toBeTruthy();
    expect(provisioningResponse.api_key).toBeTruthy();
    expect(provisioningResponse.magic_link).toContain('/console/signin');
    expect(provisioningResponse.created).toBe(true);
  });

  test('2. Provision idempotent (re-call returns same workspace, created=false)', async () => {
    const res = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
      plan: 'pro',
    });
    const body = await res.text();
    expect(res.status, body).toBe(200);
    const data = JSON.parse(body);
    expect(data.workspace_id).toBe(tenantId);
    expect(data.created).toBe(false);
  });

  test('3. Owner can sign in via auto-login URL (headful, no manual code)', async ({ page }) => {
    // Auto-login URL : self-contained HMAC token, set localStorage + redirect.
    // Pas de saisie de code requise par le user.
    expect(provisioningResponse.auto_login_url).toContain('/veridian/auto-login?token=');

    await page.goto(provisioningResponse.auto_login_url);

    // Page intermédiaire HTML stocke auth_token dans localStorage puis redirect /console
    await page.waitForURL(/\/console(\/.*)?$/, { timeout: 30_000 });

    // Vérifier que le token est dans localStorage (le user est authentifié)
    const authToken = await page.evaluate(() => localStorage.getItem('auth_token'));
    expect(authToken).toBeTruthy();
    expect(authToken!.length).toBeGreaterThan(50); // JWT realistic length

    // Sanity : workspace name visible quelque part
    await expect(page.locator('body')).toContainText(tenantId, { timeout: 10_000 });
  });

  test('4. Generate fresh magic link via API key (tenant-scoped)', async () => {
    const res = await fetch(`${NOTIFUSE_URL}/api/workspaces.generateMagicLink`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${provisioningResponse.api_key}`,
      },
      body: JSON.stringify({ user_email: ownerEmail }),
    });
    expect(res.status).toBe(200);
    const data = await res.json();
    expect(data.magic_link).toContain('/console/signin');
    // === Veridian patch === auto_login_url désormais retourné aussi
    expect(data.auto_login_url).toContain('/veridian/auto-login?token=');
    expect(data.expires_at).toBeTruthy();
  });

  test('5. Status endpoint returns plan + quota', async () => {
    const res = await hmacFetch(`/api/tenants/${tenantId}/status`, 'GET');
    expect(res.status).toBe(200);
    const data = await res.json();
    expect(data.tenant_id).toBe(tenantId);
    expect(data.status).toBe('active');
    expect(data.plan).toBe('pro');
    expect(data.monthly_email_quota).toBe(10000);
    expect(data.quota_remaining).toBe(10000);
  });

  test('6. Send transactional email (active plan, success)', async () => {
    const res = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${provisioningResponse.api_key}`,
      },
      body: JSON.stringify({
        workspace_id: tenantId,
        to: 'sink@e2e.veridian.test',
        // smoke test : si plan check passe, c'est suffisant — l'envoi reel
        // peut echouer faute de provider configure, on test juste le 402 vs autre
      }),
    });
    // Attendu : pas 402 (plan actif). 4xx legitime sur body invalide OK.
    expect(res.status).not.toBe(402);
  });

  test('7. Suspend tenant', async () => {
    const res = await hmacFetch('/api/tenants/suspend', 'POST', {
      tenant_id: tenantId,
      reason: 'e2e test suspension',
    });
    expect(res.status).toBe(200);
    const data = await res.json();
    expect(data.suspended_at).toBeTruthy();
  });

  test('8. Send transactional after suspend → 402', async () => {
    const res = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${provisioningResponse.api_key}`,
      },
      body: JSON.stringify({ workspace_id: tenantId, to: 'sink@e2e.veridian.test' }),
    });
    expect(res.status).toBe(402);
    const data = await res.json();
    expect(data.error).toMatch(/payment|suspend/i);
  });

  test('9. Resume tenant', async () => {
    const res = await hmacFetch('/api/tenants/resume', 'POST', {
      tenant_id: tenantId,
    });
    expect(res.status).toBe(200);
  });

  test('10. Send transactional after resume → not 402', async () => {
    const res = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${provisioningResponse.api_key}`,
      },
      body: JSON.stringify({ workspace_id: tenantId, to: 'sink@e2e.veridian.test' }),
    });
    expect(res.status).not.toBe(402);
  });

  test('11. Soft delete tenant', async () => {
    const res = await hmacFetch(`/api/tenants/${tenantId}`, 'DELETE');
    expect(res.status).toBe(200);
    const data = await res.json();
    expect(data.deleted_at).toBeTruthy();
  });

  test('12. Status after delete shows status=deleted', async () => {
    const res = await hmacFetch(`/api/tenants/${tenantId}/status`, 'GET');
    expect(res.status).toBe(200);
    const data = await res.json();
    expect(data.status).toBe('deleted');
    expect(data.deleted_at).toBeTruthy();
  });
});
