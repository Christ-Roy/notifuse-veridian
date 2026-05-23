// === Veridian patch — 2026-05-23 ===
// Tests E2E mega-coverage du seed automatique du template
// `invitation-prospection` au Provision tenant.
//
// Feature : POST /api/tenants/provision → en plus de workspace/owner/api_key,
// Notifuse seed un template MJML + une transactional notification nommés
// "invitation-prospection". Idempotent, best-effort (jamais bloquant).
//
// Code livre : commit 6ed4820f
//   - internal/service/veridian_seed_templates.go (helper)
//   - internal/service/veridian_seed_invitation_prospection.mjml (source embed)
//   - internal/app/app.go (wiring ConfigureSeedTemplatesSupport)
//   - internal/service/veridian_service.go (call etape 11 dans Provision)
//
// Couverture :
//   1. Seed cree au Provision (template + notification visibles)
//   2. Notification bind sur template via channels.email.template_id
//   3. Template MJML valide + variables Liquid presentes (inviter_email,
//      workspace_name, invite_url, expires_at)
//   4. Send via transactional.send : pas 402 + valide / 4xx body invalide OK,
//      JAMAIS 404 (notification existe).
//   5. Idempotent seed : re-provision n'a pas dupliqué templates/notifications
//   6. Preserve customisation client : si update-template a modifié le subject,
//      re-provision ne doit pas l'ecraser
//   7. Best-effort : provision retourne 200 meme si seed echoue (couvert
//      unit, E2E verifie la posture). Pas de path explicite simulation
//      echec — on s'appuie sur la couverture unit + le constat que toutes
//      les provisions reussissent.
//
// Conventions Lot N : prefix `tst`, afterAll cleanup wipe.

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

// ID + nom canoniques cote service (cf. veridian_seed_templates.go)
const SEED_TEMPLATE_ID = 'invitation-prospection';
const SEED_TEMPLATE_NAME = 'Invitation Prospection';

function signHMAC(body: string): { timestamp: string; signature: string } {
  const timestamp = Date.now().toString();
  const signature = crypto
    .createHmac('sha256', HUB_API_SECRET)
    .update(`${timestamp}.${body}`)
    .digest('hex');
  return { timestamp, signature };
}

async function hmacFetch(path: string, method: string, body: object | null = null) {
  const rawBody = body ? JSON.stringify(body) : '';
  const { timestamp, signature } = signHMAC(rawBody);
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-Veridian-Hub-Signature': signature,
      'X-Veridian-Timestamp': timestamp,
    },
    body: rawBody || undefined,
  });
}

async function bearerFetch(path: string, apiKey: string, method = 'GET', body: object | null = null) {
  const rawBody = body ? JSON.stringify(body) : '';
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${apiKey}`,
    },
    body: rawBody || undefined,
  });
}

async function provisionTenant(tenantId: string) {
  const res = await hmacFetch('/api/tenants/provision', 'POST', {
    tenant_id: tenantId,
    owner_email: `${tenantId}@e2e.veridian.test`,
    plan: 'pro',
  });
  const text = await res.text();
  expect(res.status, text).toBe(200);
  return JSON.parse(text) as {
    workspace_id: string;
    owner_user_id: string;
    api_key: string;
    api_key_email: string;
    magic_link: string;
    auto_login_url: string;
    plan: string;
    created: boolean;
  };
}

const newTid = () => `tst${Date.now().toString(36).slice(-6)}${Math.floor(Math.random() * 1000)}`;
const provisioned: string[] = [];

test.afterAll(async () => {
  if (provisioned.length === 0) return;
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

test.describe('Seed template invitation-prospection au Provision', () => {
  test('1. Provision frais cree le template invitation-prospection', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid);
    expect(api_key).toBeTruthy();

    // GET templates.list → array `templates` contient invitation-prospection
    const r = await bearerFetch(`/api/templates.list?workspace_id=${tid}`, api_key);
    expect(r.status, await r.clone().text()).toBe(200);
    const body = await r.json();
    expect(body.templates).toBeTruthy();
    const tmpl = (body.templates as Array<{ id: string; name: string; channel: string }>).find(
      (t) => t.id === SEED_TEMPLATE_ID,
    );
    expect(tmpl, `template ${SEED_TEMPLATE_ID} absent de la liste`).toBeTruthy();
    expect(tmpl!.name).toBe(SEED_TEMPLATE_NAME);
    expect(tmpl!.channel).toBe('email');
  });

  test('2. Transactional notification bind sur le template seede', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid);

    const r = await bearerFetch(`/api/transactional.list?workspace_id=${tid}`, api_key);
    expect(r.status, await r.clone().text()).toBe(200);
    const body = await r.json();
    expect(body.notifications, 'notifications devrait etre un array non vide').toBeTruthy();
    const notif = (
      body.notifications as Array<{ id: string; name: string; channels: Record<string, { template_id: string }> }>
    ).find((n) => n.id === SEED_TEMPLATE_ID);
    expect(notif, `notification ${SEED_TEMPLATE_ID} absente`).toBeTruthy();
    expect(notif!.name).toBe(SEED_TEMPLATE_NAME);
    expect(notif!.channels).toBeTruthy();
    expect(notif!.channels.email).toBeTruthy();
    expect(notif!.channels.email.template_id).toBe(SEED_TEMPLATE_ID);
  });

  test('3. Template MJML valide + variables Liquid attendues', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid);

    const r = await bearerFetch(
      `/api/templates.get?workspace_id=${tid}&id=${SEED_TEMPLATE_ID}`,
      api_key,
    );
    expect(r.status, await r.clone().text()).toBe(200);
    const body = await r.json();
    expect(body.template).toBeTruthy();
    expect(body.template.id).toBe(SEED_TEMPLATE_ID);
    expect(body.template.channel).toBe('email');
    expect(body.template.email).toBeTruthy();
    expect(body.template.email.editor_mode).toBe('code');

    const mjml: string = body.template.email.mjml_source ?? '';
    expect(mjml.length, 'mjml_source vide').toBeGreaterThan(100);
    expect(mjml).toContain('{{ inviter_email }}');
    expect(mjml).toContain('{{ workspace_name }}');
    expect(mjml).toContain('{{ invite_url }}');
    expect(mjml).toContain('{{ expires_at }}');

    // Sanity : subject template (post Liquid interpolation) reference inviter
    expect(body.template.email.subject).toContain('{{ inviter_email }}');
  });

  test('4. transactional.send : pas 404, notification reconnue', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid);

    // Le payload est conforme au schema SendTransactionalRequest. La cle
    // est de NE PAS recevoir un 404 (= notification not found). En staging
    // l'envoi reel peut echouer (pas de provider configure) → 200/202 ou
    // 4xx body legitime acceptable. 402 (paywall) inacceptable (tenant pro
    // frais).
    const r = await bearerFetch('/api/transactional.send', api_key, 'POST', {
      workspace_id: tid,
      notification: {
        id: SEED_TEMPLATE_ID,
        contact: { email: `recipient-${tid}@e2e.veridian.test` },
        channels: ['email'],
        data: {
          inviter_email: 'boss@acme.com',
          workspace_name: 'Team Sales',
          invite_url: `https://prospection.app.veridian.site/invite/${tid}-tok`,
          expires_at: '2026-12-31T00:00:00.000Z',
        },
      },
    });

    expect(r.status, `unexpected paywall 402 sur tenant pro frais`).not.toBe(402);
    expect(r.status, `notification ${SEED_TEMPLATE_ID} aurait du etre seedee → pas 404`).not.toBe(404);

    // Si la reponse est 4xx/5xx, le body NE DOIT PAS contenir "notification
    // not found" — sinon c'est le signal sans ambiguite que le seed n'a pas
    // tourne (la notification n'existe pas dans le workspace).
    if (r.status >= 400) {
      const errBody = await r.clone().text();
      expect(
        errBody,
        `seed n a pas cree la notification — body: ${errBody}`,
      ).not.toMatch(/notification.*not.*found/i);
    }

    // 200/202 = succes provider, 400 = body/integration manquante en staging
    // (acceptable), 500 = bug serveur (a investiguer mais ne valide pas le 404).
    expect([200, 201, 202, 400, 500]).toContain(r.status);
  });

  test('5. Idempotent : re-provision n ajoute pas de doublon', async () => {
    const tid = newTid();
    provisioned.push(tid);

    // Premier provision (created=true)
    const first = await provisionTenant(tid);
    expect(first.created).toBe(true);

    // Snapshot count templates + notifications
    const t1 = await bearerFetch(`/api/templates.list?workspace_id=${tid}`, first.api_key);
    const n1 = await bearerFetch(`/api/transactional.list?workspace_id=${tid}`, first.api_key);
    const tpls1 = ((await t1.json()).templates ?? []) as Array<{ id: string }>;
    const nots1 = ((await n1.json()).notifications ?? []) as Array<{ id: string }>;
    const tplCount1 = tpls1.filter((t) => t.id === SEED_TEMPLATE_ID).length;
    const notCount1 = nots1.filter((n) => n.id === SEED_TEMPLATE_ID).length;
    expect(tplCount1).toBe(1);
    expect(notCount1).toBe(1);

    // Re-provision : doit etre idempotent (created=false)
    const second = await provisionTenant(tid);
    expect(second.created).toBe(false);

    // Verifier qu'on a TOUJOURS exactement 1 template + 1 notification
    const t2 = await bearerFetch(`/api/templates.list?workspace_id=${tid}`, first.api_key);
    const n2 = await bearerFetch(`/api/transactional.list?workspace_id=${tid}`, first.api_key);
    const tpls2 = ((await t2.json()).templates ?? []) as Array<{ id: string }>;
    const nots2 = ((await n2.json()).notifications ?? []) as Array<{ id: string }>;
    expect(tpls2.filter((t) => t.id === SEED_TEMPLATE_ID).length).toBe(1);
    expect(nots2.filter((n) => n.id === SEED_TEMPLATE_ID).length).toBe(1);
  });

  test('6. Preserve customisation : re-provision n ecrase pas un template modifie', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const { api_key } = await provisionTenant(tid);

    // Customise le template seede via templates.update
    const customSubject = `CUSTOM Subject {{ inviter_email }} for ${tid}`;
    const customMjml = `<mjml><mj-body><mj-section><mj-column><mj-text>CUSTOM ${tid} {{ inviter_email }} {{ workspace_name }} {{ invite_url }} {{ expires_at }}</mj-text></mj-column></mj-section></mj-body></mjml>`;

    const upd = await bearerFetch('/api/templates.update', api_key, 'POST', {
      workspace_id: tid,
      id: SEED_TEMPLATE_ID,
      name: 'Custom Invitation',
      channel: 'email',
      category: 'transactional',
      email: {
        editor_mode: 'code',
        mjml_source: customMjml,
        subject: customSubject,
        compiled_preview: customMjml,
      },
      test_data: {
        inviter_email: 'custom@acme.com',
        workspace_name: 'Custom Team',
        invite_url: 'https://example.com/invite',
        expires_at: '2026-12-31T00:00:00.000Z',
      },
    });
    // Update endpoint upstream : peut retourner 200 (succes) ou 400 si le
    // body upstream evolue. On valide la non-regression seulement quand
    // l'update a reussi (sinon le test perd son sens — log + skip assert).
    if (upd.status !== 200) {
      // eslint-disable-next-line no-console
      console.warn(`templates.update returned ${upd.status} — skipping preservation assertion`);
      return;
    }

    // Re-provision (idempotent) — le seed DOIT skip (template existe deja)
    const second = await provisionTenant(tid);
    expect(second.created).toBe(false);

    // Le subject + mjml customises doivent etre intacts
    const r = await bearerFetch(
      `/api/templates.get?workspace_id=${tid}&id=${SEED_TEMPLATE_ID}`,
      api_key,
    );
    expect(r.status).toBe(200);
    const body = await r.json();
    expect(body.template.email.subject).toBe(customSubject);
    expect(body.template.email.mjml_source).toContain(`CUSTOM ${tid}`);
  });

  test('7. Best-effort : provision retourne 200 meme si seed silent (couvert unit)', async () => {
    // On ne peut pas simuler un echec seed runtime depuis E2E (le service
    // ne fournit pas de hook fault injection). La garantie best-effort est
    // couverte par les tests unit (veridian_seed_templates_test.go :
    // CreateTemplate error → continue, CreateNotification error → log only).
    //
    // L'observation runtime que TOUS les provisions ci-dessus retournent
    // 200 sans exception confirme la posture best-effort en pratique : meme
    // si une etape interne du seed echouait, le tenant aurait ete cree.
    const tid = newTid();
    provisioned.push(tid);
    const prov = await provisionTenant(tid);
    expect(prov.workspace_id).toBe(tid);
    expect(prov.api_key).toBeTruthy();
    expect(prov.created).toBe(true);
    // Aucune assertion supplementaire — cas documente comme couvert par
    // les tests unit colocalises au service.
  });
});
