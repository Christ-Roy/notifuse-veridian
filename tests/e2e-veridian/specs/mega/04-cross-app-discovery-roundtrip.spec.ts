// === MEGA E2E — Spec 04 — Cross-app discovery roundtrip ===
//
// Contribution Notifuse au ticket Hub MEGA-E2E (Bucket G-02 equivalent).
// Spec parent : ../veridian-hub/todo/2026-05-23-MEGA-E2E-post-commercialisation.md
//
// Notifuse expose /api/users/by-email en POST + GET symetriques. Le Hub
// consomme :
//   - GET (querystring + signature canonical `${ts}.`) → cron reconcile
//     (lib/sync/discovery.ts:110)
//   - POST (body JSON + signature `${ts}.${body}`)    → login flow
//
// Le bug prod 2026-05-25 (cron Hub reconcile 17 faux positifs
// `tenant_missing_app`) etait justement la regression silencieuse GET 200
// body vide (catchall root_handler.go) → la spec garantit la symetrie POST/GET.
//
// MEGA-04 ajoute :
//   1. Multi-tenant : 1 user humain dans 3 workspaces (free + pro + enterprise)
//      → POST/GET retournent les 3 workspaces avec roles + plans coherents
//   2. Symetrie stricte : POST et GET retournent EXACTEMENT meme shape
//   3. Cas inconnu : 200 found:false body NON vide (anti-regression bug prod)
//   4. Cas HMAC : drift > 5min refuse, signature wrong secret refuse
//   5. Validation email : email manquant 400, format invalide 400
//
// Conventions : tids `mega04-<RUN_STAMP>-<slug>`, afterEach wipe.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

const RUN_STAMP = Date.now().toString(36).slice(-6);

// === HMAC helpers ===========================================================

function signHMACPost(body: string, tsOverride?: string) {
  const ts = tsOverride ?? Date.now().toString();
  return {
    timestamp: ts,
    signature: crypto.createHmac('sha256', HUB_API_SECRET).update(`${ts}.${body}`).digest('hex'),
  };
}

function signHMACGet(tsOverride?: string) {
  // Canonical GET cote Hub : `${ts}.` (body vide, mais le `.` reste).
  // Pixel-parfait avec veridian-hub/lib/sync/discovery.ts:110 signGet().
  const ts = tsOverride ?? Date.now().toString();
  return {
    timestamp: ts,
    signature: crypto.createHmac('sha256', HUB_API_SECRET).update(`${ts}.`).digest('hex'),
  };
}

async function hmacFetchPost(path: string, body: object | null = null) {
  const raw = body ? JSON.stringify(body) : '';
  const { timestamp, signature } = signHMACPost(raw);
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Veridian-Hub-Signature': signature,
      'X-Veridian-Timestamp': timestamp,
    },
    body: raw || undefined,
  });
}

async function hmacGetByEmail(email: string) {
  const { timestamp, signature } = signHMACGet();
  const url = `${NOTIFUSE_URL}/api/users/by-email?email=${encodeURIComponent(email)}`;
  return fetch(url, {
    method: 'GET',
    headers: {
      'X-Veridian-Hub-Signature': signature,
      'X-Veridian-Timestamp': timestamp,
    },
  });
}

async function readBody(r: Response): Promise<{ raw: string; json: () => any }> {
  const raw = await r.text();
  return { raw, json: () => (raw ? JSON.parse(raw) : null) };
}

async function provisionTenant(tenantId: string, ownerEmail: string, plan = 'free') {
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetchPost('/api/tenants/provision', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
      plan,
    });
    const body = await readBody(r);
    if (r.status === 200) return body.json();
    if ((r.status >= 500 || r.status === 404) && attempt < 4) {
      await new Promise((res) => setTimeout(res, 500 * Math.pow(2, attempt)));
      continue;
    }
    expect(r.status, body.raw).toBe(200);
  }
  throw new Error('provision retry exhausted');
}

const provisioned: string[] = [];

test.afterEach(async () => {
  if (provisioned.length === 0) return;
  try {
    await hmacFetchPost('/api/veridian/admin/wipe-test-tenants', {
      tenant_ids: provisioned,
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest'],
    });
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn(`afterEach wipe failed: ${err}`);
  }
  provisioned.length = 0;
});

test.describe('@mega @regression MEGA-04 — cross-app discovery POST/GET roundtrip', () => {
  test('user multi-workspace (3 tenants free/pro/enterprise) : POST + GET retournent les 3', async () => {
    const userEmail = `e2e-mega-04-multi-${RUN_STAMP}@e2e.veridian.site`;
    const tid1 = `m04${RUN_STAMP}f`;
    const tid2 = `m04${RUN_STAMP}p`;
    const tid3 = `m04${RUN_STAMP}e`;
    provisioned.push(tid1, tid2, tid3);

    await provisionTenant(tid1, userEmail, 'free');
    await new Promise((res) => setTimeout(res, 20));
    await provisionTenant(tid2, userEmail, 'pro');
    await new Promise((res) => setTimeout(res, 20));
    await provisionTenant(tid3, userEmail, 'enterprise');

    // === POST ===
    const post = await hmacFetchPost('/api/users/by-email', { email: userEmail });
    const postBody = await readBody(post);
    expect(post.status, postBody.raw).toBe(200);
    const postData = postBody.json();
    expect(postData.found).toBe(true);
    expect(postData.user_email).toBe(userEmail);
    expect(Array.isArray(postData.workspaces)).toBe(true);
    expect(postData.workspaces.length).toBeGreaterThanOrEqual(3);

    const postWsIds = postData.workspaces.map((w: { workspace_id: string }) => w.workspace_id);
    expect(postWsIds).toContain(tid1);
    expect(postWsIds).toContain(tid2);
    expect(postWsIds).toContain(tid3);

    for (const w of postData.workspaces) {
      expect(w.workspace_id, 'workspace_id obligatoire').toBeTruthy();
      expect(w.role, 'role obligatoire').toBeTruthy();
      expect(w.magic_link_capable, 'Notifuse expose magic link → toujours true').toBe(true);
      expect(w.fallback_url, 'fallback_url URL signin').toMatch(/signin|notifuse/i);
    }

    // === GET (canonical Hub reconcile) ===
    const get = await hmacGetByEmail(userEmail);
    const getBody = await readBody(get);
    expect(get.status, getBody.raw).toBe(200);
    // Anti-regression bug prod 2026-05-25 : body NON vide meme sur GET
    expect(getBody.raw.length, 'GET body doit pas etre vide (bug prod cron reconcile)').toBeGreaterThan(0);

    const getData = getBody.json();
    expect(getData.found).toBe(true);
    expect(getData.user_email).toBe(userEmail);
    expect(Array.isArray(getData.workspaces)).toBe(true);
    expect(getData.workspaces.length).toBeGreaterThanOrEqual(3);

    const getWsIds = getData.workspaces.map((w: { workspace_id: string }) => w.workspace_id);
    expect(getWsIds).toContain(tid1);
    expect(getWsIds).toContain(tid2);
    expect(getWsIds).toContain(tid3);

    // === Symetrie stricte : POST et GET retournent les memes workspace_ids ===
    expect(getWsIds.sort()).toEqual(postWsIds.sort());
  });

  test('user inconnu : POST + GET symetriques 200 + found:false + body NON vide', async () => {
    const ghostEmail = `e2e-mega-04-ghost-${RUN_STAMP}@e2e.veridian.site`;

    const post = await hmacFetchPost('/api/users/by-email', { email: ghostEmail });
    const postBody = await readBody(post);
    expect(post.status, postBody.raw).toBe(200);
    const postData = postBody.json();
    expect(postData.found).toBe(false);
    expect(postData.user_email).toBe(ghostEmail);
    expect(postData.workspaces).toHaveLength(0);

    const get = await hmacGetByEmail(ghostEmail);
    const getBody = await readBody(get);
    expect(get.status, getBody.raw).toBe(200);
    // Bug prod 2026-05-25 : body vide sur user inconnu = cron Hub voyait
    // un 200 sans champ found → faux positif tenant_missing_app.
    expect(getBody.raw.length, 'ghost GET body doit pas etre vide (bug prod)').toBeGreaterThan(0);

    const getData = getBody.json();
    expect(getData.found).toBe(false);
    expect(getData.user_email).toBe(ghostEmail);
    expect(getData.workspaces).toHaveLength(0);
  });

  test('HMAC wrong secret : POST + GET refusent symetriques (401/403)', async () => {
    const raw = JSON.stringify({ email: 'whatever@e2e.veridian.site' });
    const ts = Date.now().toString();
    const wrongSig = crypto.createHmac('sha256', 'WRONG_SECRET').update(`${ts}.${raw}`).digest('hex');

    const post = await fetch(`${NOTIFUSE_URL}/api/users/by-email`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': wrongSig,
        'X-Veridian-Timestamp': ts,
      },
      body: raw,
    });
    expect([401, 403], `POST wrong secret got ${post.status}`).toContain(post.status);

    const wrongSigGet = crypto.createHmac('sha256', 'WRONG_SECRET').update(`${ts}.`).digest('hex');
    const get = await fetch(`${NOTIFUSE_URL}/api/users/by-email?email=whatever@e2e.veridian.site`, {
      method: 'GET',
      headers: {
        'X-Veridian-Hub-Signature': wrongSigGet,
        'X-Veridian-Timestamp': ts,
      },
    });
    expect([401, 403], `GET wrong secret got ${get.status}`).toContain(get.status);
  });

  test('HMAC drift > 5min : POST + GET refusent symetriques (401/403)', async () => {
    const stale = (Date.now() - 10 * 60 * 1000).toString();

    const raw = JSON.stringify({ email: 'stale@e2e.veridian.site' });
    const sigPost = crypto.createHmac('sha256', HUB_API_SECRET).update(`${stale}.${raw}`).digest('hex');
    const post = await fetch(`${NOTIFUSE_URL}/api/users/by-email`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': sigPost,
        'X-Veridian-Timestamp': stale,
      },
      body: raw,
    });
    expect([401, 403], `POST stale ts got ${post.status}`).toContain(post.status);

    const sigGet = crypto.createHmac('sha256', HUB_API_SECRET).update(`${stale}.`).digest('hex');
    const get = await fetch(`${NOTIFUSE_URL}/api/users/by-email?email=stale@e2e.veridian.site`, {
      method: 'GET',
      headers: {
        'X-Veridian-Hub-Signature': sigGet,
        'X-Veridian-Timestamp': stale,
      },
    });
    expect([401, 403], `GET stale ts got ${get.status}`).toContain(get.status);
  });

  test('email manquant / invalide : 400 symetrique POST + GET', async () => {
    // POST sans email
    const post1 = await hmacFetchPost('/api/users/by-email', {});
    const pb1 = await readBody(post1);
    expect(post1.status, pb1.raw).toBe(400);

    // POST email format invalide
    const post2 = await hmacFetchPost('/api/users/by-email', { email: 'not-an-email' });
    const pb2 = await readBody(post2);
    expect(post2.status, pb2.raw).toBe(400);

    // GET sans email query
    const { timestamp, signature } = signHMACGet();
    const get = await fetch(`${NOTIFUSE_URL}/api/users/by-email`, {
      method: 'GET',
      headers: {
        'X-Veridian-Hub-Signature': signature,
        'X-Veridian-Timestamp': timestamp,
      },
    });
    const gb = await readBody(get);
    expect(get.status, gb.raw).toBe(400);
    expect(gb.raw).toMatch(/email/i);
  });
});
