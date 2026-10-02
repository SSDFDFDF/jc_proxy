import { useState } from 'react'

import { buttonClass } from '../../app/utils'

/* ── Remark editor modal ─────────────────────────────── */

export function RemarkModal({ editor, busy, onSave, onClose }) {
  const [value, setValue] = useState(editor.value)

  return (
    <div className="modal-overlay animate-fade-in">
      <div className="modal-panel animate-slide-in max-w-lg">
        <div className="modal-header">
          <div>
            <h3>编辑 Key 备注</h3>
            <p>用于记录用途、项目、额度或其他说明。</p>
          </div>
          <button className="modal-close" onClick={onClose}>✕</button>
        </div>
        <div className="modal-body">
          <textarea
            className="textarea-base min-h-[140px] w-full"
            maxLength={500}
            value={value}
            autoFocus
            placeholder="例如：生产环境对话接口 / 财务组额度"
            onChange={(e) => setValue(e.target.value)}
          />
          <div className="text-right text-xs text-[var(--text-muted)]">{value.length}/500</div>
        </div>
        <div className="modal-footer">
          <button className={buttonClass()} onClick={onClose}>取消</button>
          <button
            className={buttonClass('primary')}
            disabled={busy}
            onClick={async () => {
              const ok = await onSave(editor.key, value)
              if (ok) onClose()
            }}
          >
            保存
          </button>
        </div>
      </div>
    </div>
  )
}
