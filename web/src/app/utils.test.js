import test from 'node:test'
import assert from 'node:assert/strict'
import { buildNewVendorConfig, emptyVendorConfig, withVendorDefaults } from './utils.js'

test('vendor edits preserve plugin order, scripts and options without aliasing saved config', () => {
  const plugins = [
    { name: 'native', builtin: 'codex-history-filter', methods: ['POST'], paths: ['/v1/responses'] },
    { name: 'custom', source: 'function onRequest(r) { return r.body; }', enabled: false, timeout: 20_000_000, max_body_bytes: 1024 }
  ]
  const draft = withVendorDefaults({ plugins })
  assert.deepEqual(draft.plugins, plugins)
  draft.plugins[0].methods.push('GET')
  assert.deepEqual(plugins[0].methods, ['POST'])
  assert.deepEqual(withVendorDefaults({ plugins: [] }).plugins, [])
})

test('new vendors use bounded uploads and empty error policies', () => {
  const config = emptyVendorConfig()
  assert.equal(config.upstream.upload_timeout, 300_000_000_000)
  assert.deepEqual(config.client_auth, { enabled: true, keys: [] })
  assert.deepEqual(config.error_policy.auto_disable.keywords, [])
  assert.deepEqual(config.error_policy.auto_disable.status_codes, [])
  assert.equal(config.error_policy.failover.request_error, false)
  assert.deepEqual(config.aggregate.retry.status_codes, [])
  assert.equal(config.aggregate.retry.network_error, false)
})

test('client auth defaults closed and only preserves an explicit opt-out with keys', () => {
  for (const provider of ['generic', 'openai', 'anthropic', 'gemini', 'deepseek', 'azure_openai', 'aggregate']) {
    for (const auth of [undefined, null, {}, { enabled: false }, { enabled: false, keys: [] }, { enabled: false, keys: [' ', '\t'] }]) {
      assert.deepEqual(withVendorDefaults({ provider, client_auth: auth }).client_auth, { enabled: true, keys: [] })
    }
    assert.deepEqual(withVendorDefaults({ provider, client_auth: { keys: [' key ', 'key'] } }).client_auth, { enabled: true, keys: ['key'] })
    assert.deepEqual(withVendorDefaults({ provider, client_auth: { enabled: null, keys: ['key'] } }).client_auth, { enabled: true, keys: ['key'] })
    assert.deepEqual(withVendorDefaults({ provider, client_auth: { enabled: false, keys: ['key'] } }).client_auth, { enabled: false, keys: ['key'] })
  }
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


test('editing omitted or cleared rule lists never adds status codes or keywords', () => {
  for (const input of [{}, { keywords: ['custom text'] }, { status_codes: [498] }, { status_codes: [], keywords: [] }]) {
    const draft = withVendorDefaults({ error_policy: { auto_disable: input } })
    assert.deepEqual(draft.error_policy.auto_disable.status_codes, input.status_codes || [])
    assert.deepEqual(draft.error_policy.auto_disable.keywords, input.keywords || [])
    assert.deepEqual(withVendorDefaults(draft).error_policy.auto_disable, draft.error_policy.auto_disable)
  }
})


test('new aggregate vendors only configure entry response masking', () => {
  assert.deepEqual(buildNewVendorConfig('', 'aggregate').error_policy, { masking: { enabled: true, rules: [] } })
})
