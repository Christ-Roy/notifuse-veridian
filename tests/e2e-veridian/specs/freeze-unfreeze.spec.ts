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

// readBody lit le body UNE seule fois en string et retourne {raw, json}.
// Le body fetch est un stream non-rewindable : appeler r.text() puis r.json()
// crash avec "Body is unusable: Body has already been read". Ce helper permet
// d'utiliser le raw comme message d'assertion ET de parser le JSON après.
async function readBody(r: Response): Promise<{ raw: string; json: () => any }> {
  const raw = await r.text();
  return { raw, json: () => (raw ? JSON.parse(raw) : null) };
}

async function provisionTenant(tenantId: string) {
  const ownerEmail = `${tenantId}@freeze.test`;
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
      plan: 'free',
    });
    const body = await readBody(r);
    if (r.status === 200) {
      return { ...body.json(), owner_email: ownerEmail };
    }
    if ((r.status >= 500 || r.status === 404) && attempt < 4) {
      await new Promise((res) => setTimeout(res, 500 * Math.pow(2, attempt)));
      continue;
    }
    expect(r.status, body.raw).toBe(200);
  }
  throw new Error('provision retry exhausted');
}

async function attachMember(tenantId: string, memberEmail: string) {
  const r = await hmacFetch(`/api/tenants/${tenantId}/sync-member`, 'POST', {
    user_email: memberEmail,
    hub_user_id: `hub-u-${memberEmail.split('@')[0]}`,
    role: 'member',
  });
  const body = await readBody(r);
  expect(r.status, body.raw).toBe(200);
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
    const b1 = await readBody(r1);
    expect(r1.status, b1.raw).toBe(200);
    const body1 = b1.json();
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
    const b2 = await readBody(r2);
    expect(r2.status, b2.raw).toBe(409);
    expect(b2.json().code).toBe('member_already_frozen');

    // (3) Unfreeze → 200, restoration flow nominal.
    const r3 = await hmacFetch(`/api/tenants/${tid}/unfreeze-member`, 'POST', {
      user_email: memberEmail,
      hub_user_id: `hub-u-${tid}-bob`,
    });
    const b3 = await readBody(r3);
    expect(r3.status, b3.raw).toBe(200);
    const body3 = b3.json();
    expect(body3.tenant_id).toBe(tid);
    expect(body3.unfrozen_at).toBeTruthy();

    // (4) Apres unfreeze, re-freeze doit a nouveau passer en 200 (etat reset).
    const r4 = await hmacFetch(`/api/tenants/${tid}/freeze-member`, 'POST', {
      user_email: memberEmail,
      hub_user_id: `hub-u-${tid}-bob`,
      reason: 'manual',
    });
    const b4 = await readBody(r4);
    expect(r4.status, b4.raw).toBe(200);
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
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(409);
    expect(body.json().code).toBe('cannot_freeze_owner');
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
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(404);
    expect(body.json().code).toBe('user_not_member');
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
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(404);
    expect(body.json().code).toBe('tenant_not_found');
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
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(400);
    expect(body.json().code).toBe('invalid_payload');
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
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(400);
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
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
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
