// === Veridian patch — Mega-coverage identity cross-app (2026-05-23) ===
// Spec E2E mega-coverage du flow d'identite cross-app Notifuse <-> Hub.
//
// Couvre TROIS features livrees le 2026-05-23 sur la branche veridian :
//
//   A. Client hub-discovery (Agent J, commit 3c91731a) :
//      Notifuse expose GET /api/veridian/hub-discovery/me qui appelle le Hub
//      via HMAC pour decouvrir si l'email du user est connu cote Hub et quels
//      tenants/apps lui sont rattaches.
//      - Code   : pkg/hub_discovery/client.go
//      - Handler: internal/http/veridian_hub_discovery_handler.go
//      - JWT requis (Authorization: Bearer <token>)
//      - Best-effort : Hub down/erreur => hub_available=false, exists=false, tenants=[]
//
//   B. Colonne users.language (V42, hotfix 2026-05-23) :
//      ALTER TABLE users ADD COLUMN language VARCHAR(10) NOT NULL DEFAULT 'en'.
//      Cherry-pick upstream "translate system emails with user language".
//      - Migration : internal/migrations/v42.go
//      - Domain    : internal/domain/user.go (User.Language)
//      - Endpoint  : POST /api/user.updateLanguage (JWT)
//      - GET /api/user.me retourne user.language
//
//   C. Colonne users.hub_user_id (V46, Agent I commit e4cf5433) :
//      ALTER TABLE users ADD COLUMN hub_user_id UUID NULL.
//      Materialise le lien d'identite cross-app du CONTRAT-HUB §3.7.
//      - Migration : internal/migrations/v46.go
//      - Backfill applicatif progressif (AttachMember/SyncMember).
//
// Conventions Lot N :
//   - tids prefixe `tst` + timestamp court
//   - afterEach cleanup via /api/veridian/admin/wipe-test-tenants
//   - Tag @regression sur tous les tests
//
// Spec parent ticket : todo/2026-05-23-call-hub-discovery-by-email.md
//                   + todo/done/2026-05-23-v46-hub-user-id-column.md
//                   + V42 hotfix (commit f1a2c3d5)

import { test, expect } from '@playwright/test';
import * as crypto from 'crypto';

const NOTIFUSE_URL = process.env.NOTIFUSE_URL!;
const HUB_API_SECRET = process.env.HUB_API_SECRET!;

if (!NOTIFUSE_URL || !HUB_API_SECRET) {
  throw new Error('NOTIFUSE_URL and HUB_API_SECRET env vars required');
}

// === HMAC tenant signer (sens Hub -> Notifuse) ===
// Utilise pour provision / wipe-test-tenants / generateMagicLink. Format
// canonique : "${timestamp}.${body}" signe HMAC-SHA256 avec HUB_API_SECRET.
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

// Bearer JWT/api_key fetch (sens user authentifie -> Notifuse).
async function bearerFetch(path: string, method: string, token: string, body: object | null = null) {
  return fetch(`${NOTIFUSE_URL}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: body ? JSON.stringify(body) : undefined,
  });
}

async function provisionTenant(tenantId: string, ownerEmail: string, plan = 'free') {
  for (let attempt = 0; attempt < 5; attempt++) {
    const r = await hmacFetch('/api/tenants/provision', 'POST', {
      tenant_id: tenantId,
      owner_email: ownerEmail,
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

// Genere un magic link pour un user d'un workspace via api_key tenant,
// puis suit le auto-login URL pour extraire le JWT du user humain
// (depuis le HTML autoLoginPageTemplate qui set localStorage.auth_token).
async function exchangeMagicLinkForUserJWT(apiKey: string, userEmail: string): Promise<string> {
  const magicResp = await bearerFetch(
    '/api/workspaces.generateMagicLink',
    'POST',
    apiKey,
    { user_email: userEmail },
  );
  // Lire body une seule fois (.text() puis JSON.parse).
  const magicRaw = await magicResp.text();
  expect(magicResp.status, magicRaw).toBe(200);
  const magic = JSON.parse(magicRaw);
  expect(magic.auto_login_url).toBeTruthy();

  // Suivre l'URL auto-login : le handler /veridian/auto-login renvoie
  // du HTML qui embarque le JWT dans `localStorage.setItem('auth_token', "<JWT>")`.
  // En Node, on parse le HTML pour extraire le token.
  const autoLoginResp = await fetch(magic.auto_login_url, { redirect: 'manual' });
  expect(autoLoginResp.status, `auto-login HTTP ${autoLoginResp.status}`).toBe(200);
  const html = await autoLoginResp.text();
  // Le template Go encode le JSON string (avec guillemets). Pattern :
  //   localStorage.setItem('auth_token', "<JWT>")
  // ou la valeur peut etre escape selon Go html/template (json-marshalled).
  const match = html.match(/localStorage\.setItem\('auth_token',\s*"([^"]+)"\)/);
  if (!match) {
    throw new Error(`Could not extract auth_token from auto-login HTML. First 200 chars: ${html.slice(0, 200)}`);
  }
  return match[1];
}

// === Cleanup tracker ===
const provisioned: string[] = [];

test.afterEach(async () => {
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
    console.warn(`afterEach wipe failed (non-fatal): ${err}`);
  }
});

const newTid = () => `tst${Date.now().toString(36).slice(-6)}`;

// ===================================================================
// Section A — hub-discovery client (GET /api/veridian/hub-discovery/me)
// ===================================================================
test.describe('@regression identity-cross-app — A. hub-discovery client', () => {
  test('A1. sans Authorization → 401', async () => {
    const r = await fetch(`${NOTIFUSE_URL}/api/veridian/hub-discovery/me`, { method: 'GET' });
    expect(r.status).toBe(401);
  });

  test('A2. avec JWT valide → 200 + structure {hub_available, exists, tenants}', async () => {
    // Provision un tenant pour obtenir une api_key. Le Bearer api_key est
    // suffisant pour passer RequireAuth (cf. middleware/auth.go : tout JWT
    // UserType in {user, api_key} est accepte).
    const tid = newTid();
    provisioned.push(tid);
    const ownerEmail = `${tid}@discovery-self.test`;
    const provision = await provisionTenant(tid, ownerEmail, 'free');
    const apiKey = provision.api_key as string;
    expect(apiKey).toBeTruthy();

    const r = await bearerFetch('/api/veridian/hub-discovery/me', 'GET', apiKey);
    const raw = await r.text();
    expect(r.status, raw).toBe(200);
    const body = JSON.parse(raw);

    // Forme strictement attendue (HubDiscoveryResponse).
    expect(body).toHaveProperty('hub_available');
    expect(body).toHaveProperty('exists');
    expect(body).toHaveProperty('tenants');
    expect(typeof body.hub_available).toBe('boolean');
    expect(typeof body.exists).toBe('boolean');
    expect(Array.isArray(body.tenants)).toBe(true);

    // Defense : tenants doit etre un array JSON (jamais null) pour le front.
    // Si hub_available=false, tenants doit etre [] (pas null).
    if (!body.hub_available) {
      expect(body.tenants).toEqual([]);
    }
  });

  test('A3. email inconnu Hub → hub_available=true|false + exists=false + tenants:[]', async () => {
    // Synthese : on cree un nouveau tenant avec un email garanti inconnu
    // du Hub (uniquement Notifuse-side). Apres provision, l'owner cree
    // dans Notifuse n'existe PAS cote hub_app.users.
    //
    // Resultat attendu cote handler :
    //  - Si Hub accessible et secret OK -> hub_available=true, exists=false, tenants=[]
    //  - Si Hub down ou disabled -> hub_available=false, exists=false, tenants=[]
    //
    // Dans les deux cas, exists=false est invariant. tenants=[] est invariant.
    const tid = newTid();
    provisioned.push(tid);
    const ghostEmail = `tst-ghost-${Date.now().toString(36).slice(-6)}@discovery-unknown.test`;
    const provision = await provisionTenant(tid, ghostEmail, 'free');
    const apiKey = provision.api_key as string;

    const r = await bearerFetch('/api/veridian/hub-discovery/me', 'GET', apiKey);
    const raw = await r.text();
    expect(r.status, raw).toBe(200);
    const body = JSON.parse(raw);

    // Cet user a ete cree uniquement cote Notifuse - jamais touche au Hub.
    // Le Hub renverra donc exists=false (ou hub_available=false si down).
    expect(body.exists).toBe(false);
    expect(body.tenants).toEqual([]);
  });

  test('A4. methode HTTP autre que GET → 405', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const provision = await provisionTenant(tid, `${tid}@discovery-method.test`, 'free');
    const apiKey = provision.api_key as string;

    const r = await bearerFetch('/api/veridian/hub-discovery/me', 'POST', apiKey, { foo: 'bar' });
    expect(r.status).toBe(405);
  });

  test.skip('A5. Hub down simule → hub_available=false (couvert par tests unit)', async () => {
    // Cf. pkg/hub_discovery/client_test.go : timeout, 5xx, refused, secret
    // manquant → (false, nil, nil). Reproduire en E2E demanderait
    // d'invalider HUB_API_SECRET dans le container -> hors-perimetre.
  });

  test.skip('A6. timeout 2s best-effort → couvert par tests unit', async () => {
    // Cf. pkg/hub_discovery/client_test.go::TestHTTPClient_Timeout.
    // Reproduire en E2E demanderait un mock-Hub lent -> hors-perimetre
    // (le client a Timeout=2s, on n'a pas de moyen de forcer le Hub
    // staging a depasser cette duree sans casser d'autres tests).
  });
});

// ===================================================================
// Section B — users.language (V42)
// ===================================================================
test.describe('@regression identity-cross-app — B. users.language (V42)', () => {
  test('B7. default language=en sur user fraichement provisionne', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const ownerEmail = `${tid}@lang-default.test`;
    const provision = await provisionTenant(tid, ownerEmail, 'free');
    const apiKey = provision.api_key as string;

    // /api/user.me retourne {user: {...}, workspaces: [...]} ; user.language
    // doit etre "en" par defaut (V42 ADD COLUMN ... DEFAULT 'en').
    // Lire body UNE fois (.text() puis JSON.parse) — sinon "already read".
    const meResp = await bearerFetch('/api/user.me', 'GET', apiKey);
    const meRaw = await meResp.text();
    expect(meResp.status, meRaw).toBe(200);
    const me = JSON.parse(meRaw);
    expect(me.user).toBeDefined();
    expect(me.user.language).toBe('en');
  });

  test('B8. POST /api/user.updateLanguage → user.language persiste', async () => {
    // Update language requiert un JWT user (pas api_key) car la session est
    // verifiee (VerifyUserSession). On obtient le JWT user en suivant le
    // auto-login URL retourne par generateMagicLink.
    const tid = newTid();
    provisioned.push(tid);
    const ownerEmail = `${tid}@lang-update.test`;
    const provision = await provisionTenant(tid, ownerEmail, 'free');
    const apiKey = provision.api_key as string;

    const userJWT = await exchangeMagicLinkForUserJWT(apiKey, ownerEmail);

    // Update vers "fr"
    const updResp = await bearerFetch('/api/user.updateLanguage', 'POST', userJWT, { language: 'fr' });
    const updRaw = await updResp.text();
    expect(updResp.status, updRaw).toBe(200);

    // Verifier persistance via /api/user.me (mais utiliser le userJWT,
    // pas l'apiKey, sinon on lit le synthetic api_key user).
    const meResp = await bearerFetch('/api/user.me', 'GET', userJWT);
    const meRaw = await meResp.text();
    expect(meResp.status, meRaw).toBe(200);
    const me = JSON.parse(meRaw);
    expect(me.user.email).toBe(ownerEmail);
    expect(me.user.language).toBe('fr');
  });

  test('B9. POST /api/user.updateLanguage avec locale invalide → 400', async () => {
    const tid = newTid();
    provisioned.push(tid);
    const ownerEmail = `${tid}@lang-invalid.test`;
    const provision = await provisionTenant(tid, ownerEmail, 'free');
    const apiKey = provision.api_key as string;
    const userJWT = await exchangeMagicLinkForUserJWT(apiKey, ownerEmail);

    const r = await bearerFetch('/api/user.updateLanguage', 'POST', userJWT, { language: 'xx' });
    expect(r.status).toBe(400);

    // L'ancien language doit etre conserve (pas de side-effect sur 400).
    const meResp = await bearerFetch('/api/user.me', 'GET', userJWT);
    const meRaw = await meResp.text();
    expect(meResp.status, meRaw).toBe(200);
    const me = JSON.parse(meRaw);
    expect(me.user.language).toBe('en'); // toujours le default
  });

  test('B10. POST /api/user.updateLanguage sans Authorization → 401', async () => {
    // Smoke check du gate JWT sur l'endpoint mutateur language.
    const r = await fetch(`${NOTIFUSE_URL}/api/user.updateLanguage`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ language: 'fr' }),
    });
    expect(r.status).toBe(401);
  });

  test('B11. update persiste cross-request (relecture independante)', async () => {
    // Garantit que la valeur est bien stockee en DB (pas juste en cache
    // request-local). Update -> nouvelle session JWT (logout + relogin
    // via auto-login) -> /api/user.me retourne toujours "fr".
    const tid = newTid();
    provisioned.push(tid);
    const ownerEmail = `${tid}@lang-persist.test`;
    const provision = await provisionTenant(tid, ownerEmail, 'free');
    const apiKey = provision.api_key as string;

    const jwt1 = await exchangeMagicLinkForUserJWT(apiKey, ownerEmail);
    const updResp = await bearerFetch('/api/user.updateLanguage', 'POST', jwt1, { language: 'fr' });
    expect(updResp.status).toBe(200);

    // Nouvelle session JWT (independante de la 1ere).
    const jwt2 = await exchangeMagicLinkForUserJWT(apiKey, ownerEmail);
    expect(jwt2).not.toBe(jwt1);
    const meResp = await bearerFetch('/api/user.me', 'GET', jwt2);
    const meRaw = await meResp.text();
    expect(meResp.status, meRaw).toBe(200);
    const me = JSON.parse(meRaw);
    expect(me.user.language).toBe('fr');
  });
});

// ===================================================================
// Section C — users.hub_user_id (V46 + backfill applicatif)
// ===================================================================
test.describe('@regression identity-cross-app — C. users.hub_user_id (V46)', () => {
  test('C12. Provision sans hub_user_id : user cree, hub_user_id NULL (smoke V46)', async () => {
    // Le body ProvisionInput n'accepte PAS de champ `hub_user_id` (cf.
    // internal/domain/veridian.go::ProvisionInput). Donc tout user cree
    // au Provision a `hub_user_id = NULL` cote DB (colonne NULLABLE V46).
    // On smoke-checke que :
    //   a) Provision marche apres l'ajout de la colonne V46 (pas de regression
    //      SQL SELECT ... FROM users a cause d'un mismatch schema).
    //   b) Le champ hub_user_id n'est PAS expose dans la reponse /api/user.me
    //      (intentionnel — interne, pas surface client).
    const tid = newTid();
    provisioned.push(tid);
    const ownerEmail = `${tid}@huid-nullable.test`;
    const provision = await provisionTenant(tid, ownerEmail, 'free');
    const apiKey = provision.api_key as string;

    // /api/user.me doit fonctionner sans crash apres l'ajout V46.
    const meResp = await bearerFetch('/api/user.me', 'GET', apiKey);
    const meRaw = await meResp.text();
    expect(meResp.status, meRaw).toBe(200);
    const me = JSON.parse(meRaw);
    expect(me.user).toBeDefined();
    expect(me.user.email).toBeTruthy();

    // hub_user_id n'est PAS dans la reponse (User struct ne le tague pas
    // json — cf. internal/domain/user.go ligne ~39, juste Language). Defense
    // anti-leak : si une future modif l'expose, ce test echoue.
    expect(me.user).not.toHaveProperty('hub_user_id');
    expect(me.user).not.toHaveProperty('HubUserID');
  });

  test('C13. AttachMember idempotent — 201 puis 200 already_member', async () => {
    // CONTRAT-HUB §5.22.2 : POST /api/veridian/workspaces/{id}/attach-member
    // body inclut `hub_user_id` (uuid Hub). Le service AttachMember resout :
    //   1. Lookup user Notifuse par email (cle canonique §3.7) -> si trouve,
    //      attach + best-effort BackfillHubUserID.
    //   2. Sinon cree user {id: nouveau UUID Notifuse} et attach,
    //      BackfillHubUserID stocke le hub_user_id en colonne dediee.
    //
    // Verifie :
    //   - 201 sur premier attach (cf. veridian_handler.go : StatusCreated)
    //   - 200 sur re-call idempotent (already_member=true)
    //   - role local intact (member) — §5.22.4 souverainete role local
    const tid = newTid();
    provisioned.push(tid);
    const ownerEmail = `${tid}@huid-attach.test`;
    const provision = await provisionTenant(tid, ownerEmail, 'free');
    expect(provision.workspace_id).toBe(tid);

    // Attach un nouveau member avec un hub_user_id arbitraire.
    const newMemberEmail = `${tid}-member@huid-attach.test`;
    const fakeHubUUID = crypto.randomUUID();
    const attachResp = await hmacFetch(
      `/api/veridian/workspaces/${tid}/attach-member`,
      'POST',
      {
        hub_user_id: fakeHubUUID,
        hub_user_email: newMemberEmail,
        role: 'member',
        invitation_id: `inv-${tid}`,
      },
    );
    const attachRaw = await attachResp.text();
    expect(attachResp.status, attachRaw).toBe(201);
    const attach = JSON.parse(attachRaw);
    expect(attach.attached).toBe(true);
    expect(attach.already_member).toBe(false);
    expect(attach.workspace_id).toBe(tid);
    expect(attach.role).toBe('member');
    expect(attach.login_url).toBeTruthy();

    // Idempotence : re-attach avec memes params -> 200 already_member=true.
    const reAttachResp = await hmacFetch(
      `/api/veridian/workspaces/${tid}/attach-member`,
      'POST',
      {
        hub_user_id: fakeHubUUID,
        hub_user_email: newMemberEmail,
        role: 'member',
        invitation_id: `inv-${tid}`,
      },
    );
    const reAttachRaw = await reAttachResp.text();
    expect(reAttachResp.status, reAttachRaw).toBe(200);
    const reAttach = JSON.parse(reAttachRaw);
    expect(reAttach.already_member).toBe(true);
  });

  test('C14. AttachMember persiste hub_user_id observable via /api/user.me', async () => {
    // Verifie le backfill applicatif (post-commit 86137e5f Agent L vague 2) :
    //   - GetUserByEmail/ID SELECT etendu retourne le champ hub_user_id
    //   - AttachMember backfill BackfillHubUserID best-effort §3.7
    //   - /api/user.me retourne user.hub_user_id pour le user concerne
    //
    // Strategie :
    //   1. Provision tenant -> cree owner X sans hub_user_id (V46 NULL).
    //   2. AttachMember pour un nouveau member email + hub_user_id Z.
    //   3. Exchange magic link -> JWT du nouveau member.
    //   4. /api/user.me -> user.hub_user_id === Z.
    const tid = newTid();
    provisioned.push(tid);
    const ownerEmail = `${tid}@huid-persist.test`;
    const provision = await provisionTenant(tid, ownerEmail, 'free');
    const apiKey = provision.api_key as string;

    const newMemberEmail = `${tid}-member@huid-persist.test`;
    const hubUUID = crypto.randomUUID();
    const attachResp = await hmacFetch(
      `/api/veridian/workspaces/${tid}/attach-member`,
      'POST',
      {
        hub_user_id: hubUUID,
        hub_user_email: newMemberEmail,
        role: 'member',
        invitation_id: `inv-${tid}`,
      },
    );
    const attachRaw = await attachResp.text();
    expect(attachResp.status, attachRaw).toBe(201);

    // Exchange magic link pour le nouveau member -> JWT user.
    const memberJWT = await exchangeMagicLinkForUserJWT(apiKey, newMemberEmail);

    const meResp = await bearerFetch('/api/user.me', 'GET', memberJWT);
    const meRaw = await meResp.text();
    expect(meResp.status, meRaw).toBe(200);
    const me = JSON.parse(meRaw);
    expect(me.user.email).toBe(newMemberEmail);
    // BackfillHubUserID a stocke le hub_user_id sur user_postgres.
    // /api/user.me l'expose via le tag json `hub_user_id,omitempty`.
    expect(me.user.hub_user_id).toBe(hubUUID);
  });

  test('C15. BackfillHubUserID non-bloquant sur mismatch (CONTRAT-HUB §3.7)', async () => {
    // CONTRAT-HUB §3.7 + commit 86137e5f : si AttachMember est rappele
    // sur le MEME user (meme email) mais avec un hub_user_id DIFFERENT,
    // le backfill renvoie ErrHubUserIDMismatch — l'attach NE doit PAS
    // echouer (warn log + continue, l'email reste cle canonique cross-app).
    //
    // Strategie :
    //   1. Provision tenant -> owner X.
    //   2. AttachMember member email Y + hub_user_id Z1 -> 201, backfill Z1.
    //   3. Tenter AttachMember meme member email Y + hub_user_id Z2 (different).
    //      Doit retourner 200 already_member=true (§5.22.4) sans erreur.
    //      Backfill rejette Z2 (mismatch) mais NE bloque PAS.
    //   4. /api/user.me du member -> user.hub_user_id === Z1 (intact).
    const tid = newTid();
    provisioned.push(tid);
    const ownerEmail = `${tid}@huid-mismatch.test`;
    const provision = await provisionTenant(tid, ownerEmail, 'free');
    const apiKey = provision.api_key as string;

    const memberEmail = `${tid}-mem@huid-mismatch.test`;
    const huid1 = crypto.randomUUID();
    const huid2 = crypto.randomUUID();
    expect(huid1).not.toBe(huid2);

    // 1er attach -> 201, backfill huid1.
    const r1 = await hmacFetch(
      `/api/veridian/workspaces/${tid}/attach-member`,
      'POST',
      { hub_user_id: huid1, hub_user_email: memberEmail, role: 'member', invitation_id: `inv-${tid}-1` },
    );
    const raw1 = await r1.text();
    expect(r1.status, raw1).toBe(201);

    // 2e attach avec huid2 different -> 200 already_member, pas d'erreur.
    const r2 = await hmacFetch(
      `/api/veridian/workspaces/${tid}/attach-member`,
      'POST',
      { hub_user_id: huid2, hub_user_email: memberEmail, role: 'member', invitation_id: `inv-${tid}-2` },
    );
    const raw2 = await r2.text();
    expect(r2.status, raw2).toBe(200);
    const body2 = JSON.parse(raw2);
    expect(body2.already_member).toBe(true);

    // /api/user.me du member doit retourner le huid initial (huid1) intact :
    // BackfillHubUserID a refuse d'ecraser (ErrHubUserIDMismatch warn log + continue).
    const memberJWT = await exchangeMagicLinkForUserJWT(apiKey, memberEmail);
    const meResp = await bearerFetch('/api/user.me', 'GET', memberJWT);
    const meRaw = await meResp.text();
    expect(meResp.status, meRaw).toBe(200);
    const me = JSON.parse(meRaw);
    expect(me.user.email).toBe(memberEmail);
    expect(me.user.hub_user_id).toBe(huid1); // intact, pas ecrase par huid2
  });
});
