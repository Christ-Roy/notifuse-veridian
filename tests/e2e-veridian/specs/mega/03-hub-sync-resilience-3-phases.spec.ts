// === MEGA E2E — Spec 03 — Hub sync resilience 3 phases ===
//
// Contribution Notifuse au ticket Hub MEGA-E2E.
// Spec parent : ../veridian-hub/todo/2026-05-23-MEGA-E2E-post-commercialisation.md
// Code         : internal/http/middleware/veridian_paywall.go (V39 gating)
//                internal/domain/veridian.go (EvaluateHubSyncStatus)
//
// V39 resilience billing niveau 1 : middleware paywall evalue last_hub_sync_at
// et bascule en 3 phases :
//
//   - fresh (< 24h)        : mode normal, writes + reads passent
//   - stale (24h..72h)     : grace optimistic, log warn rate-limite, continue
//   - dead  (> 72h)        : writes → 503 hub_sync_dead + Retry-After, reads OK
//
// Couverture MEGA vs spec hub-sync-resilience.spec.ts existante :
//   - Cette MEGA spec ajoute les invariants metier sur la RECOVERY complete
//     (touch → fresh → write reussit → workflow utilisable de bout en bout)
//   - L existante (hub-sync-resilience.spec.ts) couvre le contrat statique
//     route-par-route. Cette MEGA cible le **journey** (decouverte → panne →
//     fix → reprise).
//
// Manipulation last_hub_sync_at : pas d endpoint admin Notifuse public →
//   - Mode "lite" (defaut) : valide les invariants statiques (fresh +
//     /api/setup.status systeme exempt). Tourne partout.
//   - Mode "full" (STAGING_DB_CONTAINER ou STAGING_DB_PSQL_URL) : simule la
//     phase dead via UPDATE direct + verifie recovery via touch.
//
// Conventions : tids `mega03-<RUN_STAMP>-<slug>`, afterEach wipe, tag @mega.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;
const STAGING_DB_PSQL_URL = process.env.STAGING_DB_PSQL_URL;
const STAGING_DB_CONTAINER = process.env.STAGING_DB_CONTAINER;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

const RUN_STAMP = Date.now().toString(36).slice(-6);

// Probe DB-manip availability (pattern hub-sync-resilience.spec.ts)
let DB_MANIP_AVAILABLE = false;
let DB_MANIP_SKIP_REASON = '';

if (STAGING_DB_PSQL_URL) {
  DB_MANIP_AVAILABLE = true;
} else if (STAGING_DB_CONTAINER) {
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const { execSync } = require('child_process') as typeof import('child_process');
    execSync(`docker exec ${STAGING_DB_CONTAINER} echo ok`, {
      stdio: 'ignore',
      timeout: 5_000,
    });
    DB_MANIP_AVAILABLE = true;
  } catch {
    DB_MANIP_SKIP_REASON =
      `STAGING_DB_CONTAINER=${STAGING_DB_CONTAINER} set mais docker exec inaccessible — ` +
      `MEGA-03 phase dead skip (couvert par hub-sync-resilience.spec.ts + unit middleware test).`;
  }
} else {
  DB_MANIP_SKIP_REASON =
    'Ni STAGING_DB_PSQL_URL ni STAGING_DB_CONTAINER set — MEGA-03 phase dead skip ' +
    '(invariants statiques toujours valides). Pour activer en local : ' +
    'STAGING_DB_CONTAINER=notifuse-staging-db cote runner self-hosted.';
}

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

async function readBody(r: Response): Promise<{ raw: string; json: () => any }> {
  const raw = await r.text();
  return { raw, json: () => (raw ? JSON.parse(raw) : null) };
}

async function provisionTenant(tenantId: string, ownerEmail: string, plan = 'pro') {
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

test.describe('@mega @regression MEGA-03 — hub sync resilience 3 phases', () => {
  test('phase FRESH : tenant juste provisioned → writes nominal + /setup.status 200', async () => {
    const tid = `m03${RUN_STAMP}f`;
    const ownerEmail = `e2e-mega-03-fresh-${RUN_STAMP}@e2e.veridian.site`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, ownerEmail, 'pro');

    // /api/setup.status est systeme : toujours 200, jamais gate paywall.
    const setupResp = await fetch(`${NOTIFUSE_URL}/api/setup.status`);
    expect(setupResp.status, 'setup.status doit etre exempt du middleware paywall').toBe(200);

    // Write transactional sur tenant fresh : pas 503, pas 402.
    const send = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
      body: JSON.stringify({ workspace_id: tid, to: 'sink-fresh@e2e.veridian.site' }),
    });
    expect(send.status, 'fresh tenant writes must succeed').not.toBe(402);
    expect(send.status, 'fresh tenant must not trigger hub_sync_dead').not.toBe(503);
  });

  test('admin routes /api/veridian/* exemptees du gate hub_sync (toujours invalidable)', async () => {
    const tid = `m03${RUN_STAMP}a`;
    const ownerEmail = `e2e-mega-03-admin-${RUN_STAMP}@e2e.veridian.site`;
    provisioned.push(tid);
    await provisionTenant(tid, ownerEmail, 'pro');

    // Invariant code : isHubSyncWriteBlock retourne false pour /api/veridian/*.
    // Donc ce call passe meme si on simulait un dead state (utile pour le Hub
    // qui doit pouvoir reagir).
    const r = await hmacFetch('/api/veridian/admin/cache/invalidate', 'POST', { workspace_id: tid });
    expect(r.status, 'admin invalidate cache toujours autorisee').toBe(200);
  });

  // === Fix 2026-05-30 : phase DEAD ne bloque PLUS les writes ===
  // Notifuse stand-alone — un last_hub_sync_at vieux n'a aucun effet bloquant.
  test('phase "ancienne" (>72h) : writes PASSENT (plus de blocage hub_sync_dead)', async () => {
    test.skip(!DB_MANIP_AVAILABLE, DB_MANIP_SKIP_REASON);

    const tid = `m03${RUN_STAMP}d`;
    const ownerEmail = `e2e-mega-03-dead-${RUN_STAMP}@e2e.veridian.site`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, ownerEmail, 'pro');

    await execStagingSQL(
      `UPDATE veridian_plan SET last_hub_sync_at = NOW() - INTERVAL '73 hours' WHERE workspace_id = '${tid}'`,
    );
    await invalidatePaywallCache(tid);

    // Write → JAMAIS 503 hub_sync_dead. L'envoi est indépendant du Hub.
    const send = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
      body: JSON.stringify({ workspace_id: tid, to: 'sink-dead@e2e.veridian.site' }),
    });
    expect(send.status, 'write ne doit jamais etre 503 hub_sync_dead').not.toBe(503);
    const sendBody = await readBody(send);
    const data = sendBody.json() ?? {};
    expect(data.code ?? data.error_code, `pas de hub_sync_dead, body: ${sendBody.raw}`).not.toBe('hub_sync_dead');

    const read = await bearerFetch(`/api/contacts.list?workspace_id=${tid}&limit=1`, 'GET', api_key);
    expect(read.status, 'reads passent').not.toBe(503);
  });

  // Test RECOVERY supprimé 2026-05-30 : plus de blocage DEAD à récupérer.
});
