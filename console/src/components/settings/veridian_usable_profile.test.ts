import { describe, it, expect } from 'vitest'
import { hasUsableMarketingProfile } from './veridian_email_profiles'
import defaultTemplate from '../templates/email-template.json'

// C7 : la banniere « profil requis » des diffusions ne doit exister que sans profil utilisable.
describe('hasUsableMarketingProfile', () => {
  const smtp = { kind: 'smtp', senders: [{ id: 's1', email: 'a@x.fr', name: 'A', is_default: true }] }
  const ws = (settings: object, integrations: object[]) =>
    ({ settings, integrations }) as unknown as Parameters<typeof hasUsableMarketingProfile>[0]

  it('faux sans profil, ou avec un profil sans expediteur', () => {
    expect(hasUsableMarketingProfile(undefined)).toBe(false)
    expect(hasUsableMarketingProfile(ws({}, []))).toBe(false)
    expect(
      hasUsableMarketingProfile(
        ws({ marketing_email_provider_id: 'i1' }, [{ id: 'i1', email_provider: { kind: 'smtp', senders: [] } }])
      )
    ).toBe(false)
  })

  it('vrai des qu un profil valide est en rotation (liste Veridian OU fournisseur historique)', () => {
    const integrations = [{ id: 'i1', email_provider: smtp }]
    expect(hasUsableMarketingProfile(ws({ veridian_marketing_email_provider_ids: ['i1'] }, integrations))).toBe(true)
    expect(hasUsableMarketingProfile(ws({ marketing_email_provider_id: 'i1' }, integrations))).toBe(true)
  })

  it('faux si le profil declare n existe plus', () => {
    expect(hasUsableMarketingProfile(ws({ marketing_email_provider_id: 'ghost' }, []))).toBe(false)
  })
})

// C9 : le modele visuel par defaut ne porte la marque d'aucun tiers.
describe('modele visuel par defaut', () => {
  it('est neutre : aucune marque Notifuse, aucun lien externe, pas de faute', () => {
    const raw = JSON.stringify(defaultTemplate)
    expect(raw).not.toMatch(/notifuse/i)
    expect(raw).not.toMatch(/bellow/i)
    expect(raw).not.toMatch(/googleapis\.com/)
    expect(raw).not.toMatch(/https?:\/\/(?!placehold)/)
  })
})
