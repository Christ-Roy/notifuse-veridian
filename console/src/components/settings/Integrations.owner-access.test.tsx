import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { IntegrationOwnerNotice } from './Integrations'

i18n.loadAndActivate({ locale: 'en', messages: {} })

describe('IntegrationOwnerNotice', () => {
  it('explains that sending profiles can only be configured by the workspace owner', () => {
    render(
      <I18nProvider i18n={i18n}>
        <IntegrationOwnerNotice />
      </I18nProvider>
    )

    expect(screen.getByText('Only workspace owners can modify integrations')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toBeInTheDocument()
  })
})
