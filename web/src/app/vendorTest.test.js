import test from 'node:test'
import assert from 'node:assert/strict'
import {
  USER_AGENT_PRESETS,
  applyUserAgentToHeaders,
  matchUserAgentPreset,
  normalizeHeaderRows
} from './vendorTest.js'

test('USER_AGENT_PRESETS contains common coding client UAs including claudecode and codex', () => {
  const ids = USER_AGENT_PRESETS.map((p) => p.id)
  assert.ok(ids.includes('default'), 'should contain default')
  assert.ok(ids.includes('claudecode_cli'), 'should contain claudecode_cli')
  assert.ok(ids.includes('claudecode'), 'should contain claudecode')
  assert.ok(ids.includes('codex'), 'should contain codex')
  assert.ok(ids.includes('cursor'), 'should contain cursor')

  const claudeCli = USER_AGENT_PRESETS.find((p) => p.id === 'claudecode_cli')
  assert.ok(claudeCli.value.includes('claude-cli'), 'claude-cli should contain claude-cli')

  const codex = USER_AGENT_PRESETS.find((p) => p.id === 'codex')
  assert.ok(codex.value.includes('codex_cli_rs'), 'codex should contain codex_cli_rs')
})

test('normalizeHeaderRows trims keys and ignores empty rows', () => {
  const rows = [
    { key: ' Authorization ', value: 'Bearer abc' },
    { key: '', value: 'ignored' },
    { key: '   ', value: 'also ignored' },
    { key: 'X-Custom', value: '123' }
  ]
  const normalized = normalizeHeaderRows(rows)
  assert.deepEqual(normalized, [
    { key: 'Authorization', value: 'Bearer abc' },
    { key: 'X-Custom', value: '123' }
  ])
})

test('applyUserAgentToHeaders appends or updates User-Agent header', () => {
  // Case 1: Empty UA leaves original headers untouched
  const headers1 = [{ key: 'X-Custom', value: '1' }]
  assert.deepEqual(applyUserAgentToHeaders(headers1, ''), [{ key: 'X-Custom', value: '1' }])

  // Case 2: Injects User-Agent when not present
  const headers2 = [{ key: 'X-Custom', value: '1' }]
  const withUA = applyUserAgentToHeaders(headers2, 'claude-cli/0.2.29 (external, cli)')
  assert.deepEqual(withUA, [
    { key: 'X-Custom', value: '1' },
    { key: 'User-Agent', value: 'claude-cli/0.2.29 (external, cli)' }
  ])

  // Case 3: Overwrites existing User-Agent case-insensitively
  const headers3 = [
    { key: 'user-agent', value: 'old-agent' },
    { key: 'X-Other', value: 'val' }
  ]
  const updatedUA = applyUserAgentToHeaders(headers3, 'codex_cli_rs/0.125.0')
  assert.deepEqual(updatedUA, [
    { key: 'user-agent', value: 'codex_cli_rs/0.125.0' },
    { key: 'X-Other', value: 'val' }
  ])
})

test('matchUserAgentPreset matches preset ID or falls back to custom', () => {
  assert.equal(matchUserAgentPreset(''), 'default')
  assert.equal(matchUserAgentPreset('   '), 'default')
  assert.equal(matchUserAgentPreset('claude-cli/0.2.29 (external, cli)'), 'claudecode_cli')
  assert.equal(matchUserAgentPreset('codex_cli_rs/0.125.0 (x86_64; linux) xterm-256color'), 'codex')
  assert.equal(matchUserAgentPreset('MyCustomAgent/1.0'), '__custom__')
})
