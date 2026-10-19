import { useLingui } from '@lingui/react/macro'

// Erreurs serveur brutes (SMTP, import de contacts) -> phrase claire pour l'utilisateur,
// le detail technique restant disponible a part (audit UI 19/10/2026, C6 et D4).
// Hook (et non fonction recevant `t`) : la macro Lingui ne transforme t`...` que dans la
// portee du useLingui() qui le declare.

export interface FriendlyError {
  /** Phrase pour l'utilisateur, traduite. */
  text: string
  /** Message technique d'origine (affiche replie), absent si identique au texte. */
  detail?: string
}

/** Extrait un code reponse SMTP (535, 550...) d'un message du type "... code: 535". */
export function smtpCodeOf(raw: string): number | null {
  const m = /\bcode:?\s*(\d{3})\b/i.exec(raw) || /^\s*(?:\w[\w ]*:\s*)?(\d{3})[ -]/.exec(raw)
  return m ? Number(m[1]) : null
}

export function useFriendlyServerError(): (raw: string | null | undefined) => FriendlyError | null {
  const { t } = useLingui()
  return (raw) => {
    if (!raw) return null
    const code = smtpCodeOf(raw)
    const withDetail = (text: string): FriendlyError => ({ text, detail: raw })

    const contact = /invalid contact at index (\d+):\s*(.*)$/i.exec(raw)
    if (contact) {
      const n = Number(contact[1])
      if (/email is required/i.test(contact[2])) return withDetail(t`Contact ${n}: the email address is missing.`)
      if (/email/i.test(contact[2])) return withDetail(t`Contact ${n}: the email address is invalid.`)
      return withDetail(t`Contact ${n} is invalid.`)
    }
    if (code === 535 || code === 534 || code === 530 || /authentication (failed|unsuccessful|required)|username and password not accepted|invalid credentials/i.test(raw)) {
      return withDetail(
        t`The sending server refused the login or password. Check the username and the password (for Gmail, an app password, not your usual password).`
      )
    }
    if (code !== null && code >= 400 && code < 500) {
      return withDetail(t`The sending server is temporarily unavailable or limiting sends. Try again in a few minutes.`)
    }
    if (code !== null && code >= 500) {
      return withDetail(t`The sending server rejected the message. Check the sender address and the recipient address.`)
    }
    if (/dial tcp|connection refused|no such host|i\/o timeout|timed out|network is unreachable|connection reset|tls:|x509|handshake/i.test(raw)) {
      return withDetail(t`The sending server cannot be reached. Check the host, the port and the security mode.`)
    }
    return { text: raw }
  }
}
