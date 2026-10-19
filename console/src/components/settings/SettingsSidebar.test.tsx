import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SettingsSidebar } from './SettingsSidebar'

// Audit UI 19/10/2026 (C3) : Webhooks, API et agents, Champs personnalises et Cold outreach
// etaient exposes a tout owner au meme niveau que Equipe et Integrations. Ils vivent maintenant
// sous « Advanced », replie : accessible en un clic, rien de supprime.
describe('SettingsSidebar : section Avance repliee', () => {
  const ADVANCED = [
    'Event notifications (webhooks)',
    'Custom contact fields',
    'Sending limits',
    'API keys & AI agents'
  ]

  it('ne montre que Equipe, Integrations, General, Plan ; les reglages d ingenieur sont replies', () => {
    render(<SettingsSidebar activeSection="team" onSectionChange={() => {}} isOwner />)
    for (const label of ['Team', 'Integrations', 'General', 'Plan', 'Advanced', 'Danger Zone']) {
      expect(screen.getByText(label)).toBeInTheDocument()
    }
    for (const label of ADVANCED) {
      expect(screen.queryByText(label)).not.toBeInTheDocument()
    }
  })

  it('un clic sur Advanced les rend tous accessibles', () => {
    const picked: string[] = []
    render(
      <SettingsSidebar activeSection="team" onSectionChange={(s) => picked.push(s)} isOwner={false} />
    )
    fireEvent.click(screen.getByText('Advanced'))
    for (const label of ADVANCED) {
      expect(screen.getByText(label)).toBeInTheDocument()
    }
    fireEvent.click(screen.getByText('Sending limits'))
    expect(picked).toEqual(['cold-outreach'])
    expect(screen.queryByText('Danger Zone')).not.toBeInTheDocument()
  })

  it('reste ouverte quand la page affichee est une page avancee', () => {
    render(<SettingsSidebar activeSection="webhooks" onSectionChange={() => {}} isOwner />)
    expect(screen.getByText('Event notifications (webhooks)')).toBeInTheDocument()
  })
})
