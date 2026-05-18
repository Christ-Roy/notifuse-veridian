// Provision idempotence — couvre les 3 scenarios du contrat §5.1 et du ticket
// Hub 2026-05-18-confirm-provision-idempotence :
//
//   Cas A : replay meme tenant + meme owner       → created:false, magic_link FRAIS
//   Cas B : meme tenant + owner_email different   → 409 ErrOwnerMismatch
//   Cas C : nouveau tenant                        → created:true (smoke)
//
// PAS @prod-safe : ces tests creent des tenants (cleanup via wipe-test-tenants
// prefix `idempot`). Tournent uniquement en staging.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

function signHMAC(body: string, secret = HUB_API_SECRET, ts = Date.now().toString()) {
  const signature = crypto.createHmac('sha256', secret).update(`${ts}.${body}`).digest('hex');
  return { timestamp: ts, signature };
}

async function hmacFetch(path: string, method: string, body: object | null = null) {
  const rawBody = body ? JSON.stringify(body) : '';
  const { timestamp, signature } = signHMAC(rawBody);
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Veridian-Hub-Signature': signature,
      'X-Veridian-Timestamp': timestamp,
    },
    body: rawBody || undefined,
  });
}

// Cleanup defensif a la fin : supprime tous les tenants `idempot*` crees.
test.afterAll(async () => {
  const wipeRes = await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
    prefix: 'idempot',
  });
  if (wipeRes.status !== 200) {
    console.warn(`wipe-test-tenants returned ${wipeRes.status} (non-fatal): ${await wipeRes.text()}`);
  }
});

test.describe('Provision idempotence (ticket Hub 2026-05-18)', () => {
  test('Cas A : replay meme tenant + meme owner → created:false, magic_link FRAIS', async () => {
    const tenantId = `idempot${Date.now().toString(36).slice(-8)}`;
    const ownerEmail = `${tenantId}@idempot.test`;

    // 1. Premiere provision : created:true, magic_link M1
    const res1 = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
      plan: 'free',
    });
    expect(res1.status).toBe(200);
    const body1 = await res1.json();
    expect(body1.created).toBe(true);
    expect(body1.workspace_id).toBe(tenantId);
    expect(body1.api_key).toBeTruthy();
    expect(body1.magic_link).toMatch(/code=/);
    const m1 = body1.magic_link as string;

    // 2. Replay : created:false, magic_link M2 != M1, workspace_id identique, api_key vide
    const res2 = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
      plan: 'free',
    });
    expect(res2.status).toBe(200);
    const body2 = await res2.json();
    expect(body2.created).toBe(false);
    expect(body2.workspace_id).toBe(tenantId);
    expect(body2.api_key).toBe(''); // pas regenere
    expect(body2.magic_link).toMatch(/code=/);
    expect(body2.magic_link).not.toBe(m1); // TTL frais
    // auto_login_url : token signé HMAC self-contained, format
    // /veridian/auto-login?token=<base64>.<hmac>. Pas de query email/code.
    if (body2.auto_login_url) {
      expect(body2.auto_login_url).toMatch(/\/veridian\/auto-login\?token=/);
    }
  });

  test('Cas B : meme tenant + owner_email different → 409', async () => {
    const tenantId = `idempotb${Date.now().toString(36).slice(-8)}`;
    const aliceEmail = `${tenantId}-alice@idempot.test`;
    const malloryEmail = `${tenantId}-mallory@idempot.test`;

    // Setup : tenant cree avec alice
    const res1 = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: aliceEmail,
      plan: 'free',
    });
    expect(res1.status).toBe(200);

    // Replay malicieux : mallory tente de provisionner le meme tenant
    const res2 = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: malloryEmail,
      plan: 'free',
    });
    expect(res2.status).toBe(409);
    const body2 = await res2.json();
    expect(body2.error).toMatch(/different owner/i);
  });

  test('Cas C : nouveau tenant → created:true (smoke)', async () => {
    const tenantId = `idempotc${Date.now().toString(36).slice(-8)}`;
    const res = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: `${tenantId}@idempot.test`,
      plan: 'free',
    });
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.created).toBe(true);
    expect(body.workspace_id).toBe(tenantId);
    expect(body.api_key).toBeTruthy();
    expect(body.magic_link).toMatch(/code=/);
  });
});
