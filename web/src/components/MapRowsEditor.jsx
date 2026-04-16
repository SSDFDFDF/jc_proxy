import { buttonClass, clone, panelClass } from '../app/utils'

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
          新增一行
        </button>
      </div>
      <div className="space-y-3">
        {rows.map((row, index) => (
          <div key={index} className="grid gap-3 md:grid-cols-[1fr_1fr_auto]">
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
              删除
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}
