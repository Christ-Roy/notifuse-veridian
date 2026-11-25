import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import { useUnhandledReplies } from '../mail/veridian_mail_queries'

// Relecture du tableau de bord (25/11/2026 : 13 appels par minute, page ouverte et rien qui change). Deux lectures battent la minute, les profils
// (envoyés du jour) et les boîtes (réponses à traiter). Les totaux de la période ne sont relus que si l'une des deux a bougé, sinon toutes les dix
// minutes : un rejet ou une plainte arrive sans envoi. Onglet caché : React Query suspend tout, rien ne part.
export const PROSPECTION_SLOW_MS = 10 * 60_000

export function useProspectionPulse(workspaceId: string, sentToday: number | undefined) {
  const client = useQueryClient()
  const { count, loaded } = useUnhandledReplies(workspaceId)
  const pulse = sentToday === undefined || !loaded ? undefined : `${workspaceId} ${sentToday} ${count}`
  const seen = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (pulse === undefined) return
    const before = seen.current
    seen.current = pulse
    if (before !== undefined && before.split(' ')[0] === workspaceId) void client.invalidateQueries({ queryKey: ['prospection'] })
  }, [pulse, workspaceId, client])
}
