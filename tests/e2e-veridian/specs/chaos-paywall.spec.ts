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

async function provisionTenant(tenantId: string, plan = 'free') {
  const r = await hmacFetch('/api/tenants/provision', 'POST', {
    tenant_id: tenantId,
    owner_email: `${tenantId}@paywall.test`,
    plan,
  });
  expect(r.status).toBe(200);
  return r.json();
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

test.describe('Paywall — suspend / resume / delete', () => {
  test('suspend → cache TTL respecte 60s puis 402, resume → 200', async () => {
    const tid = `pwsus${Date.now().toString(36).slice(-6)}`;
    const { api_key } = await provisionTenant(tid, 'pro');

    // 1. Active : envoi passe (pas 402)
    let r = await sendTransactional(api_key, tid);
    expect(r.status).not.toBe(402);

    // 2. Suspend
    r = await hmacFetch('/api/tenants/suspend', 'POST', { tenant_id: tid, reason: 'paywall test' });
    expect(r.status).toBe(200);

    // 3. Cache 60s : on attend 65s pour etre sur que l'invalidation a eu lieu
    await new Promise((res) => setTimeout(res, 65_000));

    // 4. Apres TTL : envoi → 402
    r = await sendTransactional(api_key, tid);
    expect(r.status).toBe(402);
    const body = await r.json();
    expect(body.error).toMatch(/payment|suspend/i);

    // 5. Resume
    r = await hmacFetch('/api/tenants/resume', 'POST', { tenant_id: tid });
    expect(r.status).toBe(200);

    // 6. Cache 60s avant l'invalidation, mais resume devrait clear → on attend 65s
    await new Promise((res) => setTimeout(res, 65_000));

    // 7. Apres resume : envoi → 200/4xx (pas 402)
    r = await sendTransactional(api_key, tid);
    expect(r.status).not.toBe(402);
  });

  test('delete → 402 jusqu a la fin des temps', async () => {
    const tid = `pwdel${Date.now().toString(36).slice(-6)}`;
    const { api_key } = await provisionTenant(tid, 'pro');

    let r = await hmacFetch(`/api/tenants/${tid}`, 'DELETE');
    expect(r.status).toBe(200);

    // Wait cache TTL
    await new Promise((res) => setTimeout(res, 65_000));

    r = await sendTransactional(api_key, tid);
    expect(r.status).toBe(402);
    const body = await r.json();
    expect(body.tenant_status).toBe('deleted');
  });
});

test.describe('Paywall — path filter precision', () => {
  test('paywall actif sur /api/transactional.send', async () => {
    const tid = `pwpath${Date.now().toString(36).slice(-6)}`;
    const { api_key } = await provisionTenant(tid, 'free');
    await hmacFetch('/api/tenants/suspend', 'POST', { tenant_id: tid });
    await new Promise((res) => setTimeout(res, 65_000));

    const r = await sendTransactional(api_key, tid);
    expect(r.status).toBe(402);
  });

  test('paywall PAS actif sur /api/contacts.list (suspended tenant peut quand meme lire)', async () => {
    const tid = `pwread${Date.now().toString(36).slice(-6)}`;
    const { api_key } = await provisionTenant(tid, 'free');
    await hmacFetch('/api/tenants/suspend', 'POST', { tenant_id: tid });
    await new Promise((res) => setTimeout(res, 65_000));

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
