import test from 'node:test'
import assert from 'node:assert/strict'
import { emptyVendorConfig, withVendorDefaults } from './utils.js'

test('new vendors use bounded uploads and backend-aligned credential defaults', () => {
  const config = emptyVendorConfig()
  assert.equal(config.upstream.upload_timeout, 300_000_000_000)
  assert.deepEqual(config.error_policy.auto_disable.keywords, ['incorrect_api_key', 'invalid_api_key'])
})

test('vendor editing preserves disabled/custom upload limits and explicit policies', () => {
  for (const timeout of [0, 9_000_000_000]) {
    const config = withVendorDefaults({
      upstream: { upload_timeout: timeout },
      error_policy: { auto_disable: { keywords: ['custom text'] } }
    })
    assert.equal(config.upstream.upload_timeout, timeout)
    assert.deepEqual(config.error_policy.auto_disable.keywords, ['custom text'])
  }
})
