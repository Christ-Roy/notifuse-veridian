import { useEffect, useState } from 'react'

import {
  emailProfilesUsageService,
  type EmailProfilesUsageResponse
} from '../../services/api/veridian_email_profiles'

interface UsageState {
  data: EmailProfilesUsageResponse | null
  loading: boolean
  error: boolean
}

const EMPTY_USAGE: UsageState = { data: null, loading: false, error: false }

export function useEmailProfilesUsage(workspaceId?: string): UsageState {
  const [state, setState] = useState<UsageState>(() => ({
    data: null,
    loading: !!workspaceId,
    error: false
  }))

  useEffect(() => {
    if (!workspaceId) return
    let active = true
    emailProfilesUsageService
      .get(workspaceId)
      .then((data) => {
        if (active) setState({ data, loading: false, error: false })
      })
      .catch(() => {
        if (active) setState({ data: null, loading: false, error: true })
      })
    return () => {
      active = false
    }
  }, [workspaceId])

  return workspaceId ? state : EMPTY_USAGE
}
