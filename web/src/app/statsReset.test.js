import assert from 'node:assert/strict'
import test from 'node:test'

import { buildStatsResetPayload } from './statsReset.js'

test('vendor reset requires an explicit scope, id and confirmation', () => {
  assert.deepEqual(buildStatsResetPayload('vendor', ' vid_a ', 'RESET'), {
    scope: 'vendor', vendor_id: 'vid_a', confirmation: 'RESET'
  })
  for (const scope of ['', 'unknown', undefined]) {
    assert.throws(() => buildStatsResetPayload(scope, 'vid_a', 'RESET'))
  }
  for (const id of ['', ' ', undefined]) {
    assert.throws(() => buildStatsResetPayload('vendor', id, 'RESET'))
  }
  for (const confirmation of ['', 'reset', 'RESET ', undefined]) {
    assert.throws(() => buildStatsResetPayload('all', '', confirmation))
  }
})

test('global reset is explicit and does not carry the selected vendor id', () => {
  assert.deepEqual(buildStatsResetPayload('all', 'vid_a', 'RESET'), {
    scope: 'all', confirmation: 'RESET'
  })
})
