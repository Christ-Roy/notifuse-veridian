import { createElement } from 'react'
import { InboxOutlined } from '@ant-design/icons'

import { VeridianInboxMenuLabel } from './veridian_mail_menu'

/** Entrée de menu prête à poser dans la sidebar (une seule ligne d'intégration dans WorkspaceLayout). */
export function veridianInboxMenuItem(workspaceId: string) {
  return { key: 'inbox', icon: createElement(InboxOutlined), label: createElement(VeridianInboxMenuLabel, { workspaceId }) }
}
