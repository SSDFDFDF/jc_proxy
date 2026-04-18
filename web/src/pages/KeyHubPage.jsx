import { useEffect, useMemo, useState } from 'react'

import { buttonClass, panelClass, parseKeysText } from '../app/utils'

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

function normalizeStatus(status) {
  switch (String(status || '').trim()) {
    case 'disabled_manual':
      return 'disabled_manual'
    case 'disabled_auto':
      return 'disabled_auto'
    default:
      return 'active'
  }
}

function resolveDisplayState(item, rt = {}) {
  const displayStatus = normalizeStatus(rt.status || item.status)
  const displayDisableReason = String(rt.disable_reason || item.disable_reason || rt.last_error || '').trim()
  const displayDisabledBy = String(rt.disabled_by || item.disabled_by || '').trim()
  return {
    displayStatus,
    displayDisableReason,
    displayDisabledBy
  }
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

/* ── Main component ─────────────────────────────────────── */

export function KeyHubPage({
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
  vendorRows,
  runtimeStats,
  autoRefreshStats,
  refreshEverySec,
  onToggleAutoRefresh,
  onRefreshEverySecChange,
  onRefreshStats
}) {
  const [query, setQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [draftText, setDraftText] = useState('')
  const [showAddModal, setShowAddModal] = useState(false)
  const [modalHint, setModalHint] = useState('')

  const allItems = upstreamKeysData.items?.[selectedKeyVendor] || []
  const runtimeKeys = runtimeStats?.vendors?.[selectedKeyVendor] || []

  /* ── Build runtime lookup by masked key ── */
  const runtimeMap = useMemo(() => {
    const map = new Map()
    for (const item of runtimeKeys) {
      if (item.key_masked) map.set(item.key_masked, item)
    }
    return map
  }, [runtimeKeys])

  /* ── Merge upstream + runtime into unified rows ── */
  const mergedItems = useMemo(() => {
    return allItems.map((item) => {
      const rt = runtimeMap.get(item.masked) || {}
      return {
        ...item,
        rt,
        ...resolveDisplayState(item, rt)
      }
    })
  }, [allItems, runtimeMap])

  /* ── Filter ── */
  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase()
    return mergedItems.filter((item) => {
      const rt = item.rt
      if (statusFilter === 'active' && item.displayStatus !== 'active') return false
      if (statusFilter === 'disabled' && item.displayStatus === 'active') return false
      if (statusFilter === 'backoff' && !(Number(rt.backoff_remaining_seconds || 0) > 0)) return false
      if (statusFilter === 'issues' && !(
        item.displayStatus !== 'active' ||
        Number(rt.backoff_remaining_seconds || 0) > 0 ||
        Number(rt.failures || 0) > 0 ||
        Number(rt.unauthorized_count || 0) > 0 ||
        Number(rt.forbidden_count || 0) > 0 ||
        Number(rt.rate_limit_count || 0) > 0 ||
        Number(rt.other_error_count || 0) > 0 ||
        item.displayDisableReason
      )) return false
      if (statusFilter === 'inflight' && !(Number(rt.inflight || 0) > 0)) return false
      if (!q) return true
      const haystack = [item.key, item.masked, item.displayStatus, item.displayDisableReason, item.displayDisabledBy, rt.last_error]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return haystack.includes(q)
    })
  }, [mergedItems, query, statusFilter])

  /* ── Pagination ── */
  const totalPages = Math.max(1, Math.ceil(filteredItems.length / pageSize))
  const currentPage = Math.min(page, totalPages)
  const start = (currentPage - 1) * pageSize
  const pageItems = filteredItems.slice(start, start + pageSize)

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

  /* ── Build unified vendor tabs ── */
  const vendorTabs = useMemo(() => {
    const upstreamVendors = upstreamKeysData.vendors || []
    const upstreamItems = upstreamKeysData.items || {}
    const runtimeVendors = runtimeStats?.vendors || {}
    const runtimeLookup = new Map(vendorRows.map((r) => [r.name, r]))
    const seen = new Set()
    const tabs = []

    const buildCounts = (vendor, fallbackActive = 0, fallbackDisabled = 0) => {
      const vendorItems = upstreamItems[vendor] || []
      if (!vendorItems.length) {
        return { activeCount: fallbackActive, disabledCount: fallbackDisabled }
      }
      const vendorRuntimeMap = new Map()
      for (const item of runtimeVendors[vendor] || []) {
        if (item.key_masked) vendorRuntimeMap.set(item.key_masked, item)
      }
      let activeCount = 0
      let disabledCount = 0
      for (const item of vendorItems) {
        const rt = vendorRuntimeMap.get(item.masked) || {}
        const { displayStatus } = resolveDisplayState(item, rt)
        if (displayStatus === 'active') activeCount += 1
        else disabledCount += 1
      }
      return { activeCount, disabledCount }
    }

    for (const item of upstreamVendors) {
      seen.add(item.vendor)
      const runtime = runtimeLookup.get(item.vendor)
      const counts = buildCounts(item.vendor, item.active_count || 0, item.disabled_count || 0)
      tabs.push({
        vendor: item.vendor,
        activeCount: counts.activeCount,
        disabledCount: counts.disabledCount,
        backoff: runtime?.backoff || 0,
        inflight: runtime?.inflight || 0
      })
    }
    for (const row of vendorRows) {
      if (!seen.has(row.name)) {
        const counts = buildCounts(row.name, 0, 0)
        tabs.push({ vendor: row.name, activeCount: counts.activeCount, disabledCount: counts.disabledCount, backoff: row.backoff || 0, inflight: row.inflight || 0 })
      }
    }
    return tabs
  }, [upstreamKeysData.vendors, upstreamKeysData.items, vendorRows, runtimeStats?.vendors])

  /* ── Add keys handler ── */
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
            onClick={() => onSelectVendor(item.vendor)}
          >
            <span>{item.vendor}</span>
            <small>
              启用 {item.activeCount} / 禁用 {item.disabledCount}
              {(item.backoff > 0 || item.inflight > 0) && (
                <span className="ml-1 opacity-70">· 退避 {item.backoff} · 并发 {item.inflight}</span>
              )}
            </small>
          </button>
        ))}
        {!vendorTabs.length && <p className="text-sm text-[var(--text-faint)]">暂无供应商，请先创建供应商。</p>}
      </div>

      {selectedKeyVendor && (
        <div className="mt-4 space-y-5 border-t border-[var(--border)] pt-5">
          {/* ═══ Control Bar ═══ */}
          <div className="control-bar">
            <div className="flex flex-1 flex-col gap-3 sm:flex-row sm:items-center">
              <input
                className="input-base text-sm lg:max-w-sm flex-1"
                placeholder="搜索 key / 状态 / 原因 / 错误"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              <select className="select-base text-sm lg:w-44" value={statusFilter} onChange={(e) => { setStatusFilter(e.target.value); setPage(1) }}>
                <option value="all">全部状态</option>
                <option value="active">仅启用</option>
                <option value="disabled">仅禁用</option>
                <option value="backoff">仅退避</option>
                <option value="issues">仅异常</option>
                <option value="inflight">仅处理中</option>
              </select>
            </div>
            <div className="flex items-center gap-3 flex-wrap">
              <span className="text-xs text-[var(--text-muted)] whitespace-nowrap">
                <strong className="font-mono text-[var(--text-secondary)]">{filteredItems.length}</strong> / <strong className="font-mono text-[var(--text-secondary)]">{allItems.length}</strong> 条
              </span>
              <div className="flex items-center gap-1.5 border-l border-[var(--border)] pl-3">
                <button className={buttonClass('ghost')} disabled={busy} onClick={onRefreshStats} title="立即刷新运行态">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M21 2v6h-6" /><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M3 22v-6h6" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
                  </svg>
                </button>
                <button className={`text-xs px-2 py-1 rounded-md transition-colors ${autoRefreshStats ? 'text-[var(--accent)] bg-[rgba(99,102,241,0.1)]' : 'text-[var(--text-muted)] hover:text-[var(--text-secondary)]'}`} onClick={onToggleAutoRefresh} title="自动刷新">
                  {autoRefreshStats ? '自动刷新' : '自动刷新: 关'}
                </button>
                {autoRefreshStats && (
                  <select className="select-base h-7 w-16 py-0 px-1 text-xs" value={refreshEverySec} onChange={(e) => onRefreshEverySecChange(e.target.value)}>
                    <option value="2">2s</option>
                    <option value="4">4s</option>
                    <option value="8">8s</option>
                    <option value="15">15s</option>
                  </select>
                )}
              </div>
              <div className="flex items-center gap-1.5 border-l border-[var(--border)] pl-3">
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
                  {showSecrets ? '隐藏' : '显示'}
                </button>
                <button className={buttonClass('primary')} disabled={busy || !selectedKeyVendor} onClick={() => setShowAddModal(true)}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
                  </svg>
                  新增
                </button>
              </div>
            </div>
          </div>

          {/* ═══ Unified Table ═══ */}
          <div className="table-shell text-xs">
            <table className="w-full min-w-[900px]">
              <thead>
                <tr>
                  <th className="w-10 text-center">#</th>
                  <th>Key</th>
                  <th className="w-[80px]">状态</th>
                  <th className="w-[72px]">负载</th>
                  <th className="w-[130px]">请求</th>
                  <th className="w-[110px]">错误</th>
                  <th>异常 / 原因</th>
                  <th className="w-24 text-right">操作</th>
                </tr>
              </thead>
              <tbody>
                {pageItems.map((item, idx) => {
                  const rt = item.rt || {}
                  const inflight = Number(rt.inflight || 0)
                  const backoff = Number(rt.backoff_remaining_seconds || 0)
                  const totalRequests = Number(rt.total_requests || 0)
                  const successCount = Number(rt.success_count || 0)
                  const failedCount = Math.max(0, totalRequests - successCount)
                  const failures = Number(rt.failures || 0)
                  const lastStatus = Number(rt.last_status || 0)
                  const reason = item.displayDisableReason
                  const err401 = Number(rt.unauthorized_count || 0)
                  const err403 = Number(rt.forbidden_count || 0)
                  const err429 = Number(rt.rate_limit_count || 0)
                  const errOth = Number(rt.other_error_count || 0)
                  const hasErrors = err401 > 0 || err403 > 0 || err429 > 0 || errOth > 0
                  const successRate = totalRequests === 0 ? 0 : Math.round((successCount / totalRequests) * 100)
                  const errRate = totalRequests === 0 ? 0 : 100 - successRate
                  const secondaryParts = []
                  if (item.displayDisabledBy) secondaryParts.push(`by ${item.displayDisabledBy}`)
                  if (lastStatus >= 400) secondaryParts.push(`HTTP ${lastStatus}`)
                  if (failures > 0) secondaryParts.push(`连败 ${failures}`)

                  return (
                    <tr key={item.key} className="hover:bg-[var(--bg-hover)] transition-colors">
                      <td className="text-center font-mono text-[10px] text-[var(--text-faint)]">{start + idx + 1}</td>
                      <td><div className="font-mono text-[var(--text-primary)] truncate">{showSecrets ? item.key : item.masked}</div></td>
                      <td>
                        <span className={`inline-flex rounded border px-1.5 py-[1px] text-[10px] font-semibold tracking-wide ${statusTone(item.displayStatus)}`}>
                          {statusLabel(item.displayStatus)}
                        </span>
                      </td>
                      <td>
                        {(inflight > 0 || backoff > 0) ? (
                          <span className="font-mono text-[11px]">
                            {inflight > 0 && <span className="text-[var(--accent)] font-semibold">↑{inflight}</span>}
                            {inflight > 0 && backoff > 0 && <span className="text-[var(--text-faint)] mx-0.5">/</span>}
                            {backoff > 0 && <span className="text-[var(--danger)] font-semibold">{backoff}s</span>}
                          </span>
                        ) : <span className="text-[10px] text-[var(--text-faint)]">--</span>}
                      </td>
                      <td>
                        {totalRequests > 0 ? (
                          <div className="w-full">
                            <div className="flex justify-between items-baseline">
                              <span className="text-[10px] font-mono text-[var(--text-primary)]">{totalRequests.toLocaleString()}</span>
                              <span className="font-mono text-[10px] text-[var(--text-muted)]">{successRate}%{failedCount > 0 ? ` ·${failedCount}` : ''}</span>
                            </div>
                            <div className="h-1 w-full bg-[var(--border)] rounded-full overflow-hidden flex mt-0.5">
                              {successCount > 0 && <div className="h-full bg-[var(--success)]" style={{ width: `${successRate}%` }}></div>}
                              {failedCount > 0 && <div className="h-full bg-[var(--danger)]" style={{ width: `${errRate}%` }}></div>}
                            </div>
                          </div>
                        ) : <span className="text-[10px] text-[var(--text-faint)]">--</span>}
                      </td>
                      <td>
                        {hasErrors ? (
                          <div className="flex flex-wrap gap-1">
                            <ErrorPill label="401" count={err401} tone="err" />
                            <ErrorPill label="403" count={err403} tone="err" />
                            <ErrorPill label="429" count={err429} tone="warn" />
                            <ErrorPill label="oth" count={errOth} tone="default" />
                          </div>
                        ) : <span className="text-[10px] text-[var(--text-faint)]">--</span>}
                      </td>
                      <td>
                        <div className={`truncate ${reason ? 'text-[var(--text-secondary)]' : 'text-[var(--text-faint)]'}`} title={reason}>{reason || '--'}</div>
                        {secondaryParts.length > 0 && <div className="text-[10px] text-[var(--text-faint)] truncate">{secondaryParts.join(' · ')}</div>}
                      </td>
                      <td>
                        <div className="flex justify-end gap-3">
                          {item.displayStatus === 'active' ? (
                            <button className="font-medium text-[var(--warning)] hover:text-amber-400 transition-colors" onClick={() => onDisableKey(item.key)}>禁用</button>
                          ) : (
                            <button className="font-medium text-[var(--success)] hover:text-emerald-400 transition-colors" onClick={() => onEnableKey(item.key)}>启用</button>
                          )}
                          <button className="font-medium text-[var(--danger)] hover:text-red-400 transition-colors" onClick={() => onDeleteKey(item.key)}>删除</button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
                {!pageItems.length && (
                  <tr>
                    <td colSpan={8} className="px-3 py-12 text-center text-[var(--text-faint)]">暂无匹配密钥</td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          {/* ═══ Pagination ═══ */}
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
                <button className="pagination-btn" disabled={currentPage <= 1} onClick={() => setPage((prev) => Math.max(1, prev - 1))}>上一页</button>
                <button className="pagination-btn" disabled={currentPage >= totalPages} onClick={() => setPage((prev) => Math.min(totalPages, prev + 1))}>下一页</button>
              </div>
            </div>
          </div>
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
