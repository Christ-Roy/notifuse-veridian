import { test, expect } from '../fixtures/auth'
import { waitForLoading } from '../fixtures/test-utils'

const WORKSPACE_ID = 'test-workspace'

// === Veridian patch — mission "API & agents" (2026-10-03) ===
//
// Parcours couvert : lister les clés, en créer une (jeton affiché une seule
// fois, copiable), générer la commande d'installation à usage unique
// ("Brancher mon agent", jamais la vraie clé API en clair), puis révoquer
// une clé. L'exécution réelle du script d'installation (téléchargement du
// CLI, écriture de ~/.config/notifuse/env, `notifuse lists:list`) est hors
// de la portée d'un navigateur Playwright — elle est prouvée séparément en
// shell (voir le rapport de mission : preuve d'intégration locale contre le
// serveur Go réel).
test.describe('API & agents settings', () => {
  test('lists existing API keys with masked email, never the real token', async ({
    authenticatedPageWithData
  }) => {
    const page = authenticatedPageWithData

    await page.goto(`/console/workspace/${WORKSPACE_ID}/settings/api-agents`)
    await waitForLoading(page)

    await expect(page.locator('text=API & agents').first()).toBeVisible()
    await expect(page.locator('.ant-table')).toBeVisible()
    await expect(page.locator('text=agent_ci').first()).toBeVisible()
    await expect(page.locator('text=age***@notifuse.app').first()).toBeVisible()
  })

  test('creates an API key and shows the token exactly once, copyable', async ({
    authenticatedPageWithData
  }) => {
    const page = authenticatedPageWithData

    await page.goto(`/console/workspace/${WORKSPACE_ID}/settings/api-agents`)
    await waitForLoading(page)

    await page.getByRole('button', { name: /Create API Key/i }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()

    await dialog.getByPlaceholder('my_agent').fill('e2e agent')
    await dialog.getByRole('button', { name: /^Create API Key$/ }).click()

    // The mocked token must appear exactly once, in a read-only field.
    await expect(page.locator('textarea[readonly]')).toHaveValue(
      'e2e-mock-jwt-token-never-real'
    )
    await expect(dialog.getByRole('button', { name: /Copy/i })).toBeVisible()
  })

  test('"Connect my agent" generates a one-time install command without ever exposing the raw API key', async ({
    authenticatedPageWithData
  }) => {
    const page = authenticatedPageWithData

    await page.goto(`/console/workspace/${WORKSPACE_ID}/settings/api-agents`)
    await waitForLoading(page)

    await page.getByRole('button', { name: /Connect my agent/i }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()

    await dialog.getByRole('button', { name: /Generate command/i }).click()

    const commandField = page.locator('textarea[readonly]')
    await expect(commandField).toHaveValue(/agent\/install\.sh/)
    await expect(commandField).toHaveValue(/--token e2e-mock-install-token-one-time/)
    // Never the raw API key token on this screen.
    await expect(page.locator('body')).not.toContainText('e2e-mock-jwt-token-never-real')
  })

  test('non-technical explanation is reachable in 3 steps', async ({
    authenticatedPageWithData
  }) => {
    const page = authenticatedPageWithData

    await page.goto(`/console/workspace/${WORKSPACE_ID}/settings/api-agents`)
    await waitForLoading(page)

    await page.getByRole('button', { name: /Connect my agent/i }).click()
    const dialog = page.getByRole('dialog')
    await dialog.getByRole('button', { name: /not technical/i }).click()

    await expect(dialog.locator('text=Generate a one-time command')).toBeVisible()
    await expect(dialog.locator('text=Give it to your AI agent')).toBeVisible()
  })

  test('revokes an API key after confirmation', async ({ authenticatedPageWithData }) => {
    const page = authenticatedPageWithData

    await page.goto(`/console/workspace/${WORKSPACE_ID}/settings/api-agents`)
    await waitForLoading(page)

    await expect(page.locator('text=agent_ci').first()).toBeVisible()

    const row = page.locator('.ant-table-row').filter({ hasText: 'agent_ci' })
    await row.getByRole('button').click()

    // Popconfirm asks for explicit confirmation before revoking.
    await page.getByRole('button', { name: /^Revoke$/ }).click()

    // No crash, no error toast — the mocked revoke succeeds.
    await expect(page.locator('.ant-message-error')).toHaveCount(0)
  })
})
