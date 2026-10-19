// === Veridian — accessibilite transverse (audit UI 19/10/2026, F4 et F5) ===
//
// F5 : un titre d'onglet par page (« Contacts | Veridian »), mis a jour a chaque navigation.
// F4 : filet de securite pour les controles sans nom accessible (boutons-icones, champs de
//   recherche des Select, interrupteurs). Un MutationObserver pose un aria-label deduit
//   (title, placeholder, icone) sur tout controle qui n'en a pas ; un aria-label ecrit a la
//   main n'est jamais ecrase. Classe de defaut : plus besoin de penser a chaque bouton-icone.
import { useEffect } from 'react'
import { useLingui } from '@lingui/react/macro'

export function sectionOfPath(pathname: string): string {
  const m = pathname.match(/\/workspace\/[^/]+\/?([^/]*)/)
  return m ? m[1] : ''
}

export function useVeridianDocumentTitle(pathname: string): void {
  const { t } = useLingui()
  useEffect(() => {
    const titles: Record<string, string> = {
  '': t`Dashboard`,
  contacts: t`Contacts`,
  lists: t`Lists`,
  templates: t`Templates`,
  broadcasts: t`Broadcasts`,
  automations: t`Automations`,
  analytics: t`Analytics`,
  'file-manager': t`File manager`,
  'transactional-notifications': t`Transactional templates`,
  'sending-profiles': t`Sending profiles`,
  'send-queue': t`Send queue`,
  logs: t`Sending log`,
  settings: t`Settings`
}
    const section = sectionOfPath(pathname)
    const label = titles[section]
    document.title = label ? `${label} | Veridian` : 'Console | Veridian'
  }, [pathname, t])
}

/** Nom deduit d'un controle sans nom, ou null. Exporte pour les tests. */
export function deriveAccessibleName(
  el: Element,
  iconLabels: Record<string, string>,
  searchLabel: string
): string | null {
  if (el.getAttribute('aria-label') || el.getAttribute('aria-labelledby')) return null
  const tag = el.tagName
  if (tag === 'BUTTON' || el.getAttribute('role') === 'switch') {
    if ((el.textContent || '').trim()) return null
    const title = el.getAttribute('title')
    if (title) return title
    const icon = el.querySelector('[data-icon], svg[class*="lucide-"], .anticon[aria-label]')
    if (icon) {
      const di = icon.getAttribute('data-icon')
      if (di && iconLabels[di]) return iconLabels[di]
      const cls = icon.getAttribute('class') || ''
      const lm = cls.match(/lucide-([a-z0-9-]+)/)
      if (lm && iconLabels[lm[1]]) return iconLabels[lm[1]]
      const ant = icon.getAttribute('aria-label')
      if (ant && iconLabels[ant]) return iconLabels[ant]
    }
    return iconLabels.__fallback || null
  }
  if (tag === 'INPUT' || tag === 'TEXTAREA') {
    const input = el as HTMLInputElement
    if (input.id && document.querySelector(`label[for="${CSS.escape(input.id)}"]`)) return null
    if (input.closest('label')) return null
    if (input.closest('.ant-select')) return input.getAttribute('placeholder') || searchLabel
    return input.getAttribute('placeholder') || input.getAttribute('name') || null
  }
  return null
}

export function useVeridianAccessibleNames(): void {
  const { t } = useLingui()
  useEffect(() => {
    const iconLabels: Record<string, string> = {
      trash: t`Delete`,
      'trash-can': t`Delete`,
      trash2: t`Delete`,
      'trash-alt': t`Delete`,
      pen: t`Edit`,
      'pen-to-square': t`Edit`,
      edit: t`Edit`,
      pencil: t`Edit`,
      eye: t`View`,
      'eye-slash': t`Hide`,
      copy: t`Copy`,
      plus: t`Add`,
      xmark: t`Close`,
      close: t`Close`,
      x: t`Close`,
      refresh: t`Refresh`,
      'arrows-rotate': t`Refresh`,
      rotate: t`Refresh`,
      'rotate-right': t`Refresh`,
      'paper-plane': t`Send a test`,
      send: t`Send a test`,
      ellipsis: t`More actions`,
      EllipsisOutlined: t`More actions`,
      'ellipsis-vertical': t`More actions`,
      gear: t`Settings`,
      table: t`Columns`,
      columns: t`Columns`,
      undo2: t`Undo`,
      redo2: t`Redo`,
      __fallback: t`Action`
    }
    const searchLabel = t`Search`
    const apply = (root: ParentNode) => {
      root
        .querySelectorAll('button:not([aria-label]), [role="switch"]:not([aria-label]), input:not([aria-label]), textarea:not([aria-label])')
        .forEach((el) => {
          const name = deriveAccessibleName(el, iconLabels, searchLabel)
          if (name) el.setAttribute('aria-label', name)
        })
    }
    apply(document)
    let scheduled = false
    const obs = new MutationObserver(() => {
      if (scheduled) return
      scheduled = true
      setTimeout(() => {
        scheduled = false
        apply(document)
      }, 150)
    })
    obs.observe(document.body, { childList: true, subtree: true })
    return () => obs.disconnect()
  }, [t])
}
