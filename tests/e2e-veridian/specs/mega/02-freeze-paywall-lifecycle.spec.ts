// === MEGA E2E — Spec 02 — Freeze/Unfreeze member lifecycle ===
//
// Contribution Notifuse au ticket Hub MEGA-E2E (Bucket B equivalent cross-app).
// Spec parent : ../veridian-hub/todo/2026-05-23-MEGA-E2E-post-commercialisation.md
//
// Couvre le cycle complet d un membre frozen/unfrozen vu cote Notifuse
// (le 402 user_frozen avec JWT user vivant est couvert par les unit tests
// middleware veridian_paywall_frozen_test.go — ici on focus sur les invariants
// contractuels HMAC visibles de l exterieur).
//
// Sequence (8 etapes) :
//   01. Provision + attach 2 members (alice + bob)
//   02. Freeze alice (HMAC, reason=quota_seat_exceeded) → 200 + frozen_at
//   03. Re-freeze alice meme args → 409 member_already_frozen (idempotent)
//   04. Freeze bob avec reason differente (manual) → 200 (independance)
//   05. Owner refuse freeze → 409 cannot_freeze_owner
//   06. Unfreeze alice → 200 + unfrozen_at
//   07. Re-freeze alice possible (etat reset) → 200 + frozen_at nouveau
//   08. Wipe → 200 cleanup propre

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

const RUN_STAMP = Date.now().toString(36).slice(-6);

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

async function readBody(r: Response): Promise<{ raw: string; json: () => any }> {
  const raw = await r.text();
  return { raw, json: () => (raw ? JSON.parse(raw) : null) };
}

async function provisionTenant(tenantId: string, ownerEmail: string) {
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
      plan: 'pro',
    });
    const body = await readBody(r);
    if (r.status === 200) return { ...body.json(), owner_email: ownerEmail };
    if ((r.status >= 500 || r.status === 404) && attempt < 4) {
      await new Promise((res) => setTimeout(res, 500 * Math.pow(2, attempt)));
      continue;
    }
    expect(r.status, body.raw).toBe(200);
  }
  throw new Error('provision retry exhausted');
}

async function attachMember(tenantId: string, memberEmail: string, hubUserId: string) {
  const r = await hmacFetch(`/api/tenants/${tenantId}/sync-member`, 'POST', {
    user_email: memberEmail,
    hub_user_id: hubUserId,
    role: 'member',
  });
  const body = await readBody(r);
  expect(r.status, body.raw).toBe(200);
  return body.json();
}

// describe.serial : 1 seul tenant traverse les 8 tests → afterAll au lieu de
// afterEach (sinon le wipe entre step 01 et 02 efface le tenant que step 02
// va chercher). Pattern hérité de saasification.spec.ts (cf. Lot N étape 1+2).
const provisioned: string[] = [];

test.afterAll(async () => {
  if (provisioned.length === 0) return;
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

// tids contraints a varchar(20) cote DB workspaces.id. `m02<6chars>` = 9 chars.
test.describe.serial('@mega @regression MEGA-02 — freeze/unfreeze lifecycle complet', () => {
  const tid = `m02${RUN_STAMP}`;
  const ownerEmail = `e2e-mega-02-owner-${RUN_STAMP}@e2e.veridian.site`;
  const aliceEmail = `e2e-mega-02-alice-${RUN_STAMP}@e2e.veridian.site`;
  const bobEmail = `e2e-mega-02-bob-${RUN_STAMP}@e2e.veridian.site`;
  const aliceHubId = `hub-u-m02-alice-${RUN_STAMP}`;
  const bobHubId = `hub-u-m02-bob-${RUN_STAMP}`;
  const ownerHubId = `hub-u-m02-owner-${RUN_STAMP}`;

  test('01. Provision + attach 2 members (alice + bob) → all 200', async () => {
    provisioned.push(tid);
    await provisionTenant(tid, ownerEmail);

    const a = await attachMember(tid, aliceEmail, aliceHubId);
    expect(a.workspace_id ?? a.tenant_id).toBeTruthy();

    const b = await attachMember(tid, bobEmail, bobHubId);
    expect(b.workspace_id ?? b.tenant_id).toBeTruthy();
  });

  test('02. Freeze alice (reason=quota_seat_exceeded) → 200 + frozen_at RFC3339', async () => {
    const r = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: aliceEmail,
      hub_user_id: aliceHubId,
      reason: 'quota_seat_exceeded',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();

    expect(data.tenant_id).toBe(tid);
    expect(data.user_email).toBe(aliceEmail);
    expect(data.reason).toBe('quota_seat_exceeded');
    expect(data.frozen_at).toBeTruthy();
    expect(data.frozen_at as string).toMatch(
      /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})/,
    );
  });

  test('03. Re-freeze alice meme args → 409 member_already_frozen (idempotent)', async () => {
    const r = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: aliceEmail,
      hub_user_id: aliceHubId,
      reason: 'quota_seat_exceeded',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(409);
    expect(body.json().code).toBe('member_already_frozen');
  });

  test('04. Freeze bob (reason=manual) → 200 (independance vs alice)', async () => {
    const r = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: bobEmail,
      hub_user_id: bobHubId,
      reason: 'manual',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();
    expect(data.user_email).toBe(bobEmail);
    expect(data.reason).toBe('manual');
    expect(data.frozen_at).toBeTruthy();
  });

  test('05. Freeze owner refuse → 409 cannot_freeze_owner', async () => {
    const r = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: ownerEmail,
      hub_user_id: ownerHubId,
      reason: 'manual',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(409);
    expect(body.json().code).toBe('cannot_freeze_owner');
  });

  test('06. Unfreeze alice → 200 + unfrozen_at RFC3339', async () => {
    const r = await hmacFetch(`/api/tenants/${tid}/unfreeze-member`, 'POST', {
      user_email: aliceEmail,
      hub_user_id: aliceHubId,
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();
    expect(data.tenant_id).toBe(tid);
    expect(data.unfrozen_at).toBeTruthy();
    expect(data.unfrozen_at as string).toMatch(
      /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})/,
    );
  });

  test('07. Re-freeze alice apres unfreeze → 200 (etat reset, frozen_at nouveau)', async () => {
    const r = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: aliceEmail,
      hub_user_id: aliceHubId,
      reason: 'manual',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    expect(body.json().frozen_at).toBeTruthy();
  });

  test('08. Wipe nettoie le tenant proprement (wipe atomic = all-or-nothing)', async () => {
    const r = await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
      tenant_ids: [tid],
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest'],
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    // Pop tracker pour eviter double-wipe afterEach
    const idx = provisioned.indexOf(tid);
    if (idx >= 0) provisioned.splice(idx, 1);
  });
});
