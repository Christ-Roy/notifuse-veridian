import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { WebhookConfigForm } from './WebhookConfigForm'
import type { WebhookNodeConfig } from '../../../services/api/automation'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const renderForm = (config: Partial<WebhookNodeConfig>, onChange = vi.fn()) =>
  render(
    <I18nProvider i18n={i18n}>
      <WebhookConfigForm config={config as WebhookNodeConfig} onChange={onChange} />
    </I18nProvider>
  )

describe('WebhookConfigForm', () => {
  it('rejects http:// URLs (HTTPS only)', () => {
    renderForm({ url: 'http://example.com/hook' })
    expect(screen.getByText('URL must start with https://')).toBeInTheDocument()
  })

  it('accepts https:// URLs', () => {
    renderForm({ url: 'https://example.com/hook' })
    expect(screen.queryByText('URL must start with https://')).not.toBeInTheDocument()
  })

  it('never shows a stored secret: only a hidden-secret hint and a remove button', () => {
    const { container } = renderForm({ url: 'https://example.com/hook', has_secret: true })
    const input = container.querySelector('input[type="password"]') as HTMLInputElement
    expect(input.value).toBe('')
    expect(input.placeholder).toContain('Secret saved')
    expect(screen.getByText('Remove secret')).toBeInTheDocument()
  })

  it('does not offer to remove a secret when none is stored', () => {
    renderForm({ url: 'https://example.com/hook', has_secret: false })
    expect(screen.queryByText('Remove secret')).not.toBeInTheDocument()
  })

  it('sends a new secret in clear only when typed', () => {
    const onChange = vi.fn()
    const { container } = renderForm({ url: 'https://example.com/hook', has_secret: true }, onChange)
    const input = container.querySelector('input[type="password"]') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'new-secret' } })
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ secret: 'new-secret' }))
  })

  it('flags clear_secret when removing the secret', () => {
    const onChange = vi.fn()
    renderForm({ url: 'https://example.com/hook', has_secret: true }, onChange)
    fireEvent.click(screen.getByText('Remove secret'))
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ clear_secret: true, has_secret: false })
    )
  })
})
