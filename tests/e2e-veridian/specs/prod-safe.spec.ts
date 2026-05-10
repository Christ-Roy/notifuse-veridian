// Tests prod-safe : aucun side-effect, aucun tenant créé/modifié/supprimé.
// Tournés contre l'instance prod après deploy via `gh workflow run` avec
// deploy_prod=true. Le job `e2e-prod` filtre via --grep "@prod-safe".
//
// Règle d'or : un test est @prod-safe SEULEMENT si :
//   - il ne crée jamais de workspace/tenant/user (ni en succès ni en échec)
//   - il ne modifie aucune donnée existante
//   - il ne consomme pas de ressources mesurables (pas de boucle 500 envois)
//   - il vérifie un comportement de sécurité (HMAC reject, auth reject, etc.)
//   - il peut tourner 100x sans laisser de trace en DB ou logs anormaux
//
// Si tu hésites sur un test : il n'est PAS @prod-safe.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

function signHMAC(body: string, secret = HUB_API_SECRET, ts = Date.now().toString()) {
  const signature = crypto.createHmac('sha256', secret).update(`${ts}.${body}`).digest('hex');
  return { timestamp: ts, signature };
}

async function hmacFetch(
  path: string,
  method: string,
  body: object | null = null,
  overrides: Partial<{ timestamp: string; signature: string; secret: string }> = {},
) {
  const rawBody = body ? JSON.stringify(body) : '';
  const { timestamp: defaultTs, signature: defaultSig } = signHMAC(rawBody, overrides.secret);
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Veridian-Hub-Signature': overrides.signature ?? defaultSig,
      'X-Veridian-Timestamp': overrides.timestamp ?? defaultTs,
    },
    body: rawBody || undefined,
  });
}

test.describe('Prod-safe smoke — HTTP listener + base routes mountées', () => {
  test('@prod-safe HTTP serveur répond (setup.status existe)', async () => {
    const r = await fetch(`${NOTIFUSE_URL}/api/setup.status`);
    // Doit retourner 200 (setup ok) ou 4xx mais JAMAIS 5xx ni timeout
    expect(r.status).toBeLessThan(500);
  });

  test('@prod-safe route /api/transactional.send mountée (réponse non-404)', async () => {
    // Sans Authorization → 401 attendu. Le but : vérifier que la route existe.
    // Si on reçoit 404 littéral, c'est que le mux upstream n'est pas prêt.
    const r = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}',
    });
    expect(r.status).not.toBe(404);
    // 401 (auth manquante) ou 400 (body invalide) acceptables
    expect([400, 401]).toContain(r.status);
  });

  test('@prod-safe route /api/tenants/provision mountée (réponse non-404)', async () => {
    // Sans HMAC headers → 401. Vérifie que la route Veridian est mountée.
    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/provision`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}',
    });
    expect(r.status).not.toBe(404);
    expect(r.status).toBe(401);
  });
});

test.describe('Prod-safe HMAC sécurité — toutes adversaires (read-only, no tenant created)', () => {
  test('@prod-safe replay 6 min apres → 401 timestamp drift', async () => {
    const body = JSON.stringify({
      tenant_id: 'prodsafereplay',
      owner_email: 'r@prodsafe.test',
      plan: 'free',
    });
    const oldTs = (Date.now() - 6 * 60 * 1000).toString();
    const oldSig = crypto.createHmac('sha256', HUB_API_SECRET).update(`${oldTs}.${body}`).digest('hex');

    const res = await fetch(`${NOTIFUSE_URL}/api/tenants/provision`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': oldSig,
        'X-Veridian-Timestamp': oldTs,
      },
      body,
    });
    expect(res.status).toBe(401);
    const data = await res.json();
    expect(data.error).toMatch(/drift|timestamp/i);
  });

  test('@prod-safe tampering body 1 byte → 401 invalid signature', async () => {
    const body = JSON.stringify({
      tenant_id: 'prodsafetamper',
      owner_email: 't@prodsafe.test',
      plan: 'free',
    });
    const { timestamp, signature } = signHMAC(body);
    const tamperedBody = body.replace('"plan":"free"', '"plan":"enterprise"');

    const res = await fetch(`${NOTIFUSE_URL}/api/tenants/provision`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': signature,
        'X-Veridian-Timestamp': timestamp,
      },
      body: tamperedBody,
    });
    expect(res.status).toBe(401);
  });

  test('@prod-safe mauvais secret HMAC → 401', async () => {
    const res = await hmacFetch(
      '/api/tenants/provision',
      'POST',
      { tenant_id: 'prodsafewrongsec', owner_email: 'w@prodsafe.test', plan: 'free' },
      { secret: 'wrong-secret-not-the-real-one' },
    );
    expect(res.status).toBe(401);
  });

  test('@prod-safe signature manquante → 401', async () => {
    const res = await fetch(`${NOTIFUSE_URL}/api/tenants/provision`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tenant_id: 'prodsafenoSig', owner_email: 'n@prodsafe.test' }),
    });
    expect(res.status).toBe(401);
  });

  test('@prod-safe timestamp non-numerique → 401', async () => {
    const res = await hmacFetch(
      '/api/tenants/provision',
      'POST',
      { tenant_id: 'prodsafebadts', owner_email: 'b@prodsafe.test' },
      { timestamp: 'not-a-number' },
    );
    expect(res.status).toBe(401);
  });

  test('@prod-safe timestamp négatif → 401', async () => {
    const res = await hmacFetch(
      '/api/tenants/provision',
      'POST',
      { tenant_id: 'prodsafenegts', owner_email: 'n@prodsafe.test' },
      { timestamp: '-1000' },
    );
    expect(res.status).toBe(401);
  });

  test('@prod-safe generateMagicLink avec API key fake → 401', async () => {
    // Read-only : aucune ressource créée, juste verif que l'auth refuse.
    const r = await fetch(`${NOTIFUSE_URL}/api/workspaces.generateMagicLink`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: 'Bearer fake-api-key' },
      body: JSON.stringify({ user_email: 'whatever@prodsafe.test' }),
    });
    expect(r.status).toBe(401);
  });
});

test.describe('Prod-safe route status mountée', () => {
  test('@prod-safe route /api/tenants/{id}/status mountée (HMAC reject sans signature)', async () => {
    // Read-only : GET sans header HMAC. Vérifie juste que la route est mountée
    // dans le mux (sinon 404 littéral). Le middleware HMAC reject avec 401.
    //
    // On utilise ce pattern (vs un GET avec HMAC valide attendant 404) pour
    // que le test soit robuste à un mismatch secret (CI prod utilise le secret
    // prod, mais si quelqu'un run en local avec le secret staging par mégarde,
    // le test qui expect 404 avec HMAC valide fail en 401).
    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/prodsafedoesnotexist123/status`);
    expect(r.status).not.toBe(404);
    expect(r.status).toBe(401);
  });
});
