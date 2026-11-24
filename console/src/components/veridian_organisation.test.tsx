import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { theme } from 'antd'
import { VeridianBrandProvider, usableBrandColor } from './veridian_organisation'

const get = vi.fn()
vi.mock('../services/api/client', () => ({ api: { get: (...a: unknown[]) => get(...a) } }))

function Probe() {
  const { token } = theme.useToken()
  return <span data-testid="primary">{token.colorPrimary}</span>
}

const show = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <VeridianBrandProvider workspaceId="a b">
        <Probe />
      </VeridianBrandProvider>
    </QueryClientProvider>
  )

const favicon = () => document.querySelector('link[data-veridian-brand]')

describe('organization brand pushed by the Hub', () => {
  beforeEach(() => {
    get.mockReset() // sans accolades, vitest prendrait le mock rendu pour un nettoyage et l'appellerait
  })

  it('applies the color and the favicon, and removes the favicon on unmount', async () => {
    get.mockResolvedValue({ managed: true, brand: { couleur_primaire: '#0a5c36', favicon_url: 'https://cdn.test/f.ico' } })
    const view = show()
    await waitFor(() => expect(screen.getByTestId('primary').textContent).toBe('#0a5c36'))
    expect(get).toHaveBeenCalledWith('/api/veridian/organisation?workspace_id=a%20b')
    expect(favicon()?.getAttribute('href')).toBe('https://cdn.test/f.ico')
    view.unmount()
    expect(favicon()).toBeNull()
  })

  it.each([
    ['nothing pushed', { managed: false }],
    ['an unreadable color', { managed: true, brand: { couleur_primaire: '#ffff00' } }],
    ['a broken answer', null]
  ])('keeps the default theme with %s', async (_name, answer) => {
    get.mockResolvedValue(answer)
    show()
    const before = screen.getByTestId('primary').textContent
    await waitFor(() => expect(get).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 30))
    expect(screen.getByTestId('primary').textContent).toBe(before)
    expect(favicon()).toBeNull()
  })

  it('never blocks the console when the route fails', async () => {
    get.mockRejectedValue(new Error('404'))
    show()
    await waitFor(() => expect(get).toHaveBeenCalled())
    expect(screen.getByTestId('primary')).toBeInTheDocument()
  })

  it('only keeps a color white text stays readable on', () => {
    expect(usableBrandColor('#112233')).toBe('#112233')
    for (const bad of [undefined, '', 'red', '#fff', '#FFFFFF', '#ffff00']) expect(usableBrandColor(bad)).toBeUndefined()
  })
})
