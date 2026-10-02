import { useEffect, useMemo, useState } from 'react'

import { buttonClass, panelClass } from '../app/utils'
import { AddKeysModal } from './keyhub/AddKeysModal'
import { BatchBar } from './keyhub/BatchBar'
import { KeyCardList } from './keyhub/KeyCardList'
import { KeyDetailDrawer } from './keyhub/KeyDetailDrawer'
import { KeyTable } from './keyhub/KeyTable'
import { KeyToolbar } from './keyhub/KeyToolbar'
import { RemarkModal } from './keyhub/RemarkModal'
import { SummaryStrip } from './keyhub/SummaryStrip'
import { VendorChipBar } from './keyhub/VendorChipBar'
import { buildKeyMetrics, compareItems, compareValues, countByFilter, matchesFilter, resolveDisplayState } from './keyhub/keyMetrics'
import { Icon } from './keyhub/shared'

/* ══════════════════════════════════════════════════════════════
   Key hub — composition layer.

   Data flow: upstream key config (upstreamKeysData) merged with live runtime
   stats (runtimeStats) into one row model, then filtered / sorted / paginated.
   Live numbers never overwrite stored state that the backend owns; the UI only
   prefers the runtime value when the runtime actually reports one.
   ══════════════════════════════════════════════════════════════ */

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
  onRefreshStats,
  onResetStats
}) {
  const [query, setQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [sortState, setSortState] = useState({ key: 'index', direction: 'asc' })
  const [selectedKeys, setSelectedKeys] = useState(new Set())
  const [showAddModal, setShowAddModal] = useState(false)
  const [remarkEditor, setRemarkEditor] = useState(null)
  const [detailKey, setDetailKey] = useState(null)

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
      const merged = { ...item, rt, originalIndex: index, ...resolveDisplayState(item, rt) }
      return { ...merged, metrics: buildKeyMetrics(merged) }
    })
  }, [allItems, runtimeMap])

  const counts = useMemo(() => countByFilter(mergedItems), [mergedItems])

  const selectedBackoffKeys = useMemo(
    () => mergedItems.filter((item) => selectedKeys.has(item.key) && item.metrics.backoff > 0).map((item) => item.key),
    [mergedItems, selectedKeys]
  )

  /* ── Filter ── */
  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase()
    return mergedItems.filter((item) => {
      if (!matchesFilter(item, statusFilter)) return false
      if (!q) return true
      const haystack = [
        item.key,
        item.masked,
        item.remark,
        item.displayStatus,
        item.displayDisableReason,
        item.displayDisabledBy,
        item.rt.last_error
      ]
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
    setShowAddModal(false)
    setRemarkEditor(null)
    setDetailKey(null)
  }, [selectedKeyVendorID])

  useEffect(() => {
    setPage((prev) => Math.min(prev, totalPages))
  }, [totalPages])

  /* Keep the drawer honest: once the key is gone (deleted elsewhere), close it. */
  useEffect(() => {
    if (detailKey && !mergedItems.some((item) => item.key === detailKey)) setDetailKey(null)
  }, [detailKey, mergedItems])

  /* ── Selection ── */
  const handleToggleSelectAll = (e) => {
    setSelectedKeys(e.target.checked ? new Set(pageItems.map((item) => item.key)) : new Set())
  }

  const handleToggleSelect = (key) => {
    setSelectedKeys((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  useEffect(() => {
    setSelectedKeys(new Set())
  }, [query, statusFilter, page, pageSize, selectedKeyVendorID])

  /* ── Unified vendor chips ── */
  const vendorTabs = useMemo(() => {
    const upstreamVendors = upstreamKeysData.vendors || []
    const upstreamItems = upstreamKeysData.items || {}
    const runtimeVendors = runtimeStats?.vendors || {}
    const runtimeLookup = new Map(vendorRows.map((row) => [row.id, row]))
    const seen = new Set()
    const tabs = []

    const buildCounts = (vendorID, fallbackActive = 0, fallbackDisabled = 0) => {
      const vendorItems = upstreamItems[vendorID] || []
      if (!vendorItems.length) return { activeCount: fallbackActive, disabledCount: fallbackDisabled }
      const vendorRuntimeMap = new Map()
      for (const item of runtimeVendors[vendorID] || []) {
        if (item.key_id) vendorRuntimeMap.set(item.key_id, item)
        else if (item.key_masked) vendorRuntimeMap.set(item.key_masked, item)
      }
      let activeCount = 0
      let disabledCount = 0
      for (const item of vendorItems) {
        const rt = vendorRuntimeMap.get(item.key_id) || vendorRuntimeMap.get(item.masked) || {}
        if (resolveDisplayState(item, rt).displayStatus === 'active') activeCount += 1
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
        tabs.push({
          vendorID: row.id,
          vendorName: row.name,
          configured: true,
          activeCount: counts.activeCount,
          disabledCount: counts.disabledCount,
          backoff: row.backoff || 0,
          inflight: row.inflight || 0
        })
      }
    }
    return tabs
  }, [upstreamKeysData.vendors, upstreamKeysData.items, vendorRows, runtimeStats?.vendors])

  const existingKeys = useMemo(() => new Set(allItems.map((item) => item.key)), [allItems])
  const detailItem = detailKey ? mergedItems.find((item) => item.key === detailKey) || null : null

  const resetStats = (all = false) => {
    const target = all ? '全部供应商' : `供应商「${selectedKeyVendorName || selectedKeyVendorID}」`
    if (!window.confirm(`确定清空${target}的统计？\n\n累计计数和性能统计将从零开始，密钥、备注、禁用/冷却状态不变，不中断请求。此操作不可撤销。\n多实例共用存储时，请先停止其他实例，重置后再启动。`)) return
    onResetStats(all ? 'all' : 'vendor', all ? '' : selectedKeyVendorID, 'RESET')
  }

  /* ── Row action bindings shared by table / cards / drawer ── */
  const actionHandlers = {
    onTest: (item) => onTestKey?.(selectedKeyVendorID, item.key),
    onRecover: (item) => onRecoverKey(item.key),
    onToggleStatus: (item) => (item.displayStatus === 'active' ? onDisableKey(item.key) : onEnableKey(item.key)),
    onDelete: (item) => onDeleteKey(item.key),
    onDetail: (item) => setDetailKey(item.key)
  }

  return (
    <section className={`${panelClass('p-5')} key-hub-panel animate-fade-in`}>
      {/* ═══ Header ═══ */}
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-4">
        <div>
          <h3 className="section-title">密钥中心</h3>
          <p className="mt-1 text-xs text-[var(--text-muted)]">管理各供应商上游 API 密钥，监控运行状态与异常情况。</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <button
            className={buttonClass('ghost')}
            disabled={busy || !selectedKeyVendorID}
            onClick={() => resetStats()}
            title="清空当前供应商所有密钥的统计"
          >
            重置统计
          </button>
          <button
            className={buttonClass('ghost')}
            disabled={busy}
            onClick={() => resetStats(true)}
            title="清空全部供应商的统计"
          >
            全部重置
          </button>
          <button
            className={buttonClass('ghost')}
            disabled={busy}
            onClick={() => onExportBackup?.()}
            title="将全部供应商的上游密钥导出为 JSON 备份文件"
          >
            <Icon name="download" />
            全体导出
          </button>
        </div>
      </div>

      {/* ═══ Vendor chips ═══ */}
      <div className="mb-4">
        <VendorChipBar tabs={vendorTabs} selectedID={selectedKeyVendorID} onSelect={onSelectVendor} />
      </div>

      {selectedKeyVendorID ? (
        <div className="space-y-4">
          {/* ═══ KPI strip ═══ */}
          <SummaryStrip items={mergedItems} filter={statusFilter} onFilterChange={(id) => { setStatusFilter(id); setPage(1) }} />

          {/* ═══ Toolbar ═══ */}
          <KeyToolbar
            query={query}
            onQueryChange={(value) => { setQuery(value); setPage(1) }}
            statusFilter={statusFilter}
            counts={counts}
            onStatusFilterChange={(id) => { setStatusFilter(id); setPage(1) }}
            filteredCount={filteredItems.length}
            totalCount={allItems.length}
            busy={busy}
            autoRefreshStats={autoRefreshStats}
            refreshEverySec={refreshEverySec}
            onToggleAutoRefresh={onToggleAutoRefresh}
            onRefreshEverySecChange={onRefreshEverySecChange}
            onRefreshStats={onRefreshStats}
            showSecrets={showSecrets}
            onToggleSecrets={onToggleSecrets}
            onAdd={() => setShowAddModal(true)}
          />

          {/* ═══ Key list ═══ */}
          <KeyTable
            pageItems={pageItems}
            showSecrets={showSecrets}
            busy={busy}
            sortState={sortState}
            onSortChange={(next) => { setSortState(next); setPage(1) }}
            selectedKeys={selectedKeys}
            onToggleSelect={handleToggleSelect}
            onToggleSelectAll={handleToggleSelectAll}
            onEditRemark={(item) => setRemarkEditor({ key: item.key, value: item.remark || '' })}
            actionHandlers={actionHandlers}
          />
          <KeyCardList
            pageItems={pageItems}
            showSecrets={showSecrets}
            busy={busy}
            sortState={sortState}
            onSortChange={(next) => { setSortState(next); setPage(1) }}
            selectedKeys={selectedKeys}
            onToggleSelect={handleToggleSelect}
            onToggleSelectAll={handleToggleSelectAll}
            onEditRemark={(item) => setRemarkEditor({ key: item.key, value: item.remark || '' })}
            actionHandlers={actionHandlers}
          />

          {/* ═══ Pagination ═══ */}
          <div className="flex flex-col gap-4 border-t border-[var(--border)] pt-4 text-xs text-[var(--text-muted)] sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-2">
              <span>行/页</span>
              <select className="select-base !h-7 !py-0 !text-xs w-20" value={String(pageSize)} onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1) }}>
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
      ) : (
        <p className="py-10 text-center text-sm text-[var(--text-faint)]">请选择上方供应商以管理其上游密钥。</p>
      )}

      {/* ═══ Floating batch bar ═══ */}
      <BatchBar
        selectedCount={selectedKeys.size}
        recoverableCount={selectedBackoffKeys.length}
        busy={busy}
        onEnable={() => { onEnableKeys(Array.from(selectedKeys)); setSelectedKeys(new Set()) }}
        onRecover={() => { onRecoverKeys(selectedBackoffKeys); setSelectedKeys(new Set()) }}
        onDisable={() => { onDisableKeys(Array.from(selectedKeys)); setSelectedKeys(new Set()) }}
        onDelete={() => { onDeleteKeys(Array.from(selectedKeys)); setSelectedKeys(new Set()) }}
        onClear={() => setSelectedKeys(new Set())}
      />

      {/* ═══ Detail drawer ═══ */}
      <KeyDetailDrawer
        item={detailItem}
        showSecrets={showSecrets}
        busy={busy}
        onClose={() => setDetailKey(null)}
        onSetRemark={onSetRemark}
        {...actionHandlers}
      />

      {/* ═══ Modals ═══ */}
      {showAddModal && (
        <AddKeysModal
          vendorName={selectedKeyVendorName}
          busy={busy}
          existingKeys={existingKeys}
          vendorRows={vendorRows}
          upstreamItems={upstreamKeysData.items}
          onAddKeys={onAddKeys}
          onImportBackup={onImportBackup}
          onClose={() => setShowAddModal(false)}
        />
      )}
      {remarkEditor && (
        <RemarkModal
          editor={remarkEditor}
          busy={busy}
          onSave={onSetRemark}
          onClose={() => setRemarkEditor(null)}
        />
      )}
    </section>
  )
}
