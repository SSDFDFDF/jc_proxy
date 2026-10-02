import { statusLabel, statusTone } from './keyMetrics'

/* ── Icons (lucide-style inline SVG, stroke-based) ───── */

const ICON_PATHS = {
  plus: <><line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" /></>,
  copy: <><rect x="9" y="9" width="13" height="13" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></>,
  zap: <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" />,
  rotate: <><polyline points="1 4 1 10 7 10" /><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10" /></>,
  pause: <><rect x="6" y="4" width="4" height="16" /><rect x="14" y="4" width="4" height="16" /></>,
  play: <polygon points="5 3 19 12 5 21 5 3" />,
  trash: <><polyline points="3 6 5 6 21 6" /><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" /></>,
  pencil: <><path d="M17 3a2.83 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z" /></>,
  chevronRight: <polyline points="9 18 15 12 9 6" />,
  refresh: <><path d="M21 2v6h-6" /><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M3 22v-6h6" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" /></>,
  eye: <><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" /><circle cx="12" cy="12" r="3" /></>,
  eyeOff: (
    <>
      <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
      <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
      <line x1="1" y1="1" x2="23" y2="23" />
    </>
  ),
  download: <><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /><polyline points="7 10 12 15 17 10" /><line x1="12" y1="15" x2="12" y2="3" /></>,
  search: <><circle cx="11" cy="11" r="8" /><line x1="21" y1="21" x2="16.65" y2="16.65" /></>
}

export function Icon({ name, size = 14, className = '' }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={`shrink-0 ${className}`.trim()}
      aria-hidden="true"
    >
      {ICON_PATHS[name]}
    </svg>
  )
}

export function IconButton({ icon, label, danger = false, accent = false, disabled = false, onClick }) {
  const tone = danger
    ? 'hover:text-[var(--danger)]'
    : accent
      ? 'hover:text-[var(--accent)]'
      : 'hover:text-[var(--text-primary)]'
  return (
    <button
      type="button"
      className={`icon-btn ${tone}`}
      title={label}
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
    >
      <Icon name={icon} size={14} />
    </button>
  )
}

/* ── Shared cells ────────────────────────────────────── */

export function StatusBadge({ status }) {
  return (
    <span className={`inline-flex rounded border px-1.5 py-px text-[10px] font-semibold tracking-wide ${statusTone(status)}`}>
      {statusLabel(status)}
    </span>
  )
}

export function LoadChips({ metrics }) {
  const { inflight, backoff, cooldownLevel, cooldownMultiplier } = metrics
  if (!inflight && !backoff && cooldownLevel <= 1) {
    return <span className="text-[10px] text-[var(--text-faint)]">--</span>
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      {inflight > 0 && (
        <span className="kh-load-chip border-[rgba(59,130,246,0.25)] bg-[var(--accent-soft)] text-[var(--accent)]" title="当前并发">
          ↑{inflight}
        </span>
      )}
      {backoff > 0 && (
        <span className="kh-load-chip border-[rgba(245,158,11,0.25)] bg-[var(--warning-soft)] text-[var(--warning)]" title="退避剩余时间">
          ⏱{backoff}s
        </span>
      )}
      {cooldownLevel > 1 && (
        <span className="kh-load-chip border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-muted)]" title="退避等级倍率">
          x{cooldownMultiplier}
        </span>
      )}
    </span>
  )
}

const ERROR_PILL_TONES = {
  err: 'text-[var(--danger)] border-[rgba(239,68,68,0.2)] bg-[rgba(239,68,68,0.05)]',
  warn: 'text-[var(--warning)] border-[rgba(245,158,11,0.2)] bg-[rgba(245,158,11,0.05)]',
  default: 'text-[var(--text-secondary)] border-[var(--border)] bg-[var(--bg-elevated)]'
}

export function ErrorPill({ label, count, tone = 'default' }) {
  if (!count) return null
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-[4px] border px-1.5 py-0.5 text-[10px] ${ERROR_PILL_TONES[tone]}`}>
      <span className="font-medium opacity-80">{label}</span>
      <span className="font-mono">{count}</span>
    </span>
  )
}

export function ErrorPills({ metrics }) {
  const { err401, err403, err429, errOth, hasErrors } = metrics
  if (!hasErrors) return <span className="text-[10px] text-[var(--text-faint)]">--</span>
  return (
    <div className="flex flex-wrap gap-1">
      <ErrorPill label="401" count={err401} tone="err" />
      <ErrorPill label="403" count={err403} tone="err" />
      <ErrorPill label="429" count={err429} tone="warn" />
      <ErrorPill label="oth" count={errOth} tone="default" />
    </div>
  )
}

export function ReasonCell({ metrics }) {
  const { reason, secondaryText, hasErrors } = metrics
  if (!reason && !secondaryText) {
    return (
      <span
        className="text-[10px] text-[var(--text-faint)]"
        title={hasErrors ? '错误计数为累计值，暂无最近错误详情' : undefined}
      >
        --
      </span>
    )
  }
  return (
    <>
      {reason && (
        <div className="key-error-summary text-[var(--danger)]" title={reason}>{reason}</div>
      )}
      {secondaryText && (
        <div className="mt-1 break-words text-[10px] text-[var(--text-muted)]">{secondaryText}</div>
      )}
    </>
  )
}

export function copyKey(key) {
  if (navigator?.clipboard?.writeText) {
    navigator.clipboard.writeText(key).catch(() => {})
  }
}

/* ── Row actions ───────────────────────────────────────
   dense: icon buttons (desktop table); otherwise compact text buttons (mobile
   cards). Detail opens the slide-over drawer. */
export function KeyActionButtons({ item, busy, dense = false, onTest, onRecover, onToggleStatus, onDelete, onDetail }) {
  const { metrics, displayStatus } = item
  const isActive = displayStatus === 'active'
  const canRecover = metrics.backoff > 0

  if (dense) {
    return (
      <div className="flex items-center justify-end gap-0.5">
        <IconButton icon="copy" label="复制密钥" onClick={() => copyKey(item.key)} />
        <IconButton icon="zap" label="测试该密钥" onClick={() => onTest(item)} />
        {canRecover && <IconButton icon="rotate" label={`恢复（退避剩余 ${metrics.backoff}s）`} accent onClick={() => onRecover(item)} />}
        <IconButton
          icon={isActive ? 'pause' : 'play'}
          label={isActive ? '禁用' : '启用'}
          disabled={busy}
          onClick={() => onToggleStatus(item)}
        />
        <IconButton icon="trash" label="删除" danger disabled={busy} onClick={() => onDelete(item)} />
        <IconButton icon="chevronRight" label="查看详情" onClick={() => onDetail(item)} />
      </div>
    )
  }

  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
      <button type="button" className="kh-key-inline-btn text-[var(--accent)]" onClick={() => copyKey(item.key)}>复制</button>
      <button type="button" className="kh-key-inline-btn text-[var(--accent)]" onClick={() => onTest(item)}>测试</button>
      {canRecover && (
        <button type="button" className="kh-key-inline-btn text-[var(--accent)]" onClick={() => onRecover(item)}>恢复</button>
      )}
      <button
        type="button"
        className={`kh-key-inline-btn ${isActive ? 'text-[var(--warning)]' : 'text-[var(--success)]'}`}
        disabled={busy}
        onClick={() => onToggleStatus(item)}
      >
        {isActive ? '禁用' : '启用'}
      </button>
      <button type="button" className="kh-key-inline-btn text-[var(--danger)]" disabled={busy} onClick={() => onDelete(item)}>删除</button>
      <button type="button" className="kh-key-inline-btn text-[var(--text-secondary)]" onClick={() => onDetail(item)}>详情</button>
    </div>
  )
}
