import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

import { ChartVisualization } from './ChartVisualization'
import type { AnalyticsQuery, AnalyticsResponse } from '../../services/api/analytics'

// echarts n'a pas de canvas en jsdom : on neutralise le rendu et on lit les options passées.
const setOption = vi.fn()
vi.mock('echarts/core', () => ({
  use: vi.fn(),
  init: vi.fn(() => ({ setOption, resize: vi.fn(), dispose: vi.fn() }))
}))
vi.mock('echarts/charts', () => ({ LineChart: {}, BarChart: {}, PieChart: {} }))
vi.mock('echarts/components', () => ({ TitleComponent: {}, TooltipComponent: {}, GridComponent: {}, LegendComponent: {} }))
vi.mock('echarts/renderers', () => ({ CanvasRenderer: {} }))

const query = {
  measures: ['count_sent'],
  dimensions: [],
  timeDimensions: [{ dimension: 'created_at', granularity: 'day' }]
} as unknown as AnalyticsQuery

const response = {
  data: [
    { created_at_day: '2026-10-08T00:00:00Z', count_sent: 12345 },
    { created_at_day: '2026-10-09T00:00:00Z', count_sent: 7 }
  ],
  meta: { total: 2, query: '', params: [] }
} as unknown as AnalyticsResponse

const renderIt = (props: Partial<React.ComponentProps<typeof ChartVisualization>> = {}) =>
  render(
    <I18nProvider i18n={i18n}>
      <ChartVisualization data={response} chartType="line" query={query} {...props} />
    </I18nProvider>
  )

describe('ChartVisualization', () => {
  beforeEach(() => {
    setOption.mockClear()
    i18n.loadAndActivate({ locale: 'fr', messages: {} })
  })
  afterEach(() => {
    i18n.loadAndActivate({ locale: 'en', messages: {} })
  })

  it("affiche l'erreur et ne dessine rien", () => {
    renderIt({ error: 'boom' })
    expect(screen.getByText('boom')).toBeInTheDocument()
    expect(setOption).not.toHaveBeenCalled()
  })

  it('pendant le chargement, ne dessine pas non plus', () => {
    renderIt({ loading: true })
    expect(setOption).not.toHaveBeenCalled()
  })

  it('axe des dates dans la langue de la console (octobre, pas Oct)', () => {
    renderIt()
    const option = setOption.mock.calls.at(-1)![0]
    expect(option.xAxis.data[0]).toMatch(/8 oct\./)
    expect(option.xAxis.data[0]).not.toMatch(/Oct/)
  })

  it("l'infobulle formate les nombres avec l'espace des milliers français", () => {
    renderIt()
    const option = setOption.mock.calls.at(-1)![0]
    const html: string = option.tooltip.formatter([
      { axisValue: '8 oct.', seriesName: 'count_sent', value: 12345, color: '#000' }
    ])
    expect(html).toMatch(/12[\u202f\u00a0]345/)
    expect(html).not.toContain('12,345')
  })
})
