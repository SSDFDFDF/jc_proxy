import { buttonClass } from '../../app/utils'
import { KEY_FILTERS, COUNT_PALETTES } from './keyMetrics'
import { Icon, IconButton } from './shared'

/* ── Toolbar: search + filter chips + refresh cluster + actions ── */

function FilterChips({ value, counts, onChange }) {
  return (
    <div className="flex flex-wrap items-center gap-1" aria-label="按状态过滤">
      {KEY_FILTERS.map((option) => {
        const active = value === option.id
        const count = counts[option.id] ?? 0
        const palette = COUNT_PALETTES[option.id]
        const emphasize = count > 0 && palette.value !== 'normal' && option.id !== 'active'
        return (
          <button
            key={option.id}
            type="button"
            aria-pressed={active}
            className={`filter-chip ${active ? 'filter-chip-active' : ''}`}
            onClick={() => onChange(option.id)}
          >
            <span>{option.label}</span>
            <span className={`font-mono text-[10px] tabular-nums ${active ? 'text-[var(--accent)]' : emphasize ? palette.value : 'text-[var(--text-faint)]'}`}>
              {count}
            </span>
          </button>
        )
      })}
    </div>
  )
}

function RefreshCluster({ busy, autoRefresh, refreshEverySec, onToggleAutoRefresh, onRefreshEverySecChange, onRefresh }) {
  return (
    <div className="flex items-center gap-1 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-1">
      <IconButton icon="refresh" label="立即刷新运行态" disabled={busy} onClick={onRefresh} />
      <button
        type="button"
        className={`flex h-7 items-center rounded-md px-2 text-xs transition-colors ${autoRefresh ? 'bg-[var(--accent-soft)] text-[var(--accent)]' : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'}`}
        onClick={onToggleAutoRefresh}
        title={autoRefresh ? '点击关闭自动刷新' : '点击开启自动刷新'}
      >
        {autoRefresh ? '自动刷新' : '已暂停'}
      </button>
      {autoRefresh && (
        <select
          className="select-base !h-7 !py-0 !pl-1.5 !pr-6 !text-xs w-16"
          value={refreshEverySec}
          onChange={(e) => onRefreshEverySecChange(e.target.value)}
          aria-label="自动刷新间隔"
        >
          <option value="2">2s</option>
          <option value="4">4s</option>
          <option value="8">8s</option>
          <option value="15">15s</option>
        </select>
      )}
    </div>
  )
}

export function KeyToolbar({
  query,
  onQueryChange,
  statusFilter,
  counts,
  onStatusFilterChange,
  filteredCount,
  totalCount,
  busy,
  autoRefreshStats,
  refreshEverySec,
  onToggleAutoRefresh,
  onRefreshEverySecChange,
  onRefreshStats,
  showSecrets,
  onToggleSecrets,
  onAdd
}) {
  return (
    <div className="control-bar">
      <div className="flex min-w-[240px] flex-1 flex-wrap items-center gap-2">
        <div className="relative min-w-[200px] flex-1 lg:max-w-xs">
          <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-faint)]">
            <Icon name="search" size={13} />
          </span>
          <input
            className="input-base !pl-8 w-full"
            placeholder="搜索 key / 备注 / 原因 / 错误"
            value={query}
            onChange={(e) => onQueryChange(e.target.value)}
          />
        </div>
        <FilterChips value={statusFilter} counts={counts} onChange={onStatusFilterChange} />
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <span className="whitespace-nowrap text-xs text-[var(--text-muted)]">
          <strong className="font-mono text-[var(--text-secondary)]">{filteredCount}</strong>
          {' / '}
          <strong className="font-mono text-[var(--text-secondary)]">{totalCount}</strong> 条
        </span>
        <RefreshCluster
          busy={busy}
          autoRefresh={autoRefreshStats}
          refreshEverySec={refreshEverySec}
          onToggleAutoRefresh={onToggleAutoRefresh}
          onRefreshEverySecChange={onRefreshEverySecChange}
          onRefresh={onRefreshStats}
        />
        <IconButton
          icon={showSecrets ? 'eyeOff' : 'eye'}
          label={showSecrets ? '隐藏密钥明文' : '显示密钥明文'}
          onClick={onToggleSecrets}
        />
        <button className={buttonClass('primary')} disabled={busy} onClick={onAdd}>
          <Icon name="plus" />
          添加密钥
        </button>
      </div>
    </div>
  )
}
