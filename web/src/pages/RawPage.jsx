import { buttonClass, panelClass } from '../app/utils'

export function RawPage({ busy, rawConfigText, onRawConfigTextChange, onReload, onSave }) {
  return (
    <section className={`${panelClass('p-5')} animate-fade-in`}>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-4">
        <div>
          <h3 className="section-title flex items-center gap-2">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{opacity: 0.6}}>
              <polyline points="16 18 22 12 16 6" />
              <polyline points="8 6 2 12 8 18" />
            </svg>
            高级 JSON 全量编辑
          </h3>
          <p className="mt-1 text-xs text-[var(--text-muted)]">直接编辑完整 JSON 配置结构，保存后立即热更新。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button className={buttonClass('ghost')} disabled={busy} onClick={onReload}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 2v6h-6" /><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M3 22v-6h6" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
            </svg>
            从服务端重载
          </button>
          <button className={buttonClass('primary')} disabled={busy} onClick={onSave}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" /><polyline points="17,21 17,13 7,13 7,21" /><polyline points="7,3 7,8 15,8" />
            </svg>
            保存并热更新
          </button>
        </div>
      </div>
      <textarea
        className="textarea-base h-[44rem] font-mono text-xs leading-6"
        value={rawConfigText}
        onChange={(e) => onRawConfigTextChange(e.target.value)}
        style={{ background: 'var(--bg-base)', color: 'var(--text-secondary)' }}
      />
    </section>
  )
}
