import { useEffect, useState } from 'react'

import { buttonClass, panelClass } from '../app/utils'

function statusTone(status) {
  switch (status) {
    case 'disabled_manual':
      return 'border-[rgba(245,158,11,0.3)] bg-[var(--warning-soft)] text-[var(--warning)]'
    case 'disabled_auto':
      return 'border-[rgba(239,68,68,0.3)] bg-[var(--danger-soft)] text-[var(--danger)]'
    default:
      return 'border-[rgba(34,197,94,0.3)] bg-[var(--success-soft)] text-[var(--success)]'
  }
}

function statusLabel(status) {
  switch (status) {
    case 'disabled_manual':
      return '手动禁用'
    case 'disabled_auto':
      return '自动禁用'
    default:
      return '启用中'
  }
}

function runtimeReason(item) {
  return String(item.disable_reason || item.last_error || '').trim()
}

function ErrorPill({ label, count, tone = 'default' }) {
  if (!count) return null
  const toneClasses = {
    err: 'text-[var(--danger)] border-[rgba(239,68,68,0.2)] bg-[rgba(239,68,68,0.05)]',
    warn: 'text-[var(--warning)] border-[rgba(245,158,11,0.2)] bg-[rgba(245,158,11,0.05)]',
    default: 'text-[var(--text-secondary)] border-[var(--border)] bg-[var(--bg-elevated)]'
  }
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-[4px] border px-1.5 py-0.5 text-[10px] ${toneClasses[tone]}`}>
      <span className="font-medium opacity-80">{label}</span>
      <span className="font-mono">{count}</span>
    </span>
  )
}

export function StatsPage({
  busy,
  vendorRows,
  statsResult,
  statsFilters,
  autoRefreshStats,
  refreshEverySec,
  onStatsFiltersChange,
  onToggleAutoRefresh,
  onRefreshEverySecChange,
  onRefresh
}) {
  const [queryInput, setQueryInput] = useState(statsFilters.q || '')

  useEffect(() => {
    setQueryInput(statsFilters.q || '')
  }, [statsFilters.q, statsFilters.vendor])

  const rows = statsResult.vendors?.[statsFilters.vendor] || []
  const meta = statsResult.meta || {
    page: statsFilters.page,
    page_size: statsFilters.pageSize,
    total: rows.length
  }
  const total = Number(meta.total || 0)
  const pageSize = Number(meta.page_size || statsFilters.pageSize || 50)
  const currentPage = Number(meta.page || statsFilters.page || 1)
  const totalPages = Math.max(1, Math.ceil(total / Math.max(1, pageSize)))

  const patchFilters = (patch) => {
    onStatsFiltersChange((prev) => ({ ...prev, ...patch }))
  }

  const submitSearch = (event) => {
    event.preventDefault()
    patchFilters({ q: queryInput.trim(), page: 1 })
  }

  return (
    <section className={`${panelClass('p-5')} animate-fade-in`}>
      {/* Header */}
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-4">
        <div>
          <h3 className="section-title">运行状态统计</h3>
          <p className="mt-1 text-xs text-[var(--text-muted)]">实时查看并监控各供应商中 API 密钥的异常、并发、连败与熔断状态。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button className={buttonClass('ghost')} disabled={busy || !statsFilters.vendor} onClick={onRefresh}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 2v6h-6" /><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M3 22v-6h6" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
            </svg>
            立即刷新
          </button>
          <button className={autoRefreshStats ? buttonClass('primary') : buttonClass()} onClick={onToggleAutoRefresh}>
            自动刷新: {autoRefreshStats ? '开' : '关'}
          </button>
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-[var(--text-secondary)] whitespace-nowrap">间隔</span>
            <select className="select-base py-1 h-8 w-20 px-2 text-sm" value={refreshEverySec} onChange={(e) => onRefreshEverySecChange(e.target.value)}>
              <option value="2">2 秒</option>
              <option value="4">4 秒</option>
              <option value="8">8 秒</option>
              <option value="15">15 秒</option>
            </select>
          </div>
        </div>
      </div>

      {/* Vendor Tabs */}
      <div className="mb-5 flex flex-wrap gap-2">
        {vendorRows.map((row) => (
          <button
            key={row.name}
            className={`tab-link ${statsFilters.vendor === row.name ? 'tab-link-active' : ''}`}
            onClick={() => patchFilters({ vendor: row.name, page: 1 })}
          >
            <span>{row.name}</span>
            <small>退避 {row.backoff || 0} / 并发 {row.inflight || 0}</small>
          </button>
        ))}
        {!vendorRows.length && <p className="text-sm text-[var(--text-faint)]">暂无供应商，请先创建供应商。</p>}
      </div>

      {statsFilters.vendor && (
        <div className="space-y-4">
          {/* Search & Filter Bar */}
          <div className="control-bar !p-2">
            <form className="flex flex-1 flex-col gap-3 sm:flex-row sm:items-center" onSubmit={submitSearch}>
              <div className="relative flex-1 max-w-md">
                <svg className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-muted)]" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="11" cy="11" r="8" /><line x1="21" y1="21" x2="16.65" y2="16.65" />
                </svg>
                <input
                  className="input-base text-sm pl-9 w-full"
                  placeholder="搜索 key / 状态 / 错误"
                  value={queryInput}
                  onChange={(e) => setQueryInput(e.target.value)}
                />
              </div>
              <select
                className="select-base text-sm lg:w-40"
                value={statsFilters.filter}
                onChange={(e) => patchFilters({ filter: e.target.value, page: 1 })}
              >
                <option value="all">全部状态</option>
                <option value="active">仅启用</option>
                <option value="disabled">仅禁用</option>
                <option value="backoff">仅退避</option>
                <option value="issues">仅异常</option>
                <option value="inflight">仅处理中</option>
              </select>
              <button className={buttonClass('ghost')} type="submit">搜索</button>
              {statsFilters.q && (
                <button className="text-xs text-[var(--text-muted)] hover:text-[var(--text-primary)] transition-colors" type="button" onClick={() => { setQueryInput(''); patchFilters({ q: '', page: 1 }) }}>
                  清空
                </button>
              )}
            </form>

            <div className="flex items-center gap-4 text-xs text-[var(--text-muted)] mt-3 sm:mt-0">
              <span className="whitespace-nowrap">筛选 <strong className="font-mono text-[var(--text-primary)]">{rows.length}</strong> / 总共 <strong className="font-mono text-[var(--text-primary)]">{total}</strong></span>
            </div>
          </div>

          {/* Data Table */}
          <div className="mt-4 table-shell text-xs">
            <table className="w-full min-w-[1000px]">
              <thead>
                <tr>
                  <th className="w-12 text-center">#</th>
                  <th className="w-64">Key & 状态</th>
                  <th className="w-32">负载</th>
                  <th className="w-48">请求统计</th>
                  <th className="w-40">错误分类</th>
                  <th>当前异常与信息</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--border)] bg-transparent">
                {rows.map((item, idx) => {
                  const totalRequests = Number(item.total_requests || 0)
                  const successCount = Number(item.success_count || 0)
                  const failedCount = Math.max(0, totalRequests - successCount)
                  const inflight = Number(item.inflight || 0)
                  const backoff = Number(item.backoff_remaining_seconds || 0)
                  const failures = Number(item.failures || 0)
                  const lastStatus = Number(item.last_status || 0)
                  const reason = runtimeReason(item)
                  const err401 = Number(item.unauthorized_count || 0)
                  const err403 = Number(item.forbidden_count || 0)
                  const err429 = Number(item.rate_limit_count || 0)
                  const errOth = Number(item.other_error_count || 0)
                  const hasErrors = err401 > 0 || err403 > 0 || err429 > 0 || errOth > 0

                  const successRate = totalRequests === 0 ? 0 : Math.round((successCount / totalRequests) * 100)
                  const errRate = totalRequests === 0 ? 0 : 100 - successRate

                  return (
                    <tr key={`${item.key_masked}-${idx}`} className="hover:bg-[var(--bg-hover)] transition-colors group">
                      <td className="text-center font-mono text-[10px] text-[var(--text-faint)]">{(currentPage - 1) * pageSize + idx + 1}</td>
                      
                      {/* Key & Status */}
                      <td className="py-3">
                        <div className="font-mono text-[var(--text-primary)] font-medium">{item.key_masked}</div>
                        <div className="mt-1.5 flex items-center gap-2">
                          <span className={`inline-flex rounded border px-1.5 py-[1px] text-[10px] font-semibold tracking-wide ${statusTone(item.status)}`}>
                            {statusLabel(item.status)}
                          </span>
                          {item.disabled_by && <span className="text-[10px] text-[var(--text-faint)]">by {item.disabled_by}</span>}
                        </div>
                      </td>
                      
                      {/* Load */}
                      <td>
                        <div className="flex flex-col gap-1.5">
                          <div className="flex items-center gap-2">
                            <span className="text-[10px] text-[var(--text-muted)] w-8">并发</span>
                            <span className={`font-mono text-[11px] ${inflight > 0 ? 'text-[var(--accent)] font-semibold border-b border-[var(--accent)] leading-none pb-[1px]' : 'text-[var(--text-secondary)]'}`}>{inflight}</span>
                          </div>
                          <div className="flex items-center gap-2">
                            <span className="text-[10px] text-[var(--text-muted)] w-8">退避</span>
                            {backoff > 0 ? <span className="font-mono text-[11px] text-[var(--danger)] font-semibold border-b border-[var(--danger)] leading-none pb-[1px]">{backoff}s</span> : <span className="font-mono text-[11px] text-[var(--text-faint)]">--</span>}
                          </div>
                        </div>
                      </td>

                      {/* Request Stats */}
                      <td className="pr-6">
                        <div className="flex flex-col gap-1 w-full">
                          <div className="flex justify-between items-baseline">
                            <span className="text-[11px] font-mono text-[var(--text-primary)]" title="总请求">{totalRequests.toLocaleString()} reqs</span>
                            {totalRequests > 0 && <span className="font-mono text-[10px] text-[var(--text-muted)]">{successRate}% 成功</span>}
                          </div>
                          <div className="h-1.5 w-full bg-[var(--border)] rounded-full overflow-hidden flex shadow-inner mt-0.5">
                            {successCount > 0 && <div className="h-full bg-[var(--success)]" style={{ width: `${successRate}%` }}></div>}
                            {failedCount > 0 && <div className="h-full bg-[var(--danger)]" style={{ width: `${errRate}%` }}></div>}
                          </div>
                          <div className="flex justify-between items-center mt-1 text-[10px]">
                            <span className="text-[var(--text-faint)]">连败: <strong className={`font-mono ${failures > 0 ? 'text-[var(--warning)]' : ''}`}>{failures}</strong></span>
                            {failedCount > 0 && <span className="text-[var(--danger)] font-mono">{failedCount} 失败</span>}
                          </div>
                        </div>
                      </td>

                      {/* Error Stats */}
                      <td>
                        {hasErrors ? (
                          <div className="flex flex-wrap gap-1.5">
                            <ErrorPill label="401" count={err401} tone="err" />
                            <ErrorPill label="403" count={err403} tone="err" />
                            <ErrorPill label="429" count={err429} tone="warn" />
                            <ErrorPill label="oth" count={errOth} tone="default" />
                          </div>
                        ) : (
                          <span className="text-[10px] text-[var(--text-faint)] italic">无错误记录</span>
                        )}
                      </td>

                      {/* Last Error & Info */}
                      <td>
                        <div className={`max-w-[18rem] truncate ${reason ? 'text-[var(--text-secondary)] font-medium' : 'text-[var(--text-faint)]'}`} title={reason}>
                          {reason || '--'}
                        </div>
                        {lastStatus > 0 && (
                          <div className="mt-1 flex items-center gap-1">
                            <span className="inline-block w-1.5 h-1.5 rounded-full bg-[var(--danger)]"></span>
                            <span className="text-[10px] text-[var(--text-muted)] font-mono">Status: {lastStatus}</span>
                          </div>
                        )}
                      </td>
                    </tr>
                  )
                })}
                {!rows.length && (
                  <tr>
                    <td colSpan={6} className="px-3 py-16 text-center text-[var(--text-faint)] text-sm">
                      暂无匹配运行态数据
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          {/* Pagination */}
          <div className="mt-4 flex flex-col gap-4 border-t border-[var(--border)] pt-4 text-xs text-[var(--text-muted)] sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-2">
              <span>每页显示</span>
              <select
                className="select-base h-7 w-20 py-0 px-2 text-xs"
                value={String(statsFilters.pageSize)}
                onChange={(e) => patchFilters({ pageSize: Number(e.target.value), page: 1 })}
              >
                <option value="20">20</option>
                <option value="50">50</option>
                <option value="100">100</option>
                <option value="200">200</option>
              </select>
            </div>
            
            <div className="flex items-center gap-4">
              <span className="font-mono">第 {currentPage} 页 / {totalPages} 页</span>
              <div className="flex items-center gap-1.5">
                <button
                  className="pagination-btn px-3"
                  disabled={currentPage <= 1}
                  onClick={() => patchFilters({ page: Math.max(1, currentPage - 1) })}
                >
                  上一页
                </button>
                <button
                  className="pagination-btn px-3"
                  disabled={currentPage >= totalPages}
                  onClick={() => patchFilters({ page: Math.min(totalPages, currentPage + 1) })}
                >
                  下一页
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
