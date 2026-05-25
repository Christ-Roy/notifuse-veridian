// === Veridian patch — Lot R (2026-05-21) ===
// Tests E2E V39 résilience billing — gating 3 phases hub_sync :
//   - fresh (< 24h) : mode normal
//   - stale (24-72h) : grace optimistic, log warn, continue à servir
//   - dead (> 72h) : 503 + Retry-After + error_code=hub_sync_dead sur writes,
//                    reads passent best-effort
//
// Spec parent : todo/2026-05-21-resilience-billing-niveau-1.md
// Code        : internal/http/middleware/veridian_paywall.go (V39 gating)
//               internal/domain/veridian.go (EvaluateHubSyncStatus)
//
// Strategie de test :
//   1. Phase fresh : testable nativement (provision = touchHubSync → < 24h)
//   2. Phase dead : nécessite UPDATE last_hub_sync_at = NOW - 73h en DB.
//      Aucun endpoint admin Notifuse n'expose ça (cf. recon 2026-05-21).
//      → skip si STAGING_DB_PSQL_URL non set, sinon utiliser psql direct.
//      → en alternative pure E2E sans DB, on assert le contrat statique :
//         - /api/setup.status est exempté du middleware (toujours 200)
//         - /api/veridian/admin/* est exempté (toujours accessible Hub)
//
// Conventions Lot N : tids `tst`, afterEach cleanup, tag @regression.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;
// Manipulation DB pour forcer last_hub_sync_at. 2 modes supportés :
//  - STAGING_DB_PSQL_URL    : URL postgres directe (psql "url" -c ...)
//  - STAGING_DB_CONTAINER   : nom du container postgres → exécution via
//    `docker exec <container> psql -U postgres -d notifuse_system -c ...`.
//    Mode privilégié en CI self-hosted : le runner tourne sur dev-pub à
//    côté du container, pas besoin d'exposer le port 5432.
// Si aucun n'est défini, skip des tests qui manipulent la DB (le contrat
// reste couvert par les unit tests veridian_paywall_test.go).
const STAGING_DB_PSQL_URL = process.env.STAGING_DB_PSQL_URL;
const STAGING_DB_CONTAINER = process.env.STAGING_DB_CONTAINER;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

// Portabilité (ticket 2026-05-24-hub-sync-resilience-specs-portable.md) :
// `docker exec` ne marche que sur le runner self-hosted qui partage la
// machine avec le container postgres staging. Sur n'importe quelle autre
// machine (poste dev, runner GitHub-hosted), il faut détecter et skip
// proprement. On résout l'état réel au boot du fichier, AVANT que les
// tests ne s'exécutent : ENV présent ≠ ENV utilisable.
let DB_MANIP_AVAILABLE = false;
let DB_MANIP_SKIP_REASON = '';

if (STAGING_DB_PSQL_URL) {
  // URL psql directe : on assume qu'elle marche (sinon les tests fail bruyamment
  // sur le 1er execStagingSQL avec une erreur Postgres claire).
  DB_MANIP_AVAILABLE = true;
} else if (STAGING_DB_CONTAINER) {
  // Mode docker exec : ENV peut être set même si docker absent ou container
  // pas accessible (ex : on relance la suite localement avec un .env CI copié).
  // On probe une fois `docker exec <container> echo ok`.
  try {
    const { execSync } = require('child_process') as typeof import('child_process');
    execSync(`docker exec ${STAGING_DB_CONTAINER} echo ok`, {
      stdio: 'ignore',
      timeout: 5_000,
    });
    DB_MANIP_AVAILABLE = true;
  } catch {
    DB_MANIP_SKIP_REASON =
      `STAGING_DB_CONTAINER=${STAGING_DB_CONTAINER} set, mais docker exec a échoué ` +
      `(docker absent, container inaccessible, ou ce n'est pas le runner self-hosted). ` +
      `Les 3 tests qui manipulent last_hub_sync_at sont skip — le contrat reste couvert ` +
      `par unit tests veridian_paywall_test.go.`;
  }
} else {
  DB_MANIP_SKIP_REASON =
    'Ni STAGING_DB_PSQL_URL ni STAGING_DB_CONTAINER set. Les 3 tests qui manipulent ' +
    'last_hub_sync_at sont skip — le contrat reste couvert par unit tests ' +
    'veridian_paywall_test.go. Pour activer en local : SSH dev-pub + définir ' +
    'STAGING_DB_CONTAINER=notifuse-staging-db, ou expose-toi un tunnel psql vers staging.';
}

/**
 * Exécute un UPDATE SQL sur la DB system staging. Supporte les 2 modes
 * (STAGING_DB_PSQL_URL ou STAGING_DB_CONTAINER via docker exec).
 */
async function execStagingSQL(sql: string): Promise<void> {
  const { exec } = await import('child_process');
  const { promisify } = await import('util');
  const execp = promisify(exec);
  if (STAGING_DB_CONTAINER) {
    const escaped = sql.replace(/'/g, `'\\''`);
    await execp(
      `docker exec ${STAGING_DB_CONTAINER} psql -U postgres -d notifuse_system -c '${escaped}'`,
    );
  } else {
    await execp(`psql "${STAGING_DB_PSQL_URL}" -c "${sql}"`);
  }
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

async function provisionTenant(tenantId: string, plan = 'free') {
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: `${tenantId}@hub-sync.test`,
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

async function invalidatePaywallCache(workspaceId: string) {
  const r = await hmacFetch('/api/veridian/admin/cache/invalidate', 'POST', {
    workspace_id: workspaceId,
  });
  if (r.status !== 200) {
    throw new Error(`invalidatePaywallCache(${workspaceId}) failed: ${r.status} ${await r.text()}`);
  }
}

// === Cleanup tracker ===
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

test.describe('@regression hub-sync-resilience — V39 gating 3 phases', () => {
  test('tenant fresh (post-provision) : /api/setup.status 200, writes passent', async () => {
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    // /api/setup.status doit toujours 200 (route publique, pas de gating).
    const setupResp = await fetch(`${NOTIFUSE_URL}/api/setup.status`);
    expect(setupResp.status).toBe(200);

    // Write transactional sur tenant fresh : doit passer (pas 503, pas 402).
    const send = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
      body: JSON.stringify({ workspace_id: tid, to: 'sink@hub-sync.test' }),
    });
    // 200/202 si envoi OK, 4xx si body invalide upstream — mais PAS 402 (paywall)
    // ni 503 (hub_sync_dead).
    expect(send.status).not.toBe(402);
    expect(send.status).not.toBe(503);
  });

  test('routes admin /api/veridian/* exemptees meme en dead : invalidate-cache passe', async () => {
    // Invariant code : isHubSyncWriteBlock retourne false pour /api/veridian/*.
    // On verifie qu'un POST cache/invalidate sur un tenant arbitraire passe
    // toujours (le middleware HMAC est applique mais pas le gate hub_sync).
    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'pro');

    const r = await hmacFetch('/api/veridian/admin/cache/invalidate', 'POST', { workspace_id: tid });
    expect(r.status).toBe(200);
  });

  test('hub_sync_dead writes → 503 + Retry-After + error_code=hub_sync_dead, reads passent', async () => {
    test.skip(!DB_MANIP_AVAILABLE, DB_MANIP_SKIP_REASON);

    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    // === DB manipulation : last_hub_sync_at = NOW - 73h ===
    await execStagingSQL(
      `UPDATE veridian_plan SET last_hub_sync_at = NOW() - INTERVAL '73 hours' WHERE workspace_id = '${tid}'`,
    );

    // Invalider le cache paywall (sinon le timestamp obsolete reste en memoire 60s)
    await invalidatePaywallCache(tid);

    // 1. Write → 503 + Retry-After + error_code=hub_sync_dead
    const send = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
      body: JSON.stringify({ workspace_id: tid, to: 'sink@hub-sync.test' }),
    });
    expect(send.status).toBe(503);
    expect(send.headers.get('retry-after')).toBe('3600');
    const sendBody = await send.json();
    expect(sendBody.code ?? sendBody.error_code).toBe('hub_sync_dead');
    // Detail : last_hub_sync_at + retry_after_s
    const detail = sendBody.details ?? sendBody.detail ?? sendBody;
    expect(detail.last_hub_sync_at ?? detail.last_hub_sync).toBeTruthy();

    // 2. Read → passe (best-effort). /api/contacts.list est un read.
    const read = await bearerFetch(`/api/contacts.list?workspace_id=${tid}&limit=1`, 'GET', api_key);
    // 200 ou 4xx upstream tolere, mais PAS 503
    expect(read.status).not.toBe(503);
  });

  test('hub_sync_dead : /api/setup.status reste 200 (route systeme exempt)', async () => {
    test.skip(!DB_MANIP_AVAILABLE, DB_MANIP_SKIP_REASON);

    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    await provisionTenant(tid, 'pro');

    await execStagingSQL(
      `UPDATE veridian_plan SET last_hub_sync_at = NOW() - INTERVAL '73 hours' WHERE workspace_id = '${tid}'`,
    );
    await invalidatePaywallCache(tid);

    const setupResp = await fetch(`${NOTIFUSE_URL}/api/setup.status`);
    expect(setupResp.status).toBe(200);
  });

  test('hub_sync recovery : Touch tenant → last_hub_sync_at refresh → writes repassent', async () => {
    test.skip(!DB_MANIP_AVAILABLE, DB_MANIP_SKIP_REASON);

    const tid = `tst${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    // 1. Simuler dead
    await execStagingSQL(
      `UPDATE veridian_plan SET last_hub_sync_at = NOW() - INTERVAL '73 hours' WHERE workspace_id = '${tid}'`,
    );
    await invalidatePaywallCache(tid);

    let send = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
      body: JSON.stringify({ workspace_id: tid, to: 'pre-recovery@hub-sync.test' }),
    });
    expect(send.status).toBe(503);

    // 2. Touch via Hub (mutation refresh last_hub_sync_at)
    const touch = await hmacFetch(`/api/tenants/${tid}/touch`, 'POST', {});
    expect([200, 204]).toContain(touch.status);

    // 3. Invalider cache pour re-lire le plan refreshed
    await invalidatePaywallCache(tid);

    // 4. Write doit repasser (fresh)
    send = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
      body: JSON.stringify({ workspace_id: tid, to: 'post-recovery@hub-sync.test' }),
    });
    expect(send.status).not.toBe(503);
    expect(send.status).not.toBe(402);
  });
});
