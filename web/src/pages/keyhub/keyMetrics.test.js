import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildKeyMetrics,
  buildVendorInitials,
  compareItems,
  countByFilter,
  matchesFilter,
  nextSortState,
  normalizeStatus,
  resolveDisplayState,
  vendorHealth,
  vendorInitial
} from './keyMetrics.js'

const runtime = (overrides = {}) => ({
  status: 'active',
  total_requests: 0,
  success_count: 0,
  ...overrides
})

const row = (overrides = {}) => {
  const merged = {
    key: 'sk-1',
    masked: 'sk-1...1',
    displayStatus: 'active',
    displayDisableReason: '',
    displayDisabledBy: '',
    originalIndex: 0,
    rt: runtime(),
    ...overrides
  }
  return { ...merged, metrics: buildKeyMetrics(merged) }
}

test('normalizeStatus falls back to active for unknown values', () => {
  assert.equal(normalizeStatus('disabled_manual'), 'disabled_manual')
  assert.equal(normalizeStatus('disabled_auto'), 'disabled_auto')
  assert.equal(normalizeStatus('nonsense'), 'active')
  assert.equal(normalizeStatus(undefined), 'active')
})

test('runtime status wins over stored status; stale stored reason stays hidden while active', () => {
  const disabled = resolveDisplayState({ status: 'disabled_manual', disable_reason: '余额不足' }, { status: 'active', last_error: '' })
  assert.equal(disabled.displayStatus, 'active')
  assert.equal(disabled.displayDisableReason, '')

  const auto = resolveDisplayState({ status: 'active' }, { status: 'disabled_auto', disable_reason: '401 invalid_api_key' })
  assert.equal(auto.displayStatus, 'disabled_auto')
  assert.equal(auto.displayDisableReason, '401 invalid_api_key')
})

test('metrics derive counts, rates and the cooldown multiplier', () => {
  const metrics = buildKeyMetrics({
    displayDisableReason: 'boom',
    displayDisabledBy: 'system',
    rt: runtime({
      total_requests: 10,
      success_count: 9,
      inflight: 2,
      backoff_remaining_seconds: 30,
      cooldown_level: 3,
      failures: 4,
      last_status: 429,
      unauthorized_count: 1
    })
  })
  assert.equal(metrics.successRate, 90)
  assert.equal(metrics.errRate, 10)
  assert.equal(metrics.failedCount, 1)
  assert.equal(metrics.errorCount, 1)
  assert.equal(metrics.cooldownMultiplier, 4)
  assert.deepEqual(metrics.secondaryText.split(' · '), ['by system', 'HTTP 429', '连败 4', '退避 x4'])
})

test('metrics keep zero-request keys at an unknown rate', () => {
  const metrics = buildKeyMetrics({ rt: runtime() })
  assert.equal(metrics.totalRequests, 0)
  assert.equal(metrics.successRate, 0)
  assert.equal(metrics.hasErrors, false)
})

test('filters match the count chip semantics', () => {
  const active = row()
  const disabled = row({ key: 'sk-2', displayStatus: 'disabled_manual' })
  const backoff = row({ key: 'sk-3', rt: runtime({ total_requests: 1, success_count: 1, backoff_remaining_seconds: 5 }) })
  const errs = row({ key: 'sk-4', rt: runtime({ total_requests: 1, unauthorized_count: 1 }) })
  const items = [active, disabled, backoff, errs]

  assert.equal(items.filter((item) => matchesFilter(item, 'all')).length, 4)
  assert.equal(items.filter((item) => matchesFilter(item, 'active')).length, 3)
  assert.equal(items.filter((item) => matchesFilter(item, 'disabled')).length, 1)
  assert.equal(items.filter((item) => matchesFilter(item, 'backoff')).length, 1)
  assert.equal(items.filter((item) => matchesFilter(item, 'issues')).length, 3)

  assert.deepEqual(countByFilter(items), { all: 4, active: 3, disabled: 1, backoff: 1, issues: 3, inflight: 0 })
})

test('sorting is stable and respects per-column defaults', () => {
  const low = row({ key: 'sk-a', rt: runtime({ total_requests: 1, success_count: 1 }) })
  const high = row({ key: 'sk-b', originalIndex: 1, rt: runtime({ total_requests: 9, success_count: 9 }) })

  assert.deepEqual(nextSortState({ key: 'index', direction: 'asc' }, 'requests'), { key: 'requests', direction: 'desc' })
  assert.deepEqual(nextSortState({ key: 'requests', direction: 'desc' }, 'requests'), { key: 'requests', direction: 'asc' })
  assert.deepEqual(nextSortState({ key: 'requests', direction: 'asc' }, 'key'), { key: 'key', direction: 'asc' })

  const sorted = [low, high].sort((left, right) => compareItems(left, right, 'requests'))
  assert.deepEqual(sorted.map((item) => item.key), ['sk-a', 'sk-b'])
  assert.equal(compareItems(low, low, 'requests'), 0)
})

test('vendor health prefers disabled over transient backoff', () => {
  assert.equal(vendorHealth({ disabledCount: 0, backoff: 0 }), 'success')
  assert.equal(vendorHealth({ disabledCount: 0, backoff: 3 }), 'warning')
  assert.equal(vendorHealth({ disabledCount: 1, backoff: 3 }), 'danger')
})

test('vendor initials keep names identifiable on the mobile grid', () => {
  assert.equal(vendorInitial('OpenAI'), 'Op')
  assert.equal(vendorInitial('openai'), 'Op')
  assert.equal(vendorInitial('智谱 AI'), '智谱')
  assert.equal(vendorInitial(''), '?')
  assert.equal(vendorInitial('   '), '?')

  const initials = buildVendorInitials([
    { vendorID: 'a', vendorName: 'OpenAI' },
    { vendorID: 'b', vendorName: 'OpenRouter' },
    { vendorID: 'c', vendorName: '智谱' }
  ])
  assert.deepEqual(initials, { a: 'OpenAI', b: 'OpenRo', c: '智谱' })
})
