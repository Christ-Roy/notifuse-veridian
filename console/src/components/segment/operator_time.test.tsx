import { describe, it, expect } from 'vitest'
import { FieldTypeTime } from './type_time'
import { OperatorInTheLastDays, OperatorNotInTheLastDays } from './operator_time'

// Reprise amont 37.0 : « pas dans les X derniers jours » sur une date du contact.
describe('OperatorNotInTheLastDays', () => {
  it('porte son propre opérateur API et son libellé, distincts de « dans les X derniers jours »', () => {
    const negation = new OperatorNotInTheLastDays()
    const positive = new OperatorInTheLastDays()
    expect(negation.type).toBe('not_in_the_last_days')
    expect(negation.label).toBe('not in the last')
    expect(positive.type).toBe('in_the_last_days')
  })

  it('est proposé dans le sélecteur des champs de type date', () => {
    const operators = new FieldTypeTime().operators.map((op) => op.type)
    expect(operators).toContain('not_in_the_last_days')
    expect(operators).toContain('in_the_last_days')
  })
})
