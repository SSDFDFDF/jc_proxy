import { KeyRecentTiming } from '../../components/KeyRecentTiming'
import { nextSortState } from './keyMetrics'
import { Icon, StatusBadge, LoadChips, ErrorPills, ReasonCell, KeyActionButtons, copyKey } from './shared'

/* ── Desktop table ───────────────────────────────────── */

function SortableHeader({ label, sortKey, sortState, onSort, onReset }) {
  const active = sortState.key === sortKey
  const indicator = active ? (sortState.direction === 'asc' ? '↑' : '↓') : '↕'
  return (
    <div className="flex items-center gap-0.5">
      <button
        className={`table-sort-button justify-start ${active ? 'table-sort-button-active' : ''}`}
        type="button"
        onClick={() => onSort(nextSortState(sortState, sortKey))}
        title={`按${label}排序`}
      >
        <span>{label}</span>
        <span className="table-sort-indicator" aria-hidden="true">{indicator}</span>
      </button>
      {active && (
        <button
          type="button"
          className="table-sort-reset"
          title="恢复默认顺序"
          aria-label={`重置${label}排序`}
          onClick={(e) => { e.stopPropagation(); onReset() }}
        >
          ✕
        </button>
      )}
    </div>
  )
}

export function KeyTable({
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
    <div className="table-shell kh-key-table hidden text-xs md:block">
      <table className="w-full min-w-[980px] table-fixed">
        <colgroup>
          <col style={{ width: '36px' }} />
          <col style={{ width: '230px' }} />
          <col style={{ width: '128px' }} />
          <col style={{ width: '210px' }} />
          <col style={{ width: '120px' }} />
          <col />
          <col style={{ width: '172px' }} />
        </colgroup>
        <thead>
          <tr>
            <th className="!text-center">
              <input
                type="checkbox"
                className="kh-checkbox"
                aria-label="本页全选"
                checked={allChecked}
                onChange={onToggleSelectAll}
              />
            </th>
            <th><SortableHeader label="Key / 备注" sortKey="key" sortState={sortState} onSort={onSortChange} onReset={() => onSortChange({ key: 'index', direction: 'asc' })} /></th>
            <th><SortableHeader label="状态" sortKey="status" sortState={sortState} onSort={onSortChange} onReset={() => onSortChange({ key: 'index', direction: 'asc' })} /></th>
            <th><SortableHeader label="请求 / 近5次耗时" sortKey="requests" sortState={sortState} onSort={onSortChange} onReset={() => onSortChange({ key: 'index', direction: 'asc' })} /></th>
            <th><SortableHeader label="错误" sortKey="errors" sortState={sortState} onSort={onSortChange} onReset={() => onSortChange({ key: 'index', direction: 'asc' })} /></th>
            <th><SortableHeader label="异常 / 原因" sortKey="reason" sortState={sortState} onSort={onSortChange} onReset={() => onSortChange({ key: 'index', direction: 'asc' })} /></th>
            <th className="!text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          {pageItems.map((item) => {
            const { totalRequests, successCount, failedCount, successRate, errRate } = item.metrics
            const isDisabled = item.displayStatus !== 'active'
            const isSelected = selectedKeys.has(item.key)

            return (
              <tr
                key={item.key}
                className={`transition-colors ${isDisabled ? 'opacity-70' : ''} ${isSelected ? 'bg-[var(--bg-hover)]' : 'hover:bg-[var(--bg-hover)]'}`}
              >
                {/* ── Identity ── */}
                <td className="!text-center">
                  <input
                    type="checkbox"
                    aria-label={`选择 ${item.masked}`}
                    className="kh-checkbox"
                    checked={isSelected}
                    onChange={() => onToggleSelect(item.key)}
                  />
                </td>
                <td>
                  <div className="flex items-center gap-1">
                    <button
                      type="button"
                      className={`min-w-0 truncate font-mono text-left ${isDisabled ? 'text-[var(--text-muted)]' : 'text-[var(--text-primary)]'} hover:text-[var(--accent)]`}
                      title="点击复制完整密钥"
                      onClick={() => copyKey(item.key)}
                    >
                      {showSecrets ? item.key : item.masked}
                    </button>
                  </div>
                  <button
                    type="button"
                    className={`group mt-1 flex w-full min-w-0 items-center gap-1 text-left text-[11px] ${item.remark ? 'text-[var(--text-secondary)]' : 'text-[var(--text-faint)]'}`}
                    title={item.remark || '点击添加备注'}
                    onClick={() => onEditRemark(item)}
                  >
                    <span className="truncate">{item.remark || '添加备注'}</span>
                    <span className="shrink-0 opacity-0 transition-opacity group-hover:opacity-100">
                      <Icon name="pencil" size={10} />
                    </span>
                  </button>
                </td>

                {/* ── Status + live load ── */}
                <td>
                  <StatusBadge status={item.displayStatus} />
                  <div className="mt-1">
                    <LoadChips metrics={item.metrics} />
                  </div>
                </td>

                {/* ── Requests + recent timing ── */}
                <td>
                  {totalRequests > 0 ? (
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-[11px] text-[var(--text-primary)]">{totalRequests.toLocaleString()}</span>
                      <div className="key-metrics-bar">
                        {successCount > 0 && <div className="h-full bg-[var(--success)]" style={{ width: `${successRate}%` }} />}
                        {failedCount > 0 && <div className="h-full bg-[var(--danger)]" style={{ width: `${errRate}%` }} />}
                      </div>
                      <span className="font-mono text-[11px] tabular-nums text-[var(--text-muted)]">
                        {successRate}%{failedCount > 0 ? ` ·${failedCount}` : ''}
                      </span>
                    </div>
                  ) : (
                    <span className="text-[10px] text-[var(--text-faint)]">--</span>
                  )}
                  <div className="mt-0.5">
                    <KeyRecentTiming stats={item.rt} />
                  </div>
                </td>

                {/* ── Errors ── */}
                <td>
                  <ErrorPills metrics={item.metrics} />
                </td>

                {/* ── Reason ── */}
                <td>
                  <ReasonCell metrics={item.metrics} />
                </td>

                {/* ── Actions ── */}
                <td>
                  <KeyActionButtons item={item} busy={busy} dense {...actionHandlers} />
                </td>
              </tr>
            )
          })}
          {!pageItems.length && (
            <tr>
              <td colSpan={7} className="px-3 py-12 text-center text-[var(--text-faint)]">暂无匹配密钥</td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  )
}
