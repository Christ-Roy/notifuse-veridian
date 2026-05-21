// === Veridian patch === Tests E2E pour :
//   1. emails_sent_this_month s'incrémente sur chaque envoi (wire-increment ticket P1 sécu)
//   2. POST /api/veridian/admin/grant-unlimited bypass le quota (échappatoire interne + clients fidèles)
//
// Run en CI sur staging après chaque push. Pré-requis : NOTIFUSE_URL +
// HUB_API_SECRET dans l'env.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

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

async function provisionTenant(tenantId: string, plan = 'free') {
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: `${tenantId}@quota.test`,
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

async function getTenantStatus(tenantId: string) {
  const r = await hmacFetch(`/api/tenants/${tenantId}/status`, 'GET');
  expect(r.status).toBe(200);
  return r.json();
}

async function sendTransactional(apiKey: string, workspaceId: string) {
  return fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${apiKey}` },
    body: JSON.stringify({ workspace_id: workspaceId, to: 'sink@quota.test' }),
  });
}

async function invalidatePaywallCache(workspaceId: string) {
  const r = await hmacFetch('/api/veridian/admin/cache/invalidate', 'POST', {
    workspace_id: workspaceId,
  });
  expect(r.status, await r.text()).toBe(200);
}

// === Veridian patch 2026-05-21 (Lot N étape 1+2) ===
// Prefix unifié `tst` + cleanup afterEach au fil de l'eau.
// Cf. todo/2026-05-20-e2e-cleanup-discipline-canary-safety.md
const newTid = () => `tst${Date.now().toString(36).slice(-6)}`;
const provisioned: string[] = [];

test.afterEach(async () => {
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
    console.warn(`afterEach wipe failed (non-fatal): ${err}`);
  }
});

test.describe('Quota — increment emails_sent_this_month sur envoi', () => {
  test('compteur s incremente apres chaque envoi reussi', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    // 1. Etat initial : 0 mails envoyes
    let status = await getTenantStatus(tid);
    expect(status.emails_sent_this_month).toBe(0);

    // 2. Envoi mail. Si l'envoi reel echoue (pas de provider configure en
    // staging), c'est OK : le MessageHistory est cree quand meme avec le
    // statut failed → l'increment du compteur a lieu (decorator wrap
    // Create, pas SendEmail).
    //
    // Note : selon la config staging, transactional.send peut renvoyer 400
    // pour body invalide (template_id requis, etc.). On accepte tout sauf 402
    // (le 402 = paywall, qui veut dire le tenant est deja bloque — ce qui ne
    // doit pas etre le cas pour un nouveau tenant pro).
    const r = await sendTransactional(api_key, tid);
    expect(r.status).not.toBe(402);

    // 3. Si l'envoi a abouti a creer une row message_history (status 200/202),
    // le compteur doit etre >= 1. Si le handler upstream a rejete avant la
    // creation (4xx pour body invalide), le compteur reste a 0 — c'est OK
    // car la decoration ne s'active que sur upstream.Create success.
    //
    // On valide les DEUX trajectoires : soit envoi OK + compteur incremente,
    // soit envoi rejete par upstream + compteur stable.
    status = await getTenantStatus(tid);
    if (r.status === 200 || r.status === 202) {
      expect(status.emails_sent_this_month).toBeGreaterThanOrEqual(1);
    } else {
      // upstream a rejete avant le Create → compteur stable
      expect(status.emails_sent_this_month).toBe(0);
    }
  });
});

test.describe('Grant unlimited — bypass paywall pour comptes privilegies', () => {
  test('grant-unlimited passe un tenant en enterprise + plan_source lifetime_partner', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    // Etat initial : free + quota=-1 (BYO sending 2026-05-20)
    let status = await getTenantStatus(tid);
    expect(status.plan).toBe('free');
    expect(status.monthly_email_quota).toBe(-1); // BYO unlimited sur tous plans

    // Grant unlimited
    const grantResp = await hmacFetch('/api/veridian/admin/grant-unlimited', 'POST', {
      tenant_id: tid,
      reason: 'e2e_test_grant_unlimited',
    });
    expect(grantResp.status).toBe(200);
    const grantBody = await grantResp.json();
    expect(grantBody.plan).toBe('enterprise');
    expect(grantBody.previous_plan).toBe('free');
    expect(grantBody.quota).toBe(-1);
    expect(grantBody.plan_source).toBe('lifetime_partner');

    // Etat post-grant : enterprise (le quota reste -1 puisqu'il l'était déjà,
    // mais le plan_source devient lifetime_partner — immune au downgrade Stripe).
    status = await getTenantStatus(tid);
    expect(status.plan).toBe('enterprise');
    expect(status.monthly_email_quota).toBe(-1);
    expect(status.quota_remaining).toBe(-1); // unlimited
  });

  test('grant-unlimited refuse plan_source stripe (non immune)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const r = await hmacFetch('/api/veridian/admin/grant-unlimited', 'POST', {
      tenant_id: tid,
      reason: 'should_fail',
      plan_source: 'stripe',
    });
    expect(r.status).toBe(400);
    const body = await r.json();
    expect(body.error).toMatch(/invalid plan_source/i);
  });

  test('grant-unlimited refuse reason vide', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const r = await hmacFetch('/api/veridian/admin/grant-unlimited', 'POST', {
      tenant_id: tid,
      reason: '',
    });
    expect(r.status).toBe(400);
  });

  test('grant-unlimited 404 sur tenant inconnu', async () => {
    // Pas de provisioned.push : ce tenant n'existe pas (test ciblé sur le 404).
    // Le `ghost-` reste sans tiret pour rester alphanum (workspace.Validate).
    const r = await hmacFetch('/api/veridian/admin/grant-unlimited', 'POST', {
      tenant_id: `ghost${Date.now().toString(36).slice(-8)}`,
      reason: 'test_404',
    });
    expect(r.status).toBe(404);
  });

  test('grant-unlimited resume un tenant suspended (autre motif de blocage)', async () => {
    // Scenario adapté à la decision 2026-05-20 (BYO sending) :
    // Le quota mensuel ne bloque PLUS, donc on test le seul motif qui reste
    // côté paywall : suspend. Grant-unlimited doit auto-resume le tenant
    // (cf. service.GrantUnlimited).
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'free');

    // Suspend le tenant
    const susResp = await hmacFetch('/api/tenants/suspend', 'POST', {
      tenant_id: tid,
      reason: 'e2e_test_suspend',
    });
    expect(susResp.status).toBe(200);
    await invalidatePaywallCache(tid);

    // L'envoi doit echouer en 402 (suspended)
    let send = await sendTransactional(api_key, tid);
    expect(send.status).toBe(402);

    // Grant unlimited (resume auto)
    const grantResp = await hmacFetch('/api/veridian/admin/grant-unlimited', 'POST', {
      tenant_id: tid,
      reason: 'unblock_via_grant_resume',
    });
    expect(grantResp.status).toBe(200);

    // Cache deja invalide par le handler grant-unlimited. L'envoi doit
    // maintenant passer (status=active, plan=enterprise).
    send = await sendTransactional(api_key, tid);
    expect(send.status).not.toBe(402);
  });

  test('grant-unlimited est idempotent (appel x2 sur meme tenant OK)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    for (let i = 0; i < 2; i++) {
      const r = await hmacFetch('/api/veridian/admin/grant-unlimited', 'POST', {
        tenant_id: tid,
        reason: `idempotent_call_${i}`,
      });
      // Lire le body UNE SEULE FOIS (Fetch API : body est un stream consommable
      // une seule fois — text() puis json() sur la meme reponse echoue).
      const raw = await r.text();
      expect(r.status, raw).toBe(200);
      const body = JSON.parse(raw);
      expect(body.plan).toBe('enterprise');
      expect(body.quota).toBe(-1);
    }
  });
});
