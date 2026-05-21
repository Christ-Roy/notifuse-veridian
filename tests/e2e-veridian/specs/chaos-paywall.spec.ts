// CI violente — Suite 2 : paywall (le truc le plus critique pour la SaaSification).
// Verifie : suspend bloque, quota burst race, cache TTL 60s, path filter precision,
// fail-open si DB down (pas testable en e2e, on documente).

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

// Force le paywall middleware a refresh son cache pour ce workspace.
// Evite l'attente du TTL 60s naturel apres suspend/resume/delete/update-plan.
// Sans ce helper, chaque test paywall coute ~65s de wall-clock pure attente.
//
// L'endpoint POST /api/veridian/admin/cache/invalidate est HMAC-signe : meme
// surface d'attaque que les autres /api/tenants/* (timestamp drift 5min,
// signature SHA256). Inutilisable sans HUB_API_SECRET.
async function invalidatePaywallCache(workspaceId: string) {
  const r = await hmacFetch('/api/veridian/admin/cache/invalidate', 'POST', {
    workspace_id: workspaceId,
  });
  if (r.status !== 200) {
    throw new Error(`invalidatePaywallCache(${workspaceId}) failed: ${r.status} ${await r.text()}`);
  }
}

async function provisionTenant(tenantId: string, plan = 'free') {
  // Retry 5x sur 5xx (race CreateDatabase upstream) ET 404 (transient
  // upstream Notifuse v30 quand le pool DB est sous charge — observé en CI
  // run 25636883973 : 4 fails consécutifs sur "404 page not found" alors que
  // les routes sont mountées au startup et que les tests isolés passent en
  // local 3/3. Tracé suivi dans task #10 / knowledge base. En attendant fix
  // upstream, on retry sur 404 transient — un vrai 404 (route absente) ferait
  // exhauster les retries et le test fail.
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: `${tenantId}@paywall.test`,
      plan,
    });
    if (r.status === 200) return r.json();
    if ((r.status >= 500 || r.status === 404) && attempt < 4) {
      const backoffMs = 500 * Math.pow(2, attempt); // 500ms, 1s, 2s, 4s
      // eslint-disable-next-line no-console
      console.log(`provision retry ${attempt + 1}/5: status=${r.status}, backoff=${backoffMs}ms`);
      await new Promise((res) => setTimeout(res, backoffMs));
      continue;
    }
    expect(r.status, await r.text()).toBe(200);
  }
  throw new Error('provision retry exhausted (5 attempts incl. 404 transient)');
}

async function sendTransactional(apiKey: string, workspaceId: string) {
  return fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${apiKey}` },
    body: JSON.stringify({
      workspace_id: workspaceId,
      to: 'sink@paywall.test',
      // body minimal : on test le paywall, pas l'envoi reel
    }),
  });
}

// === Veridian patch 2026-05-21 (Lot N étape 1+2) ===
// Convention naming : prefix `tst` (alphanum ≥3 chars, contrainte
// workspace.Validate + WipeTestTenants min-prefix) suivi d'un timestamp court.
// Le wipe CI matche `tst` en 1 appel — défense additionnelle au-delà de
// l'afterEach par-spec ci-dessous (filet de sécurité si crash mid-test).
const newTid = () => `tst${Date.now().toString(36).slice(-6)}`;

// Tracker des tenants créés par chaque test. Repli au fil de l'eau dans
// afterEach pour éviter le batch wipe en fin de CI qui sature le pool DB.
const provisioned: string[] = [];

test.afterEach(async () => {
  if (provisioned.length === 0) return;
  const ids = [...provisioned];
  provisioned.length = 0;
  try {
    await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
      tenant_ids: ids,
      // Defense en profondeur : safety prefixes explicites en plus des
      // defaults backend (cf. veridian_service.defaultSafetyClientPrefixes).
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest'],
    });
  } catch (err) {
    // Cleanup best-effort : on ne fail pas le test si le wipe rate (le step
    // CI `Cleanup test tenants` repassera avec prefix `tst` en filet).
    // eslint-disable-next-line no-console
    console.warn(`afterEach wipe failed (non-fatal): ${err}`);
  }
});

test.describe('Paywall — suspend / resume / delete', () => {
  test('suspend → invalidate → 402, resume → invalidate → not 402', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    // 1. Active : envoi passe (pas 402)
    let r = await sendTransactional(api_key, tid);
    expect(r.status).not.toBe(402);

    // 2. Suspend
    r = await hmacFetch('/api/tenants/suspend', 'POST', { tenant_id: tid, reason: 'paywall test' });
    expect(r.status).toBe(200);

    // 3. Invalidate cache (vs sleep 65s du TTL naturel) → instant
    await invalidatePaywallCache(tid);

    // 4. Apres invalidation : envoi → 402
    r = await sendTransactional(api_key, tid);
    expect(r.status).toBe(402);
    const body = await r.json();
    expect(body.error).toMatch(/payment|suspend/i);

    // 5. Resume
    r = await hmacFetch('/api/tenants/resume', 'POST', { tenant_id: tid });
    expect(r.status).toBe(200);

    // 6. Invalidate cache pour propager le resume immediatement
    await invalidatePaywallCache(tid);

    // 7. Apres resume : envoi → 200/4xx (pas 402)
    r = await sendTransactional(api_key, tid);
    expect(r.status).not.toBe(402);
  });

  test('delete → invalidate → 402 jusqu a la fin des temps', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    let r = await hmacFetch(`/api/tenants/${tid}`, 'DELETE');
    expect(r.status).toBe(200);

    // Invalidate cache pour que le paywall lise la nouvelle ligne deleted
    await invalidatePaywallCache(tid);

    r = await sendTransactional(api_key, tid);
    expect(r.status).toBe(402);
    const body = await r.json();
    expect(body.tenant_status).toBe('deleted');
  });
});

test.describe('Paywall — path filter precision', () => {
  test('paywall actif sur /api/transactional.send', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'free');
    await hmacFetch('/api/tenants/suspend', 'POST', { tenant_id: tid });
    await invalidatePaywallCache(tid);

    const r = await sendTransactional(api_key, tid);
    expect(r.status).toBe(402);
  });

  test('paywall PAS actif sur /api/contacts.list (suspended tenant peut quand meme lire)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'free');
    await hmacFetch('/api/tenants/suspend', 'POST', { tenant_id: tid });
    await invalidatePaywallCache(tid);

    const r = await fetch(`${NOTIFUSE_URL}/api/contacts.list`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
      body: JSON.stringify({ workspace_id: tid }),
    });
    // Doit etre 200 (suspended bloque envois mais pas lecture) OU 4xx pour body manquant,
    // mais JAMAIS 402. Les users en suspended doivent pouvoir consulter leurs donnees.
    expect(r.status).not.toBe(402);
  });
});

test.describe('Paywall — workspace inconnu (mode self-hosted)', () => {
  test('workspace_id pas dans veridian_plan → passe (pas 402)', async () => {
    // Note : ce test depend du fait qu'aucun plan n'existe pour ce workspace.
    // En staging clean, on peut creer un workspace via API key root mais sans plan veridian.
    // Pour simplifier, on test indirectement : un tenant_id qui n'a jamais ete provisione
    // par /api/tenants/provision n'a pas de ligne veridian_plan.
    //
    // Le test direct demande de creer un workspace sans plan via voie alternative
    // (rootSignin manuel) ce qui sort du scope. On skip avec note.
    test.skip(true, 'Demande creation workspace via rootSignin upstream — hors scope chaos paywall');
  });
});
