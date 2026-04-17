import { useEffect, useMemo, useState } from 'react'

import { buttonClass, maskSecret, normalizeKeys, parseKeysText } from '../app/utils'

export function KeyTableEditor({
  title,
  keys,
  onChange,
  showSecrets,
  minKeys = 0,
  scopeKey,
  toneClass = 'text-[var(--text-primary)]'
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

  return (
    <div className="space-y-4">
      {/* Control Bar */}
      <div className="control-bar !p-2 sm:flex-nowrap">
        <div className="flex flex-wrap items-center gap-3 flex-1">
          <input
            className="input-base text-xs h-8 w-full sm:w-56"
            placeholder="全文检索..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <div className="text-xs text-[var(--text-muted)] flex flex-wrap gap-4 mt-1 sm:mt-0">
            <span>总量: <strong className="text-[var(--text-primary)]">{keysList.length}</strong></span>
            <span>筛选: <strong className="text-[var(--text-primary)]">{filtered.length}</strong></span>
            <span>选取: <strong className="text-[var(--text-primary)]">{selectedCount}</strong></span>
          </div>
        </div>

        <div className="flex items-center gap-2 self-start sm:self-auto flex-shrink-0">
          <button className="rounded-md border border-[rgba(59,130,246,0.3)] bg-[rgba(59,130,246,0.08)] px-2 py-1 align-middle text-xs text-[#60a5fa] hover:bg-[rgba(59,130,246,0.15)] transition-colors" onClick={() => { setInputText(''); setImportHint(''); setShowImportModal(true); }}>
            新增导入
          </button>
          <button className="rounded-md border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-1 align-middle text-xs text-[var(--text-secondary)] hover:bg-[var(--bg-hover)] transition-colors" onClick={cleanupKeys}>
            去重
          </button>
          <button className="rounded-md border border-[rgba(239,68,68,0.3)] bg-[var(--danger-soft)] px-2 py-1 align-middle text-xs text-[var(--danger)] hover:bg-[rgba(239,68,68,0.2)] disabled:opacity-50 transition-colors" disabled={selectedCount === 0} onClick={deleteSelected}>
            删除选取
          </button>
        </div>
      </div>

      {tableHint && (
        <div className="text-xs text-[#60a5fa] px-1 py-0.5">
          {tableHint}
        </div>
      )}

      {/* Table */}
      <div className="table-shell text-xs">
        <table className="w-full min-w-[680px]">
          <thead>
            <tr>
              <th className="w-12 px-4">
                <input 
                  type="checkbox" 
                  className="rounded border-[var(--border)] bg-transparent text-[var(--accent)] focus:ring-[var(--accent-ring)]" 
                  checked={allPageSelected} 
                  onChange={togglePageSelect} 
                />
              </th>
              <th className="w-16">序号</th>
              <th>密钥内容</th>
              <th className="w-24 text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            {pageItems.map((key, idx) => (
              <tr key={key}>
                <td className="px-4">
                  <input
                    type="checkbox"
                    className="rounded border-[var(--border)] bg-transparent text-[var(--accent)] focus:ring-[var(--accent-ring)]"
                    checked={!!selected[key]}
                    onChange={(e) => setSelected((prev) => ({ ...prev, [key]: e.target.checked }))}
                  />
                </td>
                <td className="font-mono text-[var(--text-faint)]">{start + idx + 1}</td>
                <td className={`font-mono ${toneClass}`}>{showSecrets ? key : maskSecret(key)}</td>
                <td className="text-right">
                  <button className="text-[var(--danger)] hover:text-red-400 font-medium transition-colors" onClick={() => removeOne(key)}>
                    移除
                  </button>
                </td>
              </tr>
            ))}
            {!pageItems.length && (
              <tr>
                <td colSpan={4} className="px-3 py-10 text-center text-[var(--text-faint)]">
                  暂无匹配数据
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 text-xs text-[var(--text-muted)] border-t border-[var(--border)] pt-4">
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
          <span className="mr-1 sm:mr-3 whitespace-nowrap font-mono">第 {currentPage} 页 / {totalPages} 页</span>
          <div className="flex items-center gap-2">
            <button className="pagination-btn" disabled={currentPage <= 1} onClick={() => setPage((prev) => Math.max(1, prev - 1))}>上一页</button>
            <button className="pagination-btn" disabled={currentPage >= totalPages} onClick={() => setPage((prev) => Math.min(totalPages, prev + 1))}>下一页</button>
          </div>
        </div>
      </div>

      {/* Import Modal */}
      {showImportModal && (
        <div className="modal-overlay animate-fade-in">
          <div className="modal-panel animate-slide-in max-w-lg">
            <div className="modal-header">
              <div>
                <h3>输入或粘贴新密钥</h3>
                <p>支持多行粘贴，一行一个。</p>
              </div>
              <button className="modal-close" onClick={() => setShowImportModal(false)}>✕</button>
            </div>
            
            <div className="modal-body">
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
                <div className="rounded border border-[rgba(239,68,68,0.3)] bg-[var(--danger-soft)] px-3 py-2 text-xs text-[var(--danger)]">
                  {importHint}
                </div>
              )}
            </div>
            
            <div className="modal-footer">
              <button className={buttonClass()} onClick={() => setShowImportModal(false)}>取消</button>
              <button className={`${buttonClass('primary')} px-6`} onClick={handleImport}>导入 (共 {parseKeysText(inputText).length} 行)</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
