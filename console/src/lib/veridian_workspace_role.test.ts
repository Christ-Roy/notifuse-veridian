import { describe, expect, it } from 'vitest'
import { isWorkspaceAdministrator, isWorkspaceOwner } from './veridian_workspace_role'

describe('veridian_workspace_role : point de decision unique', () => {
  it('administre = owner ou admin, jamais member ni vide', () => {
    expect(isWorkspaceAdministrator('owner')).toBe(true)
    expect(isWorkspaceAdministrator('admin')).toBe(true)
    expect(isWorkspaceAdministrator('member')).toBe(false)
    expect(isWorkspaceAdministrator(undefined)).toBe(false)
    expect(isWorkspaceAdministrator(null)).toBe(false)
    expect(isWorkspaceAdministrator('')).toBe(false)
  })

  it('proprietaire = owner seul (un admin ne l est jamais)', () => {
    expect(isWorkspaceOwner('owner')).toBe(true)
    expect(isWorkspaceOwner('admin')).toBe(false)
    expect(isWorkspaceOwner('member')).toBe(false)
    expect(isWorkspaceOwner(undefined)).toBe(false)
  })
})
