import { KeyRecentTiming } from '../../components/KeyRecentTiming'
import { Icon, StatusBadge, LoadChips, ErrorPills, ReasonCell, KeyActionButtons, copyKey } from './shared'

/* ── Mobile card list ────────────────────────────────── */

export function KeyCardList({
  pageItems,
  showSecrets,
  busy,
  sortState,
  onSortChange,
  selectedKeys,
  onToggleSelect,
  onToggleSelectAll,
  onEditRemark,
  actionHandlers
}) {
  const allChecked = pageItems.length > 0 && pageItems.every((item) => selectedKeys.has(item.key))

  return (
    <div className="md:hidden">
      <div className="mb-2 flex items-center justify-between gap-3">
        <label className="flex min-h-9 items-center gap-2 text-sm">
          <input type="checkbox" className="kh-checkbox" checked={allChecked} onChange={onToggleSelectAll} />
          本页全选
        </label>
        <select
          aria-label="密钥排序"
          className="select-base w-auto"
          value={`${sortState.key}:${sortState.direction}`}
          onChange={(e) => {
            const [key, direction] = e.target.value.split(':')
            onSortChange({ key, direction })
          }}
        >
          <option value="index:asc">默认顺序</option>
          <option value="requests:desc">请求最多优先</option>
          <option value="requests:asc">请求最少优先</option>
          <option value="errors:desc">错误最多优先</option>
          <option value="status:asc">启用优先</option>
          {!['index:asc', 'requests:desc', 'requests:asc', 'errors:desc', 'status:asc'].includes(`${sortState.key}:${sortState.direction}`) && (
            <option value={`${sortState.key}:${sortState.direction}`}>当前排序</option>
          )}
        </select>
      </div>

      <div className="space-y-2">
        {pageItems.map((item) => {
          const { totalRequests, successCount, failedCount, successRate, errRate } = item.metrics
          const isDisabled = item.displayStatus !== 'active'
          const isSelected = selectedKeys.has(item.key)

          return (
            <div
              key={item.key}
              className={`rounded-lg border bg-[var(--bg-surface)] p-3 ${isSelected ? 'border-[var(--accent)]' : 'border-[var(--border)]'} ${isDisabled ? 'opacity-70' : ''}`}
            >
              {/* identity row */}
              <div className="flex items-start gap-2">
                <input
                  type="checkbox"
                  aria-label={`选择 ${item.masked}`}
                  className="kh-checkbox mt-1"
                  checked={isSelected}
                  onChange={() => onToggleSelect(item.key)}
                />
                <div className="min-w-0 flex-1">
                  <button
                    type="button"
                    className={`block w-full truncate text-left font-mono text-[12px] ${isDisabled ? 'text-[var(--text-muted)]' : 'text-[var(--text-primary)]'}`}
                    title="点击复制完整密钥"
                    onClick={() => copyKey(item.key)}
                  >
                    {showSecrets ? item.key : item.masked}
                  </button>
                  <button
                    type="button"
                    className={`mt-0.5 flex w-full min-w-0 items-center gap-1 text-left text-[11px] ${item.remark ? 'text-[var(--text-secondary)]' : 'text-[var(--text-faint)]'}`}
                    onClick={() => onEditRemark(item)}
                  >
                    <span className="truncate">{item.remark || '添加备注'}</span>
                    <Icon name="pencil" size={10} className="shrink-0 opacity-60" />
                  </button>
                </div>
                <StatusBadge status={item.displayStatus} />
              </div>

              {/* metrics row */}
              <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 pl-6 text-[11px]">
                <LoadChips metrics={item.metrics} />
                {totalRequests > 0 && (
                  <span className="inline-flex items-center gap-1.5">
                    <span className="font-mono text-[var(--text-primary)]">{totalRequests.toLocaleString()}</span>
                    <span className="key-metrics-bar" style={{ width: 48, flex: '0 0 auto' }}>
                      {successCount > 0 && <span className="block h-full bg-[var(--success)]" style={{ width: `${successRate}%` }} />}
                      {failedCount > 0 && <span className="block h-full bg-[var(--danger)]" style={{ width: `${errRate}%` }} />}
                    </span>
                    <span className="font-mono tabular-nums text-[var(--text-muted)]">{successRate}%</span>
                  </span>
                )}
                <KeyRecentTiming stats={item.rt} />
              </div>

              {(item.metrics.hasErrors || item.metrics.reason || item.metrics.secondaryText) && (
                <div className="mt-2 space-y-1 pl-6">
                  <ErrorPills metrics={item.metrics} />
                  <div className="text-[11px]">
                    <ReasonCell metrics={item.metrics} />
                  </div>
                </div>
              )}

              {/* actions */}
              <div className="mt-2 border-t border-[var(--border-subtle)] pt-2 pl-6">
                <KeyActionButtons item={item} busy={busy} {...actionHandlers} />
              </div>
            </div>
          )
        })}
        {!pageItems.length && (
          <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-10 text-center text-sm text-[var(--text-faint)]">
            暂无匹配密钥
          </div>
        )}
      </div>
    </div>
  )
}
