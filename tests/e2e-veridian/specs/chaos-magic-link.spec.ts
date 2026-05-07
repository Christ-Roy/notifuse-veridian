// CI violente — Suite 3 : magic link headful. Clique partout, verifie owner role,
// teste expire / replay / tampering.

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

test.describe('Magic link — flow nominal headful', () => {
  test('user clique magic link → arrive sur console connecte → est owner du workspace', async ({
    page,
  }) => {
    const tid = `magic${Date.now().toString(36).slice(-6)}`;
    const email = `${tid}@magic.test`;

    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: email,
      plan: 'pro',
    });
    expect(r.status).toBe(200);
    const provision = await r.json();
    expect(provision.magic_link).toContain('code='); // Veridian patch must produce self-contained link

    await page.goto(provision.magic_link);

    // Doit redirect vers /console (login auto via code dans URL)
    await page.waitForURL(/\/console(\/.*)?$/, { timeout: 30_000 });

    // Le workspace name visible dans la sidebar
    await expect(page.locator('body')).toContainText(tid, { timeout: 10_000 });

    // Naviguer vers settings/members pour verifier role
    // Note : selecteurs depend de la console UI Notifuse, peuvent changer. On utilise
    // des selecteurs robustes (text-based) pour limiter le breakage sur upgrade.
    await page.getByRole('link', { name: /members|members/i }).click().catch(async () => {
      // Fallback : URL directe
      await page.goto(`${NOTIFUSE_URL}/console/${tid}/members`);
    });

    // L'email du user doit apparaitre avec le role "owner"
    const memberRow = page.locator('tr', { hasText: email }).first();
    await expect(memberRow).toContainText(/owner/i, { timeout: 10_000 });
  });
});

test.describe('Magic link — adversaires', () => {
  test('lien reutilise → premiere fois OK, deuxieme fois erreur', async ({ browser }) => {
    const tid = `magicre${Date.now().toString(36).slice(-6)}`;

    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: `${tid}@magic.test`,
      plan: 'free',
    });
    const { magic_link } = await r.json();

    // Click 1
    const ctx1 = await browser.newContext();
    const page1 = await ctx1.newPage();
    await page1.goto(magic_link);
    await page1.waitForURL(/\/console(\/.*)?$/, { timeout: 30_000 });
    await ctx1.close();

    // Click 2 (different context = different cookies = doit re-verifier le code)
    const ctx2 = await browser.newContext();
    const page2 = await ctx2.newPage();
    await page2.goto(magic_link);

    // Doit afficher une erreur (code already used / expired) ou rediriger vers signin sans login
    const url = page2.url();
    const body = await page2.locator('body').innerText();
    const errorPath = /signin|error|invalid/i.test(url) || /invalid|expired|used/i.test(body);
    expect(errorPath).toBe(true);
    await ctx2.close();
  });

  test('tampering URL : code modifie → erreur', async ({ page }) => {
    const tid = `magtam${Date.now().toString(36).slice(-6)}`;
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: `${tid}@magic.test`,
      plan: 'free',
    });
    const { magic_link } = await r.json();
    const tampered = magic_link.replace(/code=[^&]+/, 'code=000000');

    await page.goto(tampered);
    // Doit pas atteindre la console authentifiee
    await page.waitForTimeout(3000);
    expect(page.url()).not.toMatch(/\/console\/[a-z0-9]+\/(?!signin)/i);
  });

  test('lien expire (>10 min) → erreur', async ({ page }) => {
    test.skip(
      true,
      'Test demande d attendre 11+ min reels. Couvert au niveau unit test cote Go (TTL).',
    );
  });
});

test.describe('Generate magic link via API key (tenant-scoped)', () => {
  test('admin Hub demande nouveau magic link → user peut se connecter', async ({ page }) => {
    const tid = `genmag${Date.now().toString(36).slice(-6)}`;
    const email = `${tid}@magic.test`;

    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: email,
      plan: 'pro',
    });
    const { api_key } = await r.json();

    // Demande nouveau magic link via API key (le scenario Hub : user clique "Open Notifuse")
    const linkRes = await fetch(`${NOTIFUSE_URL}/api/workspaces.generateMagicLink`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${api_key}` },
      body: JSON.stringify({ user_email: email }),
    });
    expect(linkRes.status).toBe(200);
    const { magic_link, expires_at } = await linkRes.json();
    expect(magic_link).toContain('code=');
    expect(new Date(expires_at).getTime()).toBeGreaterThan(Date.now());

    // Click
    await page.goto(magic_link);
    await page.waitForURL(/\/console(\/.*)?$/, { timeout: 30_000 });
  });

  test('generateMagicLink avec mauvaise API key → 401', async () => {
    const r = await fetch(`${NOTIFUSE_URL}/api/workspaces.generateMagicLink`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: 'Bearer fake-api-key' },
      body: JSON.stringify({ user_email: 'whatever@chaos.test' }),
    });
    expect(r.status).toBe(401);
  });
});
