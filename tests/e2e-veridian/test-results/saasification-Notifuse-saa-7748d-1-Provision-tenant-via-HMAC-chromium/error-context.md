# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: saasification.spec.ts >> Notifuse saasification end-to-end >> 1. Provision tenant via HMAC
- Location: specs/saasification.spec.ts:52:7

# Error details

```
Error: {"error":"create api key: this user already exists"}


expect(received).toBe(expected) // Object.is equality

Expected: 200
Received: 500
```

# Test source

```ts
  1   | import { test, expect } from '@playwright/test';
  2   | import * as crypto from 'crypto';
  3   | 
  4   | // Scenario complet de saasification Notifuse :
  5   | // 1. Hub (HMAC) → POST /api/tenants/provision → workspace + owner reel + API key + magic link
  6   | // 2. User clique magic link → connecte sur la console Notifuse
  7   | // 3. Hub (API key tenant) → POST /api/workspaces.generateMagicLink → nouvelle session OK
  8   | // 4. Hub (HMAC) → POST /api/tenants/suspend → tentative envoi → 402
  9   | // 5. Hub (HMAC) → POST /api/tenants/resume → envoi OK
  10  | // 6. Hub (HMAC) → DELETE /api/tenants/:id → soft delete
  11  | //
  12  | // Le test ne s'appuie sur AUCUN setup wizard (lance via env vars + ROOT_EMAIL configured at boot).
  13  | 
  14  | const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
  15  | const HUB_API_SECRET = process.env.HUB_API_SECRET!;
  16  | 
  17  | if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  18  |   throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
  19  | }
  20  | 
  21  | function signHMAC(body: string): { timestamp: string; signature: string } {
  22  |   const timestamp = Date.now().toString();
  23  |   const signature = crypto
  24  |     .createHmac('sha256', HUB_API_SECRET)
  25  |     .update(`${timestamp}.${body}`)
  26  |     .digest('hex');
  27  |   return { timestamp, signature };
  28  | }
  29  | 
  30  | async function hmacFetch(path: string, method: string, body: object | null = null) {
  31  |   const rawBody = body ? JSON.stringify(body) : '';
  32  |   const { timestamp, signature } = signHMAC(rawBody);
  33  |   const res = await fetch(`${NOTIFUSE_URL}${path}`, {
  34  |     method,
  35  |     headers: {
  36  |       'Content-Type': 'application/json',
  37  |       'X-Veridian-Hub-Signature': signature,
  38  |       'X-Veridian-Timestamp': timestamp,
  39  |     },
  40  |     body: rawBody || undefined,
  41  |   });
  42  |   return res;
  43  | }
  44  | 
  45  | // Génère un tenant_id unique pour ce run (préfixe e2e + timestamp)
  46  | const tenantId = `e2e${Date.now().toString(36).slice(-8)}`;
  47  | const ownerEmail = `${tenantId}@e2e.veridian.test`;
  48  | 
  49  | let provisioningResponse: any;
  50  | 
  51  | test.describe.serial('Notifuse saasification end-to-end', () => {
  52  |   test('1. Provision tenant via HMAC', async () => {
  53  |     const res = await hmacFetch('/api/tenants/provision', 'POST', {
  54  |       tenant_id: tenantId,
  55  |       owner_email: ownerEmail,
  56  |       plan: 'pro',
  57  |     });
> 58  |     expect(res.status, await res.text()).toBe(200);
      |                                          ^ Error: {"error":"create api key: this user already exists"}
  59  |     provisioningResponse = await res.json();
  60  |     expect(provisioningResponse.workspace_id).toBe(tenantId);
  61  |     expect(provisioningResponse.owner_user_id).toBeTruthy();
  62  |     expect(provisioningResponse.api_key).toBeTruthy();
  63  |     expect(provisioningResponse.magic_link).toContain('/console/signin');
  64  |     expect(provisioningResponse.created).toBe(true);
  65  |   });
  66  | 
  67  |   test('2. Provision idempotent (re-call returns same workspace, created=false)', async () => {
  68  |     const res = await hmacFetch('/api/tenants/provision', 'POST', {
  69  |       tenant_id: tenantId,
  70  |       owner_email: ownerEmail,
  71  |       plan: 'pro',
  72  |     });
  73  |     expect(res.status).toBe(200);
  74  |     const data = await res.json();
  75  |     expect(data.workspace_id).toBe(tenantId);
  76  |     expect(data.created).toBe(false);
  77  |   });
  78  | 
  79  |   test('3. Owner can sign in via magic link (headful)', async ({ page }) => {
  80  |     await page.goto(provisioningResponse.magic_link);
  81  |     // Notifuse console redirects to dashboard once code is verified
  82  |     await page.waitForURL(/\/console(\/.*)?$/, { timeout: 30_000 });
  83  |     // Sanity check: workspace name visible
  84  |     await expect(page.locator('body')).toContainText(tenantId, { timeout: 10_000 });
  85  |   });
  86  | 
  87  |   test('4. Generate fresh magic link via API key (tenant-scoped)', async () => {
  88  |     const res = await fetch(`${NOTIFUSE_URL}/api/workspaces.generateMagicLink`, {
  89  |       method: 'POST',
  90  |       headers: {
  91  |         'Content-Type': 'application/json',
  92  |         Authorization: `Bearer ${provisioningResponse.api_key}`,
  93  |       },
  94  |       body: JSON.stringify({ user_email: ownerEmail }),
  95  |     });
  96  |     expect(res.status).toBe(200);
  97  |     const data = await res.json();
  98  |     expect(data.magic_link).toContain('/console/signin');
  99  |     expect(data.expires_at).toBeTruthy();
  100 |   });
  101 | 
  102 |   test('5. Status endpoint returns plan + quota', async () => {
  103 |     const res = await hmacFetch(`/api/tenants/${tenantId}/status`, 'GET');
  104 |     expect(res.status).toBe(200);
  105 |     const data = await res.json();
  106 |     expect(data.tenant_id).toBe(tenantId);
  107 |     expect(data.status).toBe('active');
  108 |     expect(data.plan).toBe('pro');
  109 |     expect(data.monthly_email_quota).toBe(10000);
  110 |     expect(data.quota_remaining).toBe(10000);
  111 |   });
  112 | 
  113 |   test('6. Send transactional email (active plan, success)', async () => {
  114 |     const res = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
  115 |       method: 'POST',
  116 |       headers: {
  117 |         'Content-Type': 'application/json',
  118 |         Authorization: `Bearer ${provisioningResponse.api_key}`,
  119 |       },
  120 |       body: JSON.stringify({
  121 |         workspace_id: tenantId,
  122 |         to: 'sink@e2e.veridian.test',
  123 |         // smoke test : si plan check passe, c'est suffisant — l'envoi reel
  124 |         // peut echouer faute de provider configure, on test juste le 402 vs autre
  125 |       }),
  126 |     });
  127 |     // Attendu : pas 402 (plan actif). 4xx legitime sur body invalide OK.
  128 |     expect(res.status).not.toBe(402);
  129 |   });
  130 | 
  131 |   test('7. Suspend tenant', async () => {
  132 |     const res = await hmacFetch('/api/tenants/suspend', 'POST', {
  133 |       tenant_id: tenantId,
  134 |       reason: 'e2e test suspension',
  135 |     });
  136 |     expect(res.status).toBe(200);
  137 |     const data = await res.json();
  138 |     expect(data.suspended_at).toBeTruthy();
  139 |   });
  140 | 
  141 |   test('8. Send transactional after suspend → 402', async () => {
  142 |     const res = await fetch(`${NOTIFUSE_URL}/api/transactional.send`, {
  143 |       method: 'POST',
  144 |       headers: {
  145 |         'Content-Type': 'application/json',
  146 |         Authorization: `Bearer ${provisioningResponse.api_key}`,
  147 |       },
  148 |       body: JSON.stringify({ workspace_id: tenantId, to: 'sink@e2e.veridian.test' }),
  149 |     });
  150 |     expect(res.status).toBe(402);
  151 |     const data = await res.json();
  152 |     expect(data.error).toMatch(/payment|suspend/i);
  153 |   });
  154 | 
  155 |   test('9. Resume tenant', async () => {
  156 |     const res = await hmacFetch('/api/tenants/resume', 'POST', {
  157 |       tenant_id: tenantId,
  158 |     });
```