import { keyErrorDetails } from '../../app/keyStats.js'

/* ── Status ──────────────────────────────────────────── */

export function normalizeStatus(status) {
  switch (String(status || '').trim()) {
    case 'disabled_manual':
      return 'disabled_manual'
    case 'disabled_auto':
      return 'disabled_auto'
    default:
      return 'active'
  }
}

export function statusLabel(status) {
  switch (status) {
    case 'disabled_manual':
      return '手动禁用'
    case 'disabled_auto':
      return '自动禁用'
    default:
      return '启用中'
  }
}

export function statusTone(status) {
  switch (status) {
    case 'disabled_manual':
    case 'disabled_auto':
      return 'border-[var(--border-strong)] bg-[rgba(113,113,122,0.1)] text-[var(--text-muted)]'
    default:
      return 'border-[rgba(34,197,94,0.3)] bg-[var(--success-soft)] text-[var(--success)]'
  }
}

export function resolveDisplayState(item, rt = {}) {
  return {
    displayStatus: normalizeStatus(rt.status || item.status),
    displayDisableReason: keyErrorDetails(item, rt),
    displayDisabledBy: String(rt.disabled_by || item.disabled_by || '').trim()
  }
}

/* ── Per-key metrics ─────────────────────────────────── */

export function buildKeyMetrics(item) {
  const rt = item.rt || {}
  const inflight = Number(rt.inflight || 0)
  const backoff = Number(rt.backoff_remaining_seconds || 0)
  const cooldownLevel = Number(rt.cooldown_level || 0)
  const cooldownMultiplier = cooldownLevel > 1 ? 2 ** (cooldownLevel - 1) : 1
  const totalRequests = Number(rt.total_requests || 0)
  const successCount = Number(rt.success_count || 0)
  const failures = Number(rt.failures || 0)
  const lastStatus = Number(rt.last_status || 0)
  const reason = item.displayDisableReason
  const err401 = Number(rt.unauthorized_count || 0)
  const err403 = Number(rt.forbidden_count || 0)
  const err429 = Number(rt.rate_limit_count || 0)
  const errOth = Number(rt.other_error_count || 0)
  const errorCount = err401 + err403 + err429 + errOth
  const failedCount = errorCount
  // Derived from existing lifetime counters, also works with persisted records
  // that do not have the live API's interrupted_count convenience field.
  const interruptedCount = Math.max(0, totalRequests - successCount - failedCount)
  const evaluatedRequests = successCount + failedCount
  const successRate = evaluatedRequests === 0 ? 0 : Math.round((successCount / evaluatedRequests) * 100)
  const secondaryParts = []

  if (item.displayDisabledBy) secondaryParts.push(`by ${item.displayDisabledBy}`)
  if (lastStatus >= 400) secondaryParts.push(`HTTP ${lastStatus}`)
  if (failures > 0) secondaryParts.push(`连败 ${failures}`)
  if (cooldownLevel > 1) secondaryParts.push(`退避 x${cooldownMultiplier}`)

  return {
    inflight,
    backoff,
    cooldownLevel,
    cooldownMultiplier,
    totalRequests,
    successCount,
    failedCount,
    interruptedCount,
    evaluatedRequests,
    failures,
    lastStatus,
    reason,
    err401,
    err403,
    err429,
    errOth,
    errorCount,
    hasErrors: errorCount > 0,
    successRate,
    errRate: evaluatedRequests === 0 ? 0 : 100 - successRate,
    secondaryText: secondaryParts.join(' · ')
  }
}

/* ── Sorting ─────────────────────────────────────────── */

export const STATUS_SORT_WEIGHTS = {
  active: 0,
  disabled_manual: 1,
  disabled_auto: 2
}

export const SORT_DEFAULTS = {
  key: 'asc',
  status: 'asc',
  requests: 'desc',
  errors: 'desc',
  reason: 'asc'
}

export function compareValues(left, right) {
  if (left === right) return 0
  if (typeof left === 'number' && typeof right === 'number') return left - right
  return String(left || '').localeCompare(String(right || ''), 'zh-CN', { numeric: true, sensitivity: 'base' })
}

export function compareItems(left, right, sortKey) {
  switch (sortKey) {
    case 'key':
      return compareValues(left.key, right.key)
    case 'status':
      return (
        compareValues(STATUS_SORT_WEIGHTS[left.displayStatus] ?? 99, STATUS_SORT_WEIGHTS[right.displayStatus] ?? 99) ||
        compareValues(left.metrics.backoff, right.metrics.backoff) ||
        compareValues(left.metrics.lastStatus, right.metrics.lastStatus) ||
        compareValues(left.metrics.reason, right.metrics.reason)
      )
    case 'requests':
      return (
        compareValues(left.metrics.totalRequests, right.metrics.totalRequests) ||
        compareValues(left.metrics.successCount, right.metrics.successCount) ||
        compareValues(left.metrics.failedCount, right.metrics.failedCount)
      )
    case 'errors':
      return (
        compareValues(left.metrics.errorCount, right.metrics.errorCount) ||
        compareValues(left.metrics.failures, right.metrics.failures) ||
        compareValues(left.metrics.lastStatus, right.metrics.lastStatus)
      )
    case 'reason':
      return compareValues(left.metrics.reason || left.metrics.secondaryText, right.metrics.reason || right.metrics.secondaryText)
    default:
      return compareValues(left.originalIndex, right.originalIndex)
  }
}

/* Two-state cycle: switching columns applies the column's default direction;
   clicking the active column flips it. A column click on the arrow resets to
   the insertion order. */
export function nextSortState(current, sortKey) {
  const defaultDirection = SORT_DEFAULTS[sortKey] || 'asc'
  if (current.key !== sortKey) {
    return { key: sortKey, direction: defaultDirection }
  }
  return { key: sortKey, direction: current.direction === 'asc' ? 'desc' : 'asc' }
}

/* ── Filtering ───────────────────────────────────────── */

export const KEY_FILTERS = [
  { id: 'all', label: '全部' },
  { id: 'active', label: '启用' },
  { id: 'disabled', label: '禁用' },
  { id: 'backoff', label: '退避' },
  { id: 'issues', label: '异常' },
  { id: 'inflight', label: '处理中' }
]

export function matchesFilter(item, filter) {
  const { backoff, inflight, failures, err401, err403, err429, errOth } = item.metrics
  switch (filter) {
    case 'active':
      return item.displayStatus === 'active'
    case 'disabled':
      return item.displayStatus !== 'active'
    case 'backoff':
      return backoff > 0
    case 'issues':
      return Boolean(
        item.displayStatus !== 'active' ||
        backoff > 0 ||
        failures > 0 ||
        err401 > 0 ||
        err403 > 0 ||
        err429 > 0 ||
        errOth > 0 ||
        item.displayDisableReason
      )
    case 'inflight':
      return inflight > 0
    default:
      return true
  }
}

export function countByFilter(items) {
  const counts = { all: items.length, active: 0, disabled: 0, backoff: 0, issues: 0, inflight: 0 }
  for (const item of items) {
    for (const { id } of KEY_FILTERS) {
      if (id !== 'all' && matchesFilter(item, id)) counts[id] += 1
    }
  }
  return counts
}

/* Palette for filter/KPI emphasis. 'normal' values render in primary text to
   avoid a wall of green when everything is healthy. */
export const COUNT_PALETTES = {
  all: { bg: 'border-[var(--border)] bg-[var(--bg-surface)]', value: 'normal' },
  active: { bg: 'border-[var(--border)] bg-[var(--bg-surface)]', value: 'text-[var(--success)]' },
  disabled: { bg: 'border-[var(--border)] bg-[var(--bg-surface)]', value: 'text-[var(--text-muted)]' },
  backoff: { bg: 'border-[rgba(245,158,11,0.3)] bg-[var(--warning-soft)]', value: 'text-[var(--warning)]' },
  inflight: { bg: 'border-[rgba(59,130,246,0.3)] bg-[var(--accent-soft)]', value: 'text-[var(--accent)]' },
  issues: { bg: 'border-[rgba(239,68,68,0.3)] bg-[var(--danger-soft)]', value: 'text-[var(--danger)]' }
}

/* ── Vendor summary ──────────────────────────────────── */

/* Short "avatar" text for the mobile vendor grid. Latin names keep their first
   word (OpenAI → Op, extended → Open); CJK names keep their first characters.
   Vendors whose short form collides get a longer prefix so they stay
   distinguishable without showing the full name. */
export function vendorInitial(name, maxLength = 2) {
  const text = String(name || '').trim()
  if (!text) return '?'
  const chars = Array.from(text).filter((ch) => !/\s/.test(ch))
  if (!chars.length) return '?'
  if (/^[A-Za-z0-9]/.test(chars[0])) {
    const word = chars.join('').match(/[A-Za-z0-9]+/)
    if (!word) return '?'
    const slice = word[0].slice(0, maxLength)
    return slice[0].toUpperCase() + slice.slice(1)
  }
  return chars.slice(0, maxLength).join('')
}

export function buildVendorInitials(tabs, baseLength = 2) {
  const initials = {}
  const list = tabs || []
  for (const tab of list) initials[tab.vendorID] = vendorInitial(tab.vendorName || tab.vendorID, baseLength)

  const groups = new Map()
  for (const [vendorID, text] of Object.entries(initials)) {
    const bucket = groups.get(text)
    if (bucket) bucket.push(vendorID)
    else groups.set(text, [vendorID])
  }

  for (const ids of groups.values()) {
    if (ids.length < 2) continue
    for (const vendorID of ids) {
      const tab = list.find((item) => item.vendorID === vendorID)
      const name = tab?.vendorName || vendorID
      const others = ids.filter((other) => other !== vendorID)
      let maxLength = baseLength + 2
      let text = vendorInitial(name, maxLength)
      while (
        maxLength < 12 &&
        others.some((other) => {
          const otherTab = list.find((item) => item.vendorID === other)
          return vendorInitial(otherTab?.vendorName || other, maxLength) === text
        })
      ) {
        maxLength += 2
        text = vendorInitial(name, maxLength)
      }
      initials[vendorID] = text
    }
  }
  return initials
}

/* success / warning / danger — used for the chip health dot. Disabled keys or
   live errors trump transient backoff, which trumps a clean green. */
export function vendorHealth({ disabledCount = 0, backoff = 0 }) {
  if (disabledCount > 0) return 'danger'
  if (backoff > 0) return 'warning'
  return 'success'
}
