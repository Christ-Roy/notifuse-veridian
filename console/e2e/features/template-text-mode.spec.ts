import { test, expect } from '../fixtures/auth'
import { waitForDrawer, waitForLoading } from '../fixtures/test-utils'
import type { Page, Route } from '@playwright/test'

const WORKSPACE_ID = 'test-workspace'
const API = 'https://localapi.notifuse.com:4000'

const plainTemplate = {
  id: 'plain-1',
  name: 'Plain Text Template',
  version: 1,
  channel: 'email',
  category: 'marketing',
  email: {
    subject: 'Hello {{ contact.first_name }}',
    subject_preview: 'preview',
    compiled_preview: '',
    plain_text_only: true,
    text: 'Bonjour {{ contact.first_name }}, texte initial.',
    editor_mode: 'visual',
    visual_editor_tree: {
      id: 'root',
      type: 'mjml',
      children: [{ id: 'b', type: 'mj-body', children: [] }]
    }
  },
  test_data: {},
  created_at: '2024-01-01T00:00:00Z',
  updated_at: '2024-01-01T00:00:00Z'
}

// Les routes posees ici passent avant celles de la fixture (dernier enregistre, premier servi).
async function mockPlainTemplate(page: Page, sent: Record<string, unknown>[]) {
  // config.js est demande avec une query string (?_t=...) que le glob de la fixture ne couvre pas.
  await page.route('**/config.js*', (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/javascript',
      body: `window.API_URL = "${API}"; window.API_ENDPOINT = "${API}"; window.ROOT_EMAIL = "test@example.com"; window.IS_INSTALLED = true;`
    })
  )
  await page.route(`${API}/api/templates.list*`, (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ templates: [plainTemplate] })
    })
  )
  await page.route(`${API}/api/templates.update*`, async (route: Route) => {
    sent.push(route.request().postDataJSON())
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ template: plainTemplate })
    })
  })
}

test.describe('Template editor: Text mode', () => {
  test('editing a plain_text_only template keeps plain_text_only and saves the new text', async ({
    authenticatedPage
  }) => {
    const page = authenticatedPage
    const sent: Record<string, unknown>[] = []
    await mockPlainTemplate(page, sent)

    await page.goto(`/console/workspace/${WORKSPACE_ID}/templates`)
    await waitForLoading(page)

    const row = page.locator('.ant-table-row').first()
    await expect(row).toBeVisible()
    await row.locator('button:has(svg[data-icon="pen-to-square"])').click()
    await waitForDrawer(page)

    // Mode « Texte » preselectionne, pas de bascule en HTML.
    await expect(page.locator('.ant-radio-button-wrapper-checked').filter({ hasText: 'Text' })).toBeVisible()

    await page.getByRole('button', { name: 'Next' }).click()
    const body = page.getByTestId('template-text-body')
    await expect(body).toHaveValue('Bonjour {{ contact.first_name }}, texte initial.')
    await body.fill('Salut {{ contact.first_name }}, texte MODIFIE.')

    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => sent.length).toBe(1)

    const email = (sent[0] as { email: Record<string, unknown> }).email
    expect(email.plain_text_only).toBe(true)
    expect(email.text).toBe('Salut {{ contact.first_name }}, texte MODIFIE.')
    expect(email.editor_mode).toBe('visual')
    expect(sent[0]).toMatchObject({ id: 'plain-1', channel: 'email' })
  })

  test('an empty text body is refused client-side', async ({ authenticatedPage }) => {
    const page = authenticatedPage
    const sent: Record<string, unknown>[] = []
    await mockPlainTemplate(page, sent)

    await page.goto(`/console/workspace/${WORKSPACE_ID}/templates`)
    await waitForLoading(page)
    await page.locator('.ant-table-row').first().locator('button:has(svg[data-icon="pen-to-square"])').click()
    await waitForDrawer(page)
    await page.getByRole('button', { name: 'Next' }).click()
    await page.getByTestId('template-text-body').fill('   ')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('The text body is required in Text mode')).toBeVisible()
    expect(sent.length).toBe(0)
  })

  test('creating a template in Text mode sends plain_text_only and the text', async ({
    authenticatedPage
  }) => {
    const page = authenticatedPage
    const created: Record<string, unknown>[] = []
    await mockPlainTemplate(page, [])
    await page.route(`${API}/api/templates.create*`, async (route: Route) => {
      created.push(route.request().postDataJSON())
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ template: plainTemplate })
      })
    })

    await page.goto(`/console/workspace/${WORKSPACE_ID}/templates`)
    await waitForLoading(page)
    await page.getByRole('button', { name: 'Create Template' }).first().click()
    await waitForDrawer(page)

    await page.locator('.ant-drawer-content input:visible').first().fill('Texte brut')
    await page.locator('.ant-drawer-content .ant-select').first().click()
    await page.locator('.ant-select-item-option').first().click()
    await page.locator('.ant-radio-button-wrapper').filter({ hasText: 'Text' }).click()
    await page.locator('#email_subject').fill('Sujet {{ contact.first_name }}')
    await page.locator('.ant-drawer-content #email_subject_preview').fill('apercu')
    await page.getByRole('button', { name: 'Next' }).click()
    await page.getByTestId('template-text-body').fill('Corps {{ contact.first_name }}')
    await page.getByRole('button', { name: 'Save' }).click()

    await expect.poll(() => created.length).toBe(1)
    const email = (created[0] as { email: Record<string, unknown> }).email
    expect(email.plain_text_only).toBe(true)
    expect(email.text).toBe('Corps {{ contact.first_name }}')
  })
})
