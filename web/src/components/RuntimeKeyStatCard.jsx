function statPill(label, value, tone = 'default') {
  const toneClass = {
    ok: 'border-emerald-200 bg-emerald-50 text-emerald-700',
    err: 'border-rose-200 bg-rose-50 text-rose-700',
    warn: 'border-amber-200 bg-amber-50 text-amber-700',
    info: 'border-slate-200 bg-slate-50 text-slate-700',
    default: 'border-slate-200 bg-white text-slate-700'
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
  const streak = Number(item.failures || 0)
  const status = String(item.status || 'active')
  const disableReason = String(item.disable_reason || '').trim()
  const lastError = String(item.last_error || '').trim()
  const statusTone = status === 'disabled_auto' ? 'err' : status === 'disabled_manual' ? 'warn' : 'ok'
  const statusLabel = status === 'disabled_auto' ? 'auto-off' : status === 'disabled_manual' ? 'manual-off' : 'active'

  return (
    <div className="rounded-md border border-slate-200 bg-slate-50 p-4 text-sm text-slate-700">
      <div className="flex items-start justify-between gap-3">
        <p className="font-mono text-slate-800">{item.key_masked}</p>
        <div className="flex flex-wrap justify-end gap-2">
          {statPill('state', statusLabel, statusTone)}
          {backoff > 0 && (
            <span className="rounded-md bg-rose-100 px-2 py-1 text-[11px] font-medium text-rose-700">
              cooldown {backoff}s
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
      </div>

      <div className="mt-2 flex flex-wrap gap-2">
        {statPill('401', Number(item.unauthorized_count || 0), Number(item.unauthorized_count || 0) > 0 ? 'err' : 'default')}
        {statPill('403', Number(item.forbidden_count || 0), Number(item.forbidden_count || 0) > 0 ? 'err' : 'default')}
        {statPill('429', Number(item.rate_limit_count || 0), Number(item.rate_limit_count || 0) > 0 ? 'err' : 'default')}
        {statPill('other', Number(item.other_error_count || 0), Number(item.other_error_count || 0) > 0 ? 'warn' : 'default')}
      </div>

      {(disableReason || lastError) && (
        <p className="mt-3 truncate text-[12px] text-slate-500" title={lastError}>
          err: {disableReason || lastError}
        </p>
      )}
    </div>
  )
}
