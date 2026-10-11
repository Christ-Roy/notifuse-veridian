// Test sans dépendance : node --experimental-strip-types --test prod-residue.test.mjs
import test from 'node:test'
import assert from 'node:assert/strict'
import { diffNewTenants, listManagedTenants } from './prod-residue.ts'

test('un nouveau tenant e2e est à wiper, un inconnu seulement signalé', () => {
  const d = diffNewTenants(['robertbrunon', 'animalsd'], ['robertbrunon', 'animalsd', 'e2e1791709367', 'nouveauclient'])
  assert.deepEqual(d.wipe, ['e2e1791709367'])
  assert.deepEqual(d.reportOnly, ['nouveauclient'])
})
test('rien de nouveau = rien à faire', () => {
  assert.deepEqual(diffNewTenants(['a'], ['a']), { wipe: [], reportOnly: [] })
})
test('liste illisible ou vide = null (non abouti), jamais une liste vide', async () => {
  assert.equal(await listManagedTenants('http://127.0.0.1:1', 's'), null)
})
