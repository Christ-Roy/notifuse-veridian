// === Veridian patch — console-suite : parcours authentification ===
//
// Couverture (§2 du ticket) :
//   - Auto-login URL (le flow Hub→Notifuse réel) : token HMAC self-contained
//     → page intermédiaire stocke auth_token + redirect /console
//   - Magic link freshly issued via API key : fetch /api/workspaces.generateMagicLink
//     puis suit le lien, vérifie la session est ouverte
//   - Token expiré/falsifié : la page intermédiaire affiche un <Result>
//     d'erreur, pas un crash
//
// Crée 1 tenant éphémère (cleanup afterAll). NON `@prod-safe` (side-effect).

import { test, expect } from '@playwright/test'
import {
  NOTIFUSE_URL,
  newTenantId,
  provisionTenant,
  wipeTenants,
  loginViaAutoLogin,
  trackConsoleErrors,
  waitForAppMount,
  type ProvisioningResponse
} from './helpers'

const tenantId = newTenantId()
const ownerEmail = `${tenantId}@e2e.veridian.test`
const provisioned: string[] = [tenantId]
let provisioning: ProvisioningResponse

test.beforeAll(async () => {
  provisioning = await provisionTenant(tenantId, ownerEmail, 'pro')
})

test.afterAll(async () => {
  await wipeTenants(provisioned)
})

test.describe.serial('Console — parcours authentification', () => {
  test('Auto-login URL ouvre une session navigateur', async ({ page }) => {
    const errors = trackConsoleErrors(page)

    // Pré-condition : provision a bien retourné un auto_login_url HMAC.
    expect(provisioning.auto_login_url).toContain('/veridian/auto-login?token=')

    await loginViaAutoLogin(page, provisioning.auto_login_url)
    await waitForAppMount(page)

    // Après auto-login, on est sur /console (root) ou /console/workspace/{id}.
    // L'app a hydraté, on doit voir au moins du UI authentifié.
    const url = page.url()
    expect(url, `URL post-auto-login : ${url}`).not.toContain('/signin')

    // Pas d'erreur JS = le boot post-auth a bien marché (provider auth ok,
    // workspace fetch ok, sidebar mountée).
    expect(errors, `Erreurs JS post-auto-login :\n${errors.join('\n---\n')}`).toEqual([])
  })

  test('Magic link fresh issued via API key permet aussi de login', async ({ page }) => {
    // Génère un nouveau magic link via API key (sans HMAC, le flow "app
    // utilise sa propre clé pour issuer un lien à son user").
    const res = await fetch(`${NOTIFUSE_URL}/api/workspaces.generateMagicLink`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${provisioning.api_key}`
      },
      body: JSON.stringify({ user_email: ownerEmail })
    })
    expect(res.status).toBe(200)
    const data = await res.json()
    expect(data.auto_login_url).toContain('/veridian/auto-login?token=')

    const errors = trackConsoleErrors(page)
    await loginViaAutoLogin(page, data.auto_login_url)
    await waitForAppMount(page)

    expect(page.url()).not.toContain('/signin')
    expect(errors, `Erreurs JS magic link :\n${errors.join('\n---\n')}`).toEqual([])
  })

  test('Token auto-login falsifié → page <Result> sans crash', async ({ page }) => {
    // Garde-fou anti-régression : un token corrompu (1 char modifié) doit
    // renvoyer un écran d'erreur explicite, PAS un crash JS ou un blank.
    // Le pattern d'erreur peut varier selon que la signature HMAC rate,
    // le format JWT-like rate, ou le payload est rejeté — on tolère les 3.
    const errors = trackConsoleErrors(page)

    const goodUrl = provisioning.auto_login_url
    // Falsifie 1 char dans le token (préserve la longueur pour passer le
    // 1er check de format).
    const badUrl = goodUrl.replace(/token=([A-Za-z0-9._-]{10})/, (_m, p1) => {
      const idx = Math.floor(p1.length / 2)
      const flip = p1[idx] === 'a' ? 'b' : 'a'
      return `token=${p1.slice(0, idx)}${flip}${p1.slice(idx + 1)}`
    })

    await page.goto(badUrl)
    await waitForAppMount(page)
    await page.waitForTimeout(2000)

    // Doit ne PAS être sur /console (= la falsification a été détectée).
    // Acceptable : page d'erreur Veridian, redirect /signin, ou page Notifuse
    // standard avec message d'erreur.
    const url = page.url()
    const body = await page.locator('body').innerText().catch(() => '')

    // Soit on est resté sur /veridian/auto-login (page intermédiaire qui
    // affiche l'erreur), soit on a été redirigé vers signin/logout.
    // Soit on est sur /console/ MAIS sans auth_token valide en localStorage.
    if (url.includes('/console/') && !url.includes('/signin')) {
      const token = await page.evaluate(() => localStorage.getItem('auth_token'))
      expect(
        !token || token.length < 50,
        `Token falsifié accepté : auth_token=${token?.slice(0, 30)}... (URL=${url})`
      ).toBe(true)
    } else {
      // L'écran d'erreur doit afficher quelque chose qui parle d'expiration,
      // d'invalidité, ou de problème. Si la page est complètement vide,
      // c'est un crash silencieux à investiguer.
      expect(body.length, `Body vide post-token-falsifié (URL=${url}) — crash silencieux ?`).toBeGreaterThan(20)
    }

    expect(errors, `Erreurs JS post-token-falsifié :\n${errors.join('\n---\n')}`).toEqual([])
  })
})
