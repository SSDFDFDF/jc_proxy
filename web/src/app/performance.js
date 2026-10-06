const SECOND = 1_000_000_000
const HOUR = 3600 * SECOND

export function isPerformanceStrategy(strategy, provider) {
  return provider !== 'aggregate' && ['lowest_latency', 'highest_success', 'adaptive'].includes(strategy)
}

export function performanceDefaults(strategy) {
  return {
    sample_ttl: 300 * SECOND,
    decay_duration: 300 * SECOND,
    exploration_interval: 10,
    exploration_backoff_base: 60 * SECOND,
    exploration_backoff_max: 300 * SECOND,
    success_power: strategy === 'lowest_latency' ? 1 : 2,
    inflight_weight: 1,
    latency_prior: SECOND
  }
}

export const PERFORMANCE_FIELDS = [
  { key: 'sample_ttl', label: '评分保鲜期 / TTL（秒）', unit: SECOND, min: 1, max: 86400, step: 1, help: '距最后有效样本超过此时间后开始衰减；新样本重建近期窗口。' },
  { key: 'decay_duration', label: '衰减时长（秒）', unit: SECOND, min: 0, max: 86400, step: 1, help: 'TTL 后线性回归中性评分；0 表示到期立即回归，不是永不过期。' },
  { key: 'exploration_interval', label: '探索间隔（选 Key 次数）', min: 0, max: 10000, step: 1, help: '每 N 次选择可探索一次；10 约为 10%，0 关闭探索，低流量不会后台探测。' },
  { key: 'exploration_backoff_base', label: '探索失败初始退避（秒）', unit: SECOND, min: 1, max: 86400, step: 1, help: '连续失败按 1、2、4…倍退避，仅限制探索，不替代错误冷却。' },
  { key: 'exploration_backoff_max', label: '探索失败最大退避（秒）', unit: SECOND, min: 1, max: 86400, step: 1, help: '不得小于初始退避；成功后清除探索退避。' },
  { key: 'success_power', label: '成功率惩罚指数', min: 0, max: 4, step: 0.1, help: '越大越重视成功率；0 忽略混合窗口成功率，但全失败仍强降权。' },
  { key: 'inflight_weight', label: '当前并发权重', min: 0, max: 10, step: 0.1, help: '成本乘以 (1 + 权重 × 在途数)；0 忽略并发，1 为默认。' },
  { key: 'latency_prior', label: '无样本初始响应耗时（秒）', unit: SECOND, min: 0.001, max: HOUR / SECOND, step: 0.001, help: '没有成功头耗时时的参考值，也用于过期后的中性评分。', latencyOnly: true }
]

// Keep null/omitted fields unresolved. Only explicitly edited settings travel
// back to the API, so default success_power follows later strategy changes.
export function validatePerformance(performance, strategy) {
  const defaults = performanceDefaults(strategy)
  const resolved = Object.fromEntries(Object.entries(defaults).map(([key, fallback]) => [key, performance?.[key] ?? fallback]))
  for (const field of PERFORMANCE_FIELDS) {
    const raw = resolved[field.key]
    const value = raw / (field.unit || 1)
    if (typeof raw !== 'number' || !Number.isFinite(raw) || value < field.min || value > field.max || (field.key === 'exploration_interval' && !Number.isInteger(raw))) {
      throw new Error(`${field.label} 必须在 ${field.min}–${field.max} 范围内${field.key === 'exploration_interval' ? '且为整数' : ''}`)
    }
  }
  if (resolved.exploration_backoff_max < resolved.exploration_backoff_base) {
    throw new Error('探索失败最大退避不得小于初始退避')
  }
  return resolved
}
