// CONTRAT-BILLING v2 — couverture mega du payload `update-plan` versionné.
//
// Référence : veridian-hub/docs/CONTRAT-BILLING.md v2.0 (2026-05-22), sections
// §3.2 (schéma), §3.3 (enum plan_source), §3.4 (invariants), §7 (trial).
//
// Impl Notifuse : commit 2939b65c (Agent C, 2026-05-23).
//   - internal/domain/veridian_billing_contract.go (constantes + helpers)
//   - internal/http/veridian_handler.go::handleUpdatePlan (validation 400/409)
//   - internal/service/veridian_service.go::UpdatePlan (immunity §3.4.4)
//
// === Note sur l'immunity v2 (§3.4.4) ===
//
// Le brief initial demandait de tester `stripe_trial → stripe = 409` et
// `downgrade_auto → stripe = 409`. C'est FAUX par rapport au contrat ET à
// l'impl. Cf CONTRAT-BILLING §3.3 et §7.3 :
//
//   - SEULS `grant_manual` + valeurs legacy équivalentes (manual, lifetime_*,
//     internal) sont immune (§3.4.4 invariant 4).
//   - `stripe_trial → stripe` est la conversion CB normale §7.3 (le user
//     ajoute une CB, la sub Stripe prend le relais). C'est le chemin nominal.
//   - `downgrade_auto → stripe` est le re-upgrade après checkout (un tenant
//     downgrade revient en payant). C'est légitime.
//   - `stripe → stripe_trial`, `stripe → downgrade_auto` : transitions auto
//     Stripe normales (trial réactivé, dunning).
//
// Cette spec teste donc l'immunity correctement : seuls les plan_sources
// immune bloquent les sources auto (stripe / stripe_trial / downgrade_auto).
//
// Convention : 1 worker, Chromium, tag @regression, prefix `tst`,
// wipe afterEach include_orphans:false (cf reference_admin_tenants_listing_api).

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

async function hmacFetch(path: string, method: string, body: object | null = null, extraHeaders: Record<string, string> = {}) {
  const rawBody = body ? JSON.stringify(body) : '';
  const { timestamp, signature } = signHMAC(rawBody);
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Veridian-Hub-Signature': signature,
      'X-Veridian-Timestamp': timestamp,
      ...extraHeaders,
    },
    body: rawBody || undefined,
  });
}

// Provision avec retry (404 + 5xx transient — cf pattern chaos-status-and-plan).
async function provisionTenant(tid: string, opts: { plan?: string; plan_source?: string } = {}) {
  const { plan = 'free', plan_source } = opts;
  const body: Record<string, unknown> = {
    tenant_id: tid,
    owner_email: `${tid}@billing.test`,
    plan,
  };
  if (plan_source) body.plan_source = plan_source;

  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', body);
    if (r.status === 200) return r.json();
    if ((r.status >= 500 || r.status === 404) && attempt < 4) {
      const backoffMs = 500 * Math.pow(2, attempt);
      // eslint-disable-next-line no-console
      console.log(`provision retry ${attempt + 1}/5: status=${r.status}, backoff=${backoffMs}ms`);
      await new Promise((res) => setTimeout(res, backoffMs));
      continue;
    }
    expect(r.status, await r.text()).toBe(200);
  }
  throw new Error('provision retry exhausted (5 attempts)');
}

// === Veridian patch 2026-05-21 (Lot N étape 1+2) ===
// Prefix unifié `tst` + cleanup afterEach au fil de l'eau.
const newTid = () => `tst${Date.now().toString(36).slice(-6)}${Math.floor(Math.random() * 1000)}`;
const provisioned: string[] = [];

test.afterEach(async () => {
  if (provisioned.length === 0) return;
  const ids = [...provisioned];
  provisioned.length = 0;
  try {
    await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
      tenant_ids: ids,
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest'],
      // PIÈGE : include_orphans:true en batch CI crash le container
      // (cf reference_admin_tenants_listing_api). NE PAS activer en CI.
    });
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn(`afterEach wipe failed (non-fatal): ${err}`);
  }
});

// Helper : forcer un plan_source initial via update-plan. Utilisé pour mettre
// le tenant dans un état précis avant de tester une transition.
async function setPlanSource(tid: string, plan: string, plan_source: string) {
  const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
    contract_version: '2.0',
    tenant_id: tid,
    plan,
    plan_source,
  });
  expect(r.status, `setPlanSource(${tid}, ${plan}, ${plan_source}) failed: ${await r.text()}`).toBe(200);
}

// Helper : lire plan + plan_source effectifs via /limits (StatusResponse n'a
// pas plan_source, /limits l'expose).
async function readState(tid: string): Promise<{ plan: string; plan_source: string }> {
  const r = await hmacFetch(`/api/tenants/${tid}/limits`, 'GET');
  expect(r.status).toBe(200);
  const data = await r.json();
  return { plan: data.plan, plan_source: data.plan_source };
}

// ============================================================================
// §3.4.1 — contract_version validation
// ============================================================================
test.describe('@regression contract_version validation (§3.4.1)', () => {
  test('contract_version 2.0 → 200 OK', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'pro',
      plan_source: 'stripe',
    });
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.plan).toBe('pro');
    expect(data.previous_plan).toBe('free');
    expect(data.plan_source).toBe('stripe');
    expect(data.applied_at).toBeTruthy();
  });

  test('contract_version 2.1 (same major) → 200 OK (forward-compat minor)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.1',
      tenant_id: tid,
      plan: 'business',
    });
    expect(r.status).toBe(200);
  });

  test('contract_version 3.0 (different major) → 400 invalid_payload', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '3.0',
      tenant_id: tid,
      plan: 'pro',
    });
    expect(r.status).toBe(400);
    const data = await r.json();
    expect(data.code).toBe('invalid_payload');
    expect(data.details?.supported_contract_version).toBe('2.0');
    expect(data.details?.contract_version).toBe('3.0');
  });

  test('contract_version 1.5 (major < 2) → 400 invalid_payload', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '1.5',
      tenant_id: tid,
      plan: 'pro',
    });
    expect(r.status).toBe(400);
    const data = await r.json();
    expect(data.code).toBe('invalid_payload');
  });

  test('contract_version absent (legacy v1) → 200 OK back-compat', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    // Pas de champ contract_version dans le body — Hub legacy non migré.
    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      tenant_id: tid,
      plan: 'pro',
    });
    expect(r.status).toBe(200);
  });

  test('contract_version garbage "abc" → 400 (pas de point, format invalide)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: 'abc',
      tenant_id: tid,
      plan: 'pro',
    });
    expect(r.status).toBe(400);
    const data = await r.json();
    expect(data.code).toBe('invalid_payload');
  });
});

// ============================================================================
// §3.4.2 — enum `plan` fermé
// ============================================================================
test.describe('@regression plan enum fermé (§3.4.2)', () => {
  for (const plan of ['free', 'pro', 'business', 'enterprise']) {
    test(`plan "${plan}" canonique → 200 OK`, async () => {
      const tid = newTid();
      provisioned.push(tid);
      await provisionTenant(tid, { plan: 'free' });

      const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
        contract_version: '2.0',
        tenant_id: tid,
        plan,
      });
      expect(r.status).toBe(200);
      const data = await r.json();
      expect(data.plan).toBe(plan);
    });
  }

  for (const plan of ['premium', 'basic', 'unlimited']) {
    test(`plan hors enum "${plan}" → 400 invalid_plan avec details.allowed_plans`, async () => {
      const tid = newTid();
      provisioned.push(tid);
      await provisionTenant(tid, { plan: 'free' });

      const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
        contract_version: '2.0',
        tenant_id: tid,
        plan,
      });
      expect(r.status).toBe(400);
      const data = await r.json();
      // Code machine STRICT — c'est la garantie cross-app (le Hub branche du
      // switch logique dessus, pas une chaîne humaine).
      expect(data.code).toBe('invalid_plan');
      expect(data.details?.plan).toBe(plan);
      expect(data.details?.allowed_plans).toEqual(
        expect.arrayContaining(['free', 'pro', 'business', 'enterprise']),
      );
    });
  }

  test('plan vide "" → 400 invalid_payload (champ requis)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: '',
    });
    expect(r.status).toBe(400);
    const data = await r.json();
    // Champ vide = required check (avant enum check). Code = invalid_payload.
    expect(data.code).toBe('invalid_payload');
    expect(data.details?.missing).toEqual(expect.arrayContaining(['plan']));
  });
});

// ============================================================================
// §3.3 — enum plan_source v2 + legacy v1
// ============================================================================
test.describe('@regression plan_source enum v2 (§3.3)', () => {
  // v2 canonique
  for (const src of ['stripe', 'stripe_trial', 'grant_manual', 'downgrade_auto']) {
    test(`plan_source v2 canonique "${src}" → 200 OK`, async () => {
      const tid = newTid();
      provisioned.push(tid);
      await provisionTenant(tid, { plan: 'free' });

      const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
        contract_version: '2.0',
        tenant_id: tid,
        plan: 'pro',
        plan_source: src,
      });
      expect(r.status).toBe(200);
      const data = await r.json();
      expect(data.plan_source).toBe(src);
    });
  }

  // Legacy v1 tolérés en entrée (back-compat Hub non migré)
  for (const src of ['lifetime_site_vitrine', 'lifetime_partner', 'manual', 'internal']) {
    test(`plan_source legacy v1 "${src}" → 200 OK (back-compat)`, async () => {
      const tid = newTid();
      provisioned.push(tid);
      await provisionTenant(tid, { plan: 'free' });

      const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
        contract_version: '2.0',
        tenant_id: tid,
        plan: 'pro',
        plan_source: src,
      });
      expect(r.status).toBe(200);
      // L'app stocke la valeur legacy telle quelle (back-compat data
      // existantes). NormalizePlanSourceV2 ne s'applique qu'en sortie de
      // certaines responses si l'app le décide — ici on vérifie juste que
      // le call ne rejette pas.
      const data = await r.json();
      // Acceptable : la valeur stockée est soit `src` (preserve), soit
      // `grant_manual` (normalisée). Les deux sont conformes au contrat §3.3.
      expect([src, 'grant_manual']).toContain(data.plan_source);
    });
  }

  test('plan_source inconnu "unknown_source" → 400 invalid_payload', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'pro',
      plan_source: 'unknown_source',
    });
    expect(r.status).toBe(400);
    const data = await r.json();
    expect(data.code).toBe('invalid_payload');
    expect(data.details?.plan_source).toBe('unknown_source');
    expect(data.details?.allowed_plan_sources).toEqual(
      expect.arrayContaining(['stripe', 'stripe_trial', 'grant_manual', 'downgrade_auto']),
    );
  });

  test('plan_source vide "" → 200 OK (défaut "stripe" au repo upsert)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'pro',
      // pas de plan_source → existing preserved (cf service.UpdatePlan)
    });
    expect(r.status).toBe(200);
  });
});

// ============================================================================
// §3.4.4 — Immunity grant_manual + legacy
//
// Règle : un tenant dont le plan_source EXISTANT est immune (grant_manual,
// manual, lifetime_*, internal) bloque toute mutation venant d'une source
// AUTO (stripe, stripe_trial, downgrade_auto). Réponse : 409 plan_locked.
//
// Seul un update-plan avec plan_source=grant_manual (ou legacy équivalent)
// peut écraser un tenant immune (l'admin Hub a le dernier mot).
// ============================================================================
test.describe('@regression Immunity §3.4.4', () => {
  // Cas immune : doit bloquer toute source auto.
  for (const immuneSrc of ['grant_manual', 'manual', 'lifetime_partner', 'lifetime_site_vitrine', 'internal']) {
    for (const autoSrc of ['stripe', 'stripe_trial', 'downgrade_auto']) {
      test(`existing "${immuneSrc}" + incoming "${autoSrc}" → 409 plan_locked`, async () => {
        const tid = newTid();
        provisioned.push(tid);
        await provisionTenant(tid, { plan: 'business', plan_source: immuneSrc });

        // Confirmer l'état initial.
        const before = await readState(tid);
        expect(before.plan).toBe('business');

        const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
          contract_version: '2.0',
          tenant_id: tid,
          plan: 'free',
          plan_source: autoSrc,
        });
        expect(r.status, `expected 409 for ${immuneSrc} ← ${autoSrc}, got ${r.status}: ${await r.clone().text()}`).toBe(409);
        const data = await r.json();
        expect(data.code).toBe('plan_locked');
        expect(data.details?.tenant_id).toBe(tid);
        expect(data.details?.requested_plan).toBe('free');

        // Vérifier que l'état n'a PAS bougé (défense en profondeur).
        const after = await readState(tid);
        expect(after.plan).toBe('business');
      });
    }
  }

  test('existing "grant_manual" + incoming "grant_manual" → 200 (admin Hub peut écraser)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'business', plan_source: 'grant_manual' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'enterprise',
      plan_source: 'grant_manual',
    });
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.plan).toBe('enterprise');
    expect(data.plan_source).toBe('grant_manual');
  });

  test('existing "lifetime_partner" + incoming "lifetime_partner" → 200 (legacy admin override)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'business', plan_source: 'lifetime_partner' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'enterprise',
      plan_source: 'lifetime_partner',
    });
    expect(r.status).toBe(200);
  });
});

// ============================================================================
// Transitions auto-downgrade légitimes (PAS 409 — contrat §7.3 + dunning §5.1)
//
// Le brief initial demandait 409 sur ces cas. C'est faux — ce sont les
// flux nominaux du contrat. Cette section les protège explicitement contre
// une régression future qui ajouterait à tort `stripe_trial` ou
// `downgrade_auto` dans IsImmuneV2.
// ============================================================================
test.describe('@regression Auto-source transitions légitimes (§7.3, §5.5)', () => {
  test('existing "stripe" + incoming "downgrade_auto" → 200 (dunning end)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'pro', plan_source: 'stripe' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'free',
      plan_source: 'downgrade_auto',
    });
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.plan).toBe('free');
    expect(data.plan_source).toBe('downgrade_auto');
  });

  test('existing "stripe_trial" + incoming "stripe" → 200 (trial conversion §7.3)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'pro', plan_source: 'stripe_trial' });

    // Le user a ajouté sa CB pendant l'essai → checkout complété → sub Stripe
    // active. C'est la conversion §7.3, PAS un cas immune.
    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'pro',
      plan_source: 'stripe',
    });
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.plan_source).toBe('stripe');
  });

  test('existing "stripe_trial" + incoming "downgrade_auto" → 200 (trial expired sans CB §7.3)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'pro', plan_source: 'stripe_trial' });

    // Trial expiré sans CB → Hub envoie downgrade_auto → app passe en
    // mode dégradé paywall (§5.3). C'est nominal.
    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'free',
      plan_source: 'downgrade_auto',
    });
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.plan).toBe('free');
    expect(data.plan_source).toBe('downgrade_auto');
  });

  test('existing "downgrade_auto" + incoming "stripe" → 200 (re-upgrade après checkout)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free', plan_source: 'downgrade_auto' });

    // Un tenant downgradé revient en payant → checkout v3 → sub active.
    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'pro',
      plan_source: 'stripe',
    });
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.plan).toBe('pro');
    expect(data.plan_source).toBe('stripe');
  });

  test('existing "stripe" + incoming "stripe_trial" → 200 (cas rare : re-trial admin)', async () => {
    // Cas marginal mais autorisé par le contrat (stripe_trial est une source
    // auto, peut écraser stripe). Pas d'immunity.
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'pro', plan_source: 'stripe' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'pro',
      plan_source: 'stripe_trial',
    });
    expect(r.status).toBe(200);
  });

  test('existing "stripe" + incoming "grant_manual" → 200 (admin offre lifetime à un payant)', async () => {
    // grant_manual peut écraser n'importe quoi — admin Hub a le dernier mot.
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'pro', plan_source: 'stripe' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'enterprise',
      plan_source: 'grant_manual',
    });
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.plan_source).toBe('grant_manual');
  });
});

// ============================================================================
// §3.4.3 — Idempotency-Key header (middleware passthrough)
//
// 2 calls avec même Idempotency-Key → 2e replay status + body sans modifier
// le tenant. Header X-Idempotent-Replay: true sur le replay.
// ============================================================================
test.describe('@regression Idempotency-Key middleware (§3.4.3)', () => {
  test('2 calls update-plan même clé → 2e replay no-op + X-Idempotent-Replay header', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const idempKey = `tst-billing-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const body = {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'business',
      plan_source: 'stripe',
    };

    // Call 1 : miss → execute, INSERT cache.
    const r1 = await hmacFetch('/api/tenants/update-plan', 'POST', body, {
      'Idempotency-Key': idempKey,
    });
    expect(r1.status).toBe(200);
    expect(r1.headers.get('X-Idempotent-Replay')).toBeFalsy();
    const data1 = await r1.json();
    expect(data1.plan).toBe('business');
    expect(data1.previous_plan).toBe('free');
    const appliedAt1 = data1.applied_at;

    // Mutate state intermediaire (depuis un AUTRE idempotency-key) pour
    // s'assurer que le replay renvoie bien le cache de la 1ère réponse,
    // PAS l'état courant.
    const r1b = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'enterprise',
      plan_source: 'stripe',
    });
    expect(r1b.status).toBe(200);

    // Call 2 : hit + same hash → replay cache de la 1ère réponse.
    const r2 = await hmacFetch('/api/tenants/update-plan', 'POST', body, {
      'Idempotency-Key': idempKey,
    });
    expect(r2.status).toBe(200);
    expect(r2.headers.get('X-Idempotent-Replay')).toBe('true');
    const data2 = await r2.json();
    // La réponse rejouée doit être EXACTEMENT celle du 1er call (même
    // applied_at, même previous_plan). Pas l'état courant.
    expect(data2.plan).toBe('business');
    expect(data2.previous_plan).toBe('free');
    expect(data2.applied_at).toBe(appliedAt1);

    // État réel du tenant : toujours `enterprise` (le replay n'a PAS
    // ré-écrit). Vérification critique anti-double-apply.
    const state = await readState(tid);
    expect(state.plan).toBe('enterprise');
  });

  test('même clé + body différent → 422 idempotency_key_mismatch', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const idempKey = `tst-mismatch-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;

    const r1 = await hmacFetch(
      '/api/tenants/update-plan',
      'POST',
      { contract_version: '2.0', tenant_id: tid, plan: 'pro', plan_source: 'stripe' },
      { 'Idempotency-Key': idempKey },
    );
    expect(r1.status).toBe(200);

    // Même clé, body différent (plan business au lieu de pro) → 422.
    const r2 = await hmacFetch(
      '/api/tenants/update-plan',
      'POST',
      { contract_version: '2.0', tenant_id: tid, plan: 'business', plan_source: 'stripe' },
      { 'Idempotency-Key': idempKey },
    );
    expect(r2.status).toBe(422);
    const data = await r2.json();
    expect(data.code).toBe('idempotency_key_mismatch');
  });
});

// ============================================================================
// §3.2 — Champs optionnels v2 (effective_at, stripe_subscription_id, reason)
//
// Le contrat dit que ces champs sont acceptés. L'impl Notifuse les accepte
// mais ne les persiste pas tous (effective_at + reason = audit log uniquement,
// pas de colonne DB). On valide donc :
//   - Le payload v2 complet est accepté (200).
//   - stripe_subscription_id null est OK pour stripe_trial / grant_manual.
//   - reason vide est OK.
// ============================================================================
test.describe('@regression Champs optionnels v2 (§3.2)', () => {
  test('payload v2 complet avec tous les champs → 200', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'pro',
      plan_source: 'stripe',
      effective_at: new Date().toISOString(),
      stripe_subscription_id: 'sub_test_1234567890',
      idempotency_key: `tst-uuid-${Date.now()}`,
      reason: 'checkout.session.completed evt_test',
    });
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.plan).toBe('pro');
    expect(data.plan_source).toBe('stripe');
  });

  test('payload v2 stripe_trial avec stripe_subscription_id null → 200 (§3.2 cas trial sans CB)', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'pro',
      plan_source: 'stripe_trial',
      effective_at: new Date().toISOString(),
      stripe_subscription_id: null,
      idempotency_key: `tst-trial-${Date.now()}`,
      reason: 'trial activation (5 mails sent)',
    });
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.plan_source).toBe('stripe_trial');
  });

  test('payload v2 grant_manual avec stripe_subscription_id absent → 200', async () => {
    const tid = newTid();
    provisioned.push(tid);
    await provisionTenant(tid, { plan: 'free' });

    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: tid,
      plan: 'enterprise',
      plan_source: 'grant_manual',
      reason: 'admin grant lifetime partner',
      // stripe_subscription_id absent → null implicit, cas plan offert §3.2
    });
    expect(r.status).toBe(200);
    const data = await r.json();
    expect(data.plan_source).toBe('grant_manual');
  });
});

// ============================================================================
// Erreurs structurelles (HMAC, tenant_not_found)
// ============================================================================
test.describe('@regression Erreurs structurelles update-plan', () => {
  test('tenant_id inconnu → 404 tenant_not_found', async () => {
    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      tenant_id: 'tst-does-not-exist-xyz-billing',
      plan: 'pro',
      plan_source: 'stripe',
    });
    expect(r.status).toBe(404);
    const data = await r.json();
    expect(data.code).toBe('tenant_not_found');
  });

  test('tenant_id manquant → 400 invalid_payload', async () => {
    const r = await hmacFetch('/api/tenants/update-plan', 'POST', {
      contract_version: '2.0',
      plan: 'pro',
    });
    expect(r.status).toBe(400);
    const data = await r.json();
    expect(data.code).toBe('invalid_payload');
    expect(data.details?.missing).toEqual(expect.arrayContaining(['tenant_id']));
  });

  test('body JSON invalide → 400 invalid_payload', async () => {
    const ts = Date.now().toString();
    const rawBody = '{not json';
    const signature = crypto.createHmac('sha256', HUB_API_SECRET).update(`${ts}.${rawBody}`).digest('hex');
    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/update-plan`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Veridian-Hub-Signature': signature,
        'X-Veridian-Timestamp': ts,
      },
      body: rawBody,
    });
    expect(r.status).toBe(400);
    const data = await r.json();
    expect(data.code).toBe('invalid_payload');
  });
});
