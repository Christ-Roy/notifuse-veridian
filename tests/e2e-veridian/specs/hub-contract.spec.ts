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

test.describe('Hub integration contract v1 — scenario 1-9 README', () => {
  test('full lifecycle: provision → magic-link → health → suspend/resume → attach-owner → idempotence', async () => {
    // Tenant ID éphémère pour éviter pollution entre runs. Prefix unifié `tst`
    // — cleanup auto via afterEach + safety_client_prefixes côté HMAC.
    const tenantID = newTid();
    provisioned.push(tenantID);
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

    // ─── Step 3 : suivre le auto_login_url, récupérer le auth_token, et
    // appeler /api/user.me — c'est l'API qui prouve que le user voit son
    // workspace côté serveur. Validé en staging 2026-05-18 : le JWT
    // Notifuse ne contient PAS la liste des workspaces (résolution serveur
    // via user_workspaces table à chaque request). Donc le check doit
    // passer par cette API, pas par le JWT.
    //
    // Le auto_login_url retourne du HTML qui set le token en localStorage.
    // Côté Node, on extrait le token directement depuis l'URL (claim w/e/i/x
    // côté veridian_token, signé HMAC) ET on appelle /api/user.me pour
    // récupérer le vrai auth_token JWT que la console utilise.
    //
    // Stratégie simple : appeler le endpoint d'échange auto-login → JWT
    // (côté Notifuse, c'est le handler `/veridian/auto-login` qui retourne
    // un HTML mais le JWT est aussi obtenable via une POST sur /api/user.signin
    // avec le code one-shot du magic_link). Pour éviter cette complexité,
    // on utilise directement /api/workspaces.generateMagicLink puis on
    // sign-in avec le code → JWT → /api/user.me.
    //
    // Plus simple encore : `/api/user.me` accepte Bearer api_key (tenant
    // API key scopée 1 workspace). C'est le contrat Bearer Pattern B du
    // README Hub — l'api_key voit ses workspaces.
    const meResp = await bearerFetch('/api/user.me', 'GET', apiKey);
    expect(meResp.status).toBe(200);
    const me = await meResp.json();
    expect(me.workspaces).toBeDefined();
    expect(Array.isArray(me.workspaces)).toBe(true);
    const wsIds = (me.workspaces as Array<string | { id: string }>).map((w) =>
      typeof w === 'string' ? w : w.id,
    );
    expect(wsIds).toContain(tenantID);

    // Validation magic link content (sans aller jusqu'au flow signin/JWT).
    const autoLoginParsed = new URL(magic.auto_login_url);
    const token = autoLoginParsed.searchParams.get('token');
    expect(token).toBeTruthy();

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

    // ─── Step 9 : provision idempotent ────────────────────────────────
    // Depuis le fix idempotence 2026-05-18 (b7d3fdcc), provision compare
    // l'owner_email du body avec l'owner humain réel du workspace.
    // Apres step 7 (attach-owner bob), bob est devenu owner unique.
    // Donc :
    //   - provision(alice) → 409 ErrOwnerMismatch (alice n'est plus owner)
    //   - provision(bob)   → 200 idempotent, magic_link FRAIS
    const provisionAliceAgain = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantID,
      owner_email: aliceEmail,
      plan: 'free',
    });
    expect(provisionAliceAgain.status).toBe(409);
    const aliceConflict = await provisionAliceAgain.json();
    expect(aliceConflict.error).toMatch(/different owner/i);

    const provisionBobAgain = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantID,
      owner_email: bobEmail,
      plan: 'free',
    });
    expect(provisionBobAgain.status).toBe(200);
    const bobRedo = await provisionBobAgain.json();
    expect(bobRedo.workspace_id).toBe(tenantID);
    expect(bobRedo.created).toBe(false);
    expect(bobRedo.magic_link).toMatch(/code=/);
  });

  test('regression bug 2026-05-17: auto-login lands on workspace, NOT /workspace/create', async ({ browser }) => {
    // Test décisif : suit le auto_login_url dans un vrai browser et vérifie
    // que l'user atterrit sur /console/workspace/<id> et pas sur
    // /console/workspace/create. C'est exactement ce qui était cassé sur
    // les 11 tenants prod le 2026-05-17, et c'est ce qu'un cron ne peut
    // pas détecter sans vrai navigateur (le JWT est valide mais la liste
    // des workspaces du user est vide côté API, donc la console SPA
    // redirige sur /workspace/create).
    //
    // Si CE test passe → le contrat Hub→Notifuse n'a plus de moyen de
    // casser silencieusement. Si CE test fail → le bug est de retour.

    const tenantID = newTid();
    provisioned.push(tenantID);
    const ownerEmail = `${tenantID}@regression.test`;

    const provisionResp = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantID,
      owner_email: ownerEmail,
      plan: 'free',
    });
    expect(provisionResp.status).toBe(200);
    const provision = await provisionResp.json();
    const autoLoginURL = provision.auto_login_url as string;
    expect(autoLoginURL).toBeTruthy();

    // Vrai browser context pour suivre l'auto-login (set localStorage + redirect JS).
    const context = await browser.newContext({ baseURL: NOTIFUSE_URL });
    const page = await context.newPage();
    try {
      // Suivre le auto-login URL. Le handler /veridian/auto-login set le JWT
      // en localStorage puis fait `window.location.replace(redirectURL)`.
      // On attend que la redirection soit terminée.
      await page.goto(autoLoginURL, { waitUntil: 'networkidle', timeout: 15000 });

      // Le redirect doit cibler /console (workspace picker si plusieurs ws,
      // ou /console/workspace/<id> direct si un seul ws). PAS /workspace/create.
      const finalURL = page.url();
      expect(finalURL).not.toContain('/workspace/create');
      expect(finalURL).toContain('/console');

      // Vérif côté serveur via /api/user.me avec le JWT humain.
      const userMeData = await page.evaluate(async () => {
        const token = localStorage.getItem('auth_token');
        if (!token) return { error: 'no auth_token in localStorage' };
        const r = await fetch('/api/user.me', { headers: { Authorization: 'Bearer ' + token }});
        if (!r.ok) return { error: `user.me returned ${r.status}` };
        return await r.json();
      });
      expect(userMeData.error).toBeUndefined();
      expect(userMeData.user).toBeDefined();
      expect(userMeData.user.email).toBe(ownerEmail);
      expect(userMeData.user.type).toBe('user'); // human owner, pas api_key
      expect(Array.isArray(userMeData.workspaces)).toBe(true);
      const wsIds = (userMeData.workspaces as Array<string | { id: string }>).map((w) =>
        typeof w === 'string' ? w : w.id,
      );
      expect(wsIds).toContain(tenantID);
    } finally {
      await context.close();
    }
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
