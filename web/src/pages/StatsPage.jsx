import { useEffect, useState } from 'react'

import { buttonClass, panelClass } from '../app/utils'

function statusTone(status) {
  switch (status) {
    case 'disabled_manual':
      return 'border-amber-200 bg-amber-50 text-amber-700'
    case 'disabled_auto':
      return 'border-rose-200 bg-rose-50 text-rose-700'
    default:
      return 'border-emerald-200 bg-emerald-50 text-emerald-700'
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
    <section className="space-y-4">
      <article className={panelClass('p-4')}>
        <div className="flex flex-wrap items-end gap-3">
          <button className={buttonClass('ghost')} disabled={busy || !statsFilters.vendor} onClick={onRefresh}>
            立即刷新
          </button>
          <button className={autoRefreshStats ? buttonClass('primary') : buttonClass()} onClick={onToggleAutoRefresh}>
            自动刷新: {autoRefreshStats ? '开' : '关'}
          </button>
          <label className="field-wrap w-40">
            <span className="field-label">刷新间隔</span>
            <select className="select-base" value={refreshEverySec} onChange={(e) => onRefreshEverySecChange(e.target.value)}>
              <option value="2">2 秒</option>
              <option value="4">4 秒</option>
              <option value="8">8 秒</option>
              <option value="15">15 秒</option>
            </select>
          </label>
        </div>
      </article>

      <div className="flex flex-wrap gap-2">
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
        {!vendorRows.length && <p className="text-sm text-slate-400">暂无供应商，请先创建供应商。</p>}
      </div>

      {statsFilters.vendor && (
        <section className={panelClass('p-5')}>
          <div className="flex flex-col gap-3 rounded-md border border-slate-200 bg-slate-50 p-3 lg:flex-row lg:items-center lg:justify-between">
            <form className="flex flex-1 flex-col gap-3 sm:flex-row sm:items-center" onSubmit={submitSearch}>
              <input
                className="input-base text-sm lg:max-w-sm"
                placeholder="搜索 key / 状态 / 错误"
                value={queryInput}
                onChange={(e) => setQueryInput(e.target.value)}
              />
              <select
                className="select-base text-sm lg:w-44"
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
              <button className={buttonClass('ghost')} type="submit">
                搜索
              </button>
              {statsFilters.q && (
                <button className={buttonClass('ghost')} type="button" onClick={() => { setQueryInput(''); patchFilters({ q: '', page: 1 }) }}>
                  清空
                </button>
              )}
            </form>

            <div className="flex items-center gap-3 text-xs text-slate-500">
              <span>当前展示 {rows.length} / {total} 条</span>
              <div className="flex items-center gap-2">
                <span>行数/页</span>
                <select
                  className="select-base h-8 w-24 py-0 text-xs"
                  value={String(statsFilters.pageSize)}
                  onChange={(e) => patchFilters({ pageSize: Number(e.target.value), page: 1 })}
                >
                  <option value="20">20</option>
                  <option value="50">50</option>
                  <option value="100">100</option>
                  <option value="200">200</option>
                </select>
              </div>
            </div>
          </div>

          <div className="mt-5 table-shell text-xs">
            <table className="w-full min-w-[1120px]">
              <thead>
                <tr>
                  <th className="w-20 px-3 py-2 text-left">序号</th>
                  <th className="px-3 py-2 text-left">Key</th>
                  <th className="w-36 px-3 py-2 text-left">状态</th>
                  <th className="w-36 px-3 py-2 text-left">负载</th>
                  <th className="w-48 px-3 py-2 text-left">请求统计</th>
                  <th className="w-48 px-3 py-2 text-left">错误统计</th>
                  <th className="px-3 py-2 text-left">最近错误</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((item, idx) => {
                  const totalRequests = Number(item.total_requests || 0)
                  const successCount = Number(item.success_count || 0)
                  const failedCount = Math.max(0, totalRequests - successCount)
                  const inflight = Number(item.inflight || 0)
                  const backoff = Number(item.backoff_remaining_seconds || 0)
                  const failures = Number(item.failures || 0)
                  const lastStatus = Number(item.last_status || 0)
                  const reason = runtimeReason(item)

                  return (
                    <tr key={`${item.key_masked}-${idx}`} className="hover:bg-slate-50/50">
                      <td className="px-3 py-2 font-mono text-slate-400">{(currentPage - 1) * pageSize + idx + 1}</td>
                      <td className="px-3 py-2">
                        <div className="font-mono text-slate-700">{item.key_masked}</div>
                      </td>
                      <td className="px-3 py-2">
                        <span className={`inline-flex rounded-md border px-2 py-1 ${statusTone(item.status)}`}>
                          {statusLabel(item.status)}
                        </span>
                        {item.disabled_by && <div className="mt-1 text-[11px] text-slate-400">by {item.disabled_by}</div>}
                      </td>
                      <td className="px-3 py-2 text-slate-500">
                        <div>退避 {backoff > 0 ? `${backoff}s` : '--'}</div>
                        <div className="mt-1">并发 {inflight}</div>
                      </td>
                      <td className="px-3 py-2 text-slate-500">
                        <div>总请求 {totalRequests}</div>
                        <div className="mt-1">成功 {successCount} / 失败 {failedCount}</div>
                        <div className="mt-1">连续失败 {failures}</div>
                      </td>
                      <td className="px-3 py-2 text-slate-500">
                        <div>401 {Number(item.unauthorized_count || 0)} · 403 {Number(item.forbidden_count || 0)}</div>
                        <div className="mt-1">429 {Number(item.rate_limit_count || 0)} · other {Number(item.other_error_count || 0)}</div>
                      </td>
                      <td className="px-3 py-2 text-slate-500">
                        <div className="max-w-[28rem] truncate" title={reason}>
                          {reason || '--'}
                        </div>
                        {lastStatus > 0 && <div className="mt-1 text-[11px] text-slate-400">last status {lastStatus}</div>}
                      </td>
                    </tr>
                  )
                })}
                {!rows.length && (
                  <tr>
                    <td colSpan={7} className="px-3 py-12 text-center text-slate-400">
                      暂无匹配运行态
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          <div className="mt-4 flex flex-col gap-4 border-t border-slate-100 pt-4 text-xs text-slate-500 sm:flex-row sm:items-center sm:justify-between">
            <span>第 {currentPage} 页 / {totalPages} 页</span>
            <div className="flex items-center gap-2">
              <button
                className="rounded border border-slate-200 px-3 py-1 hover:bg-slate-50 disabled:opacity-50"
                disabled={currentPage <= 1}
                onClick={() => patchFilters({ page: Math.max(1, currentPage - 1) })}
              >
                上一页
              </button>
              <button
                className="rounded border border-slate-200 px-3 py-1 hover:bg-slate-50 disabled:opacity-50"
                disabled={currentPage >= totalPages}
                onClick={() => patchFilters({ page: Math.min(totalPages, currentPage + 1) })}
              >
                下一页
              </button>
            </div>
          </div>
        </section>
      )}
    </section>
  )
}
