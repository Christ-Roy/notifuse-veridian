// === Veridian patch — Couche 4 Bounce OAuth Hub (CONTRAT-HUB §6bis.8) ===
//
// Spec E2E mega-coverage du endpoint POST /api/sso/issue-magic-link, appele
// par le Hub apres un OAuth Google/Microsoft reussi pour delivrer un magic
// link self-contained Notifuse a l'user.
//
// Handler         : internal/http/veridian_sso_handler.go
// Unit tests      : internal/http/veridian_sso_handler_test.go (Agent A)
// Service         : internal/service/veridian_service.go:IssueMagicLinkForHub
// Spec contrat    : CONTRAT-HUB §6bis.8.3 + §6bis.8.5
// Auto-login fmt  : memory/reference_auto_login_url_format.md
//
// Scenarios couverts (10) :
//   1. Happy path : HMAC valide + body {hub_user_id, email} → 200 magic_link_url
//   2. Auto-login URL fonctionnel : navigate vers URL → user logge dans console
//   3. HMAC signature invalide → 401
//   4. HMAC drift timestamp > 5min → 401
//   5. HMAC headers manquants → 401
//   6. Body invalide : email manquant / hub_user_id manquant / JSON malforme → 400
//   7. Email sans `@` → 400 (email is invalid)
//   8. User inconnu Notifuse → 400 `user_not_in_app` (litteral, parse Hub)
//   9. Multi-workspace : user dans 2 workspaces → pick MAX(UpdatedAt)
//   10. Idempotence : 2 calls successifs → 2 magic_link_url frais distincts
//   11. Bonus : simulation flow OAuth complet Hub→Notifuse (HMAC + bounce)
//
// Conventions Lot N : tids `tst`, afterEach cleanup, tag @regression.
// PAS de `@prod-safe` : la spec provisionne des tenants.

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

async function provisionTenant(tenantId: string, ownerEmail: string, plan = 'free') {
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
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

// === Cleanup tracker — supprime les tenants provisionnes a la fin de chaque test ===
const provisioned: string[] = [];

test.afterEach(async () => {
  if (provisioned.length === 0) return;
  const ids = [...provisioned];
  provisioned.length = 0;
  try {
    await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
      tenant_ids: ids,
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest'],
      // include_orphans:false — incident memory : true crash le container staging
      // (DROP DB sur goroutines actives). Cf. reference_admin_tenants_listing_api.md
      include_orphans: false,
    });
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn(`afterEach wipe failed (non-fatal): ${err}`);
  }
});

// Helper : nouveau tenant id court + unique
let _tidCounter = 0;
const newTid = () => `tst${Date.now().toString(36).slice(-6)}${(_tidCounter++).toString(36)}`;

// =====================================================================
// === Test 1 : Happy path — HMAC valide + user existant → 200 ===
// =====================================================================

test.describe('@regression sso-issue-magic-link — POST /api/sso/issue-magic-link', () => {
  test('happy path : HMAC valide + user owner d un workspace → 200 magic_link_url', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const email = `${tid}@sso.test`;
    const hubUserID = `hub-user-${tid}`;

    // Provision un tenant frais (user owner cree implicitement)
    await provisionTenant(tid, email, 'free');

    const r = await hmacFetch('/api/sso/issue-magic-link', 'POST', {
      hub_user_id: hubUserID,
      email,
    });
    const raw = await r.text();
    expect(r.status, raw).toBe(200);
    const body = JSON.parse(raw);

    // Format documente : { magic_link_url: "https://.../veridian/auto-login?token=..." }
    expect(body.magic_link_url).toBeTruthy();
    expect(body.magic_link_url).toMatch(/\/veridian\/auto-login\?token=/);
    // Token HMAC self-contained : <base64-claims>.<hmac-sha256>
    // PAS un format `?email=&code=` (cf. memory reference_auto_login_url_format)
    expect(body.magic_link_url).not.toMatch(/[?&]email=/);
    expect(body.magic_link_url).not.toMatch(/[?&]code=/);
  });

  // =====================================================================
  // === Test 2 : Auto-login URL marche → user logge dans Notifuse console
  // =====================================================================

  test('auto-login URL retourne → navigate → user logge dans /console (vrai navigateur)', async ({ page }) => {
    const tid = newTid();
    provisioned.push(tid);
    const email = `${tid}@sso.test`;
    const hubUserID = `hub-user-${tid}`;

    await provisionTenant(tid, email, 'pro');

    const r = await hmacFetch('/api/sso/issue-magic-link', 'POST', {
      hub_user_id: hubUserID,
      email,
    });
    expect(r.status).toBe(200);
    const { magic_link_url } = await r.json();
    expect(magic_link_url).toBeTruthy();

    // Click auto-login URL en vrai navigateur Chromium
    await page.goto(magic_link_url);

    // Le frontend Notifuse SPA route vers /console (root) apres echange token
    await page.waitForURL(/\/console$/, { timeout: 30_000 });
    await page.waitForTimeout(2000); // hydrate

    // auth_token en localStorage = preuve auth reussie
    const authToken = await page.evaluate(() => localStorage.getItem('auth_token'));
    expect(authToken).toBeTruthy();

    // Pas de redirect vers /signin = preuve auth OK
    expect(page.url()).not.toContain('/signin');

    // Verification que le user est bien dans le workspace cible via API workspaces.members
    const membersRes = await page.evaluate(
      async ({ url, ws, token }) => {
        const res = await fetch(`${url}/api/workspaces.members?id=${ws}`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        return { status: res.status, body: await res.json() };
      },
      { url: NOTIFUSE_URL, ws: tid, token: authToken },
    );
    expect(membersRes.status).toBe(200);
    const members = Array.isArray(membersRes.body)
      ? membersRes.body
      : membersRes.body.members || [];
    const member = members.find(
      (m: { email?: string; user?: { email?: string } }) =>
        m.email === email || m.user?.email === email,
    );
    expect(member).toBeTruthy();
  });

  // =====================================================================
  // === Test 3 : HMAC signature invalide → 401 ===
  // =====================================================================

  test('HMAC signature invalide → 401 (service jamais appele)', async () => {
    const tid = newTid();
    const raw = JSON.stringify({
      hub_user_id: `hub-user-${tid}`,
      email: `${tid}@sso.test`,
    });
    const ts = Date.now().toString();
    const wrongSig = crypto
      .createHmac('sha256', 'TOTALLY_WRONG_SECRET_NOT_THE_HUB_ONE')
      .update(`${ts}.${raw}`)
      .digest('hex');

    const r = await fetch(`${NOTIFUSE_URL}/api/sso/issue-magic-link`, {
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

  // =====================================================================
  // === Test 4 : HMAC drift timestamp > 5min → 401 (anti-replay) ===
  // =====================================================================

  test('HMAC timestamp drift > 5min → 401 (anti-replay)', async () => {
    const tid = newTid();
    const raw = JSON.stringify({
      hub_user_id: `hub-user-${tid}`,
      email: `${tid}@sso.test`,
    });
    // 10 minutes dans le passe — au-dela du seuil 5min du middleware HMAC
    const stale = (Date.now() - 10 * 60 * 1000).toString();
    const sig = crypto
      .createHmac('sha256', HUB_API_SECRET)
      .update(`${stale}.${raw}`)
      .digest('hex');

    const r = await fetch(`${NOTIFUSE_URL}/api/sso/issue-magic-link`, {
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

  // =====================================================================
  // === Test 5 : HMAC headers manquants → 401 ===
  // =====================================================================

  test('headers HMAC manquants (X-Veridian-Hub-Signature) → 401', async () => {
    const raw = JSON.stringify({
      hub_user_id: 'hub-user-x',
      email: 'someone@sso.test',
    });
    const r = await fetch(`${NOTIFUSE_URL}/api/sso/issue-magic-link`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: raw,
    });
    expect([401, 403]).toContain(r.status);
  });

  test('header X-Veridian-Timestamp manquant → 401', async () => {
    const raw = JSON.stringify({
      hub_user_id: 'hub-user-x',
      email: 'someone@sso.test',
    });
    const sig = crypto
      .createHmac('sha256', HUB_API_SECRET)
      .update(`${Date.now()}.${raw}`)
      .digest('hex');
    const r = await fetch(`${NOTIFUSE_URL}/api/sso/issue-magic-link`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': sig,
        // X-Veridian-Timestamp omis
      },
      body: raw,
    });
    expect([401, 403]).toContain(r.status);
  });

  // =====================================================================
  // === Test 6 : Body invalide — champs manquants / JSON malforme → 400 ===
  // =====================================================================

  test('body : email manquant → 400 (hub parser-friendly)', async () => {
    const r = await hmacFetch('/api/sso/issue-magic-link', 'POST', {
      hub_user_id: 'hub-user-no-email',
    });
    expect(r.status).toBe(400);
    const body = await r.json();
    // Le handler expose le champ manquant dans details.missing
    expect(JSON.stringify(body)).toMatch(/email/i);
  });

  test('body : hub_user_id manquant → 400', async () => {
    const r = await hmacFetch('/api/sso/issue-magic-link', 'POST', {
      email: 'orphan@sso.test',
    });
    expect(r.status).toBe(400);
    const body = await r.json();
    expect(JSON.stringify(body)).toMatch(/hub_user_id/i);
  });

  test('body : les 2 champs manquants → 400 + mentionne les 2', async () => {
    const r = await hmacFetch('/api/sso/issue-magic-link', 'POST', {});
    expect(r.status).toBe(400);
    const body = JSON.stringify(await r.json());
    expect(body).toMatch(/email/i);
    expect(body).toMatch(/hub_user_id/i);
  });

  test('body : JSON malforme → 400', async () => {
    // raw non-JSON, signe manuellement
    const raw = 'not-json-at-all-{{{';
    const ts = Date.now().toString();
    const sig = crypto
      .createHmac('sha256', HUB_API_SECRET)
      .update(`${ts}.${raw}`)
      .digest('hex');
    const r = await fetch(`${NOTIFUSE_URL}/api/sso/issue-magic-link`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': sig,
        'X-Veridian-Timestamp': ts,
      },
      body: raw,
    });
    expect(r.status).toBe(400);
  });

  // =====================================================================
  // === Test 7 : Email sans @ → 400 ===
  // =====================================================================

  test('email sans `@` → 400 (email is invalid)', async () => {
    const r = await hmacFetch('/api/sso/issue-magic-link', 'POST', {
      hub_user_id: 'hub-user-bad-email',
      email: 'notanemail',
    });
    expect(r.status).toBe(400);
    const body = JSON.stringify(await r.json());
    expect(body).toMatch(/email/i);
  });

  // =====================================================================
  // === Test 8 : User non membre du workspace → 400 user_not_in_app ===
  // === (champ `error` litteral parse par le Hub bounce-apps.ts:228) ===
  // =====================================================================

  test('user inconnu de Notifuse → 400 `error:"user_not_in_app"` (litteral, parse Hub)', async () => {
    // Email qui n'existe pas dans Notifuse (jamais provisionne)
    const ghostEmail = `tstghost${Date.now().toString(36).slice(-6)}@sso.test`;
    const r = await hmacFetch('/api/sso/issue-magic-link', 'POST', {
      hub_user_id: 'hub-user-ghost',
      email: ghostEmail,
    });
    expect(r.status).toBe(400);
    const body = await r.json();

    // Contrat strict : le Hub (bounce-apps.ts:228) parse `error` litteral
    // "user_not_in_app". DOIT etre exact, pas un message humain.
    expect(body.error).toBe('user_not_in_app');
    // Code redondant pour clients qui preferent le champ `code`
    expect(body.code).toBe('user_not_in_app');
    // Details.hint utile pour le Hub UI ("no workspace for this hub_user_id")
    expect(body.details).toBeTruthy();
    expect(JSON.stringify(body.details)).toMatch(/workspace|hub_user_id/i);
  });

  test('user existe mais sans workspace (impossible en pratique post-fix) → 400 user_not_in_app', async () => {
    // Edge case theorique : le service IssueMagicLinkForHub renvoie ErrUserNotInApp
    // si user.Type != UserTypeUser (ex : api_key) ou si zero workspace.
    // Mais en pratique Notifuse cree toujours user + workspace ensemble.
    // Test couvert au niveau unit (TestIssueMagicLinkForHub_ZeroWorkspaces_ErrUserNotInApp).
    // Ici on documente : un user "ghost" donne le meme code que zero workspace.
    test.skip(
      true,
      'Edge case couvert au niveau unit test Go (orphan user/api_key user). ' +
        'En E2E, provisionTenant cree toujours user + workspace ensemble.',
    );
  });

  // =====================================================================
  // === Test 9 : Multi-workspace — user dans 2 workspaces → MAX(UpdatedAt) ===
  // =====================================================================

  test('user dans 2 workspaces → magic_link_url pointe vers le dernier actif (MAX UpdatedAt)', async () => {
    // Pattern discovery-by-email : meme owner_email sur 2 provisions distincts
    const email = `tstmulti${Date.now().toString(36).slice(-6)}@sso.test`;
    const tid1 = newTid();
    const tid2 = newTid();
    provisioned.push(tid1, tid2);

    await provisionTenant(tid1, email, 'free');
    // Pause pour assurer que tid2 a un UpdatedAt strictement posterieur
    await new Promise((res) => setTimeout(res, 1500));
    await provisionTenant(tid2, email, 'pro');

    const r = await hmacFetch('/api/sso/issue-magic-link', 'POST', {
      hub_user_id: `hub-user-multi-${email}`,
      email,
    });
    const raw = await r.text();
    expect(r.status, raw).toBe(200);
    const { magic_link_url } = JSON.parse(raw);
    expect(magic_link_url).toMatch(/\/veridian\/auto-login\?token=/);

    // Decode le token base64 (claims = {w, e, i, x}) pour verifier workspace_id
    // Format : <base64url-claims>.<hex-sha256>
    const tokenMatch = magic_link_url.match(/token=([^&]+)/);
    expect(tokenMatch).toBeTruthy();
    const [claimsB64] = (tokenMatch![1] as string).split('.');
    // base64url → base64 standard pour decode (padding tolere)
    const padded = claimsB64.replace(/-/g, '+').replace(/_/g, '/');
    const padding = padded.length % 4 === 0 ? '' : '='.repeat(4 - (padded.length % 4));
    const claimsJSON = Buffer.from(padded + padding, 'base64').toString('utf-8');
    const claims = JSON.parse(claimsJSON);

    // `w` (workspace) doit etre tid2 (le dernier actif via MAX UpdatedAt).
    expect(claims.w).toBe(tid2);
    // `e` (email) doit etre l'email user
    expect(claims.e).toBe(email);
  });

  // =====================================================================
  // === Test 10 : Idempotence — 2 calls successifs → tokens frais distincts ===
  // =====================================================================

  test('2 calls successifs avec meme body → 2 magic_link_url frais distincts', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const email = `${tid}@sso.test`;
    const hubUserID = `hub-user-${tid}`;

    await provisionTenant(tid, email, 'pro');

    const r1 = await hmacFetch('/api/sso/issue-magic-link', 'POST', {
      hub_user_id: hubUserID,
      email,
    });
    expect(r1.status).toBe(200);
    const { magic_link_url: url1 } = await r1.json();

    // Pause 1s pour avoir un timestamp `i` (issued_at) different dans les claims
    await new Promise((res) => setTimeout(res, 1100));

    const r2 = await hmacFetch('/api/sso/issue-magic-link', 'POST', {
      hub_user_id: hubUserID,
      email,
    });
    expect(r2.status).toBe(200);
    const { magic_link_url: url2 } = await r2.json();

    // Les 2 URLs doivent etre distinctes (tokens HMAC frais, issued_at different)
    expect(url1).not.toBe(url2);
    // Mais les 2 doivent pointer vers le meme workspace
    expect(url1).toMatch(/\/veridian\/auto-login\?token=/);
    expect(url2).toMatch(/\/veridian\/auto-login\?token=/);
  });

  // =====================================================================
  // === Test 11 : Simulation flow complet Hub→Notifuse (OAuth bounce) ===
  // =====================================================================

  test('simulation flow OAuth complet : Hub recoit user post-Google → bounce vers Notifuse via HMAC', async ({
    page,
  }) => {
    // Scenario reel :
    //  1. User clique "Sign in with Google" sur hub.veridian.site
    //  2. Google OAuth callback → Hub identifie email + hub_user_id
    //  3. Hub appelle POST /api/sso/issue-magic-link sur Notifuse en HMAC
    //  4. Notifuse renvoie magic_link_url (auto-login token TTL 60s)
    //  5. Hub redirige user vers magic_link_url → user logge dans Notifuse console
    //
    // On simule les steps 3-5 en E2E (les steps 1-2 sont coté Hub OAuth provider).

    const tid = newTid();
    provisioned.push(tid);
    const email = `${tid}@oauth-bounce.test`;
    const hubUserID = `hub-uuid-${tid}`;

    // Step 0 (setup) : user existe deja dans Notifuse (provision = owner workspace)
    await provisionTenant(tid, email, 'pro');

    // Step 3 (Hub → Notifuse) : HMAC call
    const r = await hmacFetch('/api/sso/issue-magic-link', 'POST', {
      hub_user_id: hubUserID,
      email,
    });
    expect(r.status, await r.clone().text()).toBe(200);
    const { magic_link_url } = await r.json();
    expect(magic_link_url).toMatch(/\/veridian\/auto-login\?token=/);

    // Step 4-5 (Hub redirect → user navigate) : vrai navigateur
    await page.goto(magic_link_url);
    await page.waitForURL(/\/console$/, { timeout: 30_000 });
    await page.waitForTimeout(1500);

    // Verification finale : user authentifie cote Notifuse console
    const authToken = await page.evaluate(() => localStorage.getItem('auth_token'));
    expect(authToken).toBeTruthy();
    expect(page.url()).not.toContain('/signin');
  });
});
