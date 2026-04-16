import { useEffect, useMemo, useState } from 'react'

import { buttonClass, formatClock, panelClass, parseKeysText } from '../app/utils'

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
      setModalHint('输入的密钥都已存在。若是已禁用密钥，请直接在表格中点“启用”。')
      return
    }

    const ok = await onAddKeys(nextKeys)
    if (!ok) return

    setDraftText('')
    setModalHint('')
    setShowAddModal(false)
  }

  return (
    <section className={panelClass('p-5')}>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 pb-3">
        <div>
          <h3 className="section-title">上游 API 密钥</h3>
          <p className="mt-1 text-xs text-slate-500">新增就是新增，启用/禁用就是切换状态，删除就是永久移除。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button className={buttonClass('ghost')} onClick={onToggleSecrets}>
            {showSecrets ? '隐藏密钥' : '显示密钥'}
          </button>
          <button className={buttonClass('primary')} disabled={busy || !selectedKeyVendor} onClick={() => setShowAddModal(true)}>
            新增 / 导入
          </button>
        </div>
      </div>

      <div className="mb-4 flex flex-wrap gap-2">
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
        {!(upstreamKeysData.vendors || []).length && <p className="text-sm text-slate-400">暂无供应商，请先创建供应商。</p>}
      </div>

      {selectedKeyVendor && (
        <div className="mt-6 space-y-5 border-t border-slate-100 pt-5">
          <div className="flex flex-col gap-3 rounded-md border border-slate-200 bg-slate-50 p-3 lg:flex-row lg:items-center lg:justify-between">
            <div className="flex flex-1 flex-col gap-3 sm:flex-row sm:items-center">
              <input
                className="input-base text-sm lg:max-w-sm"
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
            <div className="text-xs text-slate-500">
              当前展示 {filteredItems.length} / {allItems.length} 条
            </div>
          </div>

          <div className="table-shell text-xs">
            <table className="w-full min-w-[780px]">
              <thead>
                <tr>
                  <th className="w-20 px-3 py-2 text-left">序号</th>
                  <th className="px-3 py-2 text-left">Key</th>
                  <th className="w-32 px-3 py-2 text-left">状态</th>
                  <th className="px-3 py-2 text-left">原因</th>
                  <th className="w-44 px-3 py-2 text-left">更新时间</th>
                  <th className="w-40 px-3 py-2 text-right">操作</th>
                </tr>
              </thead>
              <tbody>
                {pageItems.map((item, idx) => (
                  <tr key={item.key} className="hover:bg-slate-50/50">
                    <td className="px-3 py-2 font-mono text-slate-400">{start + idx + 1}</td>
                    <td className="px-3 py-2">
                      <div className="font-mono text-slate-700">{showSecrets ? item.key : item.masked}</div>
                    </td>
                    <td className="px-3 py-2">
                      <span className={`inline-flex rounded-md border px-2 py-1 ${statusTone(item.status)}`}>
                        {statusLabel(item.status)}
                      </span>
                    </td>
                    <td className="px-3 py-2 text-slate-500">
                      <div className="max-w-[26rem] truncate">{item.disable_reason || '--'}</div>
                      {item.disabled_by && <div className="mt-1 text-[11px] text-slate-400">by {item.disabled_by}</div>}
                    </td>
                    <td className="px-3 py-2 text-slate-500">{formatClock(item.updated_at || item.disabled_at)}</td>
                    <td className="px-3 py-2">
                      <div className="flex justify-end gap-3">
                        {item.status === 'active' ? (
                          <button className="font-medium text-amber-700 hover:text-amber-800" onClick={() => onDisableKey(item.key)}>
                            禁用
                          </button>
                        ) : (
                          <button className="font-medium text-emerald-700 hover:text-emerald-800" onClick={() => onEnableKey(item.key)}>
                            启用
                          </button>
                        )}
                        <button className="font-medium text-rose-600 hover:text-rose-700" onClick={() => onDeleteKey(item.key)}>
                          删除
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
                {!pageItems.length && (
                  <tr>
                    <td colSpan={6} className="px-3 py-12 text-center text-slate-400">
                      暂无匹配密钥
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>

          <div className="flex flex-col gap-4 border-t border-slate-100 pt-4 text-xs text-slate-500 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-2">
              <span>行数/页</span>
              <select className="select-base h-8 w-24 py-0 text-xs" value={String(pageSize)} onChange={(e) => setPageSize(Number(e.target.value))}>
                <option value="20">20</option>
                <option value="50">50</option>
                <option value="100">100</option>
                <option value="500">500</option>
              </select>
            </div>
            <div className="flex items-center justify-between gap-3 sm:justify-end">
              <span>第 {currentPage} 页 / {totalPages} 页</span>
              <div className="flex items-center gap-2">
                <button className="rounded border border-slate-200 px-3 py-1 hover:bg-slate-50 disabled:opacity-50" disabled={currentPage <= 1} onClick={() => setPage((prev) => Math.max(1, prev - 1))}>
                  上一页
                </button>
                <button className="rounded border border-slate-200 px-3 py-1 hover:bg-slate-50 disabled:opacity-50" disabled={currentPage >= totalPages} onClick={() => setPage((prev) => Math.min(totalPages, prev + 1))}>
                  下一页
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {showAddModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/40 p-4 pb-20 backdrop-blur-sm">
          <div className="flex max-h-[90vh] w-full max-w-lg flex-col rounded-md border border-slate-200 bg-white shadow-2xl">
            <div className="flex items-center justify-between border-b border-slate-100 px-4 py-3">
              <div>
                <h3 className="font-medium text-slate-800">新增 / 导入密钥</h3>
                <p className="mt-1 text-xs text-slate-500">一行一个。这里默认只做新增，不会覆盖现有状态。</p>
              </div>
              <button className="text-slate-400 hover:text-slate-600" onClick={() => setShowAddModal(false)}>
                ✕
              </button>
            </div>

            <div className="flex flex-1 flex-col gap-3 overflow-auto p-4">
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
              <div className="text-xs text-slate-500">本次识别到 {parseKeysText(draftText).length} 条有效密钥。</div>
              {modalHint && (
                <div className="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700">
                  {modalHint}
                </div>
              )}
            </div>

            <div className="flex items-center justify-end gap-3 rounded-b-md border-t border-slate-100 bg-slate-50 px-4 py-3">
              <button className="rounded px-4 py-2 text-sm text-slate-600 hover:bg-slate-200" onClick={() => setShowAddModal(false)}>
                取消
              </button>
              <button className={buttonClass('primary')} disabled={busy} onClick={submitAddKeys}>
                添加
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
