// === MEGA E2E — Spec 06 — Mail provider choice lifecycle (V48) ===
//
// Couverture endpoints HMAC livres vague 6 (commit f21936b5 +
// veridian_mail_provider_handler.go) :
//
//   GET  /api/workspaces/{id}/mail-provider-choice
//   POST /api/workspaces/{id}/mail-provider-choice
//
// Pattern : describe.serial + afterAll wipe (cf. 02-freeze-paywall-lifecycle).
// Tids ≤ 20 chars (varchar contrainte DB).
//
// Wire format attendu (sourced from domain/veridian_mail_provider.go
// MailProviderChoiceResponse + handleSetMailProviderChoice) :
//   200 POST : {workspace_id, choice, updated_at (RFC3339)}
//   200 GET  : {workspace_id, choice}  (updated_at omis sur read)
//   400 invalid_payload : {error, code: "invalid_payload", details}
//   404 tenant_not_found : si workspace inexistant cote POST update
//
// Note : GET sur workspace inexistant retourne 200 + choice=smtp_generic
// par design (sémantique safe read-only — repo retourne defaut). Ce
// comportement est asserté en step 08.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

const RUN_STAMP = Date.now().toString(36).slice(-6);

function signHMAC(body: string, secret: string = HUB_API_SECRET) {
  const ts = Date.now().toString();
  return {
    timestamp: ts,
    signature: crypto.createHmac('sha256', secret).update(`${ts}.${body}`).digest('hex'),
  };
}

async function hmacFetch(path: string, method: string, body: object | null = null, opts: { secret?: string } = {}) {
  const raw = body ? JSON.stringify(body) : '';
  const { timestamp, signature } = signHMAC(raw, opts.secret);
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
    if (r.status === 200) return body.json();
    if ((r.status >= 500 || r.status === 404) && attempt < 4) {
      await new Promise((res) => setTimeout(res, 500 * Math.pow(2, attempt)));
      continue;
    }
    expect(r.status, body.raw).toBe(200);
  }
  throw new Error('provision retry exhausted');
}

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

test.describe.serial('@mega @regression MEGA-06 — mail-provider-choice lifecycle V48', () => {
  const tid = `m06${RUN_STAMP}`;
  const ownerEmail = `e2e-mega-06-${RUN_STAMP}@e2e.veridian.site`;
  const ghostTid = `m06gh${RUN_STAMP}`; // workspace jamais provisionne

  test('01. Provision tenant pour les steps suivants → 200', async () => {
    provisioned.push(tid);
    await provisionTenant(tid, ownerEmail);
  });

  test('02. GET mail-provider-choice par defaut → 200 + choice=smtp_generic (V48 default)', async () => {
    const r = await hmacFetch(`/api/workspaces/${tid}/mail-provider-choice`, 'GET');
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();
    expect(data.workspace_id).toBe(tid);
    expect(data.choice).toBe('smtp_generic');
    // updated_at est omis sur GET (omitempty cote response struct)
    expect(data.updated_at ?? '').toBe('');
  });

  test('03. POST choice=hub_gmail → 200 + updated_at RFC3339 set', async () => {
    const r = await hmacFetch(`/api/workspaces/${tid}/mail-provider-choice`, 'POST', {
      choice: 'hub_gmail',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();
    expect(data.workspace_id).toBe(tid);
    expect(data.choice).toBe('hub_gmail');
    expect(data.updated_at).toBeTruthy();
    expect(data.updated_at as string).toMatch(
      /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})/,
    );
  });

  test('04. GET verifier persistence → 200 + choice=hub_gmail', async () => {
    const r = await hmacFetch(`/api/workspaces/${tid}/mail-provider-choice`, 'GET');
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();
    expect(data.workspace_id).toBe(tid);
    expect(data.choice).toBe('hub_gmail');
  });

  test('05. POST choice invalide (yahoo) → 400 invalid_payload + allowed list', async () => {
    const r = await hmacFetch(`/api/workspaces/${tid}/mail-provider-choice`, 'POST', {
      choice: 'yahoo',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(400);
    const data = body.json();
    expect(data.code).toBe('invalid_payload');
    // details doit contenir l'enum autorise pour aider le caller
    const allowed = data.details?.allowed ?? [];
    expect(allowed).toContain('smtp_generic');
    expect(allowed).toContain('hub_gmail');
  });

  test('06. POST body vide (choice manquant) → 400 invalid_payload + missing field', async () => {
    const r = await hmacFetch(`/api/workspaces/${tid}/mail-provider-choice`, 'POST', {});
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(400);
    const data = body.json();
    expect(data.code).toBe('invalid_payload');
    // Le handler renvoie missing: ["choice"] quand le champ est vide.
    const missing = data.details?.missing ?? [];
    expect(missing).toContain('choice');
  });

  test('07. POST avec HMAC invalide → 401', async () => {
    const raw = JSON.stringify({ choice: 'smtp_generic' });
    const { timestamp } = signHMAC(raw);
    const r = await fetch(`${NOTIFUSE_URL}/api/workspaces/${tid}/mail-provider-choice`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': 'deadbeef'.repeat(8), // 64 hex chars but wrong
        'X-Veridian-Timestamp': timestamp,
      },
      body: raw,
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(401);
  });

  test('08. GET workspace inexistant → 200 + choice=smtp_generic (sémantique safe read-only)', async () => {
    // Documenté explicitement dans handleGetMailProviderChoice.go :
    // "le repo retourne MailProviderSMTPGeneric comme fallback safe si le
    // workspace est absent (semantique read-only)". Ce test verrouille
    // ce comportement contre toute régression future qui voudrait basculer
    // sur un 404 (= breaking change pour callers Hub).
    const r = await hmacFetch(`/api/workspaces/${ghostTid}/mail-provider-choice`, 'GET');
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const data = body.json();
    expect(data.workspace_id).toBe(ghostTid);
    expect(data.choice).toBe('smtp_generic');
  });
});
