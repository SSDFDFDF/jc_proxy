function statPill(label, value, tone = 'default') {
  const toneClass = {
    ok: 'border-[rgba(34,197,94,0.3)] bg-[var(--success-soft)] text-[var(--success)]',
    err: 'border-[rgba(239,68,68,0.3)] bg-[var(--danger-soft)] text-[var(--danger)]',
    warn: 'border-[rgba(245,158,11,0.3)] bg-[var(--warning-soft)] text-[var(--warning)]',
    info: 'border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-secondary)]',
    default: 'border-[var(--border)] bg-[var(--bg-surface)] text-[var(--text-secondary)]'
  }[tone]

  return (
    <span className={`inline-flex items-center gap-1 rounded-md border px-2 py-1 text-[12px] leading-none ${toneClass}`}>
      <strong className="font-mono text-[11px]">{label}</strong>
      <span>{value}</span>
    </span>
  )
}

export function RuntimeKeyStatCard({ item }) {
  const total = Number(item.total_requests || 0)
  const success = Number(item.success_count || 0)
  const failed = Math.max(0, total - success)
  const inflight = Number(item.inflight || 0)
  const backoff = Number(item.backoff_remaining_seconds || 0)
  const cooldownLevel = Number(item.cooldown_level || 0)
  const cooldownMultiplier = cooldownLevel > 1 ? 2 ** (cooldownLevel - 1) : 1
  const streak = Number(item.failures || 0)
  const status = String(item.status || 'active')
  const disableReason = String(item.disable_reason || '').trim()
  const lastError = String(item.last_error || '').trim()
  const statusTone = status === 'disabled_auto' ? 'err' : status === 'disabled_manual' ? 'warn' : 'ok'
  const statusLabel = status === 'disabled_auto' ? 'auto-off' : status === 'disabled_manual' ? 'manual-off' : 'active'

  return (
    <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-4 text-sm text-[var(--text-secondary)] transition-colors hover:border-[var(--border-strong)]">
      <div className="flex items-start justify-between gap-3">
        <p className="font-mono text-[var(--text-primary)]">{item.key_masked}</p>
        <div className="flex flex-wrap justify-end gap-2">
          {statPill('state', statusLabel, statusTone)}
          {backoff > 0 && (
            <span className="rounded-md bg-[var(--danger-soft)] border border-[rgba(239,68,68,0.3)] px-2 py-1 text-[11px] font-medium text-[var(--danger)]">
              cooldown {backoff}s{cooldownLevel > 1 ? ` · x${cooldownMultiplier}` : ''}
            </span>
          )}
        </div>
      </div>

      <div className="mt-3 flex flex-wrap gap-2">
        {statPill('req', total, 'info')}
        {statPill('✔', success, 'ok')}
        {statPill('x', failed, failed > 0 ? 'err' : 'default')}
        {statPill('run', inflight, inflight > 0 ? 'warn' : 'default')}
        {statPill('streak', streak, streak > 0 ? 'warn' : 'default')}
        {cooldownLevel > 1 && statPill('expo', `x${cooldownMultiplier}`, 'warn')}
      </div>

      <div className="mt-2 flex flex-wrap gap-2">
        {statPill('401', Number(item.unauthorized_count || 0), Number(item.unauthorized_count || 0) > 0 ? 'err' : 'default')}
        {statPill('403', Number(item.forbidden_count || 0), Number(item.forbidden_count || 0) > 0 ? 'err' : 'default')}
        {statPill('429', Number(item.rate_limit_count || 0), Number(item.rate_limit_count || 0) > 0 ? 'err' : 'default')}
        {statPill('other', Number(item.other_error_count || 0), Number(item.other_error_count || 0) > 0 ? 'warn' : 'default')}
      </div>

      {(disableReason || lastError) && (
        <p className="mt-3 truncate text-[12px] text-[var(--text-muted)]" title={lastError}>
          err: {disableReason || lastError}
        </p>
      )}
    </div>
  )
}
