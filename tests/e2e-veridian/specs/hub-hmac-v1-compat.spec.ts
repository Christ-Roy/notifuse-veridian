// === [v1-volontaire] Rétrocompatibilité de la signature Hub v1 ===
//
// SEULS tests des E2E qui signent encore en v1 (signHubV1), volontairement, tant que
// VERIDIAN_HMAC_REQUIRE_V2 est absent. Sans @prod-safe : ils ne tournent pas en prod (aucune ligne
// `veridian_hmac_v1_accepted` ne doit venir des E2E prod).
//
// BASCULE : quand VERIDIAN_HMAC_REQUIRE_V2=true est posé sur l'environnement testé, passer
// HUB_V1_STILL_ACCEPTED à false dans ../hub-signature.ts : ces tests attendront alors 401 (v1 refusée)
// au lieu de « acceptée ». Le contrôle v2 (en face) ne change pas.

import { test, expect } from '@playwright/test';
import { HUB_V1_STILL_ACCEPTED, hubFetch, signHubV1 } from '../hub-signature';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

const GHOST = 'v1compatghosttenant';

test.describe('@regression [v1-volontaire] HMAC Hub v1 — rétrocompatibilité', () => {
  test('[v1-volontaire] GET health d un tenant fantôme signé en v1', async () => {
    const r = await fetch(`${NOTIFUSE_URL}/api/tenants/${GHOST}/health`, {
      method: 'GET',
      headers: signHubV1(HUB_API_SECRET, ''),
    });
    // Acceptée : la signature passe, le tenant n'existe pas (404). Refusée (REQUIRE_V2) : 401.
    expect(r.status).toBe(HUB_V1_STILL_ACCEPTED ? 404 : 401);
  });

  test('[v1-volontaire] POST by-email signé en v1', async () => {
    const raw = JSON.stringify({ email: `v1compat${Date.now().toString(36).slice(-6)}@v1compat.test` });
    const r = await fetch(`${NOTIFUSE_URL}/api/users/by-email`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...signHubV1(HUB_API_SECRET, raw) },
      body: raw,
    });
    expect(r.status).toBe(HUB_V1_STILL_ACCEPTED ? 200 : 401);
  });

  test('contrôle : la même requête en v2 passe toujours', async () => {
    const r = await hubFetch(NOTIFUSE_URL, HUB_API_SECRET, `/api/tenants/${GHOST}/health`, 'GET');
    expect(r.status).toBe(404);
  });
});
