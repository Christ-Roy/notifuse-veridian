// === Veridian patch — Freeze member per-user E2E (CONTRAT-HUB §5.21) ===
// Tests E2E des endpoints freeze-member / unfreeze-member (2026-05-25).
//
// Endpoints couverts :
//   - POST /api/tenants/{tenantId}/freeze-member    (CONTRAT-HUB §5.21)
//   - POST /api/tenants/{tenantId}/unfreeze-member  (CONTRAT-HUB §5.21)
//
// Spec parent : todo/2026-05-23-membership-freeze-per-user.md
//
// Cas testes :
//   1. HMAC valide + body bien forme → freeze 200, unfreeze 200
//   2. Idempotence : 2eme freeze → 409 member_already_frozen
//   3. Owner refuse : freeze owner → 409 cannot_freeze_owner
//   4. User pas membre → 404 user_not_member
//   5. Tenant absent → 404 tenant_not_found
//   6. HMAC invalide → 401
//   7. Validation : missing user_email → 400
//
// Note middleware paywall : le test 402 user_frozen + reads obfusques exigerait
// un JWT user signe + un endpoint upstream non-tenants/. Couvert par tests
// unitaires middleware (veridian_paywall_frozen_test.go) — ici on focus sur les
// endpoints HMAC freeze/unfreeze cote contrat Hub.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

// === HMAC helpers ===========================================================

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

async function provisionTenant(tenantId: string) {
  const ownerEmail = `${tenantId}@freeze.test`;
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
      plan: 'free',
    });
    if (r.status === 200) {
      const body = await r.json();
      return { ...body, owner_email: ownerEmail };
    }
    if ((r.status >= 500 || r.status === 404) && attempt < 4) {
      await new Promise((res) => setTimeout(res, 500 * Math.pow(2, attempt)));
      continue;
    }
    expect(r.status, await r.text()).toBe(200);
  }
  throw new Error('provision retry exhausted');
}

async function attachMember(tenantId: string, memberEmail: string) {
  const r = await hmacFetch(`/api/tenants/${tenantId}/sync-member`, 'POST', {
    user_email: memberEmail,
    hub_user_id: `hub-u-${memberEmail.split('@')[0]}`,
    role: 'member',
  });
  expect(r.status, await r.text()).toBe(200);
}

// === Cleanup tracker ========================================================

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

// === freeze-member happy path + idempotent ==================================

test.describe('@regression freeze-member — POST /api/tenants/{tenantId}/freeze-member (§5.21)', () => {
  test('happy path : freeze member 200 + idempotent 2eme freeze 409 + unfreeze 200', async () => {
    const tid = `tstfr${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid);

    const memberEmail = `${tid}-bob@freeze.test`;
    await attachMember(tid, memberEmail);

    // (1) Freeze nouveau → 200, alreadyFrozen=false implicite (200 vs 409).
    const r1 = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: memberEmail,
      hub_user_id: `hub-u-${tid}-bob`,
      reason: 'quota_seat_exceeded',
    });
    expect(r1.status, await r1.text()).toBe(200);
    const body1 = await r1.json();
    expect(body1.tenant_id).toBe(tid);
    expect(body1.user_email).toBe(memberEmail);
    expect(body1.reason).toBe('quota_seat_exceeded');
    expect(body1.frozen_at).toBeTruthy();

    // (2) Replay (memes args) → 409 member_already_frozen idempotent.
    const r2 = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: memberEmail,
      hub_user_id: `hub-u-${tid}-bob`,
      reason: 'quota_seat_exceeded',
    });
    expect(r2.status, await r2.text()).toBe(409);
    const body2 = await r2.json();
    expect(body2.code).toBe('member_already_frozen');

    // (3) Unfreeze → 200, restoration flow nominal.
    const r3 = await hmacFetch(`/api/tenants/${tid}/unfreeze-member`, 'POST', {
      user_email: memberEmail,
      hub_user_id: `hub-u-${tid}-bob`,
    });
    expect(r3.status, await r3.text()).toBe(200);
    const body3 = await r3.json();
    expect(body3.tenant_id).toBe(tid);
    expect(body3.unfrozen_at).toBeTruthy();

    // (4) Apres unfreeze, re-freeze doit a nouveau passer en 200 (etat reset).
    const r4 = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: memberEmail,
      hub_user_id: `hub-u-${tid}-bob`,
      reason: 'manual',
    });
    expect(r4.status, await r4.text()).toBe(200);
  });

  test('owner refuse : freeze owner → 409 cannot_freeze_owner', async () => {
    const tid = `tstfo${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { owner_email } = await provisionTenant(tid);

    const r = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: owner_email,
      hub_user_id: `hub-u-owner-${tid}`,
      reason: 'manual',
    });
    expect(r.status, await r.text()).toBe(409);
    const body = await r.json();
    expect(body.code).toBe('cannot_freeze_owner');
  });

  test('user pas membre du workspace → 404 user_not_member', async () => {
    const tid = `tstnm${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid);

    const r = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: `ghost-${tid}@freeze.test`,
      hub_user_id: 'hub-u-ghost',
      reason: 'manual',
    });
    expect(r.status, await r.text()).toBe(404);
    const body = await r.json();
    expect(body.code).toBe('user_not_member');
  });

  test('tenant inexistant → 404 tenant_not_found', async () => {
    // Pas de provisioned.push : on ne provisionne pas.
    const ghostTid = `tstnotfound${Date.now().toString(36).slice(-6)}`;

    // On doit avoir un email user existant pour passer le check user en amont
    // du check workspace cote service. Provisionne un OTHER tenant juste pour
    // creer un user qu'on referencera.
    const probeTid = `tstprobe${Date.now().toString(36).slice(-6)}`;
    provisioned.push(probeTid);
    const { owner_email } = await provisionTenant(probeTid);

    const r = await hmacFetch(`/api/tenants/${ghostTid}/freeze-member`, 'POST', {
      user_email: owner_email,
      hub_user_id: 'hub-u-anyone',
      reason: 'manual',
    });
    expect(r.status, await r.text()).toBe(404);
    const body = await r.json();
    expect(body.code).toBe('tenant_not_found');
  });

  test('HMAC invalide → 401', async () => {
    const tid = `tsthmac${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid);

    // Signature avec mauvais secret → middleware HMAC rejette.
    const body = JSON.stringify({
      user_email: 'whatever@x.test',
      hub_user_id: 'u-1',
      reason: 'manual',
    });
    const ts = Date.now().toString();
    const badSig = crypto.createHmac('sha256', 'wrong-secret-32-bytes-ahahahaha').update(`${ts}.${body}`).digest('hex');

    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/${tid}/freeze-member`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': badSig,
        'X-Veridian-Timestamp': ts,
      },
      body,
    });
    expect(r.status).toBe(401);
  });

  test('validation : missing user_email → 400', async () => {
    const tid = `tstval${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid);

    const r = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      hub_user_id: 'u-1',
      reason: 'manual',
    });
    expect(r.status, await r.text()).toBe(400);
    const body = await r.json();
    expect(body.code).toBe('invalid_payload');
  });

  test('validation : reason inconnue → 400', async () => {
    const tid = `tstinvr${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { owner_email } = await provisionTenant(tid);

    const r = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: owner_email,
      hub_user_id: 'u-1',
      reason: 'invalid_reason_zzz',
    });
    expect(r.status, await r.text()).toBe(400);
  });
});

// === unfreeze-member ========================================================

test.describe('@regression unfreeze-member — POST /api/tenants/{tenantId}/unfreeze-member (§5.21)', () => {
  test('idempotent : unfreeze user pas frozen → 200 (replay safe)', async () => {
    const tid = `tstui${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid);

    const memberEmail = `${tid}-carol@freeze.test`;
    await attachMember(tid, memberEmail);

    // Unfreeze sur un user qui n'a JAMAIS ete frozen → 200 idempotent
    // (pas de webhook tenant.member_unfrozen emit cote service, mais 200 OK
    // cote handler — pas de 404 surprise).
    const r = await hmacFetch(`/api/tenants/${tid}/unfreeze-member`, 'POST', {
      user_email: memberEmail,
      hub_user_id: `hub-u-${tid}-carol`,
    });
    expect(r.status, await r.text()).toBe(200);
  });

  test('HMAC invalide → 401', async () => {
    const tid = `tstuhmac${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid);

    const body = JSON.stringify({
      user_email: 'whatever@x.test',
      hub_user_id: 'u-1',
    });
    const ts = Date.now().toString();
    const badSig = crypto.createHmac('sha256', 'still-wrong-secret-padding').update(`${ts}.${body}`).digest('hex');

    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/${tid}/unfreeze-member`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': badSig,
        'X-Veridian-Timestamp': ts,
      },
      body,
    });
    expect(r.status).toBe(401);
  });
});
