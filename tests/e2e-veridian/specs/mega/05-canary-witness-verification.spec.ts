// === MEGA E2E — Spec 05 (BONUS) — Canary witness verification ===
//
// Contribution Notifuse au ticket Hub MEGA-E2E.
// Spec parent : ../veridian-hub/todo/2026-05-23-MEGA-E2E-post-commercialisation.md
//
// Verifie que les 3 canary tenants long-lived (canaryfree, canarypro,
// canaryenterprise) :
//   - Existent toujours sur l env cible (staging OU prod via NOTIFUSE_URL)
//   - Ont plan_source=internal (immune downgrade Stripe)
//   - Ne sont jamais soft-deleted ni suspended
//   - Ont leur plan attendu (free / pro / enterprise)
//   - Ont quota=-1 (pivot 2026-05-21 BYO illimite partout)
//   - Sont exposes par /limits avec les features attendues du plan
//
// Difference vs canary-witness.spec.ts existante :
//   - L existante : @prod-safe @canary, smoke /status uniquement
//   - Cette MEGA : verification plus profonde via /limits (plan_source +
//     features dimension par plan) + assert plan_source=internal explicite
//     (anti-regression : si un canary perd internal il est expose aux
//     downgrades Stripe automatiques et casse en silence).
//
// Tag @prod-safe @canary : lecture seule via /api/tenants/{id}/status et
// /api/tenants/{id}/limits, aucun side-effect. Inclus automatiquement dans
// le job e2e-prod apres chaque promote (cf canary-witness.spec.ts entete).

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

function signHMACGet() {
  const ts = Date.now().toString();
  return {
    timestamp: ts,
    signature: crypto.createHmac('sha256', HUB_API_SECRET).update(`${ts}.`).digest('hex'),
  };
}

async function hmacGet(path: string) {
  const { timestamp, signature } = signHMACGet();
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method: 'GET',
    headers: {
      'X-Veridian-Hub-Signature': signature,
      'X-Veridian-Timestamp': timestamp,
    },
  });
}

async function readBody(r: Response): Promise<{ raw: string; json: () => any }> {
  const raw = await r.text();
  return { raw, json: () => (raw ? JSON.parse(raw) : null) };
}

// Plans attendus (seeding manuel 2026-05-20). Si Robert change un canary,
// mettre a jour ici en parallele.
//
// Pivot pricing 2026-05-21 : tous plans = tout illimite (-1 / true) sauf
// FeatureWhiteLabel reserve aux business+. SEUL differenciant Free vs paid
// = duree 15j visible (geree par Hub state machine, pas par limits).
// Cf ticket todo/2026-05-25-drift-canary-free-limits-vs-pivot-pricing.md
// (resolu : data fix via update-plan plan_source=internal le 2026-05-25).
const CANARIES = [
  { id: 'canaryfree', plan: 'free', whiteLabel: false },
  { id: 'canarypro', plan: 'pro', whiteLabel: false },
  { id: 'canaryenterprise', plan: 'enterprise', whiteLabel: true },
];

test.describe('@mega @prod-safe @canary MEGA-05 — canary witness verification (deep)', () => {
  for (const canary of CANARIES) {
    test(`${canary.id} — /status active + /limits coherent + plan_source=internal`, async () => {
      // === Verif 1 : /status active, jamais soft-deleted/suspended, plan attendu ===
      const statusResp = await hmacGet(`/api/tenants/${canary.id}/status`);
      const statusBody = await readBody(statusResp);
      expect(statusResp.status, statusBody.raw).toBe(200);
      const status = statusBody.json();

      expect(status.tenant_id).toBe(canary.id);
      expect(status.status, `canary ${canary.id} must be active`).toBe('active');
      expect(status.plan, `canary ${canary.id} plan drift`).toBe(canary.plan);
      // Pivot 2026-05-21 : tous quotas -1 (BYO sending illimite)
      expect(status.monthly_email_quota, 'quota must be -1 (unlimited)').toBe(-1);
      expect(status.quota_remaining).toBe(-1);
      expect(status.suspended_at, 'canary must not be suspended').toBeFalsy();
      expect(status.deleted_at, 'canary must never be soft-deleted').toBeFalsy();

      // === Verif 2 : /limits expose plan_source=internal + plan correct ===
      const limitsResp = await hmacGet(`/api/tenants/${canary.id}/limits`);
      const limitsBody = await readBody(limitsResp);
      expect(limitsResp.status, limitsBody.raw).toBe(200);
      const limits = limitsBody.json();

      expect(limits.tenant_id).toBe(canary.id);
      expect(limits.plan).toBe(canary.plan);
      expect(limits.status).toBe('active');
      // Anti-regression critique : si plan_source != internal, le canary est
      // expose aux downgrades Stripe automatiques (plus un canary).
      expect(limits.plan_source, `canary ${canary.id} doit etre immune (plan_source=internal)`).toBe('internal');

      // === Verif 3 : limits structure presente + quota -1 (sending unlimited) ===
      expect(limits.limits, 'limits object obligatoire').toBeTruthy();
      expect(limits.limits.MonthlyEmailQuota).toBe(-1);

      // === Verif 4 : invariants pivot pricing 2026-05-21 ===
      // Tous les plans (Free inclus) doivent avoir TOUTES les dimensions
      // V37 illimitees (-1) et toutes les features actives (true), sauf
      // FeatureWhiteLabel qui reste reserve business+.
      //
      // Anti-regression : si un canary drift sur ces valeurs, soit le pivot
      // 2026-05-21 a ete viole cote code (DefaultPlanLimits modifie), soit
      // un Upsert legacy a fige des vieilles dimensions V37 (cf bug fix
      // 2026-05-25 — re-run update-plan plan_source=internal pour resoudre).
      expect(limits.limits.MaxContacts, `${canary.id} MaxContacts pivot violated`).toBe(-1);
      expect(limits.limits.MaxSeats, `${canary.id} MaxSeats pivot violated`).toBe(-1);
      expect(limits.limits.MaxOAuthAccounts, `${canary.id} MaxOAuthAccounts pivot violated`).toBe(-1);
      expect(limits.limits.MaxCustomDomains, `${canary.id} MaxCustomDomains pivot violated`).toBe(-1);
      expect(limits.limits.MaxActiveSequences, `${canary.id} MaxActiveSequences pivot violated`).toBe(-1);
      expect(limits.limits.HistoryRetentionDays, `${canary.id} HistoryRetentionDays pivot violated`).toBe(-1);
      expect(limits.limits.FeatureABTesting, `${canary.id} FeatureABTesting must be true (A/B gratuit pour tous post-pivot)`).toBe(true);
      expect(limits.limits.FeatureBrandingRemoved, `${canary.id} FeatureBrandingRemoved must be true (branding optionnel post-pivot)`).toBe(true);

      // White-label : SEUL differenciant business+ stable depuis le pivot →
      //   - free/pro : white-label false (branding non-personnalisable)
      //   - enterprise (business+) : white-label true
      // Si ce drift, le canary signale un casse business critique
      // (downgrade silencieux ou montee illegitime).
      expect(
        limits.limits.FeatureWhiteLabel,
        `${canary.id} white-label expected ${canary.whiteLabel}`,
      ).toBe(canary.whiteLabel);

      expect(limits.generated_at, 'generated_at obligatoire').toBeTruthy();
    });
  }
});
