// Suite de scripts/e2e/gmail-real-send-create-profile.mjs : ouvre la modale
// "Test Email Provider" du SEUL profil email du workspace et declenche un VRAI
// envoi SMTP vers Gmail. Le backend n'autorise ce test QUE vers l'adresse de
// l'OWNER authentifie du workspace (garde-fou anti-spam cote API) : provisionne
// le workspace jetable avec --email <adresse du vrai compte Gmail testé>.
//
// Usage :
//   AUTO_LOGIN_URL=... WID=<workspace jetable> GMAIL_SMTP_USER=... \
//     node scripts/e2e/gmail-real-send-trigger-test.mjs
//
import { chromium } from 'playwright'
import fs from 'node:fs'

const BASE = process.env.NOTIFUSE_URL || 'https://notifuse.app.veridian.site'
const AUTO_LOGIN_URL = process.env.AUTO_LOGIN_URL
const WID = process.env.WID
const GMAIL_USER = process.env.GMAIL_SMTP_USER

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1600, height: 1200 } })

try {
  await page.goto(AUTO_LOGIN_URL, { waitUntil: 'networkidle', timeout: 20000 })
  await page.waitForTimeout(1000)
  await page.goto(`${BASE}/console/workspace/${WID}/settings/integrations`, { waitUntil: 'networkidle', timeout: 20000 })
  await page.waitForTimeout(1000)

  console.log('STEP click Test button (single profile)')
  const testBtn = page.getByRole('button', { name: 'Test' })
  await testBtn.waitFor({ timeout: 10000 })
  await testBtn.dispatchEvent('click')
  await page.waitForTimeout(1000)

  console.log('STEP wait for modal title')
  await page.getByText('Test Email Provider', { exact: true }).waitFor({ timeout: 10000 })
  const emailField = page.locator('input[placeholder="recipient@example.com"]')
  await emailField.waitFor({ timeout: 10000 })
  await emailField.fill(GMAIL_USER)
  await page.getByRole('button', { name: 'Send Test Email' }).click()
  await page.waitForTimeout(6000)
  const after = await page.locator('body').innerText()
  fs.writeFileSync('/tmp/gmail-e2e-after-test.txt', after)
  console.log('TEST_EMAIL_TRIGGERED_OK')
} catch (e) {
  console.error('FAILED', e.message)
  fs.writeFileSync('/tmp/gmail-e2e-test-error-body.txt', await page.locator('body').innerText().catch(()=>'n/a'))
  process.exitCode = 1
} finally {
  await browser.close()
}
