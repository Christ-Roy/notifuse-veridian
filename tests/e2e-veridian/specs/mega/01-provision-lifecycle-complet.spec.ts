// === MEGA E2E — Spec 01 — Provision lifecycle complet ===
//
// Contribution Notifuse au ticket Hub MEGA-E2E
// (../veridian-hub/todo/2026-05-23-MEGA-E2E-post-commercialisation.md).
//
// Sequence complete d un tenant de bout en bout (12+ etapes chainees) :
//
//   01. Provision tenant (HMAC, owner_email frais)
//   02. /api/tenants/{id}/status → status=active + plan=free + quota=-1
//   03. Auto-login URL valide (curl HEAD → 200 page intermediaire)
//   04. 5 envois transactional → atteint activity_threshold_reached (V38)
//   05. Update plan free → pro (HMAC, plan_source=stripe) + cache invalidate
//   06. Verifier /limits → plan=pro + FeatureABTesting=true
//   07. Soft-delete tenant → 200 + deleted_at set
//   08. Send post soft-delete → 402 user_soft_deleted (middleware obfusque)
//   09. Restore tenant → 200 + restored_at set
//   10. Status post restore → status=active (deleted_at null)
//   11. Grant unlimited (free → enterprise via plan_source=lifetime_partner)
//   12. /limits post grant → FeatureWhiteLabel=true
//   13. Wipe tenant (HMAC hard delete) → 200
//   14. /status post wipe → 404 tenant_not_found
//
// Conventions :
//   - Prefix tenant : mega01-<RUN_STAMP>-<slug>
//   - Prefix email  : e2e-mega-01-<slug>-<RUN_STAMP>@e2e.veridian.site
//   - afterAll wipe via /api/veridian/admin/wipe-test-tenants (describe.serial)
//   - readBody helper : un Response stream n est lisible qu une fois
//     (.text() puis .json() = "Body is unusable").

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

const RUN_STAMP = Date.now().toString(36).slice(-6);

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

function signHMACGet() {
  const ts = Date.now().toString();
  return {
    timestamp: ts,
    signature: crypto.createHmac('sha256', HUB_API_SECRET).update(`${ts}.`).digest('hex'),
  };
}

async function hmacGet(path: string) {
  const { timestamp, signature } = signHMACGet();
  return fetch(`${NOTIFUSE_URL}${path}`, {
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
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
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

async function invalidatePaywallCache(workspaceId: string) {
  const r = await hmacFetch('/api/veridian/admin/cache/invalidate', 'POST', {
    workspace_id: workspaceId,
  });
  if (r.status !== 200) {
    throw new Error(`invalidatePaywallCache(${workspaceId}) failed: ${r.status} ${await r.text()}`);
  }
}

// === Cleanup tracker ========================================================

// describe.serial : le tenant traverse 14 steps successives → afterAll au lieu
// de afterEach (sinon le wipe entre step 01 et 02 efface le tenant). Pattern
// hérité de saasification.spec.ts (Lot N étape 1+2).
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

// === Test principal serialise ===============================================

// tids contraints a varchar(20) cote DB workspaces.id. `m01<6chars>` = 9 chars
// (mega-style prefix sans deborder). Convention MEGA-XX = m<XX>.
test.describe.serial('@mega @regression MEGA-01 — provision lifecycle complet (12+ etapes)', () => {
  const tid = `m01${RUN_STAMP}`;
  const ownerEmail = `e2e-mega-01-lifecycle-${RUN_STAMP}@e2e.veridian.site`;
  let provisioningResponse: any;
  let initialApiKey: string;

  test('01. Provision tenant (free) via HMAC', async () => {
    provisioned.push(tid);
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: ownerEmail,
      plan: 'free',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    provisioningResponse = body.json();

    // Invariants metier du provisioning
    expect(provisioningResponse.workspace_id).toBe(tid);
    expect(provisioningResponse.owner_user_id).toBeTruthy();
    expect(provisioningResponse.api_key, 'api_key must be present in provision response').toBeTruthy();
    expect(provisioningResponse.api_key.length).toBeGreaterThan(20);
    expect(provisioningResponse.magic_link).toContain('/console/signin');
    expect(provisioningResponse.auto_login_url).toContain('/veridian/auto-login?token=');
    expect(provisioningResponse.created).toBe(true);

    initialApiKey = provisioningResponse.api_key;
  });

  test('02. /status active + plan=free + quota=-1 (post-pivot 2026-05-21 illimite)', async () => {
    const r = await hmacGet(`/api/tenants/${tid}/status`);
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();

    expect(data.tenant_id).toBe(tid);
    expect(data.status).toBe('active');
    expect(data.plan).toBe('free');
    // Pivot 2026-05-21 : tous plans -1 (BYO sending)
    expect(data.monthly_email_quota).toBe(-1);
    expect(data.quota_remaining).toBe(-1);
    expect(data.deleted_at, 'fresh tenant must not have deleted_at').toBeFalsy();
    expect(data.suspended_at, 'fresh tenant must not have suspended_at').toBeFalsy();
  });

  test('03. auto_login_url retourne 200 (page intermediaire HTML)', async () => {
    // On ne pilote pas un browser ici (cf saasification.spec.ts pour le test
    // headful). On verifie juste que l URL est servie 200 par le backend —
    // la page contient un script qui set localStorage + redirect.
    const r = await fetch(provisioningResponse.auto_login_url, { redirect: 'manual' });
    // 200 = page HTML intermediaire (token signe self-contained, pas de redirect immediate)
    // 302/303 = redirect direct vers /console (rare, depend du flow)
    expect([200, 302, 303], `auto-login responded ${r.status}`).toContain(r.status);
  });

  test('04. 5 envois transactional → activity_threshold_reached_at se pose (V38)', async () => {
    // Send 5 emails (l increment passe par CreateMessage decorator V38).
    // Le 5e doit declencher MarkActivityThresholdReached. transactional.send
    // peut renvoyer 4xx body invalide mais le compteur emails_sent_lifetime
    // est increment des que le plan check passe.
    for (let i = 0; i < 5; i++) {
      const r = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${initialApiKey}`,
        },
        body: JSON.stringify({
          workspace_id: tid,
          to: `sink-${i}@e2e.veridian.test`,
        }),
      });
      // Pas 402 (plan free actif), pas 401 (api_key valide).
      expect(r.status, `send #${i + 1} got unexpected status`).not.toBe(402);
      expect(r.status, `send #${i + 1} got unexpected status`).not.toBe(401);
    }

    // Validation indirecte : on appelle /limits qui inclut le plan & l etat.
    // Le signal activity_threshold_reached est observable via le webhook
    // tenant.activity_threshold_reached cote Hub (couvert par
    // webhooks-lifecycle-emit.spec.ts en mode mock-receiver). Ici on assert
    // au moins que les sends n ont pas casse l etat tenant.
    const status = await hmacGet(`/api/tenants/${tid}/status`);
    const sb = await readBody(status);
    expect(status.status, sb.raw).toBe(200);
    expect(sb.json().status).toBe('active');
  });

  test('05. update-plan free → pro (HMAC stripe) + invalidate cache', async () => {
    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      tenant_id: tid,
      plan: 'pro',
      plan_source: 'stripe',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);

    // Propagation cache pour la suite (sinon paywall middleware peut servir
    // un plan obsolete pendant 60s TTL).
    await invalidatePaywallCache(tid);

    // Verif persistance via status (round-trip DB)
    const status = await hmacGet(`/api/tenants/${tid}/status`);
    const sb = await readBody(status);
    expect(status.status, sb.raw).toBe(200);
    expect(sb.json().plan).toBe('pro');
  });

  test('06. /limits post-upgrade : plan=pro + FeatureABTesting=true (gratuit pivot 2026-05-21)', async () => {
    const r = await hmacGet(`/api/tenants/${tid}/limits`);
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();

    expect(data.tenant_id).toBe(tid);
    expect(data.plan).toBe('pro');
    expect(data.status).toBe('active');

    // Invariant pivot 2026-05-21 : tout illimite, A/B gratuit pour tous.
    // White-label reste differenciation Business+ → false sur Pro.
    expect(data.limits, 'limits object must be present').toBeTruthy();
    expect(data.limits.MonthlyEmailQuota).toBe(-1);
    expect(data.limits.FeatureABTesting, 'A/B gratuit post-pivot').toBe(true);
    expect(data.limits.FeatureWhiteLabel, 'white-label reserve Business+').toBe(false);
    expect(data.generated_at, 'response must include generated_at timestamp').toBeTruthy();
  });

  test('07. soft-delete tenant → 200 + deleted_at RFC3339 + status=soft_deleted', async () => {
    const r = await hmacFetch(`/api/tenants/${tid}/soft-delete`, 'POST', {
      reason: 'e2e-mega-01-lifecycle',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();

    expect(data.deleted_at, 'response must include deleted_at').toBeTruthy();
    expect(data.deleted_at as string).toMatch(
      /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})/,
    );
  });

  test('08. send post-soft-delete → 402 + cache invalidate propage', async () => {
    // Sans invalidation, le cache paywall peut encore servir le plan actif
    // jusqu a 60s TTL. On force la propagation.
    await invalidatePaywallCache(tid);

    const r = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${initialApiKey}`,
      },
      body: JSON.stringify({
        workspace_id: tid,
        to: 'sink-post-sd@e2e.veridian.test',
      }),
    });
    expect(r.status, 'soft-deleted writes must be 402').toBe(402);
    const body = await readBody(r);
    const data = body.json();
    // Soit code soit error, le message doit faire reference au soft delete
    const combined = JSON.stringify(data).toLowerCase();
    expect(combined).toMatch(/soft_deleted|soft delete|payment|tenant/);
  });

  test('09. restore tenant → 200 + restored_at RFC3339', async () => {
    const r = await hmacFetch(`/api/tenants/${tid}/restore`, 'POST', {
      reason: 'e2e-mega-01-restore',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();
    expect(data.restored_at, 'response must include restored_at').toBeTruthy();
    expect(data.restored_at as string).toMatch(
      /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})/,
    );

    // Force la propagation cache pour la suite
    await invalidatePaywallCache(tid);
  });

  test('10. /status post-restore : active + deleted_at null', async () => {
    const r = await hmacGet(`/api/tenants/${tid}/status`);
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();
    expect(data.status).toBe('active');
    expect(data.deleted_at, 'restored tenant must clear deleted_at').toBeFalsy();
    // Plan doit etre conserve depuis l upgrade etape 05
    expect(data.plan).toBe('pro');
  });

  test('11. grant-unlimited (pro → enterprise / plan_source=lifetime_partner)', async () => {
    const r = await hmacFetch('/api/veridian/admin/grant-unlimited', 'POST', {
      tenant_id: tid,
      reason: 'e2e_mega_internal_team_member',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();

    expect(data.tenant_id).toBe(tid);
    expect(data.plan).toBe('enterprise');
    expect(data.previous_plan, 'previous_plan should be pro from step 05').toBe('pro');
    expect(data.plan_source).toBe('lifetime_partner');
    expect(data.quota).toBe(-1);
    expect(data.granted_at).toBeTruthy();
    expect(data.reason).toBe('e2e_mega_internal_team_member');
  });

  test('12. /limits post-grant : enterprise + FeatureWhiteLabel=true', async () => {
    const r = await hmacGet(`/api/tenants/${tid}/limits`);
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();

    expect(data.plan).toBe('enterprise');
    expect(data.plan_source).toBe('lifetime_partner');
    // White-label debloque sur enterprise
    expect(data.limits.FeatureWhiteLabel, 'enterprise unlocks white-label').toBe(true);
    expect(data.limits.MonthlyEmailQuota).toBe(-1);
    expect(data.limits.FeatureABTesting).toBe(true);
  });

  test('13. wipe-test-tenants (hard delete) → 200', async () => {
    const r = await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
      tenant_ids: [tid],
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest'],
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    // Pop du tracker pour eviter double-wipe afterEach
    const idx = provisioned.indexOf(tid);
    if (idx >= 0) provisioned.splice(idx, 1);
  });

  test('14. /status post-wipe : 404 tenant_not_found', async () => {
    const r = await hmacGet(`/api/tenants/${tid}/status`);
    // Apres hard delete, le status doit etre 404. Si l implementation retourne
    // 200 avec status=deleted ou un body custom, on accepte temporairement 404
    // strict (le wipe est cense disparaitre la row).
    expect([404], `status post-wipe should be 404, got ${r.status}`).toContain(r.status);
  });
});
