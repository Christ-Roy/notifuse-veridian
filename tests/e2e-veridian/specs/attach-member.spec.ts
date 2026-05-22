// === Veridian patch — Lot R (2026-05-21) ===
// Tests E2E du endpoint POST /api/tenants/{tenantId}/attach-member.
//
// Le Hub appelle ce endpoint apres acceptation d'une invitation cross-app
// pour attacher un user au workspace Notifuse correspondant. Idempotent,
// re-call avec memes params = 200 already_member=true.
//
// Spec parent : todo/done/2026-05-21-hub-attach-member-endpoint.md
// Handler     : internal/http/veridian_handler.go:handleAttachMember
//
// Conventions Lot N :
//   - tids prefixe `tst` + timestamp court
//   - afterEach cleanup via /api/veridian/admin/wipe-test-tenants
//   - Tag @regression sur tous les tests

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

async function provisionTenant(tenantId: string, plan = 'free') {
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: `${tenantId}@attach-member.test`,
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

async function getTenantHealth(tenantId: string) {
  const r = await hmacFetch(`/api/tenants/${tenantId}/health`, 'GET');
  expect(r.status).toBe(200);
  return r.json();
}

// === Cleanup tracker ===
const provisioned: string[] = [];

test.afterEach(async () => {
  if (provisioned.length === 0) return;
  // Best-effort cleanup : on log warn si fail mais on ne fait pas planter
  // la suite (sinon un wipe transient masque l'echec du test reel).
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

test.describe('@regression attach-member — endpoint POST /api/tenants/{tenantId}/attach-member', () => {
  test('happy path : provision puis attach-member user X → 201 + login_url + user dans members_count', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    // Etat initial : 2 membres (owner humain + api_key technique)
    let health = await getTenantHealth(tid);
    expect(health.members_count).toBeGreaterThanOrEqual(2);
    const baselineMembers = health.members_count;

    // Attach un nouveau membre via Hub
    const inviteeID = `hub-user-${tid}`;
    const inviteeEmail = `${tid}-invitee@attach-member.test`;
    const attachResp = await hmacFetch(`/api/tenants/${tid}/attach-member`, 'POST', {
      hub_user_id: inviteeID,
      hub_user_email: inviteeEmail,
      role: 'member',
      invitation_id: `inv-${tid}`,
    });
    // Lire le body une seule fois (.text() puis .json() = "Body already read").
    const attachRaw = await attachResp.text();
    expect(attachResp.status, attachRaw).toBe(201);
    const attachBody = JSON.parse(attachRaw);
    expect(attachBody.attached).toBe(true);
    expect(attachBody.already_member).toBe(false);
    expect(attachBody.workspace_id).toBe(tid);
    expect(attachBody.role).toBe('member');
    expect(attachBody.login_url).toMatch(/auto-login|magic|signin|code=/);

    // Verification post-attach : members_count s'est incremente
    health = await getTenantHealth(tid);
    expect(health.members_count).toBeGreaterThan(baselineMembers);
  });

  test('idempotence : re-attach memes params → 200 already_member=true', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const inviteeID = `hub-user-idem-${tid}`;
    const inviteeEmail = `${tid}-idem@attach-member.test`;
    const body = {
      hub_user_id: inviteeID,
      hub_user_email: inviteeEmail,
      role: 'member',
      invitation_id: `inv-idem-${tid}`,
    };

    // 1er attach → 201
    const r1 = await hmacFetch(`/api/tenants/${tid}/attach-member`, 'POST', body);
    const raw1 = await r1.text();
    expect(r1.status, raw1).toBe(201);
    const b1 = JSON.parse(raw1);
    expect(b1.already_member).toBe(false);

    // 2e attach memes params → 200 already_member=true
    const r2 = await hmacFetch(`/api/tenants/${tid}/attach-member`, 'POST', body);
    const raw2 = await r2.text();
    expect(r2.status, raw2).toBe(200);
    const b2 = JSON.parse(raw2);
    expect(b2.already_member).toBe(true);
    expect(b2.attached).toBe(true);
    expect(b2.workspace_id).toBe(tid);
    expect(b2.role).toBe('member');
    expect(b2.login_url).toBeTruthy(); // toujours un login_url frais
  });

  test('role Hub admin → mappé member (Notifuse n a pas de role admin)', async () => {
    // Notifuse upstream n'a QUE 2 roles workspace : owner et member.
    // Le Hub envoie owner|admin|member ; attach-member mappe TOUT invité
    // vers 'member' (CONTRAT-HUB §3.5 : le Hub n'est pas autoritatif sur
    // les rôles internes app). Un 2e attach du même user = idempotent.
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const inviteeID = `hub-user-role-${tid}`;
    const inviteeEmail = `${tid}-role@attach-member.test`;

    // 1er attach role=member → 201, mappé member
    const r1 = await hmacFetch(`/api/tenants/${tid}/attach-member`, 'POST', {
      hub_user_id: inviteeID,
      hub_user_email: inviteeEmail,
      role: 'member',
      invitation_id: `inv-role-1-${tid}`,
    });
    const raw1 = await r1.text();
    expect(r1.status, raw1).toBe(201);
    expect(JSON.parse(raw1).role).toBe('member');

    // 2e attach même user role=admin → 200 already_member, toujours mappé member
    // (admin Hub n'existe pas côté Notifuse — pas d'UPDATE, idempotent).
    const r2 = await hmacFetch(`/api/tenants/${tid}/attach-member`, 'POST', {
      hub_user_id: inviteeID,
      hub_user_email: inviteeEmail,
      role: 'admin',
      invitation_id: `inv-role-2-${tid}`,
    });
    const raw2 = await r2.text();
    expect(r2.status, raw2).toBe(200);
    const b2 = JSON.parse(raw2);
    expect(b2.already_member).toBe(true);
    expect(b2.role).toBe('member');
  });

  test('tenant inconnu : 404', async () => {
    const ghostTid = `tstghost${Date.now().toString(36).slice(-4)}`;
    // PAS de provisioned.push → ce tenant n'existe pas, rien a wiper.

    const r = await hmacFetch(`/api/tenants/${ghostTid}/attach-member`, 'POST', {
      hub_user_id: 'ghost-user',
      hub_user_email: 'ghost@attach-member.test',
      role: 'member',
      invitation_id: 'inv-ghost',
    });
    expect(r.status).toBe(404);
    const body = await r.json();
    // Le contrat retourne error_code "tenant_not_found"
    expect(JSON.stringify(body)).toMatch(/tenant_not_found|tenant not found/i);
  });

  test('HMAC invalide : 401', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    // Forge une signature wrong (HMAC avec secret bidon)
    const raw = JSON.stringify({
      hub_user_id: 'x',
      hub_user_email: 'x@attach-member.test',
      role: 'member',
    });
    const ts = Date.now().toString();
    const wrongSig = crypto.createHmac('sha256', 'WRONG_SECRET').update(`${ts}.${raw}`).digest('hex');

    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/${tid}/attach-member`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': wrongSig,
        'X-Veridian-Timestamp': ts,
      },
      body: raw,
    });
    // 401 ou 403 selon middleware HMAC
    expect([401, 403]).toContain(r.status);
  });

  test('HMAC drift > 5min : 401', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    // Timestamp 10 minutes dans le passe (MaxClockDrift = 5 min cote middleware)
    const stale = (Date.now() - 10 * 60 * 1000).toString();
    const raw = JSON.stringify({
      hub_user_id: 'drift-user',
      hub_user_email: 'drift@attach-member.test',
      role: 'member',
    });
    const sig = crypto.createHmac('sha256', HUB_API_SECRET).update(`${stale}.${raw}`).digest('hex');

    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/${tid}/attach-member`, {
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

  test('body invalide / champs manquants : 400', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    // Cas 1 : role absent
    const r1 = await hmacFetch(`/api/tenants/${tid}/attach-member`, 'POST', {
      hub_user_id: 'x',
      hub_user_email: 'x@attach-member.test',
      // role: manquant
    });
    expect(r1.status).toBe(400);

    // Cas 2 : role invalide (hors enum owner|admin|member)
    const r2 = await hmacFetch(`/api/tenants/${tid}/attach-member`, 'POST', {
      hub_user_id: 'x',
      hub_user_email: 'x@attach-member.test',
      role: 'superuser', // pas dans l'enum
    });
    expect(r2.status).toBe(400);
  });
});
