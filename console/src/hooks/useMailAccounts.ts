/**
 * Veridian — hooks useMailAccountsList + useSetDefaultMailAccount.
 *
 * Wrappers React Query autour de `veridianMailAccountsApi`. Pattern
 * identique a `useMailProviderChoice` :
 *   - staleTime 30s pour eviter le re-fetch a chaque mount
 *   - refetchOnWindowFocus pour synchro si l'utilisateur revient apres
 *     avoir reconnecte Gmail cote Hub
 *   - retry: false (les erreurs sont deja avalees par le service en mode
 *     optimiste, inutile de re-essayer)
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  veridianMailAccountsApi,
  type MailAccountsListResponse,
  type MailAccountSetDefaultResponse
} from '../services/api/veridian_mail_accounts'

const listQueryKey = () => ['veridian', 'mail-accounts', 'list'] as const

export function useMailAccountsList() {
  return useQuery<MailAccountsListResponse>({
    queryKey: listQueryKey(),
    queryFn: () => veridianMailAccountsApi.list(),
    staleTime: 30 * 1000,
    refetchOnWindowFocus: true,
    retry: false
  })
}

export function useSetDefaultMailAccount() {
  const queryClient = useQueryClient()
  return useMutation<MailAccountSetDefaultResponse, Error, string>({
    mutationFn: (accountId: string) => veridianMailAccountsApi.setDefault(accountId),
    onSuccess: (data) => {
      if (!data.hub_available || !data.is_default || !data.account_id) {
        return
      }
      // Optimistic patch du cache list : flip is_default sur le bon compte
      // pour eviter un re-fetch reseau immediat.
      queryClient.setQueryData<MailAccountsListResponse | undefined>(
        listQueryKey(),
        (prev) => {
          if (!prev) return prev
          return {
            ...prev,
            accounts: prev.accounts.map((acc) => ({
              ...acc,
              is_default: acc.id === data.account_id
            }))
          }
        }
      )
    }
  })
}
