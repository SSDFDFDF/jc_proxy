import test from 'node:test'
import assert from 'node:assert/strict'
import { isPerformanceStrategy, performanceDefaults, validatePerformance } from './performance.js'
import { withVendorDefaults } from './utils.js'

const SECOND = 1_000_000_000

test('performance controls only apply to managed-key performance strategies', () => {
  for (const strategy of ['adaptive', 'lowest_latency', 'highest_success']) {
    assert.equal(isPerformanceStrategy(strategy, 'openai'), true)
    assert.equal(isPerformanceStrategy(strategy, 'aggregate'), false)
  }
  for (const strategy of ['round_robin', 'random', 'least_used', 'least_requests', undefined]) {
    assert.equal(isPerformanceStrategy(strategy, 'generic'), false)
  }
})

test('defaults align with backend and follow strategy without freezing fields', () => {
  for (const strategy of ['adaptive', 'lowest_latency', 'highest_success']) {
    const config = withVendorDefaults({ load_balance: strategy })
    assert.deepEqual(config.performance, {})
    const defaults = performanceDefaults(strategy)
    assert.deepEqual(validatePerformance(config.performance, strategy), defaults)
    assert.equal(defaults.success_power, strategy === 'lowest_latency' ? 1 : 2)
    assert.equal(defaults.sample_ttl, 300 * SECOND)
    assert.equal(defaults.decay_duration, 300 * SECOND)
    assert.equal(defaults.exploration_interval, 10)
    assert.equal(defaults.exploration_backoff_base, 60 * SECOND)
    assert.equal(defaults.exploration_backoff_max, 300 * SECOND)
    assert.equal(defaults.inflight_weight, 1)
    assert.equal(defaults.latency_prior, SECOND)
  }
})

test('edited settings survive draft mutations and strategy changes including explicit zeros', () => {
  const performance = { sample_ttl: 90 * SECOND, decay_duration: 0, exploration_interval: 0, success_power: 0, inflight_weight: 0, latency_prior: 250_000_000 }
  for (const strategy of ['adaptive', 'round_robin', 'highest_success', 'lowest_latency']) {
    const config = withVendorDefaults({ load_balance: strategy, performance })
    assert.deepEqual(config.performance, performance)
    validatePerformance(config.performance, strategy)
    assert.notEqual(config.performance, performance)
  }
  assert.equal(validatePerformance({ success_power: null }, 'lowest_latency').success_power, 1)
  assert.equal(validatePerformance({ success_power: null }, 'adaptive').success_power, 2)
})

test('invalid settings fail validation instead of silently falling back', () => {
  for (const settings of [
    { sample_ttl: 0 }, { sample_ttl: 86401 * SECOND }, { decay_duration: -1 },
    { exploration_interval: 1.5 }, { exploration_interval: -1 }, { exploration_interval: 10001 },
    { exploration_backoff_base: 0 }, { exploration_backoff_max: SECOND },
    { success_power: NaN }, { success_power: 5 }, { inflight_weight: Infinity }, { inflight_weight: -1 },
    { latency_prior: 0 }, { latency_prior: 'bad' }
  ]) assert.throws(() => validatePerformance(settings, 'adaptive'))
})
