// === Giga E2E — Cold lifecycle end-to-end (sans envoi réel) ===
//
// Deux comportements cold que Robert veut validés en E2E DÉROULÉ, pas seulement
// unitairement :
//
//   1. RÉPONSE REÇUE → STOP SÉQUENCE : un prospect répond à un de nos envois →
//      le mail entrant est détecté comme réponse (match Message-ID via In-Reply-To)
//      → le contact passe 'replied' (signal durable, source de vérité du gate
//      Lot 9) → il SORT des séquences. On vérifie le SIGNAL réel + l'event timeline
//      email.replied.
//
//   2. ENVOI VERS UNE ADRESSE DÉJÀ CONTACTÉE → BLOQUÉ par le cap destinataire :
//      le cap journalier par destinataire (anti-harcèlement) empêche un envoi de
//      plus vers une adresse qui a déjà atteint son quota du jour. On vérifie la
//      DÉCISION RÉELLE du gate (COUNT message_history depuis minuit UTC vs cap).
//
// 🔴 CONTRAINTE DURE (Robert) : ZÉRO mail réel (les alias test routent vers sa
// boîte perso). On ne déclenche AUCUN broadcast. On frappe le VRAI code métier
// via l'endpoint de test staging-only POST /api/veridian/admin/cold-simulate
// (cf. internal/http/veridian_cold_simulate_handler.go) qui :
//   - mode inbound_reply       : appelle le VRAI VeridianReplyService.ProcessInboundMessage
//     (détection forte par Message-ID, signal 'replied', exit automations) sans IMAP réel ;
//   - mode seed_sent           : pose N entrées message_history "sent" via le VRAI repo Create ;
//   - mode daily_cap_decision  : renvoie le COUNT réel (CountSentSinceForContact) + la
//     décision count>=cap, le prédicat EXACT du gate worker veridian_daily_cap.go.
//
// L'endpoint est STAGING-ONLY (503 en prod). Ces tests sont donc skip hors staging.
//
// Conventions héritées de cold-config.spec.ts : readBody (stream lisible 1×),
// prefix tenant ≤20 chars varchar(20), afterAll wipe (describe.serial), loginAsOwner.

import { test, expect, chromium } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL) {
  throw new Error('NOTIFUSE_URL env var required');
}

const RUN_STAMP = Date.now().toString(36).slice(-6);

// === Helpers (mêmes que cold-config.spec.ts) ================================

async function readBody(r: Response): Promise<{ raw: string; json: () => any }> {
  const raw = await r.text();
  return { raw, json: () => (raw ? JSON.parse(raw) : null) };
}

function signHMAC(body: string, tsOverride?: string) {
  const ts = tsOverride ?? Date.now().toString();
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

async function bearerFetch(path: string, jwt: string, method = 'GET', body: object | null = null) {
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${jwt}`,
    },
    body: body ? JSON.stringify(body) : undefined,
  });
}

// Connecte l'OWNER du workspace et retourne son JWT de session (cf. cold-config.spec.ts).
async function loginAsOwner(autoLoginUrl: string): Promise<string> {
  const browser = await chromium.launch();
  try {
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
    const page = await ctx.newPage();
    await page.goto(autoLoginUrl, { waitUntil: 'networkidle' });
    await page.waitForTimeout(2500);
    const jwt = await page.evaluate(() => localStorage.getItem('auth_token'));
    if (!jwt) throw new Error('auto-login did not yield an auth_token');
    return jwt;
  } finally {
    await browser.close();
  }
}

// ============================================================================
// NIVEAU 1 — @prod-safe : route cold-simulate montée + gate staging (read-only)
// ============================================================================

test.describe('@prod-safe Cold lifecycle — route montée + garde-fous', () => {
  test('@prod-safe cold-simulate : POST monté + rejet sans auth (pas 404 catchall)', async () => {
    // Piège catchall root_handler (incident 2026-05-25) : si la route n'est pas
    // montée, on tombe sur la SPA (HTML 200). On doit voir un rejet d'auth JSON,
    // jamais un 200 HTML ni un 404.
    const r = await fetch(`${NOTIFUSE_URL}/api/veridian/admin/cold-simulate`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{"mode":"seed_sent","workspace_id":"nope"}',
    });
    expect(r.status).not.toBe(404);
    expect(r.status).not.toBe(200); // pas de SPA servie par le catchall
    expect([400, 401, 503]).toContain(r.status);
  });

  test('@prod-safe cold-simulate : en PROD, gate staging → 503 (jamais d\'exécution)', async () => {
    // L'endpoint est staging-only. En prod il doit refuser, même avec un HMAC
    // valide. On signe correctement : si on est en prod, attendu 503
    // (cold-simulate disabled) ; en staging, la requête passe la garde et
    // échoue plus loin (workspace inconnu → 4xx/5xx) mais PAS sur le gate.
    const r = await hmacFetch('/api/veridian/admin/cold-simulate', 'POST', {
      mode: 'daily_cap_decision',
      workspace_id: 'definitely-not-a-real-workspace',
      contact_email: 'x@x.test',
      per_recipient_cap: 1,
    });
    const b = await readBody(r);
    expect(b.raw).not.toContain('<!doctype'); // pas de catchall SPA
    // On ne sait pas si on tourne en prod ou staging ici : on vérifie juste que
    // si c'est un 503, c'est bien la garde staging (code paywall_unavailable),
    // pas un crash silencieux.
    if (r.status === 503) {
      expect(b.json()?.code).toBe('paywall_unavailable');
    } else {
      // En staging : la garde est passée, l'échec vient d'ailleurs (workspace inconnu).
      expect([200, 400, 500]).toContain(r.status);
    }
  });
});

// ============================================================================
// NIVEAU 2 — Mutation réelle sur STAGING (provision jetable, zéro mail envoyé)
// ============================================================================

const mutationDescribe = HUB_API_SECRET ? test.describe.serial : test.describe.skip;

const provisioned: string[] = [];

test.afterAll(async () => {
  if (!HUB_API_SECRET || provisioned.length === 0) return;
  const ids = [...provisioned];
  provisioned.length = 0;
  try {
    await hmacFetch('/api/veridian/admin/wipe-test-tenants', 'POST', {
      tenant_ids: ids,
      safety_client_prefixes: ['canary', 'robertbrunon', 'robertstagingtest'],
    });
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn(`afterAll wipe failed (non-fatal): ${err}`);
  }
});

mutationDescribe('@cold Cold lifecycle — comportement réel (staging)', () => {
  // varchar(20) : `clf<6chars>` = 9 chars.
  const tid = `clf${RUN_STAMP}`;
  const ownerEmail = `e2e-cold-lifecycle-${RUN_STAMP}@e2e.veridian.site`;
  // Le prospect qui répondra (scénario 1).
  const prospectEmail = `prospect-${RUN_STAMP}@some-corp-${RUN_STAMP}.com`;
  // L'adresse déjà sur-sollicitée (scénario 2).
  const cappedEmail = `already-contacted-${RUN_STAMP}@some-corp-${RUN_STAMP}.com`;
  let jwt = '';

  test('00. Provision workspace jetable + login owner (JWT)', async () => {
    provisioned.push(tid);
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tid,
      owner_email: ownerEmail,
      plan: 'free',
    });
    const body = await readBody(r);
    expect(r.status, body.raw).toBe(200);
    const prov = body.json();
    expect(prov.api_key).toBeTruthy();
    expect(prov.auto_login_url, 'provision must return auto_login_url').toBeTruthy();
    jwt = await loginAsOwner(prov.auto_login_url);
    expect(jwt.length).toBeGreaterThan(20);
  });

  // === SCÉNARIO 1 — RÉPONSE REÇUE → STOP SÉQUENCE ===========================

  test('01. Stop-on-reply : réponse entrante détectée → contact marqué replied', async () => {
    // L'endpoint seed notre envoi initial (message_history) puis joue une réponse
    // entrante (VeridianIMAPMessage) qui le cite en In-Reply-To, et la passe au
    // VRAI VeridianReplyService.ProcessInboundMessage. AUCUN mail n'est envoyé.
    const r = await hmacFetch('/api/veridian/admin/cold-simulate', 'POST', {
      mode: 'inbound_reply',
      workspace_id: tid,
      from: prospectEmail,
      subject: 'Re: votre proposition',
    });
    const b = await readBody(r);
    expect(b.raw).not.toContain('<!doctype'); // pas de catchall SPA
    expect(r.status, b.raw).toBe(200);
    const data = b.json();
    // Source de vérité : le contact est marqué 'replied' (signal durable consommé
    // par le gate Lot 9 → il sort de toute séquence active).
    expect(data.has_replied, 'le prospect doit être marqué replied').toBe(true);
    expect(data.is_reply).toBe(true);
    expect(data.seeded_message_id, 'envoi initial seedé (cité par la réponse)').toBeTruthy();
  });

  test('02. Stop-on-reply : event timeline email.replied posé (visibilité console)', async () => {
    // Le service pose un event timeline 'email.replied' sur le contact → observable
    // par l'owner via /api/timeline.list (la console l'affiche dans l'historique).
    const r = await bearerFetch(
      `/api/timeline.list?workspace_id=${tid}&email=${encodeURIComponent(prospectEmail)}&limit=50`,
      jwt,
    );
    const b = await readBody(r);
    expect(r.status, b.raw).toBe(200);
    const timeline = b.json().timeline || [];
    const replied = timeline.find((e: any) => e.kind === 'email.replied');
    expect(replied, 'un event email.replied doit exister sur la timeline du prospect').toBeTruthy();
    expect(replied.email).toBe(prospectEmail.toLowerCase());
  });

  test('03. Stop-on-reply : idempotent — re-jouer la réponse ne casse rien', async () => {
    // Le poller IMAP peut re-dispatcher un UID (MarkSeen échoue). MarkReplied est
    // idempotent (ON CONFLICT DO NOTHING) : re-jouer → toujours replied, pas d'erreur.
    const r = await hmacFetch('/api/veridian/admin/cold-simulate', 'POST', {
      mode: 'inbound_reply',
      workspace_id: tid,
      from: prospectEmail,
      subject: 'Re: votre proposition (renvoi)',
    });
    const b = await readBody(r);
    expect(r.status, b.raw).toBe(200);
    expect(b.json().has_replied).toBe(true);
  });

  // === SCÉNARIO 2 — ENVOI VERS UNE ADRESSE DÉJÀ CONTACTÉE → CAP DESTINATAIRE =

  test('04. Cap destinataire : sous le cap → l\'envoi PASSERAIT', async () => {
    // Adresse vierge ce jour : 0 envoi < cap 1 → pas bloqué.
    const r = await hmacFetch('/api/veridian/admin/cold-simulate', 'POST', {
      mode: 'daily_cap_decision',
      workspace_id: tid,
      contact_email: cappedEmail,
      per_recipient_cap: 1,
    });
    const b = await readBody(r);
    expect(r.status, b.raw).toBe(200);
    const data = b.json();
    expect(data.sent_today).toBe(0);
    expect(data.would_be_capped, '0 envoi < cap 1 → autorisé').toBe(false);
  });

  test('05. Cap destinataire : 1 envoi déjà fait aujourd\'hui (seed sans mail réel)', async () => {
    // Pose 1 entrée message_history "sent" vers l'adresse (notre 1er envoi cold du
    // jour) via le VRAI repo Create. AUCUN mail réel n'est envoyé.
    const r = await hmacFetch('/api/veridian/admin/cold-simulate', 'POST', {
      mode: 'seed_sent',
      workspace_id: tid,
      contact_email: cappedEmail,
      count: 1,
    });
    const b = await readBody(r);
    expect(r.status, b.raw).toBe(200);
    const data = b.json();
    expect(data.seeded).toBe(1);
    expect(data.sent_today, '1 envoi compté depuis minuit UTC').toBe(1);
  });

  test('06. Cap destinataire : adresse au quota du jour → 2e envoi BLOQUÉ (anti-harcèlement)', async () => {
    // 1 envoi déjà fait, cap = 1 → count(1) >= cap(1) → le gate skip le prochain
    // envoi (prédicat exact de veridian_daily_cap.go). C'est la protection
    // anti-harcèlement : on ne re-contacte pas une adresse déjà servie ce jour.
    const r = await hmacFetch('/api/veridian/admin/cold-simulate', 'POST', {
      mode: 'daily_cap_decision',
      workspace_id: tid,
      contact_email: cappedEmail,
      per_recipient_cap: 1,
    });
    const b = await readBody(r);
    expect(r.status, b.raw).toBe(200);
    const data = b.json();
    expect(data.sent_today).toBe(1);
    expect(data.would_be_capped, 'count 1 >= cap 1 → 2e envoi bloqué').toBe(true);
  });

  test('07. Cap destinataire : relèvement du cap → l\'envoi repasse', async () => {
    // Si on relève le cap à 2, la même adresse (1 envoi) repasse sous le seuil :
    // count(1) < cap(2) → autorisé. Confirme que la décision suit le cap, pas un
    // état figé.
    const r = await hmacFetch('/api/veridian/admin/cold-simulate', 'POST', {
      mode: 'daily_cap_decision',
      workspace_id: tid,
      contact_email: cappedEmail,
      per_recipient_cap: 2,
    });
    const b = await readBody(r);
    expect(r.status, b.raw).toBe(200);
    const data = b.json();
    expect(data.sent_today).toBe(1);
    expect(data.would_be_capped, '1 < cap 2 → autorisé après relèvement').toBe(false);
  });
});
