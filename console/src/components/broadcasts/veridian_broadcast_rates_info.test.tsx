import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import {
  VeridianBroadcastRatesInfo,
  parseBroadcastRates
} from './veridian_broadcast_rates_info'

i18n.loadAndActivate({ locale: 'en', messages: {} })

function renderInfo(metadata?: Record<string, unknown>) {
  return render(
    <I18nProvider i18n={i18n}>
      <VeridianBroadcastRatesInfo metadata={metadata} />
    </I18nProvider>
  )
}

describe('parseBroadcastRates', () => {
  it('returns empty for undefined / missing / malformed metadata', () => {
    expect(parseBroadcastRates(undefined)).toEqual({})
    expect(parseBroadcastRates({})).toEqual({})
    expect(parseBroadcastRates({ veridian_provider_class_rates: 'nope' })).toEqual({})
  })

  it('keeps only canonical classes with strictly positive numeric rates', () => {
    const out = parseBroadcastRates({
      veridian_provider_class_rates: {
        google: 1,
        microsoft: 0, // ignoré (pas > 0)
        freemail_fr: 4.5,
        INVALID: 99, // ignoré (classe non canonique)
        corporate: -3 // ignoré (négatif)
      }
    })
    expect(out).toEqual({ google: 1, freemail_fr: 4.5 })
  })
})

describe('VeridianBroadcastRatesInfo', () => {
  it('renders nothing when no rates are set (transparent for classic broadcasts)', () => {
    const { container } = renderInfo(undefined)
    expect(container).toBeEmptyDOMElement()
    const { container: c2 } = renderInfo({ foo: 'bar' })
    expect(c2).toBeEmptyDOMElement()
  })

  it('renders one tag per configured class with its rate', () => {
    renderInfo({
      veridian_provider_class_rates: { google: 1, freemail_fr: 4 }
    })
    expect(screen.getByText(/Per-provider sending rates/i)).toBeInTheDocument()
    expect(screen.getByText(/Google: 1 emails\/min/)).toBeInTheDocument()
    expect(screen.getByText(/FAI FR: 4 emails\/min/)).toBeInTheDocument()
  })
})
