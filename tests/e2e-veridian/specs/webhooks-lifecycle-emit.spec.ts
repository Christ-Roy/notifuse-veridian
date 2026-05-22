// === Veridian patch — Lot R (2026-05-21) ===
// Tests E2E des webhooks lifecycle emis par Notifuse vers Hub.
//
// Le service Notifuse pousse vers `HUB_WEBHOOK_URL` (HMAC sign avec
// `HUB_WEBHOOK_SECRET`) les events :
//   - tenant.soft_deleted (V40 Lot I) — payload deleted_at + purge_eligible_at RFC3339
//   - tenant.restored     (V40 Lot I) — payload restored_at RFC3339
//   - tenant.touched      (V40 Lot I) — payload touched_at RFC3339
//   - tenant.quota_exceeded (V40 Lot I) — 1×/mois max (idempotence DB)
//
// Spec parent : todo/2026-05-19-webhooks-manquants.md
// Code        : internal/service/veridian_webhook_emitter.go
//
// Strategie de test :
//   - Test 100% staging impossible : HUB_WEBHOOK_URL pointe vers Hub prod/staging,
//     l'agent E2E ne peut pas l'intercepter.
//   - Solution : test.skip(!process.env.MOCK_WEBHOOK_RECEIVER_URL).
//     Pour lancer ces tests, executer Notifuse local avec
//     `HUB_WEBHOOK_URL=http://localhost:PORT/webhook HUB_WEBHOOK_SECRET=test-secret`
//     puis `MOCK_WEBHOOK_RECEIVER_URL=http://localhost:PORT MOCK_WEBHOOK_SECRET=test-secret`.
//     Le test demarre un http.Server qui ecoute sur ce PORT, recoit les webhooks,
//     verifie les signatures HMAC + payloads.
//
// Conventions Lot N : tids `tst`, afterEach cleanup, tag @regression.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';
import * as http from 'http';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;
// MOCK_WEBHOOK_RECEIVER_URL : URL HTTP que le test va ecouter (e.g. http://localhost:9876).
// Doit etre passee a Notifuse au boot via HUB_WEBHOOK_URL = ${MOCK_WEBHOOK_RECEIVER_URL}/webhook.
const MOCK_WEBHOOK_RECEIVER_URL = process.env.MOCK_WEBHOOK_RECEIVER_URL;
// Secret HMAC partage avec Notifuse pour valider la signature reception.
const MOCK_WEBHOOK_SECRET = process.env.MOCK_WEBHOOK_SECRET ?? '';

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

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
      owner_email: `${tenantId}@webhooks.test`,
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

// === Mock webhook receiver ===
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

// === Cleanup tracker ===
const provisioned: string[] = [];

test.beforeAll(async () => {
  // Le test n'a de sens que si Notifuse est configure pour pousser vers ce
  // receiver. On skip la suite si MOCK_WEBHOOK_RECEIVER_URL absent.
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

test.afterEach(async () => {
  receivedWebhooks.length = 0;
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

test.describe('@regression webhooks lifecycle — events emis vers Hub mock', () => {
  test('soft-delete → tenant.soft_deleted avec deleted_at + purge_eligible_at RFC3339', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const r = await hmacFetch(`/api/tenants/${tid}/soft-delete`, 'POST', { reason: 'e2e-test' });
    expect(r.status).toBe(200);

    const hook = await waitForEvent('tenant.soft_deleted', tid);
    expect(hook.signatureValid).toBe(true);
    expect(hook.payload.deleted_at).toBeTruthy();
    expect(hook.payload.purge_eligible_at).toBeTruthy();
    // RFC3339 format check : YYYY-MM-DDTHH:MM:SSZ ou +HH:MM
    expect(hook.payload.deleted_at as string).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})/);
    expect(hook.payload.purge_eligible_at as string).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})/);
  });

  test('restore → tenant.restored avec restored_at RFC3339', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const sd = await hmacFetch(`/api/tenants/${tid}/soft-delete`, 'POST', { reason: 'pre-restore' });
    expect(sd.status).toBe(200);
    await waitForEvent('tenant.soft_deleted', tid);

    const r = await hmacFetch(`/api/tenants/${tid}/restore`, 'POST', { reason: 'e2e-test-restore' });
    expect(r.status, await r.text()).toBe(200);

    const hook = await waitForEvent('tenant.restored', tid);
    expect(hook.signatureValid).toBe(true);
    expect(hook.payload.restored_at).toBeTruthy();
    expect(hook.payload.restored_at as string).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})/);
  });

  test('touch → tenant.touched avec touched_at RFC3339', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'free');

    const r = await hmacFetch(`/api/tenants/${tid}/touch`, 'POST', {});
    expect([200, 204]).toContain(r.status);

    const hook = await waitForEvent('tenant.touched', tid);
    expect(hook.signatureValid).toBe(true);
    expect(hook.payload.touched_at).toBeTruthy();
    expect(hook.payload.touched_at as string).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})/);
  });

  test('quota_exceeded : N+1 envois sur quota=N → emit 1× + 2e emit meme mois = pas re-emit', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'free');

    // Force un quota de 5 mails (depuis le pivot 2026-05-21 le defaut est -1
    // illimite). update-plan avec quotas custom.
    const upResp = await hmacFetch('/api/tenants/update-plan', 'POST', {
      tenant_id: tid,
      plan: 'free',
      plan_source: 'manual',
      quotas: { monthly_emails: 5 },
    });
    expect(upResp.status, await upResp.text()).toBe(200);

    // Invalide cache paywall pour propager immediatement.
    await hmacFetch('/api/veridian/admin/cache/invalidate', 'POST', { workspace_id: tid });

    // Envoi de 6 mails (5 = quota, 6e doit emit quota_exceeded).
    // transactional.send peut renvoyer 4xx body invalide mais le decorator wrap
    // CreateMessage → l'increment + eval quota_exceeded ont lieu.
    for (let i = 0; i < 6; i++) {
      await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
        body: JSON.stringify({ workspace_id: tid, to: `sink+${i}@webhooks.test` }),
      });
    }

    // Attendre quota_exceeded (best-effort 8s timeout)
    const hook = await waitForEvent('tenant.quota_exceeded', tid, 12000);
    expect(hook.signatureValid).toBe(true);
    expect(hook.payload.plan).toBe('free');
    expect(hook.payload.monthly_email_quota).toBe(5);
    expect(hook.payload.emails_sent_this_month).toBeGreaterThanOrEqual(5);
    expect(hook.payload.exceeded_at).toBeTruthy();

    // Compter combien de fois quota_exceeded a ete emis : DOIT etre exactement 1
    // (idempotence DB via quota_exceeded_emitted_at_month).
    const allQuotaExceeded = receivedWebhooks.filter(
      (w) => w.eventType === 'tenant.quota_exceeded' && w.tenantId === tid,
    );
    // On envoie 6 mails au-dessus du quota — l'idempotence garantit 1 seul emit.
    expect(allQuotaExceeded.length).toBe(1);

    // Bonus : 7e mail meme mois → pas de re-emit (deja marque).
    const beforeCount = receivedWebhooks.filter(
      (w) => w.eventType === 'tenant.quota_exceeded' && w.tenantId === tid,
    ).length;
    await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
      body: JSON.stringify({ workspace_id: tid, to: 'sink-extra@webhooks.test' }),
    });
    // Petite attente pour eviter false negative
    await new Promise((res) => setTimeout(res, 2000));
    const afterCount = receivedWebhooks.filter(
      (w) => w.eventType === 'tenant.quota_exceeded' && w.tenantId === tid,
    ).length;
    expect(afterCount).toBe(beforeCount); // pas de re-emit
  });
});
