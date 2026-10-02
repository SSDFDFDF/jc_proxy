import { COUNT_PALETTES, countByFilter } from './keyMetrics'

/* Clickable KPI strip for the selected vendor. Every card doubles as a
   shortcut for the matching status filter. */
export function SummaryStrip({ items, filter, onFilterChange }) {
  const counts = countByFilter(items)
  const evaluatedRequests = items.reduce((sum, item) => sum + item.metrics.evaluatedRequests, 0)
  const successCount = items.reduce((sum, item) => sum + item.metrics.successCount, 0)
  const successRate = evaluatedRequests > 0 ? ((successCount / evaluatedRequests) * 100).toFixed(1) : null

  const cards = [
    { filterId: 'all', label: '密钥总数', value: counts.all },
    { filterId: 'active', label: '启用', value: counts.active },
    { filterId: 'disabled', label: '禁用', value: counts.disabled },
    { filterId: 'backoff', label: '退避中', value: counts.backoff },
    { filterId: 'inflight', label: '处理中', value: counts.inflight },
    { filterId: 'issues', label: '异常', value: counts.issues }
  ]

  return (
    <div className="kh-kpi-strip">
      {cards.map((card) => {
        const palette = COUNT_PALETTES[card.filterId] || COUNT_PALETTES.all
        const isActiveFilter = filter === card.filterId
        const emphasized = card.value > 0 && palette.value !== 'normal'
        return (
          <button
            key={card.filterId}
            type="button"
            onClick={() => onFilterChange(card.filterId === filter ? 'all' : card.filterId)}
            title={card.filterId === 'all' ? '显示全部密钥' : isActiveFilter ? '取消过滤' : `仅看${card.label}`}
            aria-pressed={isActiveFilter}
            className={`kh-kpi kh-kpi-clickable ${palette.bg} ${isActiveFilter ? 'kh-kpi-active' : ''} ${card.filterId === 'issues' && card.value > 0 ? 'kh-kpi-danger-active' : ''}`}
          >
            <div className="text-[11px] text-[var(--text-muted)]">{card.label}</div>
            <div className={`mt-0.5 font-mono text-lg font-semibold leading-none tabular-nums ${emphasized ? palette.value : 'text-[var(--text-primary)]'}`}>
              {card.value}
            </div>
          </button>
        )
      })}
      <div className="kh-kpi border-[var(--border)] bg-[var(--bg-surface)]" title="累计成功率：成功 /（成功 + 上游失败），不含客户端取消或下游中断">
        <div className="text-[11px] text-[var(--text-muted)]">成功率</div>
        <div className={`mt-0.5 font-mono text-lg font-semibold leading-none tabular-nums ${
          successRate === null ? 'text-[var(--text-faint)]'
            : Number(successRate) >= 99 ? 'text-[var(--success)]'
            : Number(successRate) >= 90 ? 'text-[var(--warning)]'
            : 'text-[var(--danger)]'
        }`}>
          {successRate === null ? '--' : `${successRate}%`}
        </div>
      </div>
    </div>
  )
}
