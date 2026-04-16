import { useEffect, useMemo, useState } from 'react'

import { buttonClass, maskSecret, normalizeKeys, panelClass, parseKeysText } from '../app/utils'

export function KeyTableEditor({
  title,
  keys,
  onChange,
  showSecrets,
  minKeys = 0,
  scopeKey,
  toneClass = 'text-slate-700'
}) {
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [selected, setSelected] = useState({})
  const [inputText, setInputText] = useState('')
  const [showImportModal, setShowImportModal] = useState(false)
  const [importHint, setImportHint] = useState('')
  const [tableHint, setTableHint] = useState('')

  const keysList = useMemo(() => normalizeKeys(keys || []), [keys])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return keysList
    return keysList.filter((key) => key.toLowerCase().includes(q))
  }, [keysList, query])

  const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize))
  const currentPage = Math.min(page, totalPages)
  const start = (currentPage - 1) * pageSize
  const pageItems = filtered.slice(start, start + pageSize)
  const selectedCount = Object.values(selected).filter(Boolean).length

  useEffect(() => {
    setQuery('')
    setPage(1)
    setSelected({})
    setInputText('')
    setTableHint('')
  }, [scopeKey])

  useEffect(() => {
    setSelected((prev) => {
      const next = {}
      for (const key of Object.keys(prev)) {
        if (prev[key] && keysList.includes(key)) next[key] = true
      }
      return next
    })
    setPage((prev) => Math.min(prev, totalPages))
  }, [keysList, totalPages])

  const handleImport = () => {
    const incoming = parseKeysText(inputText)
    if (!incoming.length) {
      setImportHint('未检测到任何有效密钥')
      return
    }
    const existed = new Set(keysList)
    const append = incoming.filter((key) => !existed.has(key))
    if (!append.length) {
      setImportHint('输入密钥已全量存在')
      return
    }
    onChange([...keysList, ...append])
    setInputText('')
    setImportHint('')
    setShowImportModal(false)
  }

  const cleanupKeys = () => {
    const cleaned = normalizeKeys(keys || [])
    if (cleaned.length === (keys || []).length) {
      setTableHint('无需清理')
      return
    }
    onChange(cleaned)
    setTableHint(`已清理 ${(keys || []).length - cleaned.length} 条重复或空值`)
  }

  const deleteSelected = () => {
    const targets = Object.keys(selected).filter((key) => selected[key])
    if (!targets.length) {
      setTableHint('请先勾选要删除的密钥')
      return
    }
    const next = keysList.filter((key) => !selected[key])
    if (next.length < minKeys) {
      setTableHint(`至少保留 ${minKeys} 条密钥`)
      return
    }
    onChange(next)
    setSelected({})
    setTableHint(`已删除 ${targets.length} 条`)
  }

  const removeOne = (key) => {
    const next = keysList.filter((item) => item !== key)
    if (next.length < minKeys) {
      setTableHint(`至少保留 ${minKeys} 条密钥`)
      return
    }
    onChange(next)
    setSelected((prev) => {
      const copy = { ...prev }
      delete copy[key]
      return copy
    })
  }

  const allPageSelected = pageItems.length > 0 && pageItems.every((key) => !!selected[key])

  const togglePageSelect = () => {
    setSelected((prev) => {
      const next = { ...prev }
      if (allPageSelected) {
        for (const key of pageItems) delete next[key]
      } else {
        for (const key of pageItems) next[key] = true
      }
      return next
    })
  }

  const selectAllFiltered = () => {
    const next = {}
    for (const key of filtered) next[key] = true
    setSelected(next)
  }

  return (
    <div className="space-y-4">
      {/* Control Bar */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded-md border border-slate-200 bg-slate-50 p-2">
        <div className="flex flex-wrap items-center gap-3 flex-1">
          <input
            className="input-base text-xs h-8 w-full sm:w-56"
            placeholder="全文检索..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <div className="text-xs text-slate-500 flex flex-wrap gap-4 mt-1 sm:mt-0">
            <span>总量: <strong className="text-slate-700">{keysList.length}</strong></span>
            <span>筛选: <strong className="text-slate-700">{filtered.length}</strong></span>
            <span>选取: <strong className="text-slate-700">{selectedCount}</strong></span>
          </div>
        </div>

        <div className="flex items-center gap-2 self-start sm:self-auto flex-shrink-0">
          <button className="rounded border border-blue-200 bg-blue-50 px-2 py-1 align-middle text-xs text-blue-700 hover:bg-blue-100" onClick={() => { setInputText(''); setImportHint(''); setShowImportModal(true); }}>新增导入</button>
          <button className="rounded border border-slate-300 bg-white px-2 py-1 align-middle text-xs text-slate-700 hover:bg-slate-100" onClick={cleanupKeys}>去重</button>
          <button className="rounded border border-red-200 bg-red-50 px-2 py-1 align-middle text-xs text-red-600 hover:bg-red-100 disabled:opacity-50" disabled={selectedCount === 0} onClick={deleteSelected}>删除选取</button>
        </div>
      </div>

      {tableHint && (
        <div className="text-xs text-blue-600 px-1 py-0.5">
          {tableHint}
        </div>
      )}

      <div className="table-shell text-xs">
        <table className="w-full min-w-[680px]">
          <thead>
            <tr>
              <th className="w-12 px-3 py-2 text-left">
                <input type="checkbox" checked={allPageSelected} onChange={togglePageSelect} />
              </th>
              <th className="w-20 px-3 py-2 text-left text-slate-500">序号</th>
              <th className="px-3 py-2 text-left">密钥内容</th>
              <th className="w-24 px-3 py-2 text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            {pageItems.map((key, idx) => (
              <tr key={key} className="hover:bg-slate-50/50">
                <td className="px-3 py-2">
                  <input
                    type="checkbox"
                    checked={!!selected[key]}
                    onChange={(e) => setSelected((prev) => ({ ...prev, [key]: e.target.checked }))}
                  />
                </td>
                <td className="px-3 py-2 text-slate-400 font-mono">{start + idx + 1}</td>
                <td className={`px-3 py-2 font-mono ${toneClass}`}>{showSecrets ? key : maskSecret(key)}</td>
                <td className="px-3 py-2 text-right">
                  <button className="text-rose-600 hover:text-rose-700 font-medium" onClick={() => removeOne(key)}>
                    移除
                  </button>
                </td>
              </tr>
            ))}
            {!pageItems.length && (
              <tr>
                <td colSpan={4} className="px-3 py-12 text-center text-slate-400">
                  暂无匹配数据
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 text-xs text-slate-500 border-t border-slate-100 pt-4">
        <div className="flex items-center gap-2 order-2 sm:order-1 whitespace-nowrap">
          <span className="shrink-0">行数/页</span>
          <select className="select-base text-xs h-7 py-0 px-2 w-20 shrink-0" value={String(pageSize)} onChange={(e) => setPageSize(Number(e.target.value))}>
            <option value="20">20</option>
            <option value="50">50</option>
            <option value="100">100</option>
            <option value="500">500</option>
          </select>
        </div>
        <div className="flex flex-wrap items-center gap-2 relative z-0 order-1 sm:order-2 w-full sm:w-auto justify-between sm:justify-end">
          <span className="mr-1 sm:mr-3 whitespace-nowrap">第 {currentPage} 页 / {totalPages} 页</span>
          <div className="flex items-center gap-2">
            <button className="rounded border border-slate-200 px-3 py-1 hover:bg-slate-50 disabled:opacity-50" disabled={currentPage <= 1} onClick={() => setPage((prev) => Math.max(1, prev - 1))}>上一页</button>
            <button className="rounded border border-slate-200 px-3 py-1 hover:bg-slate-50 disabled:opacity-50" disabled={currentPage >= totalPages} onClick={() => setPage((prev) => Math.min(totalPages, prev + 1))}>下一页</button>
          </div>
        </div>
      </div>

      {showImportModal && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/40 p-4 backdrop-blur-sm pb-20">
          <div className="w-full max-w-lg rounded-md border border-slate-200 bg-white shadow-2xl flex flex-col max-h-[90vh]">
            <div className="flex items-center justify-between border-b border-slate-100 px-4 py-3">
              <h3 className="font-medium text-slate-800">输入或粘贴新密钥</h3>
              <button className="text-slate-400 hover:text-slate-600" onClick={() => setShowImportModal(false)}>✕</button>
            </div>
            <div className="flex-1 overflow-auto p-4 flex flex-col gap-3">
              <textarea
                className="textarea-base w-full flex-1 min-h-[300px]"
                placeholder={"在此粘贴一行或多行密钥长文本，例如:\nsk-1234\nsk-5678"}
                value={inputText}
                autoFocus
                onChange={(e) => {
                  setInputText(e.target.value)
                  if (importHint) setImportHint('')
                }}
              />
              {importHint && (
                <div className="rounded border border-rose-200 bg-rose-50 px-3 py-2 text-xs text-rose-700">
                  {importHint}
                </div>
              )}
            </div>
            <div className="flex items-center justify-end gap-3 border-t border-slate-100 bg-slate-50 px-4 py-3 rounded-b-md">
              <button className="rounded px-4 py-2 text-sm text-slate-600 hover:bg-slate-200" onClick={() => setShowImportModal(false)}>取消</button>
              <button className={`${buttonClass('primary')} px-6`} onClick={handleImport}>导入 (共 {parseKeysText(inputText).length} 行)</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
