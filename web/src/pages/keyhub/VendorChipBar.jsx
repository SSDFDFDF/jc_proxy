import { useState } from 'react'

import { buildVendorInitials, vendorHealth } from './keyMetrics'

/* Vendor selector.
   Desktop keeps the horizontal chips (name + health dot + active/total).
   Phones get a wrapped text-avatar grid — a long vendor list grows downwards
   instead of sideways, so there is always something to tap without swiping the
   strip to its end. The selected vendor's full name and live numbers are shown
   in the row above the grid. Any vendor with live load gets a pulsing ↑n
   inflight marker right on its chip/tile, so the busy vendors are visible at
   a glance without selecting them first. */
const MOBILE_LIMIT = 11

function totals(item) {
  return item.activeCount + item.disabledCount
}

export function VendorChipBar({ tabs, selectedID, onSelect }) {
  // 'auto' → collapsed unless the selected vendor would be hidden; the explicit
  // states win so an operator can always collapse/expand the grid themselves.
  const [view, setView] = useState('auto')

  if (!tabs.length) {
    return <p className="text-sm text-[var(--text-faint)]">暂无供应商，请先创建供应商。</p>
  }

  const selected = tabs.find((item) => item.vendorID === selectedID) || null
  const selectedIndex = tabs.findIndex((item) => item.vendorID === selectedID)
  const isExpanded = view === 'expanded' || (view === 'auto' && selectedIndex >= MOBILE_LIMIT)
  const shownTabs = isExpanded ? tabs : tabs.slice(0, MOBILE_LIMIT)
  const hiddenCount = tabs.length - shownTabs.length
  const initials = buildVendorInitials(tabs)

  return (
    <>
      {/* ── Phones: current vendor + avatar grid ── */}
      <div className="kh-vendor-mobile">
        {selected && (
          <div className="kh-vendor-current">
            <span className={`kh-dot kh-dot-${vendorHealth(selected)}`} aria-hidden="true" />
            <span className="min-w-0 truncate font-medium text-[var(--text-primary)]">{selected.vendorName}</span>
            {!selected.configured && <span className="shrink-0 text-[10px] text-[var(--text-muted)]">未配置</span>}
            <span className="ml-auto shrink-0 font-mono text-[11px] tabular-nums">
              <span className={selected.activeCount > 0 ? 'text-[var(--success)]' : 'text-[var(--text-muted)]'}>{selected.activeCount}</span>
              <span className="text-[var(--text-faint)]">/{totals(selected)}</span>
            </span>
            {selected.inflight > 0 && (
              <span className="kh-inflight shrink-0" title="并发处理中">↑{selected.inflight}</span>
            )}
            {selected.backoff > 0 && (
              <span className="shrink-0 font-mono text-[11px] text-[var(--warning)]" title="退避">⏱{selected.backoff}</span>
            )}
          </div>
        )}

        <div className="kh-vendor-grid" role="listbox" aria-label="选择供应商">
          {shownTabs.map((item) => {
            const active = selectedID === item.vendorID
            const total = totals(item)
            return (
              <button
                key={item.vendorID}
                type="button"
                role="option"
                aria-selected={active}
                aria-label={`${item.vendorName}${item.configured ? '' : '（未配置）'}：启用 ${item.activeCount} / 共 ${total}${item.inflight > 0 ? `，并发 ${item.inflight}` : ''}`}
                title={`${item.vendorName}${item.configured ? '' : '（未配置）'}`}
                className={`kh-vendor-tile ${active ? 'kh-vendor-tile-active' : ''} ${item.configured ? '' : 'kh-vendor-tile-unconfigured'}`}
                onClick={() => onSelect(item.vendorID)}
              >
                <span className={`kh-dot kh-dot-${vendorHealth(item)}`} aria-hidden="true" />
                {item.inflight > 0 && <span className="kh-inflight" aria-hidden="true">↑{item.inflight}</span>}
                <span className="kh-vendor-avatar">{initials[item.vendorID]}</span>
                <span className="kh-vendor-count">
                  <span className={item.activeCount > 0 ? 'text-[var(--success)]' : undefined}>{item.activeCount}</span>
                  <span className="text-[var(--text-faint)]">/{total}</span>
                </span>
              </button>
            )
          })}

          {tabs.length > MOBILE_LIMIT && (
            <button
              type="button"
              className="kh-vendor-tile kh-vendor-tile-toggle"
              aria-expanded={isExpanded}
              title={isExpanded ? '收起供应商列表' : `展开其余 ${hiddenCount} 个供应商`}
              onClick={() => setView(isExpanded ? 'collapsed' : 'expanded')}
            >
              <span className="kh-vendor-avatar">{isExpanded ? '－' : `+${hiddenCount}`}</span>
              <span className="kh-vendor-count">{isExpanded ? '收起' : '更多'}</span>
            </button>
          )}
        </div>
      </div>

      {/* ── Desktop: chips ── */}
      <div className="kh-vendor-bar">
        {tabs.map((item) => {
          const active = selectedID === item.vendorID
          const total = totals(item)
          return (
            <button
              key={item.vendorID}
              type="button"
              className={`kh-vendor-chip ${active ? 'kh-vendor-chip-active' : ''}`}
              onClick={() => onSelect(item.vendorID)}
              title={active ? undefined : `切换到 ${item.vendorName}`}
            >
              <span className={`kh-dot kh-dot-${vendorHealth(item)}`} aria-hidden="true" />
              <span className="max-w-[180px] truncate font-medium">
                {item.vendorName}{item.configured ? '' : '（未配置）'}
              </span>
              <span className="font-mono text-[11px] tabular-nums text-[var(--text-muted)]">
                <span className={item.activeCount > 0 ? 'text-[var(--success)]' : ''}>{item.activeCount}</span>
                <span className="text-[var(--text-faint)]">/{total}</span>
              </span>
              {item.inflight > 0 && (
                <span className="kh-inflight" title="并发处理中">↑{item.inflight}</span>
              )}
            </button>
          )
        })}
      </div>
    </>
  )
}
