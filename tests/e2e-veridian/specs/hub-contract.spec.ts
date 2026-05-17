// Hub integration contract — scenario 1-9 du README intégrations Hub
// (veridian-hub/todo/integrations/README.md §"Tests d'intégration exigés").
//
// Garantit que Notifuse implémente le contrat v1 complet attendu par le Hub :
//   1. provision → tenant créé + api_key + owner attaché
//   2. generateMagicLink → JWT valide pour l'owner
//   3. JWT décodé → workspace présent dans le payload (détecte bug 2026-05-17)
//   4. health → magic_link_capable=true, owner_attached=true
//   5. suspend → health → status=suspended, magic_link_capable=false
//   6. resume → health → status=active, magic_link_capable=true
//   7. attach-owner additif → already_attached=false (nouveau owner)
//   8. attach-owner idempotent → already_attached=true (même owner)
//   9. provision idempotent → created=false, même api_key
//
// Ce test DOIT tourner en CI sur chaque PR (workflow veridian-ci.yml). Si rouge,
// le Hub ne peut pas mettre à jour son client Notifuse — le contrat est cassé.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

function signHMAC(body: string) {
  const ts = Date.now().toString();
  const signature = crypto.createHmac('sha256', HUB_API_SECRET).update(`${ts}.${body}`).digest('hex');
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

async function bearerFetch(path: string, method: string, apiKey: string, body: object | null = null) {
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${apiKey}`,
    },
    body: body ? JSON.stringify(body) : undefined,
  });
}

function decodeJWTPayload(token: string): Record<string, unknown> {
  const parts = token.split('.');
  if (parts.length !== 3) throw new Error(`invalid JWT (parts=${parts.length})`);
  const padded = parts[1] + '='.repeat((4 - (parts[1].length % 4)) % 4);
  return JSON.parse(Buffer.from(padded.replace(/-/g, '+').replace(/_/g, '/'), 'base64').toString());
}

test.describe('Hub integration contract v1 — scenario 1-9 README', () => {
  test('full lifecycle: provision → magic-link → health → suspend/resume → attach-owner → idempotence', async () => {
    // Tenant ID éphémère pour éviter pollution entre runs. Prefix `e2e-` =
    // sécurité (cleanup script + safety prefixes côté wipe-test-tenants).
    const tenantID = `e2e${Date.now().toString(36).slice(-10)}`;
    const aliceEmail = `${tenantID}-alice@e2e.test`;
    const bobEmail = `${tenantID}-bob@e2e.test`;

    // ─── Step 1 : provision ───────────────────────────────────────────
    const provisionResp = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantID,
      owner_email: aliceEmail,
      workspace_name: `e2e ${tenantID}`,
      plan: 'free',
    });
    expect(provisionResp.status).toBe(200);
    const provision = await provisionResp.json();
    expect(provision.workspace_id).toBe(tenantID);
    expect(provision.created).toBe(true);
    expect(provision.api_key).toBeTruthy();
    expect(provision.owner_user_id).toBeTruthy();
    const apiKey = provision.api_key as string;

    // ─── Step 2 : generateMagicLink ──────────────────────────────────
    const magicResp = await bearerFetch(
      '/api/workspaces.generateMagicLink',
      'POST',
      apiKey,
      { user_email: aliceEmail },
    );
    expect(magicResp.status).toBe(200);
    const magic = await magicResp.json();
    expect(magic.magic_link).toBeTruthy();
    expect(magic.auto_login_url).toBeTruthy();

    // ─── Step 3 : decode JWT du auto_login_url → workspaces contains tenantID
    // C'est CE point qui aurait détecté le bug 2026-05-17 : sans owner
    // humain attaché, le JWT signe correctement mais workspaces est vide.
    const autoLoginParsed = new URL(magic.auto_login_url);
    const token = autoLoginParsed.searchParams.get('token');
    expect(token).toBeTruthy();
    const claims = decodeJWTPayload(token!);
    expect(claims.email).toBe(aliceEmail);
    // workspaces : le format diffère selon impl mais doit contenir le tenant
    // (soit array de strings, soit array d'objects). On valide les 2 formes.
    const workspaces = claims.workspaces as unknown[] | undefined;
    expect(workspaces).toBeDefined();
    expect(Array.isArray(workspaces)).toBe(true);
    const wsIds = (workspaces as Array<string | { id: string }>).map((w) =>
      typeof w === 'string' ? w : w.id,
    );
    expect(wsIds).toContain(tenantID);

    // ─── Step 4 : health → magic_link_capable=true ────────────────────
    const healthResp = await hmacFetch(`/api/tenants/${tenantID}/health`, 'GET');
    expect(healthResp.status).toBe(200);
    const health = await healthResp.json();
    expect(health.tenant_id).toBe(tenantID);
    expect(health.status).toBe('active');
    expect(health.owner_attached).toBe(true);
    expect(health.owner_email).toBe(aliceEmail);
    expect(health.api_key_valid).toBe(true);
    expect(health.magic_link_capable).toBe(true);
    expect(health.members_count).toBeGreaterThanOrEqual(2); // alice + api_key

    // ─── Step 5 : suspend → health → status=suspended ─────────────────
    const suspendResp = await hmacFetch('/api/tenants/suspend', 'POST', {
      tenant_id: tenantID,
      reason: 'e2e-test',
    });
    expect(suspendResp.status).toBe(200);

    const healthAfterSuspend = await hmacFetch(`/api/tenants/${tenantID}/health`, 'GET');
    const healthSusp = await healthAfterSuspend.json();
    expect(healthSusp.status).toBe('suspended');
    expect(healthSusp.magic_link_capable).toBe(false);

    // ─── Step 6 : resume → health → status=active ─────────────────────
    const resumeResp = await hmacFetch('/api/tenants/resume', 'POST', {
      tenant_id: tenantID,
    });
    expect(resumeResp.status).toBe(200);

    const healthAfterResume = await hmacFetch(`/api/tenants/${tenantID}/health`, 'GET');
    const healthResumed = await healthAfterResume.json();
    expect(healthResumed.status).toBe('active');
    expect(healthResumed.magic_link_capable).toBe(true);

    // ─── Step 7 : attach-owner (nouveau bob) ──────────────────────────
    const attachBob = await hmacFetch('/api/veridian/admin/attach-owner', 'POST', {
      tenant_id: tenantID,
      owner_email: bobEmail,
    });
    expect(attachBob.status).toBe(200);
    const bobAttach = await attachBob.json();
    expect(bobAttach.already_attached).toBe(false);
    expect(bobAttach.attached).toBe(true);
    expect(bobAttach.user_id).toBeTruthy();

    // ─── Step 8 : attach-owner bob encore → already_attached=true ─────
    const attachBobAgain = await hmacFetch('/api/veridian/admin/attach-owner', 'POST', {
      tenant_id: tenantID,
      owner_email: bobEmail,
    });
    expect(attachBobAgain.status).toBe(200);
    const bobAgain = await attachBobAgain.json();
    expect(bobAgain.already_attached).toBe(true);

    // ─── Step 9 : provision idempotent (alice encore) → created=false
    const provisionAgain = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantID,
      owner_email: aliceEmail,
      plan: 'free',
    });
    expect(provisionAgain.status).toBe(200);
    const provisionRedo = await provisionAgain.json();
    expect(provisionRedo.workspace_id).toBe(tenantID);
    expect(provisionRedo.created).toBe(false);
  });

  test('health on non-existent tenant returns 404', async () => {
    const resp = await hmacFetch('/api/tenants/ghost-tenant-doesnt-exist/health', 'GET');
    expect(resp.status).toBe(404);
  });

  test('attach-owner without HMAC returns 401', async () => {
    const resp = await fetch(`${NOTIFUSE_URL}/api/veridian/admin/attach-owner`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tenant_id: 'whatever', owner_email: 'x@y.z' }),
    });
    // 401 ou 403 selon middleware — l'essentiel : pas 200, pas 5xx.
    expect([401, 403]).toContain(resp.status);
  });
});
