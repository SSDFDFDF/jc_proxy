// Runtime timing values are milliseconds. Always show seconds in the key table;
// a missing sample is unknown, not a zero-second response.
export function formatSampleSeconds(milliseconds, samples) {
  const value = Number(milliseconds)
  if (!(Number(samples) > 0) || milliseconds == null || !Number.isFinite(value) || value < 0) return '—'
  if (value > 0 && value < 1) return '<0.001s'
  return `${Number((value / 1000).toFixed(value < 1000 ? 3 : 2))}s`
}

export function recentTimingPair(stats = {}) {
  return `${formatSampleSeconds(stats.avg_header_ms, stats.header_samples)}/${formatSampleSeconds(stats.avg_response_ms, stats.response_samples)}`
}

export function keyErrorDetails(item = {}, runtime = {}) {
  const status = runtime.status || item.status || 'active'
  // Empty live values are authoritative: do not resurrect a stale stored
  // disable reason after a key has been enabled or a request has succeeded.
  const disabled = status === 'active' ? '' : String(runtime.disable_reason ?? item.disable_reason ?? '').trim()
  const last = String(runtime.last_error ?? item.last_error ?? '').trim()
  return [...new Set([last, disabled].filter(Boolean))].join('\n')
}
