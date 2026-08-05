import { api } from './client'
export interface EmailProfileUsage {
  integration_id: string
  // `used` is quota capacity consumed. It may be greater than accepted_used
  // when SMTP returned an ambiguous result after DATA.
  used: number
  accepted_used: number
  cap: number
  remaining: number
  // Keep unknown future backend classes visible instead of silently folding
  // them into a misleading canonical bucket.
  accepted_by_provider_class: Record<string, number>
}

export interface EmailProfilesUsageResponse {
  date: string
  total_used: number
  total_accepted: number
  profiles: EmailProfileUsage[]
}

export const emailProfilesUsageService = {
  get: (workspaceId: string) => {
    const searchParams = new URLSearchParams({ workspace_id: workspaceId })
    return api.get<EmailProfilesUsageResponse>(
      `/api/veridian/emailProfiles.usage?${searchParams.toString()}`
    )
  }
}
