import { useEffect, useMemo, useState } from 'react'

import { buttonClass, formatClock, panelClass, parseKeysText } from '../app/utils'

/* ── Shared helpers ─────────────────────────────────────── */

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

/* ── Sub-tab definitions ────────────────────────────────── */

const SUB_TABS = [
  { id: 'manage', label: '密钥管理', icon: '📋' },
  { id: 'monitor', label: '运行监控', icon: '📊' }
]

/* ── Main component ─────────────────────────────────────── */

export function KeyHubPage({
  // Upstream keys (manage tab)
  upstreamKeysData,
  selectedKeyVendor,
  showSecrets,
  busy,
  onToggleSecrets,
  onSelectVendor,
  onAddKeys,
  onEnableKey,
  onDisableKey,
  onDeleteKey,
  // Stats (monitor tab)
  vendorRows,
  statsResult,
  statsFilters,
  autoRefreshStats,
  refreshEverySec,
  onStatsFiltersChange,
  onToggleAutoRefresh,
  onRefreshEverySecChange,
  onRefreshStats
}) {
  const [subTab, setSubTab] = useState('manage')

  /* ── Manage tab state ── */
  const [query, setQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [draftText, setDraftText] = useState('')
  const [showAddModal, setShowAddModal] = useState(false)
  const [modalHint, setModalHint] = useState('')

  /* ── Monitor tab state ── */
  const [queryInput, setQueryInput] = useState(statsFilters.q || '')

  /* ── Derived: upstream keys for manage tab ── */
  const allItems = upstreamKeysData.items?.[selectedKeyVendor] || []

  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase()
    return allItems.filter((item) => {
      if (statusFilter === 'active' && item.status !== 'active') return false
      if (statusFilter === 'disabled' && item.status === 'active') return false
      if (!q) return true
      const haystack = [item.key, item.masked, item.status, item.disable_reason, item.disabled_by]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return haystack.includes(q)
    })
  }, [allItems, query, statusFilter])

  const totalPages = Math.max(1, Math.ceil(filteredItems.length / pageSize))
  const currentPage = Math.min(page, totalPages)
  const start = (currentPage - 1) * pageSize
  const pageItems = filteredItems.slice(start, start + pageSize)

  /* ── Derived: stats for monitor tab ── */
  const statsRows = statsResult.vendors?.[statsFilters.vendor] || []
  const statsMeta = statsResult.meta || {
    page: statsFilters.page,
    page_size: statsFilters.pageSize,
    total: statsRows.length
  }
  const statsTotal = Number(statsMeta.total || 0)
  const statsPageSize = Number(statsMeta.page_size || statsFilters.pageSize || 50)
  const statsCurrentPage = Number(statsMeta.page || statsFilters.page || 1)
  const statsTotalPages = Math.max(1, Math.ceil(statsTotal / Math.max(1, statsPageSize)))

  /* ── Reset on vendor change ── */
  useEffect(() => {
    setQuery('')
    setStatusFilter('all')
    setPage(1)
    setDraftText('')
    setShowAddModal(false)
    setModalHint('')
  }, [selectedKeyVendor])

  useEffect(() => {
    setPage((prev) => Math.min(prev, totalPages))
  }, [totalPages])

  useEffect(() => {
    setQueryInput(statsFilters.q || '')
  }, [statsFilters.q, statsFilters.vendor])

  /* ── Handlers ── */
  const patchFilters = (patch) => {
    onStatsFiltersChange((prev) => ({ ...prev, ...patch }))
  }

  const submitSearch = (event) => {
    event.preventDefault()
    patchFilters({ q: queryInput.trim(), page: 1 })
  }

  const handleSelectVendor = (vendorName) => {
    onSelectVendor(vendorName)
    // Sync stats vendor as well
    patchFilters({ vendor: vendorName, page: 1 })
  }

  const submitAddKeys = async () => {
    const keys = parseKeysText(draftText)
    if (!keys.length) {
      setModalHint('请输入至少一条有效密钥，一行一个。')
      return
    }

    const existing = new Set(allItems.map((item) => item.key))
    const nextKeys = keys.filter((key) => !existing.has(key))
    if (!nextKeys.length) {
      setModalHint('输入的密钥都已存在。若是已禁用密钥，请直接在表格中点"启用"。')
      return
    }

    const ok = await onAddKeys(nextKeys)
    if (!ok) return

    setDraftText('')
    setModalHint('')
    setShowAddModal(false)
  }

  /* ── Build unified vendor tabs from both data sources ── */
  const vendorTabs = useMemo(() => {
    const upstreamVendors = upstreamKeysData.vendors || []
    const runtimeMap = new Map(vendorRows.map((r) => [r.name, r]))

    // Merge: use upstream vendor list as the primary, enrich with runtime data
    const seen = new Set()
    const tabs = []

    for (const item of upstreamVendors) {
      seen.add(item.vendor)
      const runtime = runtimeMap.get(item.vendor)
      tabs.push({
        vendor: item.vendor,
        activeCount: item.active_count || 0,
        disabledCount: item.disabled_count || 0,
        backoff: runtime?.backoff || 0,
        inflight: runtime?.inflight || 0
      })
    }

    // Add any vendor that exists in config but not in upstream keys
    for (const row of vendorRows) {
      if (!seen.has(row.name)) {
        tabs.push({
          vendor: row.name,
          activeCount: 0,
          disabledCount: 0,
          backoff: row.backoff || 0,
          inflight: row.inflight || 0
        })
      }
    }

    return tabs
  }, [upstreamKeysData.vendors, vendorRows])

  return (
    <section className={`${panelClass('p-5')} animate-fade-in`}>
      {/* ═══ Header ═══ */}
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-4">
        <div>
          <h3 className="section-title">密钥中心</h3>
          <p className="mt-1 text-xs text-[var(--text-muted)]">管理各供应商上游 API 密钥，监控运行状态与异常情况。</p>
        </div>
      </div>

      {/* ═══ Vendor Tabs ═══ */}
      <div className="mb-5 flex flex-wrap gap-2">
        {vendorTabs.map((item) => (
          <button
            key={item.vendor}
            className={`tab-link ${selectedKeyVendor === item.vendor ? 'tab-link-active' : ''}`}
            onClick={() => handleSelectVendor(item.vendor)}
          >
            <span>{item.vendor}</span>
            <small>
              启用 {item.activeCount} / 禁用 {item.disabledCount}
              {(item.backoff > 0 || item.inflight > 0) && (
                <span className="ml-1 opacity-70">
                  · 退避 {item.backoff} · 并发 {item.inflight}
                </span>
              )}
            </small>
          </button>
        ))}
        {!vendorTabs.length && <p className="text-sm text-[var(--text-faint)]">暂无供应商，请先创建供应商。</p>}
      </div>

      {/* ═══ Sub Tabs ═══ */}
      {selectedKeyVendor && (
        <div className="mt-4 space-y-5 border-t border-[var(--border)] pt-5">
          <div className="flex items-center gap-1 border-b border-[var(--border)] pb-0">
            {SUB_TABS.map((tab) => (
              <button
                key={tab.id}
                className={`inline-flex items-center gap-1.5 px-4 py-2.5 text-sm font-medium transition-colors border-b-2 -mb-[1px] ${
                  subTab === tab.id
                    ? 'border-[var(--accent)] text-[var(--accent)]'
                    : 'border-transparent text-[var(--text-muted)] hover:text-[var(--text-secondary)]'
                }`}
                onClick={() => setSubTab(tab.id)}
              >
                <span>{tab.icon}</span>
                {tab.label}
              </button>
            ))}
          </div>

          {/* ═══════════════════════════════════════════════ */}
          {/* ═══ MANAGE TAB ═══════════════════════════════ */}
          {/* ═══════════════════════════════════════════════ */}
          {subTab === 'manage' && (
            <div className="space-y-5 animate-fade-in">
              {/* Control Bar */}
              <div className="control-bar">
                <div className="flex flex-1 flex-col gap-3 sm:flex-row sm:items-center">
                  <input
                    className="input-base text-sm lg:max-w-sm flex-1"
                    placeholder="搜索 key / 状态 / 原因"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                  />
                  <select className="select-base text-sm lg:w-44" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
                    <option value="all">全部状态</option>
                    <option value="active">仅启用</option>
                    <option value="disabled">仅禁用</option>
                  </select>
                </div>
                <div className="flex items-center gap-3">
                  <span className="text-xs text-[var(--text-muted)]">
                    展示 <strong className="font-mono text-[var(--text-secondary)]">{filteredItems.length}</strong> / <strong className="font-mono text-[var(--text-secondary)]">{allItems.length}</strong> 条
                  </span>
                  <button className={buttonClass('ghost')} onClick={onToggleSecrets}>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      {showSecrets ? (
                        <>
                          <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
                          <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
                          <line x1="1" y1="1" x2="23" y2="23" />
                        </>
                      ) : (
                        <>
                          <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                          <circle cx="12" cy="12" r="3" />
                        </>
                      )}
                    </svg>
                    {showSecrets ? '隐藏密钥' : '显示密钥'}
                  </button>
                  <button className={buttonClass('primary')} disabled={busy || !selectedKeyVendor} onClick={() => setShowAddModal(true)}>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
                    </svg>
                    新增 / 导入
                  </button>
                </div>
              </div>

              {/* Table */}
              <div className="table-shell text-xs">
                <table className="w-full min-w-[780px]">
                  <thead>
                    <tr>
                      <th className="w-16">序号</th>
                      <th>Key</th>
                      <th className="w-28">状态</th>
                      <th>原因</th>
                      <th className="w-40">更新时间</th>
                      <th className="w-36 text-right">操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {pageItems.map((item, idx) => (
                      <tr key={item.key}>
                        <td className="font-mono text-[var(--text-faint)]">{start + idx + 1}</td>
                        <td>
                          <div className="font-mono text-[var(--text-primary)]">{showSecrets ? item.key : item.masked}</div>
                        </td>
                        <td>
                          <span className={`inline-flex rounded-md border px-2 py-1 text-[11px] font-semibold ${statusTone(item.status)}`}>
                            {statusLabel(item.status)}
                          </span>
                        </td>
                        <td>
                          <div className="max-w-[26rem] truncate">{item.disable_reason || '--'}</div>
                          {item.disabled_by && <div className="mt-1 text-[11px] text-[var(--text-faint)]">by {item.disabled_by}</div>}
                        </td>
                        <td className="font-mono text-[var(--text-muted)]">{formatClock(item.updated_at || item.disabled_at)}</td>
                        <td>
                          <div className="flex justify-end gap-3">
                            {item.status === 'active' ? (
                              <button className="font-medium text-[var(--warning)] hover:text-amber-400 transition-colors" onClick={() => onDisableKey(item.key)}>
                                禁用
                              </button>
                            ) : (
                              <button className="font-medium text-[var(--success)] hover:text-emerald-400 transition-colors" onClick={() => onEnableKey(item.key)}>
                                启用
                              </button>
                            )}
                            <button className="font-medium text-[var(--danger)] hover:text-red-400 transition-colors" onClick={() => onDeleteKey(item.key)}>
                              删除
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                    {!pageItems.length && (
                      <tr>
                        <td colSpan={6} className="px-3 py-12 text-center text-[var(--text-faint)]">
                          暂无匹配密钥
                        </td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>

              {/* Pagination */}
              <div className="flex flex-col gap-4 border-t border-[var(--border)] pt-4 text-xs text-[var(--text-muted)] sm:flex-row sm:items-center sm:justify-between">
                <div className="flex items-center gap-2">
                  <span>行/页</span>
                  <select className="select-base h-7 w-20 py-0 text-xs" value={String(pageSize)} onChange={(e) => setPageSize(Number(e.target.value))}>
                    <option value="20">20</option>
                    <option value="50">50</option>
                    <option value="100">100</option>
                    <option value="500">500</option>
                  </select>
                </div>
                <div className="flex items-center justify-between gap-3 sm:justify-end">
                  <span className="font-mono">第 {currentPage} 页 / {totalPages} 页</span>
                  <div className="flex items-center gap-2">
                    <button className="pagination-btn" disabled={currentPage <= 1} onClick={() => setPage((prev) => Math.max(1, prev - 1))}>
                      上一页
                    </button>
                    <button className="pagination-btn" disabled={currentPage >= totalPages} onClick={() => setPage((prev) => Math.min(totalPages, prev + 1))}>
                      下一页
                    </button>
                  </div>
                </div>
              </div>
            </div>
          )}

          {/* ═══════════════════════════════════════════════ */}
          {/* ═══ MONITOR TAB ═════════════════════════════ */}
          {/* ═══════════════════════════════════════════════ */}
          {subTab === 'monitor' && (
            <div className="space-y-4 animate-fade-in">
              {/* Control Bar */}
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
                  <span className="whitespace-nowrap">筛选 <strong className="font-mono text-[var(--text-primary)]">{statsRows.length}</strong> / 总共 <strong className="font-mono text-[var(--text-primary)]">{statsTotal}</strong></span>
                  <div className="flex items-center gap-2">
                    <button className={buttonClass('ghost')} disabled={busy || !statsFilters.vendor} onClick={onRefreshStats}>
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M21 2v6h-6" /><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M3 22v-6h6" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
                      </svg>
                      刷新
                    </button>
                    <button className={autoRefreshStats ? buttonClass('primary') : buttonClass()} onClick={onToggleAutoRefresh}>
                      自动: {autoRefreshStats ? '开' : '关'}
                    </button>
                    <select className="select-base py-1 h-8 w-20 px-2 text-sm" value={refreshEverySec} onChange={(e) => onRefreshEverySecChange(e.target.value)}>
                      <option value="2">2 秒</option>
                      <option value="4">4 秒</option>
                      <option value="8">8 秒</option>
                      <option value="15">15 秒</option>
                    </select>
                  </div>
                </div>
              </div>

              {/* Data Table */}
              <div className="mt-4 table-shell text-xs">
                <table className="w-full min-w-[1000px] table-fixed">
                  <thead>
                    <tr>
                      <th className="w-10 text-center">#</th>
                      <th className="w-44">Key & 状态</th>
                      <th className="w-28">负载</th>
                      <th className="w-40">请求统计</th>
                      <th className="w-36">错误分类</th>
                      <th>当前异常与信息</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[var(--border)] bg-transparent">
                    {statsRows.map((item, idx) => {
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
                          <td className="text-center font-mono text-[10px] text-[var(--text-faint)]">{(statsCurrentPage - 1) * statsPageSize + idx + 1}</td>
                          
                          {/* Key & Status */}
                          <td className="py-3">
                            <div className="font-mono text-[var(--text-primary)] font-medium truncate">{item.key_masked}</div>
                            <div className="mt-1 flex items-center gap-2">
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
                            {lastStatus >= 400 && (
                              <div className="mt-1 flex items-center gap-1">
                                <span className="inline-block w-1.5 h-1.5 rounded-full bg-[var(--danger)]"></span>
                                <span className="text-[10px] text-[var(--text-muted)] font-mono">Status: {lastStatus}</span>
                              </div>
                            )}
                            {lastStatus > 0 && lastStatus < 400 && (
                              <div className="mt-1 flex items-center gap-1">
                                <span className="inline-block w-1.5 h-1.5 rounded-full bg-[var(--success)]"></span>
                                <span className="text-[10px] text-[var(--text-muted)] font-mono">Status: {lastStatus}</span>
                              </div>
                            )}
                          </td>
                        </tr>
                      )
                    })}
                    {!statsRows.length && (
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
                  <span className="font-mono">第 {statsCurrentPage} 页 / {statsTotalPages} 页</span>
                  <div className="flex items-center gap-1.5">
                    <button
                      className="pagination-btn px-3"
                      disabled={statsCurrentPage <= 1}
                      onClick={() => patchFilters({ page: Math.max(1, statsCurrentPage - 1) })}
                    >
                      上一页
                    </button>
                    <button
                      className="pagination-btn px-3"
                      disabled={statsCurrentPage >= statsTotalPages}
                      onClick={() => patchFilters({ page: Math.min(statsTotalPages, statsCurrentPage + 1) })}
                    >
                      下一页
                    </button>
                  </div>
                </div>
              </div>
            </div>
          )}
        </div>
      )}

      {/* ═══ Add Modal ═══ */}
      {showAddModal && (
        <div className="modal-overlay animate-fade-in">
          <div className="modal-panel animate-slide-in">
            <div className="modal-header">
              <div>
                <h3>新增 / 导入密钥</h3>
                <p>一行一个。默认只做新增，不会覆盖现有状态。</p>
              </div>
              <button className="modal-close" onClick={() => setShowAddModal(false)}>✕</button>
            </div>

            <div className="modal-body">
              <textarea
                className="textarea-base min-h-[300px] w-full flex-1"
                placeholder={'在此粘贴一行或多行密钥，例如：\nsk-1234\nsk-5678'}
                value={draftText}
                autoFocus
                onChange={(e) => {
                  setDraftText(e.target.value)
                  if (modalHint) setModalHint('')
                }}
              />
              <div className="text-xs text-[var(--text-muted)]">
                识别到 <strong className="font-mono text-[var(--text-secondary)]">{parseKeysText(draftText).length}</strong> 条有效密钥
              </div>
              {modalHint && (
                <div className="rounded-lg border border-[rgba(245,158,11,0.3)] bg-[var(--warning-soft)] px-3 py-2 text-xs text-[var(--warning)]">
                  {modalHint}
                </div>
              )}
            </div>

            <div className="modal-footer">
              <button className={buttonClass()} onClick={() => setShowAddModal(false)}>取消</button>
              <button className={buttonClass('primary')} disabled={busy} onClick={submitAddKeys}>添加</button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
