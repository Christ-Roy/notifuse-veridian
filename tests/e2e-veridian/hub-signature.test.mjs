// Test sans dépendance : node --experimental-strip-types --test hub-signature.test.mjs
// Les vecteurs sont produits par le Go (internal/http/middleware/veridian_hmac_vectors_test.go), jamais recalculés ici.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { signHubV2, signHubV1, splitHubUrl } from './hub-signature.ts'

const vectors = JSON.parse(readFileSync(new URL('./fixtures/hub-hmac-v2-vectors.json', import.meta.url), 'utf8'))

test('les vecteurs Go sont présents', () => assert.ok(vectors.length >= 4))

for (const v of vectors) {
  test(`v2 identique au Go : ${v.name}`, () => {
    const h = signHubV2(v.secret, v.method, v.url, v.body, { timestamp: v.timestamp })
    assert.equal(h['X-Veridian-Hub-Signature'], v.signature)
    assert.equal(h['X-Veridian-Sig-Version'], '2')
    assert.equal(h['X-Veridian-Timestamp'], v.timestamp)
  })
}

test('chemin encodé et query brute comme Go', () => {
  assert.deepEqual(splitHubUrl('https://h/api/a%20b?x=1&y=%2B'), { path: '/api/a%20b', rawQuery: 'x=1&y=%2B' })
  assert.deepEqual(splitHubUrl('https://h/api/a'), { path: '/api/a', rawQuery: '' })
})

test('deux signatures successives ne réutilisent rien (horodatage à neuf)', async () => {
  const a = signHubV2('s', 'GET', 'https://h/x')
  await new Promise((r) => setTimeout(r, 3))
  const b = signHubV2('s', 'GET', 'https://h/x')
  assert.notEqual(a['X-Veridian-Hub-Signature'], b['X-Veridian-Hub-Signature'])
})

test('v1 : ts.corps, sans en-tête de version', () => {
  const h = signHubV1('s', 'x', { timestamp: '1' })
  assert.equal(h['X-Veridian-Sig-Version'], undefined)
  assert.equal(h['X-Veridian-Hub-Signature'].length, 64)
})

test('même requête signée 500 fois dans la même milliseconde : toutes les signatures sont distinctes (usage unique)', () => {
  const sigs = new Set()
  for (let i = 0; i < 500; i++) sigs.add(signHubV2('s', 'POST', 'https://h/api/tenants/provision', '{"a":1}')['X-Veridian-Hub-Signature'])
  assert.equal(sigs.size, 500)
})
