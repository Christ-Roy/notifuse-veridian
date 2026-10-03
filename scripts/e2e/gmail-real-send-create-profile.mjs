// Preuve Playwright REELLE (Chromium headless) du parcours "Gmail par mot de
// passe d'application" depuis l'UI console, mission plafonds Gmail 2026-10-03.
//
// Contrairement a scripts/e2e/gmail-multi-profile-sink.sh (staging, SMTP sink
// loopback, ne prouve que le contrat backend), ce script pointe un VRAI compte
// Gmail (smtp.gmail.com reel) : il cree le profil via l'UI, PROD par defaut.
// A lancer uniquement contre un workspace JETABLE (notifuse-admin provision),
// jamais sur robertbrunon ni un workspace client.
//
// Usage :
//   AUTO_LOGIN_URL=... WID=<workspace jetable> GMAIL_SMTP_USER=... \
//     GMAIL_SMTP_PASS=... node scripts/e2e/gmail-real-send-create-profile.mjs
//
// AUTO_LOGIN_URL vient de `notifuse-admin provision <WID> --email <owner>`
// (TTL ~60s, a regenerer juste avant l'execution). GMAIL_SMTP_USER/PASS sont
// dans ~/credentials/.all-creds.env (mot de passe d'application Google, pas
// le mot de passe du compte).
//
import { chromium } from 'playwright'
import fs from 'node:fs'

const BASE = process.env.NOTIFUSE_URL || 'https://notifuse.app.veridian.site'
const AUTO_LOGIN_URL = process.env.AUTO_LOGIN_URL
const WID = process.env.WID
const GMAIL_USER = process.env.GMAIL_SMTP_USER
const GMAIL_PASS = process.env.GMAIL_SMTP_PASS
const PROFILE_NAME = 'Gmail E2E ' + Date.now()

if (!AUTO_LOGIN_URL || !WID || !GMAIL_USER || !GMAIL_PASS) {
  console.error('missing env'); process.exit(2)
}

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1600, height: 1200 } })
const logs = []
page.on('console', (m) => logs.push('[console] ' + m.text()))

try {
  console.log('STEP goto auto-login')
  await page.goto(AUTO_LOGIN_URL, { waitUntil: 'networkidle', timeout: 20000 })
  await page.waitForTimeout(1500)
  console.log('URL after auto-login:', page.url())

  const target = `${BASE}/console/workspace/${WID}/settings/integrations`
  console.log('STEP goto integrations', target)
  await page.goto(target, { waitUntil: 'networkidle', timeout: 20000 })
  await page.waitForTimeout(1000)

  console.log('STEP click Add Gmail profile (empty state or dropdown)')
  const emptyStateBtn = page.getByRole('button', { name: 'Add Gmail profile' })
  if (await emptyStateBtn.count() > 0) {
    await emptyStateBtn.click()
  } else {
    await page.getByRole('button', { name: /Add profile or integration/i }).click()
    await page.waitForTimeout(300)
    await page.getByText('Gmail with an app password', { exact: true }).click({ force: true })
  }
  await page.waitForTimeout(500)

  console.log('STEP fill form')
  await page.getByLabel('Profile name').fill(PROFILE_NAME)
  await page.getByLabel('Gmail address').fill(GMAIL_USER)
await page.getByLabel('Sender name').fill('Veridian Gmail E2E')
  const pwInput = page.locator('input[type="password"]').first()
  await pwInput.fill(GMAIL_PASS)

  console.log('STEP submit')
  const saveBtn = page.getByRole('button', { name: /Save|Create|Add profile/i }).last()
  await saveBtn.click()
  await page.waitForTimeout(2500)

  const bodyText = await page.locator('body').innerText()
  fs.writeFileSync('/tmp/gmail-e2e-after-save.txt', bodyText)
  console.log('SAVED, body snapshot written')

  console.log('STEP find profile card and test button')
  const card = page.locator('text=' + PROFILE_NAME).first()
  await card.waitFor({ timeout: 10000 })
  let testClicked = false
  for (const label of ['Send test', 'Test', 'Send test email']) {
    const btn = page.getByRole('button', { name: new RegExp(label, 'i') })
    if (await btn.count() > 0) {
      await btn.first().click({ force: true })
      testClicked = true
      break
    }
  }
  if (!testClicked) {
    console.log('WARN no explicit test button found by role; dumping page text')
    fs.writeFileSync('/tmp/gmail-e2e-no-test-btn.txt', await page.locator('body').innerText())
    throw new Error('no test button')
  }

  await page.waitForTimeout(1500)
  fs.writeFileSync('/tmp/gmail-e2e-after-test-click.txt', await page.locator('body').innerText())
  const emailField = page.locator('input[placeholder="recipient@example.com"]')
  await emailField.waitFor({ timeout: 10000 })
  await emailField.fill(GMAIL_USER)
  const sendBtn = page.getByRole('button', { name: /Send Test Email/i })
  await sendBtn.click()
  await page.waitForTimeout(5000)
  const after = await page.locator('body').innerText()
  fs.writeFileSync('/tmp/gmail-e2e-after-test.txt', after)
  console.log('TEST EMAIL TRIGGERED')

  console.log('DONE_OK')
} catch (e) {
  console.error('FAILED', e.message)
  await page.screenshot({ path: '/tmp/gmail-e2e-error.png', fullPage: true }).catch(()=>{})
  fs.writeFileSync('/tmp/gmail-e2e-error-body.txt', await page.locator('body').innerText().catch(()=>'n/a'))
  process.exitCode = 1
} finally {
  console.log(logs.join('\n'))
  await browser.close()
}
