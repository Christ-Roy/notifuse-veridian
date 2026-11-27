import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

import { useQueueLabels } from './veridian_queue_labels'

i18n.loadAndActivate({ locale: 'en', messages: {} })

// Le détail brut du moteur (« class=… », « reopens=… ») ne doit jamais sortir tel quel à un client.
function Probe({ detail }: { detail: string }) {
  const labels = useQueueLabels()
  return <span data-testid="out">{labels.reasonDetailLabel('deferred', detail)}</span>
}

const rendu = (detail: string) =>
  render(
    <I18nProvider i18n={i18n}>
      <Probe detail={detail} />
    </I18nProvider>
  )

describe('détail d un groupe de la file d envoi', () => {
  it('une classe de fournisseur est affichée par son libellé, pas par sa clé brute', () => {
    rendu('class=corporate_selfhost')
    expect(screen.getByTestId('out').textContent).toBe('Self-hosted corporate')
  })

  it('une réouverture reste dans la colonne « Prochaine tentative » : aucun détail brut affiché', () => {
    rendu('reopens=2026-11-27T08:00:00Z')
    expect(screen.getByTestId('out').textContent).toBe('')
  })
})
