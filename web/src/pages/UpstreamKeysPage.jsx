import { useEffect, useMemo, useState } from 'react'

import { buttonClass, formatClock, panelClass, parseKeysText } from '../app/utils'

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

export function UpstreamKeysPage({
  upstreamKeysData,
  selectedKeyVendor,
  showSecrets,
  busy,
  onToggleSecrets,
  onSelectVendor,
  onAddKeys,
  onEnableKey,
  onDisableKey,
  onDeleteKey
}) {
  const [query, setQuery] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [draftText, setDraftText] = useState('')
  const [showAddModal, setShowAddModal] = useState(false)
  const [modalHint, setModalHint] = useState('')

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
      {/* Header */}
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-4">
        <div>
          <h3 className="section-title">上游 API 密钥</h3>
          <p className="mt-1 text-xs text-[var(--text-muted)]">管理各供应商的上游 API 密钥，支持新增、启用、禁用与删除。</p>
        </div>
        <div className="flex flex-wrap gap-2">
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

      {/* Vendor Tabs */}
      <div className="mb-5 flex flex-wrap gap-2">
        {(upstreamKeysData.vendors || []).map((item) => (
          <button
            key={item.vendor}
            className={`tab-link ${selectedKeyVendor === item.vendor ? 'tab-link-active' : ''}`}
            onClick={() => onSelectVendor(item.vendor)}
          >
            <span>{item.vendor}</span>
            <small>启用 {item.active_count || 0} / 禁用 {item.disabled_count || 0}</small>
          </button>
        ))}
        {!(upstreamKeysData.vendors || []).length && <p className="text-sm text-[var(--text-faint)]">暂无供应商，请先创建供应商。</p>}
      </div>

      {selectedKeyVendor && (
        <div className="mt-4 space-y-5 border-t border-[var(--border)] pt-5">
          {/* Search Bar */}
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
            <div className="text-xs text-[var(--text-muted)]">
              展示 <strong className="font-mono text-[var(--text-secondary)]">{filteredItems.length}</strong> / <strong className="font-mono text-[var(--text-secondary)]">{allItems.length}</strong> 条
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

      {/* Add Modal */}
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
                placeholder={'在此粘贴一行或多行密钥，例如:\nsk-1234\nsk-5678'}
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
