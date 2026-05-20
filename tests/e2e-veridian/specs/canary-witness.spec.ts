// Canary witness — vérifie que les 3 tenants long-lived survivent à chaque
// migration / deploy. Tournés en `@prod-safe` (read-only, aucun side-effect)
// donc inclus AUTOMATIQUEMENT dans le job e2e-prod après chaque promote.
//
// Si un de ces tests fail post-deploy, on a un signal immédiat qu'une
// migration / ALTER COLUMN / refactor a cassé un tenant en place — *avant*
// qu'un vrai client subisse la régression.
//
// Les 3 canary tenants ont été créés manuellement le 2026-05-20 sur prod et
// staging avec plan_source=internal (immune downgrade Stripe) et quota=-1
// (BYO sending). Cf. todo/2026-05-20-e2e-cleanup-discipline-canary-safety.md
// et runbooks/canary-tenants.md.

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

async function hmacGet(path: string) {
  const { timestamp, signature } = signHMAC('');
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method: 'GET',
    headers: {
      'X-Veridian-Hub-Signature': signature,
      'X-Veridian-Timestamp': timestamp,
    },
  });
}

// Plans attendus (correspond au seeding manuel 2026-05-20).
// Si Robert change le plan d'un canary, mettre à jour ici en parallèle.
const CANARIES = [
  { id: 'canaryfree', plan: 'free' },
  { id: 'canarypro', plan: 'pro' },
  { id: 'canaryenterprise', plan: 'enterprise' },
];

test.describe('Canary witness — tenants long-lived survivent aux deploys', () => {
  for (const canary of CANARIES) {
    test(`@prod-safe @canary ${canary.id} — status endpoint répond + plan cohérent`, async () => {
      const r = await hmacGet(`/api/tenants/${canary.id}/status`);
      // Lire le body une seule fois (fail si HTTP !=200, le body sert d'indice).
      const bodyText = await r.text();
      expect(r.status, bodyText).toBe(200);

      const data = JSON.parse(bodyText);
      expect(data.tenant_id).toBe(canary.id);
      expect(data.status).toBe('active');
      expect(data.plan).toBe(canary.plan);
      // BYO sending : quota=-1 sur tous les plans (cf. 2026-05-20).
      expect(data.monthly_email_quota).toBe(-1);
      expect(data.quota_remaining).toBe(-1);
      // Tenant actif, pas suspendu, pas soft-deleted.
      expect(data.suspended_at).toBeFalsy();
      expect(data.deleted_at).toBeFalsy();
    });
  }
});
