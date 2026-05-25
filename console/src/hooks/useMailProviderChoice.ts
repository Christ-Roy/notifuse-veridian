/**
 * Veridian — hook useMailProviderChoice.
 *
 * Wrappe `veridianMailProviderApi.getChoice` + `.setChoice` dans TanStack
 * Query (read + mutation). Pattern identique à `useVeridianPlan` : staleTime
 * 30s + refetchOnWindowFocus pour que l'UI reste synchro si l'utilisateur
 * change la prefs depuis un autre onglet.
 *
 * Le mutate invalide le cache pour que `getChoice` re-fetch et que le radio
 * se mette à jour visuellement.
 */

import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  veridianMailProviderApi,
  type MailProviderChoice,
  type MailProviderChoiceResponse
} from '../services/api/veridian_mail_provider'

const queryKey = (workspaceId: string) => ['veridian', 'mail-provider-choice', workspaceId]

export function useMailProviderChoice(workspaceId: string | undefined) {
  return useQuery<MailProviderChoiceResponse | null>({
    queryKey: queryKey(workspaceId ?? ''),
    queryFn: () =>
      workspaceId ? veridianMailProviderApi.getChoice(workspaceId) : Promise.resolve(null),
    enabled: Boolean(workspaceId),
    staleTime: 30 * 1000,
    refetchOnWindowFocus: true,
    retry: false
  })
}

export function useSetMailProviderChoice(workspaceId: string | undefined) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (choice: MailProviderChoice) => {
      if (!workspaceId) {
        return Promise.reject(new Error('workspaceId required'))
      }
      return veridianMailProviderApi.setChoice(workspaceId, choice)
    },
    onSuccess: (data) => {
      if (workspaceId) {
        queryClient.setQueryData(queryKey(workspaceId), data)
      }
    }
  })
}
