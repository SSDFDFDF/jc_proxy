import assert from 'node:assert/strict'
import test from 'node:test'
import { formatSampleSeconds, keyErrorDetails, recentTimingPair } from './keyStats.js'

test('timings use seconds and distinguish no sample from zero', () => {
  assert.equal(recentTimingPair({ avg_header_ms: 1000, header_samples: 5, avg_response_ms: 30000, response_samples: 5 }), '1s/30s')
  assert.equal(formatSampleSeconds(1250, 2), '1.25s')
  assert.equal(formatSampleSeconds(120, 5), '0.12s')
  assert.equal(formatSampleSeconds(0.5, 1), '<0.001s')
  assert.equal(formatSampleSeconds(0, 1), '0s')
  assert.equal(formatSampleSeconds(0, 0), '—')
  assert.equal(formatSampleSeconds(undefined, 1), '—')
  assert.equal(formatSampleSeconds(NaN, 1), '—')
  assert.equal(recentTimingPair({ avg_header_ms: 1000, header_samples: 1 }), '1s/—')
})

test('show last error and disable reason, without duplicates or stale fallback', () => {
  assert.equal(keyErrorDetails({}, { status: 'active', last_error: 'upstream timeout' }), 'upstream timeout')
  assert.equal(keyErrorDetails({}, { status: 'disabled_auto', last_error: 'quota exhausted', disable_reason: 'auto disabled' }), 'quota exhausted\nauto disabled')
  assert.equal(keyErrorDetails({}, { status: 'disabled_auto', last_error: 'bad key', disable_reason: 'bad key' }), 'bad key')
  assert.equal(keyErrorDetails({ status: 'disabled_auto', disable_reason: 'old', last_error: 'old' }, { status: 'active', disable_reason: '', last_error: '' }), '')
  assert.equal(keyErrorDetails({ status: 'disabled_manual', disable_reason: 'maintenance' }), 'maintenance')
})
