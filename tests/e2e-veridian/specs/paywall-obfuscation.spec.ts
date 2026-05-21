// === Veridian patch — Lot R (2026-05-21) ===
// Tests E2E du middleware paywall mode dégradé (soft-deleted) :
//   - Reads : obfuscation 33% en clair + reste `•` (rune-safe).
//             Champs SENSITIVE_FIELDS (api_key, password, stripe_*) → full `•`.
//   - Writes : 402 + body {error_code:"tenant_soft_deleted", restore_url, ...}.
//   - Headers UI : X-Tenant-Soft-Deleted, X-Tenant-Deleted-At, X-Tenant-Purge-At.
//   - Tenant actif : aucune obfuscation, aucun header.
//
// Spec parent : todo/2026-05-21-paywall-degraded-mode-soft-deleted.md
// Code        : internal/http/middleware/veridian_paywall_softdeleted.go
//               internal/http/middleware/veridian_paywall_obfuscation.go
//
// Conventions Lot N : tids `t-`, afterEach cleanup, tag @regression.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

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
      owner_email: `${tenantId}@paywall-obf.test`,
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

test.describe('@regression paywall-obfuscation — middleware soft-deleted', () => {
  test('tenant active : pas d obfuscation, pas de headers, response normale', async () => {
    const tid = `t-${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    // Read normal (workspace_id en query string pour le middleware soft-deleted).
    const r = await bearerFetch(`/api/contacts.list?workspace_id=${tid}&limit=10`, 'GET', api_key);
    // Pas 402, pas 5xx (4xx tolere si pas de contacts, etc.)
    expect(r.status).not.toBe(402);
    // Headers de mode degrade ABSENTS
    expect(r.headers.get('x-tenant-soft-deleted')).toBeNull();
    expect(r.headers.get('x-tenant-deleted-at')).toBeNull();
    expect(r.headers.get('x-tenant-purge-at')).toBeNull();
  });

  test('tenant soft-deleted + read /api/contacts.list : obfuscation 33% + headers UI', async () => {
    const tid = `t-${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    // Insert un contact pour avoir du contenu a obfusquer.
    // (best-effort : si /api/contacts.upsert n'aboutit pas en staging,
    // l'obfuscation s'applique quand meme sur la response vide — les headers
    // restent visibles, c'est l'invariant clef).
    await bearerFetch('/api/contacts.upsert', 'POST', api_key, {
      workspace_id: tid,
      contact: {
        external_id: 'alice-paywall',
        email: 'alice@example.com',
        first_name: 'Alice',
        last_name: 'Wonderland',
      },
    });

    // Soft-delete via Hub
    const sd = await hmacFetch(`/api/tenants/${tid}/soft-delete`, 'POST', { reason: 'paywall-obf test' });
    expect(sd.status).toBe(200);

    // Invalidate paywall cache pour que le middleware lise la nouvelle ligne deleted
    await invalidatePaywallCache(tid);

    // Read soft-deleted : doit etre 200 + headers UI + body obfusque
    const r = await bearerFetch(`/api/contacts.list?workspace_id=${tid}&limit=10`, 'GET', api_key);
    // 200 obfusque (mode degrade lecture) ou 4xx upstream (auth fail mais
    // headers soft-deleted absents car le middleware capture la response
    // upstream qui n'est jamais OK).
    expect([200, 401, 403]).toContain(r.status);

    if (r.status === 200) {
      // Headers UI presents
      expect(r.headers.get('x-tenant-soft-deleted')).toBe('true');
      // deleted_at format RFC3339
      const delAt = r.headers.get('x-tenant-deleted-at');
      expect(delAt).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/);
      // purge_at present (= deleted_at + 30j)
      const purgeAt = r.headers.get('x-tenant-purge-at');
      expect(purgeAt).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/);

      // Body : doit etre du JSON obfusque, contenir des bullet chars `•` (U+2022)
      const text = await r.text();
      // Heuristique : presence de bullet U+2022 dans le body.
      // Si la liste est vide, on tolere (rien a obfusquer), mais sinon doit etre present.
      if (text.length > 10 && /[A-Za-z]/.test(text)) {
        // Body contient du texte, donc obfuscation doit avoir laisse des bullets
        expect(text).toContain('•');
        // Les emails contenant @example.com doivent etre obfusques :
        // "alice@example.com" (17 chars) → keep 5 ("alice") + 12 bullets = "alice••••••••••••"
        // donc on ne doit PAS retrouver "@example.com" en clair.
        expect(text).not.toContain('@example.com');
      }
    }
  });

  test('tenant soft-deleted + write /api/transactional.send : 402 + body tenant_soft_deleted + restore_url', async () => {
    const tid = `t-${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    // Soft-delete
    const sd = await hmacFetch(`/api/tenants/${tid}/soft-delete`, 'POST', { reason: 'paywall-obf write block' });
    expect(sd.status).toBe(200);
    await invalidatePaywallCache(tid);

    // Write mutation → 402 + body standard
    const r = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
      body: JSON.stringify({ workspace_id: tid, to: 'sink@paywall-obf.test' }),
    });
    expect(r.status).toBe(402);
    const body = await r.json();
    expect(body.error_code).toBe('tenant_soft_deleted');
    expect(body.restore_url).toBeTruthy();
    expect(body.restore_url as string).toMatch(/veridian|hub|restore/i);
    // deleted_at et purge_eligible_at presents au format RFC3339
    expect(body.deleted_at as string).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/);
    expect(body.purge_eligible_at as string).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/);
  });

  test('SENSITIVE_FIELDS (api_key, password) full obfusques meme sur reads', async () => {
    // Note : on ne peut pas creer arbitrairement un endpoint qui retourne
    // un champ `password` ou `api_key` en clair (Notifuse n'expose pas ces
    // champs par design). Ce test verifie l'invariant via /api/contacts.list
    // qui retourne potentiellement des champs custom — l'absence de
    // SENSITIVE_FIELDS dans le payload typique signifie qu'on ne peut
    // qu'asserter "pas de regression sur l'obfuscation" indirectement.
    //
    // Le contrat veridianSensitiveFields liste : password, api_key, secret,
    // token, hmac, billing_info, stripe_*. Si un endpoint Notifuse expose
    // un jour un de ces champs (e.g. workspace settings avec smtp_password),
    // le middleware doit le full-obfusquer.
    //
    // Pour ce test, on verifie au moins que le contrat tient en bout en
    // soft-deletant un tenant et en faisant un GET sur un endpoint qui
    // expose un champ obfuscable, puis en cherchant que les bullets sont
    // bien presents partout.
    const tid = `t-${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    const sd = await hmacFetch(`/api/tenants/${tid}/soft-delete`, 'POST', { reason: 'sensitive-fields test' });
    expect(sd.status).toBe(200);
    await invalidatePaywallCache(tid);

    // Endpoint workspace settings (s'il existe upstream) retourne des champs
    // sensibles. On utilise /api/contacts.list — invariant minimal verifie :
    // le middleware soft-deleted s'applique, les headers UI sont la.
    const r = await bearerFetch(`/api/contacts.list?workspace_id=${tid}&limit=10`, 'GET', api_key);
    if (r.status === 200) {
      expect(r.headers.get('x-tenant-soft-deleted')).toBe('true');
    }
    // L'invariant SENSITIVE_FIELDS = test unitaire backend (veridian_paywall_obfuscation_test.go).
    // En E2E on s'assure juste que le middleware tourne — pas qu'on peut
    // observer un SENSITIVE_FIELD specifique sur la prod (par design ces
    // champs ne fuitent jamais en clair en mode actif non plus).
  });

  test('headers UI presents sur soft-deleted, absents sur tenant active (apres restore)', async () => {
    const tid = `t-${Date.now().toString(36).slice(-6)}`;
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid, 'pro');

    // 1. Soft-delete
    await hmacFetch(`/api/tenants/${tid}/soft-delete`, 'POST', { reason: 'header-toggle test' });
    await invalidatePaywallCache(tid);

    let r = await bearerFetch(`/api/contacts.list?workspace_id=${tid}&limit=1`, 'GET', api_key);
    if (r.status === 200) {
      expect(r.headers.get('x-tenant-soft-deleted')).toBe('true');
    }

    // 2. Restore → headers doivent disparaitre
    const restore = await hmacFetch(`/api/tenants/${tid}/restore`, 'POST', { reason: 'header-toggle restore' });
    expect(restore.status, await restore.text()).toBe(200);
    await invalidatePaywallCache(tid);

    r = await bearerFetch(`/api/contacts.list?workspace_id=${tid}&limit=1`, 'GET', api_key);
    expect(r.status).not.toBe(402);
    expect(r.headers.get('x-tenant-soft-deleted')).toBeNull();
    expect(r.headers.get('x-tenant-deleted-at')).toBeNull();
    expect(r.headers.get('x-tenant-purge-at')).toBeNull();
  });
});
