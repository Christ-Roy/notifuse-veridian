// CI violente — Suite 4 : status endpoint + update-plan + transitions.

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

async function provisionTenant(tid: string, plan = 'free') {
  // Retry 5x sur 5xx + 404 : meme race upstream que chaos-paywall, voir
  // commentaire detaille la-bas. 404 transient observe en CI quand le pool DB
  // est sous charge (test #10 territory).
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: `${tid}@status.test`,
      plan,
    });
    if (r.status === 200) {
      return r.json();
    }
    if ((r.status >= 500 || r.status === 404) && attempt < 4) {
      const backoffMs = 500 * Math.pow(2, attempt);
      // eslint-disable-next-line no-console
      console.log(`provision retry ${attempt + 1}/5: status=${r.status}, backoff=${backoffMs}ms`);
      await new Promise((res) => setTimeout(res, backoffMs));
      continue;
    }
    expect(r.status, await r.text()).toBe(200);
  }
  throw new Error('provision retry exhausted (5 attempts incl. 404 transient)');
}

test.describe('Status endpoint', () => {
  test('status apres provision : active, plan, quota matchent input', async () => {
    const tid = `stat${Date.now().toString(36).slice(-6)}`;
    await provisionTenant(tid, 'pro');

    const r = await hmacFetch(`/api/tenants/${tid}/status`, 'GET');
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.tenant_id).toBe(tid);
    expect(data.status).toBe('active');
    expect(data.plan).toBe('pro');
    // 2026-05-20 : tous plans en quota=-1 (BYO sending — pas de provider Veridian)
    expect(data.monthly_email_quota).toBe(-1);
    expect(data.emails_sent_this_month).toBe(0);
    expect(data.quota_remaining).toBe(-1); // unlimited
    expect(data.suspended_at).toBeFalsy();
    expect(data.deleted_at).toBeFalsy();
  });

  test('status apres suspend : status=suspended, suspended_at populated', async () => {
    const tid = `statsus${Date.now().toString(36).slice(-6)}`;
    await provisionTenant(tid, 'free');
    await hmacFetch('/api/tenants/suspend', 'POST', { tenant_id: tid, reason: 'overdue' });

    const r = await hmacFetch(`/api/tenants/${tid}/status`, 'GET');
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.status).toBe('suspended');
    expect(data.suspended_at).toBeTruthy();
    expect(data.suspended_reason).toBe('overdue');
  });

  test('status apres delete : status=deleted, deleted_at populated', async () => {
    const tid = `statdel${Date.now().toString(36).slice(-6)}`;
    await provisionTenant(tid, 'free');
    await hmacFetch(`/api/tenants/${tid}`, 'DELETE');

    const r = await hmacFetch(`/api/tenants/${tid}/status`, 'GET');
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.status).toBe('deleted');
    expect(data.deleted_at).toBeTruthy();
  });

  test('status tenant inexistant → 404', async () => {
    const r = await hmacFetch('/api/tenants/doesnotexist123/status', 'GET');
    expect(r.status).toBe(404);
  });
});

test.describe('Update-plan transitions', () => {
  test('upgrade free → pro : compteur emails preserve, quota updated', async () => {
    const tid = `up${Date.now().toString(36).slice(-6)}`;
    await provisionTenant(tid, 'free');

    let r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      tenant_id: tid,
      plan: 'pro',
    });
    expect(r.status).toBe(200);

    r = await hmacFetch(`/api/tenants/${tid}/status`, 'GET');
    const data = await r.json();
    expect(data.plan).toBe('pro');
    // 2026-05-20 : tous plans en quota=-1 (BYO sending)
    expect(data.monthly_email_quota).toBe(-1);
  });

  test('downgrade pro → free : quota reste illimite (BYO sending)', async () => {
    // 2026-05-20 : avec la décision BYO, tous plans ont quota=-1 — donc
    // pas de "reduction" sur downgrade côté emails. Le test reste utile
    // pour valider que update-plan ne casse pas (plan change bien).
    const tid = `down${Date.now().toString(36).slice(-6)}`;
    await provisionTenant(tid, 'pro');

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      tenant_id: tid,
      plan: 'free',
    });
    expect(r.status).toBe(200);

    const status = await hmacFetch(`/api/tenants/${tid}/status`, 'GET');
    const data = await status.json();
    expect(data.plan).toBe('free');
    expect(data.monthly_email_quota).toBe(-1); // BYO unlimited
  });

  test('plan inconnu → 400 ou fallback free (selon decision)', async () => {
    const tid = `unkplan${Date.now().toString(36).slice(-6)}`;
    await provisionTenant(tid, 'free');

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      tenant_id: tid,
      plan: 'imaginary_plan_xyz',
    });
    // Decision : on accepte mais quota fallback sur free (cf domain.QuotaForPlan)
    // OU 400 si validation stricte. Tester juste pas 500.
    expect([200, 400]).toContain(r.status);
  });
});

test.describe('Resume sans suspend prealable', () => {
  test('resume tenant active → 200 idempotent ou 409 selon impl', async () => {
    const tid = `resume${Date.now().toString(36).slice(-6)}`;
    await provisionTenant(tid, 'free');

    const r = await hmacFetch('/api/tenants/resume', 'POST', { tenant_id: tid });
    // Acceptable : 200 (idempotent) ou 409 (rien a resume). Pas 500.
    expect([200, 409]).toContain(r.status);
  });
});

test.describe('Suspend deja suspended', () => {
  test('suspend deux fois → 200 (idempotent), suspended_reason ecrase', async () => {
    const tid = `sus2${Date.now().toString(36).slice(-6)}`;
    await provisionTenant(tid, 'free');

    let r = await hmacFetch('/api/tenants/suspend', 'POST', {
      tenant_id: tid,
      reason: 'first reason',
    });
    expect(r.status).toBe(200);

    r = await hmacFetch('/api/tenants/suspend', 'POST', {
      tenant_id: tid,
      reason: 'second reason',
    });
    expect(r.status).toBe(200);

    const status = await hmacFetch(`/api/tenants/${tid}/status`, 'GET');
    const data = await status.json();
    expect(data.suspended_reason).toBe('second reason');
  });
});
