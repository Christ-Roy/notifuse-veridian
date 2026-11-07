import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'

i18n.loadAndActivate({ locale: 'en', messages: {} })

const navigate = vi.fn()
const signout = vi.fn().mockResolvedValue(undefined)
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => navigate }))
vi.mock('../contexts/AuthContext', () => ({ useAuth: () => ({ signout }) }))

import { LogoutPage } from './LogoutPage'
import { clearBounceMarks, hasCookie, LOGOUT_COOKIE } from '../lib/veridian_auto_bounce'

// CONTRAT-HUB 6bis.8.0 point 2 : un clic sur « Se déconnecter » pose le repère local, sans lui la
// console repartirait vers le Hub et reconnecterait l'utilisateur à son insu.
describe('LogoutPage — déconnexion explicite', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    clearBounceMarks()
  })

  it('coupe la session, pose le repère de déconnexion, puis affiche la connexion', async () => {
    render(
      <I18nProvider i18n={i18n}>
        <LogoutPage />
      </I18nProvider>
    )
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/console/signin' }))
    expect(signout).toHaveBeenCalledTimes(1)
    expect(hasCookie(LOGOUT_COOKIE, document.cookie)).toBe(true)
  })
})
