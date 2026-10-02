/* Floating selection bar pinned to the bottom of the viewport area. */
export function BatchBar({ selectedCount, recoverableCount, busy, onEnable, onRecover, onDisable, onDelete, onClear }) {
  if (selectedCount === 0) return null
  return (
    <div className="kh-batch-bar animate-slide-in">
      <div className="flex items-center gap-3">
        <span className="text-sm font-medium">已选 {selectedCount} 项</span>
        {selectedCount > 0 && (
          <span className={`text-xs ${recoverableCount > 0 ? 'text-[var(--warning)]' : 'text-[var(--text-faint)]'}`}>
            {recoverableCount > 0 ? `其中 ${recoverableCount} 项退避中，可恢复` : '没有退避中的密钥'}
          </span>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" className="kh-batch-btn text-[var(--success)] hover:bg-[rgba(16,185,129,0.12)]" onClick={onEnable}>
          批量启用
        </button>
        <button
          type="button"
          className="kh-batch-btn text-[var(--accent)] hover:bg-[var(--accent-soft)]"
          disabled={busy || recoverableCount === 0}
          title={recoverableCount === 0 ? '选中的密钥均不在退避中' : `恢复 ${recoverableCount} 项退避中的密钥`}
          onClick={onRecover}
        >
          批量恢复{recoverableCount > 0 ? `（${recoverableCount}）` : ''}
        </button>
        <button type="button" className="kh-batch-btn text-[var(--warning)] hover:bg-[rgba(245,158,11,0.12)]" onClick={onDisable}>
          批量禁用
        </button>
        <button type="button" className="kh-batch-btn text-[var(--danger)] hover:bg-[rgba(239,68,68,0.12)]" onClick={onDelete}>
          批量删除
        </button>
        <button type="button" className="kh-batch-btn text-[var(--text-muted)]" onClick={onClear}>
          取消选择
        </button>
      </div>
    </div>
  )
}
