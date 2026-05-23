// === Veridian patch — membership cross-app mega-coverage (2026-05-23) ===
// Tests E2E des endpoints sync-member / remove-member / restore-member livres
// par Agent G (commit df1b920e) et de l'alias workspace-level attach-member
// livre par Agent I (commit e4cf5433).
//
// Endpoints couverts :
//   - POST /api/tenants/{id}/sync-member        (CONTRAT-HUB §5.18.3)
//   - POST /api/tenants/{id}/remove-member      (CONTRAT-HUB §5.19.2)
//   - POST /api/tenants/{id}/restore-member     (CONTRAT-HUB §5.20)
//   - POST /api/veridian/workspaces/{tenantId}/attach-member  (§5.22.2 alias)
//
// Spec parent : todo/2026-05-19-v13-multi-membre-cross-app.md
//              + todo/2026-05-21-contrat-hub-v15-sync.md (alias workspace-level)
//
// Conventions Lot N :
//   - tids prefixe `tst` + timestamp court (cleanup wipe-test-tenants)
//   - afterEach cleanup tous les tenants provisionnes
//   - Tag @regression sur tous les tests
//
// Note webhooks tenant.member_* : l'emission est code-livree (Agent G dans
// veridian_membership_service.go via s.emitter.Emit) mais la VERIFICATION
// reception cote Hub mock necessite MOCK_WEBHOOK_RECEIVER_URL +
// MOCK_WEBHOOK_SECRET (meme pattern que webhooks-lifecycle-emit.spec.ts) ce
// qui n'est pas dispo en CI staging par defaut. Tests gates pareil :
// test.skip si env absent.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';
import * as http from 'http';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;
const MOCK_WEBHOOK_RECEIVER_URL = process.env.MOCK_WEBHOOK_RECEIVER_URL;
const MOCK_WEBHOOK_SECRET = process.env.MOCK_WEBHOOK_SECRET ?? '';

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

// === HMAC helpers ==========================================================

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
  const ownerEmail = `${tenantId}@membership.test`;
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
      plan,
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

async function getTenantHealth(tenantId: string) {
  const r = await hmacFetch(`/api/tenants/${tenantId}/health`, 'GET');
  expect(r.status).toBe(200);
  return r.json();
}

// === Cleanup tracker =======================================================

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

// === sync-member ===========================================================

test.describe('@regression sync-member — POST /api/tenants/{id}/sync-member (§5.18.3)', () => {
  test('happy path : user inconnu → user cree + attache + 200 synced=true app_role=member', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const baselineHealth = await getTenantHealth(tid);
    const baseCount = baselineHealth.members_count;

    const inviteeEmail = `${tid}-sync-new@membership.test`;
    const r = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: inviteeEmail,
      hub_user_id: `hub-sync-${tid}`,
      role: 'member',
    });
    const raw = await r.text();
    expect(r.status, raw).toBe(200);
    const body = JSON.parse(raw);
    expect(body.synced).toBe(true);
    expect(body.tenant_id).toBe(tid);
    expect(body.user_email).toBe(inviteeEmail);
    expect(body.app_user_id).toBeTruthy();
    expect(body.app_role).toBe('member');

    // members_count s'est incremente.
    const after = await getTenantHealth(tid);
    expect(after.members_count).toBeGreaterThan(baseCount);
  });

  test('idempotent : 2e call memes params → 200 synced=true sans nouvel attach', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const inviteeEmail = `${tid}-sync-idem@membership.test`;
    const reqBody = {
      user_email: inviteeEmail,
      hub_user_id: `hub-sync-idem-${tid}`,
      role: 'member' as const,
    };

    // 1er sync → 200 + member ajoute.
    const r1 = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', reqBody);
    const raw1 = await r1.text();
    expect(r1.status, raw1).toBe(200);
    const b1 = JSON.parse(raw1);
    expect(b1.synced).toBe(true);
    expect(b1.app_role).toBe('member');
    const userIDFirst = b1.app_user_id;

    const midHealth = await getTenantHealth(tid);
    const midCount = midHealth.members_count;

    // 2e sync identique → 200, meme app_user_id, count inchange (additif).
    const r2 = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', reqBody);
    const raw2 = await r2.text();
    expect(r2.status, raw2).toBe(200);
    const b2 = JSON.parse(raw2);
    expect(b2.synced).toBe(true);
    expect(b2.app_user_id).toBe(userIDFirst);
    expect(b2.app_role).toBe('member');

    const afterHealth = await getTenantHealth(tid);
    expect(afterHealth.members_count).toBe(midCount);
  });

  test('owner kept no-downgrade : sync sur owner avec role=member → 200 app_role=owner', async () => {
    // Provision cree un owner avec owner_email. Si on sync ce meme email avec
    // role=member, le service DOIT garder le role owner (§5.18.3 additif, jamais
    // de downgrade silencieux). Verifie le contrat §5.22.4 souverainete locale.
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const prov = await provisionTenant(tid, 'free');

    const r = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: prov.owner_email,
      hub_user_id: `hub-owner-${tid}`,
      role: 'member',
    });
    const raw = await r.text();
    expect(r.status, raw).toBe(200);
    const body = JSON.parse(raw);
    expect(body.synced).toBe(true);
    // L'owner doit etre preserve : pas de downgrade.
    expect(body.app_role).toBe('owner');
  });

  test('role admin Hub → app_role=member (Notifuse n a pas de role admin)', async () => {
    // CONTRAT-HUB §3.5 : Hub non-autoritatif sur les roles internes app.
    // sync-member accepte member|admin (IsValid), mais Notifuse upstream n'a
    // que owner/member → tout user invite devient `member` cote app.
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const inviteeEmail = `${tid}-admin@membership.test`;
    const r = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: inviteeEmail,
      hub_user_id: `hub-admin-${tid}`,
      role: 'admin',
    });
    const raw = await r.text();
    expect(r.status, raw).toBe(200);
    const body = JSON.parse(raw);
    expect(body.synced).toBe(true);
    expect(body.app_role).toBe('member');
  });

  test('role invalide (superuser) → 400 invalid_role', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const r = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: 'x@membership.test',
      hub_user_id: 'hub-x',
      role: 'superuser',
    });
    expect(r.status).toBe(400);
    expect(await r.text()).toMatch(/invalid_role/);
  });

  test('role=owner refuse → 400 (owner = provision/transfer-owner uniquement)', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const r = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: 'x@membership.test',
      hub_user_id: 'hub-x',
      role: 'owner',
    });
    expect(r.status).toBe(400);
    expect(await r.text()).toMatch(/invalid_role/);
  });

  test('champs manquants → 400 invalid_payload', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    // user_email absent
    const r1 = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      hub_user_id: 'h-1',
      role: 'member',
    });
    expect(r1.status).toBe(400);

    // hub_user_id absent
    const r2 = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: 'x@membership.test',
      role: 'member',
    });
    expect(r2.status).toBe(400);

    // role absent
    const r3 = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: 'x@membership.test',
      hub_user_id: 'h-1',
    });
    expect(r3.status).toBe(400);
  });

  test('email malforme (sans @) → 422 invalid_payload', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const r = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: 'not-an-email',
      hub_user_id: 'h-1',
      role: 'member',
    });
    expect(r.status).toBe(422);
  });

  test('tenant inconnu → 404 tenant_not_found', async () => {
    const ghostTid = `tstghost${Date.now().toString(36).slice(-4)}`;
    // PAS de provisioned.push : ce tenant n'existe pas.

    const r = await hmacFetch(`/api/tenants/${ghostTid}/sync-member`, 'POST', {
      user_email: 'x@membership.test',
      hub_user_id: 'h-1',
      role: 'member',
    });
    expect(r.status).toBe(404);
    expect(await r.text()).toMatch(/tenant_not_found/);
  });

  test('HMAC invalide → 401/403', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const raw = JSON.stringify({
      user_email: 'x@membership.test',
      hub_user_id: 'h-1',
      role: 'member',
    });
    const ts = Date.now().toString();
    const wrongSig = crypto.createHmac('sha256', 'WRONG_SECRET').update(`${ts}.${raw}`).digest('hex');
    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/${tid}/sync-member`, {
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
});

// === remove-member =========================================================

test.describe('@regression remove-member — POST /api/tenants/{id}/remove-member (§5.19.2)', () => {
  test('happy path : sync puis remove member existant → 200 + members_count decremente', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    // Sync un membre d'abord.
    const inviteeEmail = `${tid}-remove@membership.test`;
    const syncResp = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: inviteeEmail,
      hub_user_id: `hub-rm-${tid}`,
      role: 'member',
    });
    expect(syncResp.status, await syncResp.text()).toBe(200);

    const beforeRemove = await getTenantHealth(tid);
    const beforeCount = beforeRemove.members_count;

    // Remove le membre.
    const r = await hmacFetch(`/api/tenants/${tid}/remove-member`, 'POST', {
      user_email: inviteeEmail,
      reason: 'admin_action',
    });
    const raw = await r.text();
    expect(r.status, raw).toBe(200);
    const body = JSON.parse(raw);
    expect(body.tenant_id).toBe(tid);
    expect(body.user_email).toBe(inviteeEmail);
    expect(body.removed_at).toBeTruthy();
    // RFC3339 format
    expect(body.removed_at as string).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/);

    const afterRemove = await getTenantHealth(tid);
    expect(afterRemove.members_count).toBe(beforeCount - 1);
  });

  test('refuse owner : remove sur owner du workspace → 409 cannot_remove_owner', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const prov = await provisionTenant(tid, 'free');

    const r = await hmacFetch(`/api/tenants/${tid}/remove-member`, 'POST', {
      user_email: prov.owner_email,
      reason: 'admin_action',
    });
    expect(r.status).toBe(409);
    const text = await r.text();
    expect(text).toMatch(/cannot_remove_owner/);
    // Le contrat donne un hint pointant vers transfer-owner.
    expect(text).toMatch(/transfer-owner/);
  });

  test('user non membre → 200 idempotent (rien a retirer)', async () => {
    // Spec service : user inconnu → short-circuit idempotent, ne sort PAS 404.
    // Verifie le contrat §5.19.2 idempotence.
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const r = await hmacFetch(`/api/tenants/${tid}/remove-member`, 'POST', {
      user_email: `ghost-${tid}@membership.test`,
    });
    const raw = await r.text();
    expect(r.status, raw).toBe(200);
    const body = JSON.parse(raw);
    expect(body.tenant_id).toBe(tid);
    expect(body.removed_at).toBeTruthy();
  });

  test('user_email manquant → 400 invalid_payload', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const r = await hmacFetch(`/api/tenants/${tid}/remove-member`, 'POST', {
      reason: 'admin_action',
    });
    expect(r.status).toBe(400);
  });

  test('tenant inconnu → 404 tenant_not_found', async () => {
    const ghostTid = `tstghost${Date.now().toString(36).slice(-4)}`;
    const r = await hmacFetch(`/api/tenants/${ghostTid}/remove-member`, 'POST', {
      user_email: 'x@membership.test',
    });
    expect(r.status).toBe(404);
    expect(await r.text()).toMatch(/tenant_not_found/);
  });

  test('HMAC invalide → 401/403', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const raw = JSON.stringify({ user_email: 'x@membership.test' });
    const ts = Date.now().toString();
    const wrongSig = crypto.createHmac('sha256', 'WRONG_SECRET').update(`${ts}.${raw}`).digest('hex');
    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/${tid}/remove-member`, {
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
});

// === restore-member ========================================================

test.describe('@regression restore-member — POST /api/tenants/{id}/restore-member (§5.20)', () => {
  test('happy path : remove puis restore → 200 + member re-attache role=member', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    // 1. Sync
    const inviteeEmail = `${tid}-restore@membership.test`;
    const sync = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: inviteeEmail,
      hub_user_id: `hub-restore-${tid}`,
      role: 'member',
    });
    expect(sync.status, await sync.text()).toBe(200);

    const afterSyncHealth = await getTenantHealth(tid);
    const afterSyncCount = afterSyncHealth.members_count;

    // 2. Remove
    const rm = await hmacFetch(`/api/tenants/${tid}/remove-member`, 'POST', {
      user_email: inviteeEmail,
    });
    expect(rm.status, await rm.text()).toBe(200);

    const afterRmHealth = await getTenantHealth(tid);
    expect(afterRmHealth.members_count).toBe(afterSyncCount - 1);

    // 3. Restore
    const r = await hmacFetch(`/api/tenants/${tid}/restore-member`, 'POST', {
      user_email: inviteeEmail,
    });
    const raw = await r.text();
    expect(r.status, raw).toBe(200);
    const body = JSON.parse(raw);
    expect(body.tenant_id).toBe(tid);
    expect(body.user_email).toBe(inviteeEmail);
    expect(body.restored_at).toBeTruthy();
    expect(body.restored_at as string).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/);

    // members_count revenu au niveau post-sync (re-attach reussi).
    const afterRestoreHealth = await getTenantHealth(tid);
    expect(afterRestoreHealth.members_count).toBe(afterSyncCount);
  });

  test('idempotent : restore sur membre actif → 200 sans effet', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const inviteeEmail = `${tid}-restore-idem@membership.test`;
    const sync = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: inviteeEmail,
      hub_user_id: `hub-restore-idem-${tid}`,
      role: 'member',
    });
    expect(sync.status, await sync.text()).toBe(200);

    const beforeHealth = await getTenantHealth(tid);
    const beforeCount = beforeHealth.members_count;

    // Restore sans remove prealable → idempotent.
    const r = await hmacFetch(`/api/tenants/${tid}/restore-member`, 'POST', {
      user_email: inviteeEmail,
    });
    expect(r.status, await r.text()).toBe(200);

    const afterHealth = await getTenantHealth(tid);
    expect(afterHealth.members_count).toBe(beforeCount);
  });

  test('restore sur user inconnu → 200 (defensive create) + attache role=member', async () => {
    // CONTRAT-HUB §5.20 : restore-member recree le user defensif s'il a ete
    // physiquement supprime entre-temps. Pas d'erreur si user inconnu.
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const baseHealth = await getTenantHealth(tid);
    const baseCount = baseHealth.members_count;

    const fakeEmail = `${tid}-never-existed@membership.test`;
    const r = await hmacFetch(`/api/tenants/${tid}/restore-member`, 'POST', {
      user_email: fakeEmail,
    });
    expect(r.status, await r.text()).toBe(200);

    // User cree defensivement + attache → count +1.
    const afterHealth = await getTenantHealth(tid);
    expect(afterHealth.members_count).toBe(baseCount + 1);
  });

  test('user_email manquant → 400 invalid_payload', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const r = await hmacFetch(`/api/tenants/${tid}/restore-member`, 'POST', {});
    expect(r.status).toBe(400);
  });

  test('tenant inconnu → 404 tenant_not_found', async () => {
    const ghostTid = `tstghost${Date.now().toString(36).slice(-4)}`;
    const r = await hmacFetch(`/api/tenants/${ghostTid}/restore-member`, 'POST', {
      user_email: 'x@membership.test',
    });
    expect(r.status).toBe(404);
    expect(await r.text()).toMatch(/tenant_not_found/);
  });

  test('HMAC invalide → 401/403', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const raw = JSON.stringify({ user_email: 'x@membership.test' });
    const ts = Date.now().toString();
    const wrongSig = crypto.createHmac('sha256', 'WRONG_SECRET').update(`${ts}.${raw}`).digest('hex');
    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/${tid}/restore-member`, {
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
});

// === attach-member workspace-level alias (§5.22.2) =========================

test.describe('@regression attach-member alias workspace-level — POST /api/veridian/workspaces/{tenantId}/attach-member', () => {
  test('alias workspace-level : meme handler que /api/tenants/{tenantId}/attach-member, idempotent', async () => {
    // Notifuse est mono-workspace (tenantId == workspaceId). L'alias delegue
    // au meme handler handleAttachMember (cf veridian_handler.go ligne 147).
    // On verifie : 1er call 201 + login_url + members_count++,
    //              2e call meme params 200 already_member=true.
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const baseHealth = await getTenantHealth(tid);
    const baseCount = baseHealth.members_count;

    const inviteeEmail = `${tid}-ws-alias@membership.test`;
    const body = {
      hub_user_id: `hub-alias-${tid}`,
      hub_user_email: inviteeEmail,
      role: 'member',
      invitation_id: `inv-alias-${tid}`,
    };

    // 1er attach via l'alias workspace-level.
    const r1 = await hmacFetch(`/api/veridian/workspaces/${tid}/attach-member`, 'POST', body);
    const raw1 = await r1.text();
    expect(r1.status, raw1).toBe(201);
    const b1 = JSON.parse(raw1);
    expect(b1.attached).toBe(true);
    expect(b1.already_member).toBe(false);
    expect(b1.workspace_id).toBe(tid);
    expect(b1.role).toBe('member');
    expect(b1.login_url).toMatch(/auto-login|magic|signin|code=/);

    const afterFirst = await getTenantHealth(tid);
    expect(afterFirst.members_count).toBeGreaterThan(baseCount);

    // 2e attach memes params via l'alias → 200 already_member=true.
    const r2 = await hmacFetch(`/api/veridian/workspaces/${tid}/attach-member`, 'POST', body);
    const raw2 = await r2.text();
    expect(r2.status, raw2).toBe(200);
    const b2 = JSON.parse(raw2);
    expect(b2.already_member).toBe(true);
    expect(b2.attached).toBe(true);
    expect(b2.workspace_id).toBe(tid);
    expect(b2.login_url).toBeTruthy();
  });

  test('equivalence : alias workspace-level === route tenant-level (semantique identique)', async () => {
    // Verifie que les 2 routes delegent au meme handler. Strategie :
    //   - tenant A : attach via /api/tenants/{tid}/attach-member
    //   - tenant B : attach via /api/veridian/workspaces/{tid}/attach-member
    //   - les 2 reponses ont la meme shape (attached, already_member, role,
    //     workspace_id, login_url) + meme status code.
    const tidA = `tst${Date.now().toString(36).slice(-6)}a`;
    const tidB = `tst${Date.now().toString(36).slice(-6)}b`;
    provisioned.push(tidA, tidB);
    await provisionTenant(tidA, 'free');
    await provisionTenant(tidB, 'free');

    const baseBody = {
      role: 'member',
    };

    // Tenant A : route tenant-level
    const rA = await hmacFetch(`/api/tenants/${tidA}/attach-member`, 'POST', {
      ...baseBody,
      hub_user_id: `hub-a-${tidA}`,
      hub_user_email: `${tidA}-A@membership.test`,
      invitation_id: `inv-a-${tidA}`,
    });
    const rawA = await rA.text();
    expect(rA.status, rawA).toBe(201);
    const bA = JSON.parse(rawA);

    // Tenant B : route workspace-level alias
    const rB = await hmacFetch(`/api/veridian/workspaces/${tidB}/attach-member`, 'POST', {
      ...baseBody,
      hub_user_id: `hub-b-${tidB}`,
      hub_user_email: `${tidB}-B@membership.test`,
      invitation_id: `inv-b-${tidB}`,
    });
    const rawB = await rB.text();
    expect(rB.status, rawB).toBe(201);
    const bB = JSON.parse(rawB);

    // Memes champs presents, memes valeurs structurelles.
    expect(Object.keys(bA).sort()).toEqual(Object.keys(bB).sort());
    expect(bA.attached).toBe(bB.attached);
    expect(bA.already_member).toBe(bB.already_member);
    expect(bA.role).toBe(bB.role);
    expect(typeof bA.login_url).toBe(typeof bB.login_url);
    // workspace_id renvoie le tenant id correspondant a chaque route.
    expect(bA.workspace_id).toBe(tidA);
    expect(bB.workspace_id).toBe(tidB);
  });

  test('alias : tenant inconnu → 404 tenant_not_found', async () => {
    const ghostTid = `tstghost${Date.now().toString(36).slice(-4)}`;
    const r = await hmacFetch(`/api/veridian/workspaces/${ghostTid}/attach-member`, 'POST', {
      hub_user_id: 'ghost',
      hub_user_email: 'ghost@membership.test',
      role: 'member',
      invitation_id: 'inv-ghost',
    });
    expect(r.status).toBe(404);
    expect(await r.text()).toMatch(/tenant_not_found/);
  });

  test('alias : HMAC invalide → 401/403', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const raw = JSON.stringify({
      hub_user_id: 'x',
      hub_user_email: 'x@membership.test',
      role: 'member',
    });
    const ts = Date.now().toString();
    const wrongSig = crypto.createHmac('sha256', 'WRONG_SECRET').update(`${ts}.${raw}`).digest('hex');
    const r = await fetch(`${NOTIFUSE_URL}/api/veridian/workspaces/${tid}/attach-member`, {
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
});

// === Webhooks emis vers Hub (best-effort, gated sur mock receiver) =========
//
// Ces tests verifient que Notifuse emet bien tenant.member_added /
// tenant.member_removed apres sync/restore/remove. L'emitter est livre cote
// service (veridian_membership_service.go : s.emitter.Emit), donc les events
// sont push vers HUB_WEBHOOK_URL. Pour les VERIFIER en E2E il faut un mock
// receiver HTTP local — meme pattern que webhooks-lifecycle-emit.spec.ts.
// En CI staging par defaut MOCK_WEBHOOK_RECEIVER_URL est absent, donc skip.

interface ReceivedWebhook {
  eventType: string;
  tenantId: string;
  payload: Record<string, unknown>;
  signatureValid: boolean;
  rawBody: string;
}

const receivedWebhooks: ReceivedWebhook[] = [];
let mockServer: http.Server | null = null;

function startMockReceiver(): Promise<{ port: number }> {
  return new Promise((resolve, reject) => {
    mockServer = http.createServer((req, res) => {
      let raw = '';
      req.on('data', (chunk) => {
        raw += chunk.toString();
      });
      req.on('end', () => {
        try {
          const ts = req.headers['x-veridian-timestamp'] as string;
          const sigReceived = req.headers['x-veridian-notifuse-signature'] as string;
          let sigValid = false;
          if (MOCK_WEBHOOK_SECRET && ts && sigReceived) {
            const expected = crypto
              .createHmac('sha256', MOCK_WEBHOOK_SECRET)
              .update(`${ts}.${raw}`)
              .digest('hex');
            sigValid = expected === sigReceived;
          }
          const parsed = JSON.parse(raw);
          receivedWebhooks.push({
            eventType: parsed.event_type ?? parsed.event ?? '',
            tenantId: parsed.tenant_id ?? '',
            payload: parsed.data ?? {},
            signatureValid: sigValid,
            rawBody: raw,
          });
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end('{"ok":true}');
        } catch (err) {
          res.writeHead(400);
          res.end(`bad request: ${err}`);
        }
      });
    });
    mockServer.on('error', reject);
    mockServer.listen(0, () => {
      const addr = mockServer!.address();
      if (typeof addr === 'object' && addr) {
        resolve({ port: addr.port });
      } else {
        reject(new Error('unable to bind mock receiver'));
      }
    });
  });
}

function stopMockReceiver(): Promise<void> {
  return new Promise((resolve) => {
    if (!mockServer) return resolve();
    mockServer.close(() => resolve());
    mockServer = null;
  });
}

function waitForEvent(eventType: string, tenantId: string, timeoutMs = 8000): Promise<ReceivedWebhook> {
  return new Promise((resolve, reject) => {
    const start = Date.now();
    const check = () => {
      const hit = receivedWebhooks.find((w) => w.eventType === eventType && w.tenantId === tenantId);
      if (hit) return resolve(hit);
      if (Date.now() - start > timeoutMs) {
        return reject(new Error(`webhook ${eventType} for ${tenantId} not received within ${timeoutMs}ms`));
      }
      setTimeout(check, 200);
    };
    check();
  });
}

test.describe('@regression membership webhooks — events tenant.member_* emis vers Hub mock', () => {
  test.beforeAll(async () => {
    test.skip(
      !MOCK_WEBHOOK_RECEIVER_URL,
      'MOCK_WEBHOOK_RECEIVER_URL not set. To run : lancer Notifuse local avec ' +
        'HUB_WEBHOOK_URL=http://localhost:PORT/webhook puis exporter ' +
        'MOCK_WEBHOOK_RECEIVER_URL=http://localhost:PORT et MOCK_WEBHOOK_SECRET=<meme secret>.',
    );
    await startMockReceiver();
  });

  test.afterAll(async () => {
    await stopMockReceiver();
  });

  test.afterEach(() => {
    receivedWebhooks.length = 0;
  });

  test('sync-member happy path → emit tenant.member_added', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const inviteeEmail = `${tid}-wh-add@membership.test`;
    const sync = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: inviteeEmail,
      hub_user_id: `hub-wh-${tid}`,
      role: 'member',
    });
    expect(sync.status, await sync.text()).toBe(200);

    const hook = await waitForEvent('tenant.member_added', tid);
    expect(hook.signatureValid).toBe(true);
    expect(hook.payload.user_email).toBe(inviteeEmail);
    expect(hook.payload.role).toBe('member');
    expect(hook.payload.hub_user_id).toBe(`hub-wh-${tid}`);
    expect(hook.payload.app_user_id).toBeTruthy();
    expect(hook.payload.actor).toBe('hub');
  });

  test('remove-member happy path → emit tenant.member_removed', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const inviteeEmail = `${tid}-wh-rm@membership.test`;
    // Sync d'abord
    const sync = await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: inviteeEmail,
      hub_user_id: `hub-wh-rm-${tid}`,
      role: 'member',
    });
    expect(sync.status, await sync.text()).toBe(200);
    await waitForEvent('tenant.member_added', tid);

    // Remove
    const rm = await hmacFetch(`/api/tenants/${tid}/remove-member`, 'POST', {
      user_email: inviteeEmail,
      reason: 'admin_action',
    });
    expect(rm.status, await rm.text()).toBe(200);

    const hook = await waitForEvent('tenant.member_removed', tid);
    expect(hook.signatureValid).toBe(true);
    expect(hook.payload.user_email).toBe(inviteeEmail);
    expect(hook.payload.reason).toBe('admin_action');
    expect(hook.payload.app_user_id).toBeTruthy();
    expect(hook.payload.actor).toBe('hub');
  });

  test('restore-member happy path → emit tenant.member_added avec restored=true', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const inviteeEmail = `${tid}-wh-restore@membership.test`;
    // Sync puis remove pour avoir un soft-soft scenario
    await hmacFetch(`/api/tenants/${tid}/sync-member`, 'POST', {
      user_email: inviteeEmail,
      hub_user_id: `hub-wh-restore-${tid}`,
      role: 'member',
    });
    await waitForEvent('tenant.member_added', tid);
    await hmacFetch(`/api/tenants/${tid}/remove-member`, 'POST', {
      user_email: inviteeEmail,
    });
    await waitForEvent('tenant.member_removed', tid);
    receivedWebhooks.length = 0; // reset pour ne capter QUE le restore event

    // Restore
    const r = await hmacFetch(`/api/tenants/${tid}/restore-member`, 'POST', {
      user_email: inviteeEmail,
    });
    expect(r.status, await r.text()).toBe(200);

    const hook = await waitForEvent('tenant.member_added', tid);
    expect(hook.signatureValid).toBe(true);
    expect(hook.payload.user_email).toBe(inviteeEmail);
    expect(hook.payload.role).toBe('member');
    expect(hook.payload.restored).toBe(true);
    expect(hook.payload.actor).toBe('hub');
  });
});
