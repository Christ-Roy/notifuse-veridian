// === Veridian patch — Lot R (2026-05-21) ===
// Tests E2E du endpoint POST /api/users/by-email (Hub discovery cross-app).
//
// Le Hub appelle ce endpoint au login user pour decouvrir si l'utilisateur a
// un compte Notifuse et quels workspaces il possede, sans dependre des colonnes
// denormalisees hub_app.tenants.
//
// Spec parent : todo/done/2026-05-20-add-discovery-endpoint-by-email.md
// Handler     : internal/http/veridian_discovery_handler.go
//
// Conventions Lot N : tids `t-`, afterEach cleanup, tag @regression.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
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

async function provisionTenant(tenantId: string, ownerEmail: string, plan = 'free') {
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
      plan,
    });
    if (r.status === 200) return r.json();
    if ((r.status >= 500 || r.status === 404) && attempt < 4) {
      await new Promise((res) => setTimeout(res, 500 * Math.pow(2, attempt)));
      continue;
    }
    expect(r.status, await r.text()).toBe(200);
  }
  throw new Error('provision retry exhausted');
}

// === Cleanup tracker ===
const provisioned: string[] = [];

test.afterEach(async () => {
  if (provisioned.length === 0) return;
  try {
    await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
      tenant_ids: provisioned,
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest'],
    });
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn(`afterEach wipe failed: ${err}`);
  }
  provisioned.length = 0;
});

test.describe('@regression discovery-by-email — POST /api/users/by-email', () => {
  test('user existant avec 2 workspaces → found:true + 2 workspaces remplis', async () => {
    // Sharing user entre 2 tenants : meme owner_email sur 2 provisions distincts.
    // Notifuse expose /api/users/by-email qui resout les workspaces partages par
    // un meme user humain (cf. handler).
    const userEmail = `t-multi-${Date.now().toString(36).slice(-6)}@discovery.test`;
    const tid1 = `t-${Date.now().toString(36).slice(-6)}a`;
    const tid2 = `t-${Date.now().toString(36).slice(-6)}b`;
    provisioned.push(tid1, tid2);

    await provisionTenant(tid1, userEmail, 'free');
    // Petite pause pour eviter collision timestamp tids
    await new Promise((res) => setTimeout(res, 20));
    await provisionTenant(tid2, userEmail, 'pro');

    const r = await hmacFetch('/api/users/by-email', 'POST', { email: userEmail });
    expect(r.status, await r.text()).toBe(200);
    const body = await r.json();
    expect(body.found).toBe(true);
    expect(body.user_email).toBe(userEmail);
    expect(Array.isArray(body.workspaces)).toBe(true);
    expect(body.workspaces.length).toBeGreaterThanOrEqual(2);

    const wsIds = body.workspaces.map((w: { workspace_id: string }) => w.workspace_id);
    expect(wsIds).toContain(tid1);
    expect(wsIds).toContain(tid2);

    // Chaque workspace doit exposer les champs documentes
    for (const w of body.workspaces) {
      expect(w.workspace_id).toBeTruthy();
      expect(w.role).toBeTruthy();
      // magic_link_capable doit etre true puisque Notifuse expose
      // veridian_magic_handler / veridian_autologin_handler
      expect(w.magic_link_capable).toBe(true);
      // fallback_url presente (URL signin Notifuse)
      expect(w.fallback_url).toMatch(/signin|notifuse/i);
    }
  });

  test('user inconnu : found:false + workspaces:[]', async () => {
    const ghostEmail = `t-ghost-${Date.now().toString(36).slice(-6)}@discovery.test`;
    const r = await hmacFetch('/api/users/by-email', 'POST', { email: ghostEmail });
    // Spec : toujours 200 (jamais 404) car la decouverte est best-effort
    expect(r.status, await r.text()).toBe(200);
    const body = await r.json();
    expect(body.found).toBe(false);
    expect(body.user_email).toBe(ghostEmail);
    expect(Array.isArray(body.workspaces)).toBe(true);
    expect(body.workspaces).toHaveLength(0);
  });

  test('HMAC invalide : 401', async () => {
    const raw = JSON.stringify({ email: 'whatever@discovery.test' });
    const ts = Date.now().toString();
    const wrongSig = crypto.createHmac('sha256', 'WRONG_SECRET').update(`${ts}.${raw}`).digest('hex');

    const r = await fetch(`${NOTIFUSE_URL}/api/users/by-email`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': wrongSig,
        'X-Veridian-Timestamp': ts,
      },
      body: raw,
    });
    expect([401, 403]).toContain(r.status);
  });

  test('email manquant : 400', async () => {
    const r = await hmacFetch('/api/users/by-email', 'POST', {});
    expect(r.status).toBe(400);
    const body = await r.json();
    expect(JSON.stringify(body)).toMatch(/email/i);
  });

  test('email invalide (sans @) : 400', async () => {
    const r = await hmacFetch('/api/users/by-email', 'POST', { email: 'not-an-email' });
    expect(r.status).toBe(400);
  });

  test('HMAC drift > 5min : 401', async () => {
    const raw = JSON.stringify({ email: 'whatever@discovery.test' });
    const stale = (Date.now() - 10 * 60 * 1000).toString();
    const sig = crypto.createHmac('sha256', HUB_API_SECRET).update(`${stale}.${raw}`).digest('hex');

    const r = await fetch(`${NOTIFUSE_URL}/api/users/by-email`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': sig,
        'X-Veridian-Timestamp': stale,
      },
      body: raw,
    });
    expect([401, 403]).toContain(r.status);
  });
});
