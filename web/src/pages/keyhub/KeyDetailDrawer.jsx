import { useEffect, useState } from 'react'

import { buttonClass } from '../../app/utils'
import { formatSampleSeconds } from '../../app/keyStats'
import { StatusBadge, LoadChips, ErrorPills, Icon, IconButton, copyKey } from './shared'

/* ── Key detail slide-over ───────────────────────────── */

function DetailRow({ label, children }) {
  return (
    <div className="flex items-start justify-between gap-4 py-1.5 text-xs">
      <span className="shrink-0 text-[var(--text-muted)]">{label}</span>
      <span className="min-w-0 text-right font-mono tabular-nums text-[var(--text-primary)]">{children}</span>
    </div>
  )
}

export function KeyDetailDrawer({ item, showSecrets, busy, onClose, onSetRemark, onTest, onRecover, onToggleStatus, onDelete }) {
  const [remarkDraft, setRemarkDraft] = useState('')
  const [remarkDirty, setRemarkDirty] = useState(false)
  const [revealed, setRevealed] = useState(false)

  useEffect(() => {
    setRemarkDraft(item?.remark || '')
    setRemarkDirty(false)
    setRevealed(false)
  }, [item?.key])

  if (!item) return null

  const { metrics, rt } = item
  const isActive = item.displayStatus === 'active'
  const canRecover = metrics.backoff > 0
  const showFull = showSecrets || revealed
  const lastError = item.displayDisableReason
  const timingTitle = `首包 ${formatSampleSeconds(rt.avg_header_ms, rt.header_samples)} / 整包 ${formatSampleSeconds(rt.avg_response_ms, rt.response_samples)}（样本 ${Number(rt.header_samples || 0)}/${Number(rt.response_samples || 0)}）`

  return (
    <div className="drawer-overlay" onClick={onClose}>
      <div className="drawer-panel animate-slide-in-left" onClick={(e) => e.stopPropagation()}>
        {/* header */}
        <div className="flex items-start justify-between gap-3 border-b border-[var(--border)] p-4">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <StatusBadge status={item.displayStatus} />
              {!isActive && item.displayDisabledBy && (
                <span className="text-[11px] text-[var(--text-muted)]">by {item.displayDisabledBy}</span>
              )}
            </div>
            <div className="mt-2 flex items-center gap-1">
              <span className={`break-all font-mono text-[13px] leading-snug ${isActive ? 'text-[var(--text-primary)]' : 'text-[var(--text-muted)]'}`}>
                {showFull ? item.key : item.masked}
              </span>
              <IconButton icon={showFull ? 'eyeOff' : 'eye'} label={showFull ? '隐藏明文' : '显示明文'} onClick={() => setRevealed((v) => !v)} />
              <IconButton icon="copy" label="复制密钥" onClick={() => copyKey(item.key)} />
            </div>
          </div>
          <button className="modal-close shrink-0" onClick={onClose} aria-label="关闭详情">✕</button>
        </div>

        {/* body */}
        <div className="drawer-body">
          {/* actions */}
          <div className="grid grid-cols-2 gap-2">
            <button className={buttonClass()} onClick={() => onTest(item)}>
              <Icon name="zap" size={13} /> 测试密钥
            </button>
            {canRecover ? (
              <button
                className={`${buttonClass()} text-[var(--accent)]`}
                disabled={busy}
                onClick={() => onRecover(item)}
              >
                <Icon name="rotate" size={13} /> 恢复（{metrics.backoff}s）
              </button>
            ) : null}
          </div>

          {/* remark */}
          <section>
            <h4 className="kh-drawer-section-title">备注</h4>
            <textarea
              className="textarea-base min-h-[88px] w-full text-[13px]"
              maxLength={500}
              placeholder="例如：生产环境对话接口 / 财务组额度"
              value={remarkDraft}
              onChange={(e) => { setRemarkDraft(e.target.value); setRemarkDirty(true) }}
            />
            <div className="mt-1 flex items-center justify-between">
              <span className="text-[10px] text-[var(--text-faint)]">{remarkDraft.length}/500</span>
              <button
                className={buttonClass('primary') + ' !h-7 !px-3 !text-xs'}
                disabled={busy || !remarkDirty}
                onClick={async () => {
                  const ok = await onSetRemark(item.key, remarkDraft)
                  if (ok) setRemarkDirty(false)
                }}
              >
                保存备注
              </button>
            </div>
          </section>

          {/* runtime */}
          <section>
            <h4 className="kh-drawer-section-title">运行状态</h4>
            <div className="kh-drawer-box divide-y divide-[var(--border-subtle)] px-3">
              <DetailRow label="当前并发">{metrics.inflight}</DetailRow>
              <DetailRow label="退避剩余">{metrics.backoff > 0 ? `${metrics.backoff}s` : '--'}</DetailRow>
              <DetailRow label="退避等级">{metrics.cooldownLevel > 1 ? `x${metrics.cooldownMultiplier}` : '--'}</DetailRow>
              <DetailRow label="连败次数">{metrics.failures > 0 ? metrics.failures : '--'}</DetailRow>
              <DetailRow label="最近 HTTP 状态">{metrics.lastStatus > 0 ? metrics.lastStatus : '--'}</DetailRow>
            </div>
            <div className="mt-2 px-1"><LoadChips metrics={metrics} /></div>
          </section>

          {/* traffic */}
          <section>
            <h4 className="kh-drawer-section-title">请求统计</h4>
            <div className="kh-drawer-box divide-y divide-[var(--border-subtle)] px-3">
              <DetailRow label="累计请求">{metrics.totalRequests.toLocaleString()}</DetailRow>
              <DetailRow label="成功 / 失败">
                <span className="text-[var(--success)]">{metrics.successCount}</span>
                <span className="text-[var(--text-faint)]"> / </span>
                <span className={metrics.failedCount > 0 ? 'text-[var(--danger)]' : ''}>{metrics.failedCount}</span>
              </DetailRow>
              <DetailRow label="成功率">{metrics.totalRequests > 0 ? `${metrics.successRate}%` : '--'}</DetailRow>
              <DetailRow label="近5次首包/整包">
                <span title={timingTitle}>
                  {formatSampleSeconds(rt.avg_header_ms, rt.header_samples)} / {formatSampleSeconds(rt.avg_response_ms, rt.response_samples)}
                </span>
              </DetailRow>
            </div>
            {metrics.hasErrors && (
              <div className="mt-2 px-1"><ErrorPills metrics={metrics} /></div>
            )}
          </section>

          {/* last error */}
          {lastError && (
            <section>
              <h4 className="kh-drawer-section-title">最近异常</h4>
              <pre className="kh-drawer-box whitespace-pre-wrap break-words p-3 font-mono text-[11px] leading-relaxed text-[var(--danger)]">{lastError}</pre>
            </section>
          )}
        </div>

        {/* footer */}
        <div className="flex items-center justify-between gap-2 border-t border-[var(--border)] bg-[var(--bg-elevated)] p-4">
          <button
            className={isActive ? buttonClass() + ' text-[var(--warning)]' : buttonClass('primary')}
            disabled={busy}
            onClick={() => onToggleStatus(item)}
          >
            <Icon name={isActive ? 'pause' : 'play'} size={13} />
            {isActive ? '禁用密钥' : '启用密钥'}
          </button>
          <button className={buttonClass('danger')} disabled={busy} onClick={() => onDelete(item)}>
            <Icon name="trash" size={13} /> 删除
          </button>
        </div>
      </div>
    </div>
  )
}
