import { useEffect, useMemo, useRef, useState } from 'react'

import { buttonClass, panelClass, parseKeysText } from '../app/utils'

/* ── Shared helpers ─────────────────────────────────────── */

function statusTone(status) {
  switch (status) {
    case 'disabled_manual':
    case 'disabled_auto':
      return 'border-[var(--border-strong)] bg-[rgba(113,113,122,0.1)] text-[var(--text-muted)]'
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

const STATUS_SORT_WEIGHTS = {
  active: 0,
  disabled_manual: 1,
  disabled_auto: 2
}

const SORT_DEFAULTS = {
  key: 'asc',
  status: 'asc',
  load: 'desc',
  requests: 'desc',
  errors: 'desc',
  reason: 'asc'
}

function buildKeyMetrics(item) {
  const rt = item.rt || {}
  const inflight = Number(rt.inflight || 0)
  const backoff = Number(rt.backoff_remaining_seconds || 0)
  const cooldownLevel = Number(rt.cooldown_level || 0)
  const cooldownMultiplier = cooldownLevel > 1 ? 2 ** (cooldownLevel - 1) : 1
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
  const errorCount = err401 + err403 + err429 + errOth
  const successRate = totalRequests === 0 ? 0 : Math.round((successCount / totalRequests) * 100)
  const secondaryParts = []

  if (item.displayDisabledBy) secondaryParts.push(`by ${item.displayDisabledBy}`)
  if (lastStatus >= 400) secondaryParts.push(`HTTP ${lastStatus}`)
  if (failures > 0) secondaryParts.push(`连败 ${failures}`)
  if (cooldownLevel > 1) secondaryParts.push(`退避 x${cooldownMultiplier}`)

  return {
    inflight,
    backoff,
    cooldownLevel,
    cooldownMultiplier,
    totalRequests,
    successCount,
    failedCount,
    failures,
    lastStatus,
    reason,
    err401,
    err403,
    err429,
    errOth,
    errorCount,
    hasErrors: errorCount > 0,
    successRate,
    errRate: totalRequests === 0 ? 0 : 100 - successRate,
    secondaryText: secondaryParts.join(' · ')
  }
}

function compareValues(left, right) {
  if (left === right) return 0
  if (typeof left === 'number' && typeof right === 'number') return left - right
  return String(left || '').localeCompare(String(right || ''), 'zh-CN', { numeric: true, sensitivity: 'base' })
}

function compareItems(left, right, sortKey) {
  switch (sortKey) {
    case 'key':
      return compareValues(left.key, right.key)
    case 'status':
      return (
        compareValues(STATUS_SORT_WEIGHTS[left.displayStatus] ?? 99, STATUS_SORT_WEIGHTS[right.displayStatus] ?? 99) ||
        compareValues(left.metrics.lastStatus, right.metrics.lastStatus) ||
        compareValues(left.metrics.reason, right.metrics.reason)
      )
    case 'load':
      return (
        compareValues(left.metrics.inflight, right.metrics.inflight) ||
        compareValues(left.metrics.backoff, right.metrics.backoff) ||
        compareValues(left.metrics.totalRequests, right.metrics.totalRequests)
      )
    case 'requests':
      return (
        compareValues(left.metrics.totalRequests, right.metrics.totalRequests) ||
        compareValues(left.metrics.successCount, right.metrics.successCount) ||
        compareValues(left.metrics.failedCount, right.metrics.failedCount)
      )
    case 'errors':
      return (
        compareValues(left.metrics.errorCount, right.metrics.errorCount) ||
        compareValues(left.metrics.failures, right.metrics.failures) ||
        compareValues(left.metrics.lastStatus, right.metrics.lastStatus)
      )
    case 'reason':
      return compareValues(left.metrics.reason || left.metrics.secondaryText, right.metrics.reason || right.metrics.secondaryText)
    default:
      return compareValues(left.originalIndex, right.originalIndex)
  }
}

function nextSortState(current, sortKey) {
  const defaultDirection = SORT_DEFAULTS[sortKey] || 'asc'
  if (current.key !== sortKey) {
    return { key: sortKey, direction: defaultDirection }
  }
  if (current.direction === defaultDirection) {
    return { key: sortKey, direction: defaultDirection === 'asc' ? 'desc' : 'asc' }
  }
  return { key: 'index', direction: 'asc' }
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
  selectedKeyVendorID,
  selectedKeyVendorName,
  showSecrets,
  busy,
  onToggleSecrets,
  onSelectVendor,
  onAddKeys,
  onEnableKey,
  onEnableKeys,
  onRecoverKey,
  onRecoverKeys,
  onDisableKey,
  onDisableKeys,
  onDeleteKey,
  onDeleteKeys,
  onSetRemark,
  onTestKey,
  onExportBackup,
  onImportBackup,
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
  const [sortState, setSortState] = useState({ key: 'index', direction: 'asc' })
  const [selectedKeys, setSelectedKeys] = useState(new Set())
  const [remarkEditor, setRemarkEditor] = useState(null)
  const [showImportModal, setShowImportModal] = useState(false)
  const [importBackup, setImportBackup] = useState(null)
  const [importHint, setImportHint] = useState('')
  const [importReport, setImportReport] = useState(null)
  const fileInputRef = useRef(null)

  const allItems = upstreamKeysData.items?.[selectedKeyVendorID] || []
  const runtimeKeys = runtimeStats?.vendors?.[selectedKeyVendorID] || []

  /* ── Build runtime lookup by stable key id ── */
  const runtimeMap = useMemo(() => {
    const map = new Map()
    for (const item of runtimeKeys) {
      if (item.key_id) map.set(item.key_id, item)
      else if (item.key_masked) map.set(item.key_masked, item)
    }
    return map
  }, [runtimeKeys])

  /* ── Merge upstream + runtime into unified rows ── */
  const mergedItems = useMemo(() => {
    return allItems.map((item, index) => {
      const rt = runtimeMap.get(item.key_id) || runtimeMap.get(item.masked) || {}
      const displayState = resolveDisplayState(item, rt)
      const mergedItem = {
        ...item,
        rt,
        originalIndex: index,
        ...displayState
      }
      return { ...mergedItem, metrics: buildKeyMetrics(mergedItem) }
    })
  }, [allItems, runtimeMap])

  const selectedBackoffKeys = useMemo(() => {
    return mergedItems
      .filter((item) => selectedKeys.has(item.key) && item.metrics.backoff > 0)
      .map((item) => item.key)
  }, [mergedItems, selectedKeys])

  /* ── Filter ── */
  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase()
    return mergedItems.filter((item) => {
      const rt = item.rt
      const { backoff, inflight, failures, err401, err403, err429, errOth } = item.metrics
      if (statusFilter === 'active' && item.displayStatus !== 'active') return false
      if (statusFilter === 'disabled' && item.displayStatus === 'active') return false
      if (statusFilter === 'backoff' && !(backoff > 0)) return false
      if (statusFilter === 'issues' && !(
        item.displayStatus !== 'active' ||
        backoff > 0 ||
        failures > 0 ||
        err401 > 0 ||
        err403 > 0 ||
        err429 > 0 ||
        errOth > 0 ||
        item.displayDisableReason
      )) return false
      if (statusFilter === 'inflight' && !(inflight > 0)) return false
      if (!q) return true
      const haystack = [item.key, item.masked, item.remark, item.displayStatus, item.displayDisableReason, item.displayDisabledBy, rt.last_error]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return haystack.includes(q)
    })
  }, [mergedItems, query, statusFilter])

  const sortedItems = useMemo(() => {
    if (sortState.key === 'index') return filteredItems
    const direction = sortState.direction === 'asc' ? 1 : -1
    return [...filteredItems].sort((left, right) => {
      const result = compareItems(left, right, sortState.key)
      if (result !== 0) return result * direction
      return compareValues(left.originalIndex, right.originalIndex)
    })
  }, [filteredItems, sortState])

  /* ── Pagination ── */
  const totalPages = Math.max(1, Math.ceil(sortedItems.length / pageSize))
  const currentPage = Math.min(page, totalPages)
  const start = (currentPage - 1) * pageSize
  const pageItems = sortedItems.slice(start, start + pageSize)

  /* ── Reset on vendor change ── */
  useEffect(() => {
    setQuery('')
    setStatusFilter('all')
    setPage(1)
    setDraftText('')
    setShowAddModal(false)
    setModalHint('')
    setRemarkEditor(null)
    setShowImportModal(false)
    setImportBackup(null)
    setImportHint('')
    setImportReport(null)
  }, [selectedKeyVendorID])

  useEffect(() => {
    setPage((prev) => Math.min(prev, totalPages))
  }, [totalPages])

  /* ── Selection Logic ── */
  const handleToggleSelectAll = (e) => {
    if (e.target.checked) {
      setSelectedKeys(new Set(pageItems.map(item => item.key)))
    } else {
      setSelectedKeys(new Set())
    }
  }

  const handleToggleSelect = (key) => {
    const next = new Set(selectedKeys)
    if (next.has(key)) {
      next.delete(key)
    } else {
      next.add(key)
    }
    setSelectedKeys(next)
  }

  useEffect(() => {
    setSelectedKeys(new Set())
  }, [query, statusFilter, page, pageSize, selectedKeyVendorID])

  /* ── Build unified vendor tabs ── */
  const vendorTabs = useMemo(() => {
    const upstreamVendors = upstreamKeysData.vendors || []
    const upstreamItems = upstreamKeysData.items || {}
    const runtimeVendors = runtimeStats?.vendors || {}
    const runtimeLookup = new Map(vendorRows.map((r) => [r.id, r]))
    const seen = new Set()
    const tabs = []

    const buildCounts = (vendorID, fallbackActive = 0, fallbackDisabled = 0) => {
      const vendorItems = upstreamItems[vendorID] || []
      if (!vendorItems.length) {
        return { activeCount: fallbackActive, disabledCount: fallbackDisabled }
      }
      const vendorRuntimeMap = new Map()
      for (const item of runtimeVendors[vendorID] || []) {
        if (item.key_id) vendorRuntimeMap.set(item.key_id, item)
        else if (item.key_masked) vendorRuntimeMap.set(item.key_masked, item)
      }
      let activeCount = 0
      let disabledCount = 0
      for (const item of vendorItems) {
        const rt = vendorRuntimeMap.get(item.key_id) || vendorRuntimeMap.get(item.masked) || {}
        const { displayStatus } = resolveDisplayState(item, rt)
        if (displayStatus === 'active') activeCount += 1
        else disabledCount += 1
      }
      return { activeCount, disabledCount }
    }

    for (const item of upstreamVendors) {
      const row = runtimeLookup.get(item.vendor_id)
      if (row?.provider === 'aggregate') continue
      seen.add(item.vendor_id)
      const counts = buildCounts(item.vendor_id, item.active_count || 0, item.disabled_count || 0)
      tabs.push({
        vendorID: item.vendor_id,
        // An unconfigured partition has no config entry to take a name from.
        vendorName: row?.name || item.vendor || item.vendor_id,
        configured: item.configured !== false,
        activeCount: counts.activeCount,
        disabledCount: counts.disabledCount,
        backoff: row?.backoff || 0,
        inflight: row?.inflight || 0
      })
    }
    for (const row of vendorRows) {
      if (row.provider === 'aggregate') continue
      if (!seen.has(row.id)) {
        const counts = buildCounts(row.id, 0, 0)
        tabs.push({ vendorID: row.id, vendorName: row.name, configured: true, activeCount: counts.activeCount, disabledCount: counts.disabledCount, backoff: row.backoff || 0, inflight: row.inflight || 0 })
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

  /* ── Backup import preview ──
   * Analyse a parsed backup against the live vendor set (from vendorRows, which
   * is the union of configured vendors and any orphan key partitions). Vendors
   * missing from the live set are flagged and their keys are excluded from the
   * pending import set. This preview is recomputed whenever either the backup
   * or the vendor list changes, so the operator sees the consequences before
   * committing. */
  const importAnalysis = useMemo(() => {
    if (!importBackup) return null
    const liveVendorIDs = new Set(vendorRows.map((row) => row.id).filter(Boolean))
    const liveItems = upstreamKeysData.items || {}
    const backupVendors = Array.isArray(importBackup.vendors) ? importBackup.vendors : []
    const valid = []
    const skipped = []
    let totalKeys = 0
    let validKeys = 0
    for (const block of backupVendors) {
      const vendorID = String(block?.vendor_id || '').trim()
      const keys = Array.isArray(block?.keys) ? block.keys : []
      totalKeys += keys.length
      if (!vendorID) {
        skipped.push({ vendor_id: '(空)', vendor_name: block?.vendor_name || '', count: keys.length, reason: '备份中缺少供应商 ID' })
        continue
      }
      if (!liveVendorIDs.has(vendorID)) {
        skipped.push({ vendor_id: vendorID, vendor_name: block?.vendor_name || vendorID, count: keys.length, reason: '供应商不存在，将跳过该供应商下全部 key' })
        continue
      }
      const existing = new Set((liveItems[vendorID] || []).map((item) => item.key))
      let newCount = 0
      let dupCount = 0
      for (const entry of keys) {
        const key = String(entry?.key || '').trim()
        if (!key) continue
        if (existing.has(key)) dupCount += 1
        else newCount += 1
      }
      validKeys += keys.length
      valid.push({
        vendor_id: vendorID,
        vendor_name: block?.vendor_name || vendorID,
        count: keys.length,
        new_count: newCount,
        dup_count: dupCount
      })
    }
    return { valid, skipped, totalKeys, validKeys, schema: importBackup.schema, version: importBackup.version, exportedAt: importBackup.exported_at }
  }, [importBackup, vendorRows, upstreamKeysData.items])

  const handleBackupFile = (file) => {
    setImportHint('')
    setImportReport(null)
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      try {
        const parsed = JSON.parse(String(reader.result || ''))
        if (!Array.isArray(parsed?.vendors) || !parsed.vendors.length) {
          setImportHint('备份文件无效：未找到 vendors 字段或为空。')
          setImportBackup(null)
          return
        }
        setImportBackup(parsed)
      } catch (err) {
        setImportHint(`解析备份失败：${String(err?.message || err)}`)
        setImportBackup(null)
      }
    }
    reader.onerror = () => {
      setImportHint('读取文件失败，请重试。')
      setImportBackup(null)
    }
    reader.readAsText(file)
  }

  const submitImportBackup = async () => {
    if (!importBackup) {
      setImportHint('请先选择备份文件。')
      return
    }
    if (!importAnalysis?.valid.length) {
      setImportHint('备份中不存在可导入的供应商，请先在系统中创建对应供应商。')
      return
    }
    const skippedNames = importAnalysis.skipped.map((item) => item.vendor_name || item.vendor_id).join('、')
    const msg = importAnalysis.skipped.length
      ? `将导入 ${importAnalysis.valid.length} 个供应商，跳过 ${importAnalysis.skipped.length} 个不存在的供应商${skippedNames ? `（${skippedNames}）` : ''}。确认继续？`
      : `将导入 ${importAnalysis.valid.length} 个供应商、共 ${importAnalysis.validKeys} 条密钥。确认继续？`
    if (!window.confirm(msg)) return
    const report = await onImportBackup?.(importBackup)
    setImportReport(report || null)
    if (report?.ok) {
      setImportBackup(null)
      if (fileInputRef.current) fileInputRef.current.value = ''
    }
  }

  const renderSortableHeader = (label, sortKey, alignClass = 'justify-start') => {
    const active = sortState.key === sortKey
    const indicator = active ? (sortState.direction === 'asc' ? '↑' : '↓') : '↕'
    return (
      <button
        className={`table-sort-button ${alignClass} ${active ? 'table-sort-button-active' : ''}`}
        type="button"
        onClick={() => {
          setPage(1)
          setSortState((current) => nextSortState(current, sortKey))
        }}
        title={active ? '再次点击切换方向，再点一次恢复默认顺序' : `按${label}排序`}
      >
        <span>{label}</span>
        <span className="table-sort-indicator" aria-hidden="true">{indicator}</span>
      </button>
    )
  }

  return (
    <section className={`${panelClass('p-5')} animate-fade-in`}>
      {/* ═══ Header ═══ */}
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-4">
        <div>
          <h3 className="section-title">密钥中心</h3>
          <p className="mt-1 text-xs text-[var(--text-muted)]">管理各供应商上游 API 密钥，监控运行状态与异常情况。</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            className={buttonClass('ghost')}
            disabled={busy}
            onClick={() => onExportBackup?.()}
            title="将全部供应商的上游密钥导出为 JSON 备份文件"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
              <polyline points="7 10 12 15 17 10" />
              <line x1="12" y1="15" x2="12" y2="3" />
            </svg>
            全体导出
          </button>
          <button
            className={buttonClass('ghost')}
            disabled={busy}
            onClick={() => { setShowImportModal(true); setImportHint(''); setImportReport(null); setImportBackup(null); if (fileInputRef.current) fileInputRef.current.value = '' }}
            title="从备份文件导入密钥，不存在的供应商会跳过"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
              <polyline points="17 8 12 3 7 8" />
              <line x1="12" y1="3" x2="12" y2="15" />
            </svg>
            导入备份
          </button>
        </div>
      </div>

      {/* ═══ Vendor Tabs ═══ */}
      <div className="mb-5 flex flex-wrap gap-2">
        {vendorTabs.map((item) => (
          <button
            key={item.vendorID}
            className={`tab-link ${selectedKeyVendorID === item.vendorID ? 'tab-link-active' : ''}`}
            onClick={() => onSelectVendor(item.vendorID)}
          >
            <span>{item.vendorName}{item.configured ? '' : '（未配置）'}</span>
            <small className="inline-flex items-center gap-1.5 font-mono tabular-nums whitespace-nowrap">
              <span className="text-[var(--success)]" title="启用">◉</span><span>{item.activeCount}</span>
              <span className="text-[var(--text-faint)]">·</span>
              <span className="text-[var(--text-faint)]" title="禁用">◎</span><span>{item.disabledCount}</span>
              <span className="text-[var(--text-faint)]">·</span>
              <span className={item.backoff > 0 ? 'text-[var(--warning)]' : 'text-[var(--text-faint)]'} title="退避">⏱</span><span>{item.backoff}</span>
              <span className="text-[var(--text-faint)]">·</span>
              <span className={item.inflight > 0 ? 'text-[var(--accent)]' : 'text-[var(--text-faint)]'} title="并发">↑</span><span>{item.inflight}</span>
            </small>
          </button>
        ))}
        {!vendorTabs.length && <p className="text-sm text-[var(--text-faint)]">暂无供应商，请先创建供应商。</p>}
      </div>

      {selectedKeyVendorID && (
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
              <div className="flex items-center gap-2 text-xs text-[var(--text-muted)] whitespace-nowrap">
                <span>
                  <strong className="font-mono text-[var(--text-secondary)]">{filteredItems.length}</strong> / <strong className="font-mono text-[var(--text-secondary)]">{allItems.length}</strong> 条
                </span>
                <span className="text-[10px] text-[var(--text-faint)]">点击列头排序</span>
              </div>
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
                <button className={buttonClass('primary')} disabled={busy || !selectedKeyVendorID} onClick={() => setShowAddModal(true)}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
                  </svg>
                  新增
                </button>
              </div>
            </div>
          </div>

          {selectedKeys.size > 0 && (
            <div className="mb-4 flex items-center justify-between rounded bg-[var(--bg-elevated)] px-4 py-2 border border-[var(--accent)] border-opacity-30">
              <span className="text-sm font-medium">已选择 {selectedKeys.size} 项</span>
              <div className="flex items-center gap-2">
                <button
                  className={buttonClass('ghost') + ' text-[var(--success)] hover:bg-[rgba(16,185,129,0.1)]'}
                  onClick={() => {
                    onEnableKeys(Array.from(selectedKeys))
                    setSelectedKeys(new Set())
                  }}
                >
                  批量启用
                </button>
                <button
                  className={buttonClass('ghost') + ' text-[var(--accent)] hover:bg-[rgba(99,102,241,0.1)]'}
                  disabled={busy || selectedBackoffKeys.length === 0}
                  onClick={() => {
                    onRecoverKeys(selectedBackoffKeys)
                    setSelectedKeys(new Set())
                  }}
                >
                  批量恢复
                </button>
                <button
                  className={buttonClass('ghost') + ' text-[var(--warning)] hover:bg-[rgba(245,158,11,0.1)]'}
                  onClick={() => {
                    onDisableKeys(Array.from(selectedKeys))
                    setSelectedKeys(new Set())
                  }}
                >
                  批量禁用
                </button>
                <button
                  className={buttonClass('ghost') + ' text-[var(--danger)] hover:bg-[rgba(239,68,68,0.1)]'}
                  onClick={() => {
                    onDeleteKeys(Array.from(selectedKeys))
                    setSelectedKeys(new Set())
                  }}
                >
                  批量删除
                </button>
              </div>
            </div>
          )}

          {/* ═══ Unified Table ═══ */}
          <div className="table-shell text-xs">
            <table className="w-full min-w-[1200px] table-fixed">
              <colgroup>
                <col style={{ width: '56px' }} />
                <col style={{ width: '260px' }} />
                <col style={{ width: '180px' }} />
                <col style={{ width: '112px' }} />
                <col style={{ width: '104px' }} />
                <col style={{ width: '168px' }} />
                <col style={{ width: '156px' }} />
                <col />
                <col style={{ width: '156px' }} />
              </colgroup>
              <thead>
                <tr>
                  <th className="w-10 text-center">
                    <input
                      type="checkbox"
                      className="rounded border-[var(--border)] bg-transparent text-[var(--accent)]"
                      checked={pageItems.length > 0 && selectedKeys.size === pageItems.length}
                      onChange={handleToggleSelectAll}
                    />
                  </th>
                  <th>{renderSortableHeader('Key', 'key')}</th>
                  <th>备注</th>
                  <th>{renderSortableHeader('状态', 'status')}</th>
                  <th>{renderSortableHeader('负载', 'load')}</th>
                  <th>{renderSortableHeader('请求', 'requests')}</th>
                  <th>{renderSortableHeader('错误', 'errors')}</th>
                  <th>{renderSortableHeader('异常 / 原因', 'reason')}</th>
                  <th className="w-24 text-right">操作</th>
                </tr>
              </thead>
              <tbody>
                {pageItems.map((item) => {
                  const { inflight, backoff, totalRequests, successCount, failedCount, reason, err401, err403, err429, errOth, hasErrors, successRate, errRate, secondaryText } = item.metrics
                  const isDisabled = item.displayStatus !== 'active'
                  const canRecover = backoff > 0

                  return (
                    <tr key={item.key} className={`transition-colors ${isDisabled ? 'key-row-disabled' : ''} ${selectedKeys.has(item.key) ? 'bg-[var(--bg-hover)]' : 'hover:bg-[var(--bg-hover)]'}`}>
                      <td className="text-center">
                        <input
                          type="checkbox"
                          className="rounded border-[var(--border)] bg-transparent text-[var(--accent)]"
                          checked={selectedKeys.has(item.key)}
                          onChange={() => handleToggleSelect(item.key)}
                        />
                      </td>
                      <td><div className={`font-mono truncate ${isDisabled ? 'text-[var(--text-muted)]' : 'text-[var(--text-primary)]'}`}>{showSecrets ? item.key : item.masked}</div></td>
                      <td>
                        <button
                          type="button"
                          className={`block w-full truncate text-left ${item.remark ? 'text-[var(--text-secondary)]' : 'text-[var(--text-faint)]'}`}
                          title={item.remark || '点击添加备注'}
                          onClick={() => setRemarkEditor({ key: item.key, value: item.remark || '' })}
                        >
                          {item.remark || '添加备注'}
                        </button>
                      </td>
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
                        {secondaryText && <div className="text-[10px] text-[var(--text-faint)] truncate">{secondaryText}</div>}
                      </td>
                      <td>
                        <div className="flex justify-end gap-3">
                          <button
                            className="font-medium text-[var(--accent)] hover:text-blue-400 transition-colors"
                            onClick={() => {
                              if (navigator?.clipboard?.writeText) {
                                navigator.clipboard.writeText(item.key).catch(() => {})
                              }
                            }}
                          >
                            复制
                          </button>
                          <button
                            className="font-medium text-[var(--accent)] hover:text-blue-400 transition-colors"
                            onClick={() => onTestKey?.(selectedKeyVendorID, item.key)}
                          >
                            测试
                          </button>
                          {canRecover && (
                            <button className="font-medium text-[var(--accent)] hover:text-blue-400 transition-colors" onClick={() => onRecoverKey(item.key)}>恢复</button>
                          )}
                          {item.displayStatus === "active" ? (
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
                    <td colSpan={9} className="px-3 py-12 text-center text-[var(--text-faint)]">暂无匹配密钥</td>
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
      {remarkEditor && (
        <div className="modal-overlay animate-fade-in">
          <div className="modal-panel animate-slide-in max-w-lg">
            <div className="modal-header">
              <div><h3>编辑 Key 备注</h3><p>用于记录用途、项目、额度或其他说明。</p></div>
              <button className="modal-close" onClick={() => setRemarkEditor(null)}>✕</button>
            </div>
            <div className="modal-body">
              <textarea
                className="textarea-base min-h-[140px] w-full"
                maxLength={500}
                value={remarkEditor.value}
                autoFocus
                placeholder="例如：生产环境对话接口 / 财务组额度"
                onChange={(e) => setRemarkEditor((current) => ({ ...current, value: e.target.value }))}
              />
              <div className="text-right text-xs text-[var(--text-muted)]">{remarkEditor.value.length}/500</div>
            </div>
            <div className="modal-footer">
              <button className={buttonClass()} onClick={() => setRemarkEditor(null)}>取消</button>
              <button className={buttonClass('primary')} disabled={busy} onClick={async () => {
                const ok = await onSetRemark?.(remarkEditor.key, remarkEditor.value)
                if (ok) setRemarkEditor(null)
              }}>保存</button>
            </div>
          </div>
        </div>
      )}

      {/* ═══ Import Backup Modal ═══ */}
      {showImportModal && (
        <div className="modal-overlay animate-fade-in">
          <div className="modal-panel animate-slide-in max-w-2xl">
            <div className="modal-header">
              <div>
                <h3>导入密钥备份</h3>
                <p>从全体导出的 JSON 备份恢复密钥。不存在的供应商会被跳过，不会自动创建。</p>
              </div>
              <button className="modal-close" onClick={() => setShowImportModal(false)}>✕</button>
            </div>
            <div className="modal-body space-y-4">
              <input
                ref={fileInputRef}
                type="file"
                accept="application/json,.json"
                onChange={(e) => handleBackupFile(e.target.files?.[0])}
              />
              {importHint && (
                <div className="rounded-lg border border-[rgba(239,68,68,0.3)] bg-[rgba(239,68,68,0.05)] px-3 py-2 text-xs text-[var(--danger)]">
                  {importHint}
                </div>
              )}
              {importAnalysis && (
                <div className="space-y-3">
                  <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-3 text-xs">
                    <div className="flex flex-wrap gap-x-6 gap-y-1">
                      <span>备份总计：<strong className="font-mono text-[var(--text-secondary)]">{importAnalysis.totalKeys}</strong> 条</span>
                      <span>可导入供应商：<strong className="font-mono text-[var(--success)]">{importAnalysis.valid.length}</strong></span>
                      <span className={importAnalysis.skipped.length ? 'text-[var(--warning)]' : ''}>跳过供应商：<strong className="font-mono">{importAnalysis.skipped.length}</strong></span>
                    </div>
                  </div>

                  {importAnalysis.valid.length > 0 && (
                    <div>
                      <div className="mb-1 text-xs font-medium text-[var(--text-secondary)]">可导入的供应商</div>
                      <div className="table-shell">
                        <table className="w-full text-xs">
                          <thead>
                            <tr>
                              <th className="text-left">供应商</th>
                              <th className="text-right">备份</th>
                              <th className="text-right">新增</th>
                              <th className="text-right">已存在</th>
                            </tr>
                          </thead>
                          <tbody>
                            {importAnalysis.valid.map((row) => (
                              <tr key={row.vendor_id}>
                                <td>
                                  <div className="font-medium text-[var(--text-primary)]">{row.vendor_name}</div>
                                  <div className="font-mono text-[10px] text-[var(--text-faint)]">{row.vendor_id}</div>
                                </td>
                                <td className="text-right font-mono">{row.count}</td>
                                <td className="text-right font-mono text-[var(--success)]">{row.new_count}</td>
                                <td className="text-right font-mono text-[var(--text-muted)]">{row.dup_count}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  )}

                  {importAnalysis.skipped.length > 0 && (
                    <div>
                      <div className="mb-1 text-xs font-medium text-[var(--warning)]">将跳过的供应商（系统中不存在）</div>
                      <div className="table-shell">
                        <table className="w-full text-xs">
                          <thead>
                            <tr>
                              <th className="text-left">供应商</th>
                              <th className="text-right">跳过 key</th>
                              <th>原因</th>
                            </tr>
                          </thead>
                          <tbody>
                            {importAnalysis.skipped.map((row) => (
                              <tr key={`${row.vendor_id}-skipped`} className="text-[var(--text-muted)]">
                                <td>
                                  <div className="font-medium">{row.vendor_name}</div>
                                  <div className="font-mono text-[10px] text-[var(--text-faint)]">{row.vendor_id}</div>
                                </td>
                                <td className="text-right font-mono">{row.count}</td>
                                <td className="text-[var(--warning)]">{row.reason}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </div>
                  )}
                </div>
              )}

              {importReport && (
                <div className={`rounded-lg border px-3 py-2 text-xs ${importReport.ok ? 'border-[rgba(34,197,94,0.3)] bg-[var(--success-soft)] text-[var(--success)]' : 'border-[rgba(239,68,68,0.3)] bg-[rgba(239,68,68,0.05)] text-[var(--danger)]'}`}>
                  <div className="font-medium">
                    {importReport.ok
                      ? `导入完成：新增 ${importReport.totalAdded} 条，跳过 ${importReport.totalSkipped} 条`
                      : `导入失败：${importReport.fatalError || '未知错误'}`}
                  </div>
                  {importReport.vendors?.length > 0 && (
                    <ul className="mt-1 list-inside list-disc space-y-0.5 text-[11px] opacity-90">
                      {importReport.vendors.map((row) => (
                        <li key={row.vendor_id}>
                          {row.vendor_id}：新增 {row.added} · 备注 {row.remarks} · 禁用 {row.disabled}
                          {row.failed ? ` · 失败: ${row.failed}` : ''}
                        </li>
                      ))}
                    </ul>
                  )}
                  {importReport.skippedVendors?.length > 0 && (
                    <div className="mt-1 text-[11px] opacity-80">
                      跳过的供应商：{importReport.skippedVendors.map((item) => item.vendor_name || item.vendor_id).join('、')}
                    </div>
                  )}
                </div>
              )}
            </div>
            <div className="modal-footer">
              <button className={buttonClass()} onClick={() => setShowImportModal(false)}>关闭</button>
              <button
                className={buttonClass('primary')}
                disabled={busy || !importBackup || !importAnalysis?.valid.length}
                onClick={submitImportBackup}
              >
                确认导入
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
