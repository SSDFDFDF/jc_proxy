import { buttonClass, panelClass } from '../app/utils'

export function RawPage({ busy, rawConfigText, onRawConfigTextChange, onReload, onSave }) {
  return (
    <section className={panelClass('p-4')}>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 className="section-title">高级 JSON 全量编辑</h3>
        </div>
        <div className="flex flex-wrap gap-2">
          <button className={buttonClass('ghost')} disabled={busy} onClick={onReload}>
            从服务端重载
          </button>
          <button className={buttonClass('primary')} disabled={busy} onClick={onSave}>
            保存并热更新
          </button>
        </div>
      </div>
      <textarea className="textarea-base h-[44rem] font-mono text-xs leading-6" value={rawConfigText} onChange={(e) => onRawConfigTextChange(e.target.value)} />
    </section>
  )
}
