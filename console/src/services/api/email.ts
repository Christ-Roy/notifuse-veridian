import { api } from './client'
import type { TestEmailProviderResponse } from './template'

export const emailService = {
  /**
   * Test an email provider configuration by sending a test email
   * @param workspaceId The ID of the workspace
   * @param integrationId The persisted sending profile to test
   * @param to The recipient email address for the test
   * @returns A response indicating success or failure
   */
  testProvider: (
    workspaceId: string,
    integrationId: string,
    to: string
  ): Promise<TestEmailProviderResponse> => {
    return api.post<TestEmailProviderResponse>('/api/email.testProvider', {
      integration_id: integrationId,
      to,
      workspace_id: workspaceId
    })
  }
}
