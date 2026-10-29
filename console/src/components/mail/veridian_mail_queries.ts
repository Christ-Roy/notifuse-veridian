// Boîte unifiée : clés et requêtes React Query partagées (page, menu, tuile du tableau de bord, fiche contact).
// Une seule définition de chaque lecture : le badge du menu et la page lisent le même cache.

import { useQuery } from '@tanstack/react-query'

import { mailService, type MailSummary } from '../../services/api/veridian_mail'
import { unhandledTotal } from './veridian_mail_rules'

export const mailKeys = {
  all: (ws: string) => ['veridian-mail', ws] as const,
  mailboxes: (ws: string) => ['veridian-mail', ws, 'mailboxes'] as const,
  lists: (ws: string) => ['veridian-mail', ws, 'list'] as const,
  list: (ws: string, filters: unknown) => ['veridian-mail', ws, 'list', filters] as const,
  message: (ws: string, id: string) => ['veridian-mail', ws, 'message', id] as const,
  thread: (ws: string, threadId: string) => ['veridian-mail', ws, 'thread', threadId] as const,
  contactThreads: (ws: string, email: string) => ['veridian-mail', ws, 'contact-threads', email] as const,
  lastReply: (ws: string) => ['veridian-mail', ws, 'last-reply'] as const
}

/** Pas de rafraîchissement automatique tant que la lecture échoue (route absente, droit refusé). */
const everyMinute = (query: { state: { status: string } }): number | false => (query.state.status === 'error' ? false : 60_000)

export function useMailboxes(workspaceId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: mailKeys.mailboxes(workspaceId ?? ''),
    queryFn: () => mailService.mailboxes(workspaceId as string),
    enabled: !!workspaceId && enabled,
    staleTime: 30_000,
    retry: false,
    refetchInterval: everyMinute
  })
}

/** Nombre de réponses humaines à traiter (badge du menu). */
export function useUnhandledReplies(workspaceId: string | undefined, enabled = true) {
  const q = useMailboxes(workspaceId, enabled)
  return { count: unhandledTotal(q.data), loaded: q.isSuccess, error: q.isError }
}

/** Dernière réponse humaine non traitée (tuile du tableau de bord). */
export function useLastUnhandledReply(workspaceId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: mailKeys.lastReply(workspaceId ?? ''),
    queryFn: async (): Promise<MailSummary | null> => {
      const res = await mailService.list({ workspace_id: workspaceId as string, kind: ['reply_human'], handled: false, limit: 1 })
      return res.messages[0] ?? null
    },
    enabled: !!workspaceId && enabled,
    staleTime: 30_000,
    retry: false,
    refetchInterval: everyMinute
  })
}
