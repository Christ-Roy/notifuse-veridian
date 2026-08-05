import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import type { ReactNode } from 'react'

import type { Workspace } from '../../services/api/types'
import { RecipientProviderPolicy, SendingProfilesOverview } from './veridian_sending_profiles_ui'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const workspace = {
  id: 'workspace-1',
  name: 'Demo',
  settings: {
    timezone: 'Europe/Paris',
    email_tracking_enabled: true,
    default_language: 'en',
    languages: ['en'],
    marketing_email_provider_id: 'gmail-a',
    veridian_marketing_email_provider_ids: ['gmail-a', 'gmail-b'],
    veridian_provider_class_rates: { google: 1 },
    veridian_provider_class_daily_cap: { google: 20 },
    veridian_sending_window: {
      days: [1, 2, 3, 4, 5],
      start_hour: 9,
      end_hour: 18,
      timezone: 'Europe/Paris'
    }
  },
  integrations: [
    {
      id: 'gmail-a',
      name: 'Gmail A',
      type: 'email',
      created_at: '',
      updated_at: '',
      email_provider: {
        kind: 'smtp',
        rate_limit_per_minute: 1,
        veridian_profile_daily_cap: 30,
        senders: []
      }
    },
    {
      id: 'gmail-b',
      name: 'Gmail B',
      type: 'email',
      created_at: '',
      updated_at: '',
      email_provider: {
        kind: 'smtp',
        rate_limit_per_minute: 1,
        veridian_profile_daily_cap: 30,
        senders: []
      }
    }
  ]
} as Workspace

const renderWithI18n = (node: ReactNode) => render(<I18nProvider i18n={i18n}>{node}</I18nProvider>)

describe('sending profiles overview', () => {
  it('shows the real multi-profile rotation and global business window', () => {
    renderWithI18n(<SendingProfilesOverview workspace={workspace} usage={null} usageError />)

    expect(screen.getByText('2 active profiles')).toBeInTheDocument()
    expect(screen.getByText(/Mon, Tue, Wed, Thu, Fri.*09:00-18:00/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Configure policy' })).toHaveAttribute(
      'href',
      '/console/workspace/workspace-1/settings/cold-outreach'
    )
    expect(screen.getByText("Today's usage is unavailable")).toBeInTheDocument()
    expect(screen.queryByText(/emails sent today/)).not.toBeInTheDocument()
  })

  it('ventilates effective limits by recipient provider', async () => {
    renderWithI18n(
      <RecipientProviderPolicy
        workspace={workspace}
        usage={{
          integration_id: 'gmail-a',
          used: 12,
          cap: 30,
          remaining: 18,
          by_provider_class: { google: 7, microsoft: 5, future_provider: 3 }
        }}
        provider={{
          kind: 'smtp',
          rate_limit_per_minute: 1,
          senders: [],
          veridian_provider_class_rates: { microsoft: 0.5 },
          veridian_provider_class_daily_cap: { microsoft: 10 }
        }}
      />
    )

    expect(screen.getByText('Recipient-provider policy')).toBeInTheDocument()
    expect(screen.getByText('Google')).toBeInTheDocument()
    expect(screen.getByText('Microsoft')).toBeInTheDocument()
    expect(screen.getByText('OVH')).toBeInTheDocument()
    expect(screen.getByText('IONOS / 1&1')).toBeInTheDocument()
    expect(screen.getByText('Passerelles anti-spam')).toBeInTheDocument()
    expect(screen.getByText('Corporate auto-hébergé')).toBeInTheDocument()
    expect(screen.getByText('0.5 / min')).toBeInTheDocument()
    expect(screen.getByText('10 / day')).toBeInTheDocument()
    expect(screen.getByText('7 sent today')).toBeInTheDocument()
    expect(screen.getByText('Unknown class: future_provider')).toBeInTheDocument()
    expect(screen.getByText('3 sent today')).toBeInTheDocument()
  })
})
