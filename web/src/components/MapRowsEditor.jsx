import { buttonClass, clone } from '../app/utils'

export function MapRowsEditor({ title, rows, setRows, keyPlaceholder, valuePlaceholder }) {
  const updateRow = (index, patch) => {
    setRows((prev) => {
      const next = clone(prev)
      next[index] = { ...next[index], ...patch }
      return next
    })
  }

  const removeRow = (index) => {
    setRows((prev) => {
      const next = prev.filter((_, i) => i !== index)
      return next.length ? next : [{ key: '', value: '' }]
    })
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <h3 className="section-title text-sm">{title}</h3>
        <button className={buttonClass('ghost')} onClick={() => setRows((prev) => [...prev, { key: '', value: '' }])}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
          </svg>
          新增一行
        </button>
      </div>
      <div className="space-y-2">
        {rows.map((row, index) => (
          <div key={index} className="grid gap-3 md:grid-cols-[1fr_1fr_auto] items-center">
            <input
              className="input-base"
              placeholder={keyPlaceholder}
              value={row.key}
              onChange={(e) => updateRow(index, { key: e.target.value })}
            />
            <input
              className="input-base"
              placeholder={valuePlaceholder}
              value={row.value}
              onChange={(e) => updateRow(index, { value: e.target.value })}
            />
            <button className={buttonClass('danger')} onClick={() => removeRow(index)}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="3 6 5 6 21 6" /><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
              </svg>
              删除
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}
