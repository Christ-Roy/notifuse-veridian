// CI violente — Suite 1 : stress provisioning, replay, tampering, cas limites HMAC.
// Aucun navigateur ici (API only) — Playwright juste pour le runtime.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

function signHMAC(body: string, secret = HUB_API_SECRET, ts = Date.now().toString()) {
  const signature = crypto.createHmac('sha256', secret).update(`${ts}.${body}`).digest('hex');
  return { timestamp: ts, signature };
}

async function hmacFetch(
  path: string,
  method: string,
  body: object | null = null,
  overrides: Partial<{ timestamp: string; signature: string; secret: string }> = {},
) {
  const rawBody = body ? JSON.stringify(body) : '';
  const { timestamp: defaultTs, signature: defaultSig } = signHMAC(rawBody, overrides.secret);
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Veridian-Hub-Signature': overrides.signature ?? defaultSig,
      'X-Veridian-Timestamp': overrides.timestamp ?? defaultTs,
    },
    body: rawBody || undefined,
  });
}

test.describe('Chaos provisioning — concurrence', () => {
  test('10 provisions concurrentes meme tenant → 1 created, 9 idempotent, 0 erreur', async () => {
    const tenantId = `chaos${Date.now().toString(36).slice(-8)}`;
    const promises = Array.from({ length: 10 }, () =>
      hmacFetch('/api/tenants/provision', 'POST', {
        tenant_id: tenantId,
        owner_email: `${tenantId}@chaos.test`,
        plan: 'free',
      }),
    );
    const results = await Promise.all(promises);

    const statuses = results.map((r) => r.status);
    expect(statuses.every((s) => s === 200)).toBe(true);

    const bodies = await Promise.all(results.map((r) => r.json()));
    const createdCount = bodies.filter((b) => b.created === true).length;
    const idempotentCount = bodies.filter((b) => b.created === false).length;

    // En vrai concurrent, plusieurs goroutines peuvent passer le check existence
    // avant que le INSERT n'ait lieu. Le service doit gérer ça : 1 created, le reste idempotent.
    expect(createdCount).toBe(1);
    expect(idempotentCount).toBe(9);

    // Tous doivent retourner le meme workspace_id et la meme api_key (ou null si idempotent)
    const workspaceIds = new Set(bodies.map((b) => b.workspace_id));
    expect(workspaceIds.size).toBe(1);
  });

  test('50 provisions concurrentes tenants distincts → tous 200, pas de fuite DB', async () => {
    const promises = Array.from({ length: 50 }, (_, i) => {
      const tid = `chaos50${Date.now().toString(36).slice(-6)}${i}`;
      return hmacFetch('/api/tenants/provision', 'POST', {
        tenant_id: tid,
        owner_email: `${tid}@chaos.test`,
        plan: 'free',
      });
    });
    const results = await Promise.all(promises);
    const failed = results.filter((r) => r.status !== 200);
    if (failed.length > 0) {
      const errors = await Promise.all(failed.map((r) => r.text()));
      console.error('Failed responses:', errors);
    }
    expect(failed.length).toBe(0);
  });
});

test.describe('Chaos HMAC — replay et tampering', () => {
  test('replay attaque : meme signature 6 min apres → 401 timestamp drift', async () => {
    const body = JSON.stringify({
      tenant_id: 'replaytest',
      owner_email: 'r@chaos.test',
      plan: 'free',
    });
    const oldTs = (Date.now() - 6 * 60 * 1000).toString();
    const oldSig = crypto.createHmac('sha256', HUB_API_SECRET).update(`${oldTs}.${body}`).digest('hex');

    const res = await fetch(`${NOTIFUSE_URL}/api/tenants/provision`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': oldSig,
        'X-Veridian-Timestamp': oldTs,
      },
      body,
    });
    expect(res.status).toBe(401);
    const data = await res.json();
    expect(data.error).toMatch(/drift|timestamp/i);
  });

  test('tampering body : modifie 1 byte apres signature → 401 invalid signature', async () => {
    const body = JSON.stringify({
      tenant_id: 'tampertest',
      owner_email: 't@chaos.test',
      plan: 'free',
    });
    const { timestamp, signature } = signHMAC(body);
    const tamperedBody = body.replace('"plan":"free"', '"plan":"enterprise"');

    const res = await fetch(`${NOTIFUSE_URL}/api/tenants/provision`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': signature,
        'X-Veridian-Timestamp': timestamp,
      },
      body: tamperedBody,
    });
    expect(res.status).toBe(401);
  });

  test('mauvais secret HMAC → 401', async () => {
    const res = await hmacFetch(
      '/api/tenants/provision',
      'POST',
      { tenant_id: 'wrongsecret', owner_email: 'w@chaos.test', plan: 'free' },
      { secret: 'wrong-secret-not-the-real-one' },
    );
    expect(res.status).toBe(401);
  });

  test('signature manquante → 401', async () => {
    const res = await fetch(`${NOTIFUSE_URL}/api/tenants/provision`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tenant_id: 'nosig', owner_email: 'n@chaos.test' }),
    });
    expect(res.status).toBe(401);
  });

  test('timestamp non-numerique → 401', async () => {
    const res = await hmacFetch(
      '/api/tenants/provision',
      'POST',
      { tenant_id: 'badts', owner_email: 'b@chaos.test' },
      { timestamp: 'not-a-number' },
    );
    expect(res.status).toBe(401);
  });

  test('timestamp negatif → 401', async () => {
    const res = await hmacFetch(
      '/api/tenants/provision',
      'POST',
      { tenant_id: 'negts', owner_email: 'n@chaos.test' },
      { timestamp: '-1000' },
    );
    expect(res.status).toBe(401);
  });

  test('body 5MB → 413 request entity too large', async () => {
    const huge = 'x'.repeat(5 * 1024 * 1024);
    const body = JSON.stringify({
      tenant_id: 'huge',
      owner_email: 'h@chaos.test',
      workspace_name: huge,
    });
    const { timestamp, signature } = signHMAC(body);
    const res = await fetch(`${NOTIFUSE_URL}/api/tenants/provision`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': signature,
        'X-Veridian-Timestamp': timestamp,
      },
      body,
    });
    // 413 si middleware reject avant, 400 si JSON invalid post-parse, jamais 500
    expect([413, 400]).toContain(res.status);
  });

  test('body vide POST → 400 ou 401 (signature de body vide doit matcher)', async () => {
    // Signature pour body vide doit etre coherente
    const { timestamp, signature } = signHMAC('');
    const res = await fetch(`${NOTIFUSE_URL}/api/tenants/provision`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': signature,
        'X-Veridian-Timestamp': timestamp,
      },
    });
    // Body vide → handler doit retourner 400 (pas crash 500)
    expect(res.status).toBe(400);
  });

  test('JSON invalide → 400', async () => {
    const body = '{not really json';
    const { timestamp, signature } = signHMAC(body);
    const res = await fetch(`${NOTIFUSE_URL}/api/tenants/provision`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': signature,
        'X-Veridian-Timestamp': timestamp,
      },
      body,
    });
    expect(res.status).toBe(400);
  });

  test('owner_email vide → 400', async () => {
    const res = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: 'noemail',
      owner_email: '',
    });
    expect(res.status).toBe(400);
  });

  test('tenant_id manquant → 400', async () => {
    const res = await hmacFetch('/api/tenants/provision', 'POST', {
      owner_email: 'nofix@chaos.test',
    });
    expect(res.status).toBe(400);
  });

  test('GET sur endpoint POST → 405 ou 404', async () => {
    const res = await hmacFetch('/api/tenants/provision', 'GET');
    expect([404, 405]).toContain(res.status);
  });
});

test.describe('Chaos delete → re-provision', () => {
  test('delete puis re-provision meme tenant → 409 Conflict tant que pas purge 30j', async () => {
    const tid = `delconf${Date.now().toString(36).slice(-6)}`;

    // 1. Provision
    let r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: `${tid}@chaos.test`,
      plan: 'free',
    });
    expect(r.status).toBe(200);

    // 2. Delete
    r = await hmacFetch(`/api/tenants/${tid}`, 'DELETE');
    expect(r.status).toBe(200);

    // 3. Re-provision → 409
    r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: `${tid}@chaos.test`,
      plan: 'free',
    });
    expect(r.status).toBe(409);
    const body = await r.json();
    expect(body.error).toMatch(/deleted|purge/i);
  });
});
