import { DURATION_UNITS } from './constants'

export function clone(value) {
  return JSON.parse(JSON.stringify(value))
}

export function maskSecret(value) {
  const text = String(value || '')
  if (!text) return ''
  if (text.length <= 8) return '****'
  return `${text.slice(0, 4)}...${text.slice(-4)}`
}

export function nsToText(ns) {
  if (!Number.isFinite(ns) || ns <= 0) return '0s'
  if (ns % DURATION_UNITS.h === 0) return `${ns / DURATION_UNITS.h}h`
  if (ns % DURATION_UNITS.m === 0) return `${ns / DURATION_UNITS.m}m`
  if (ns % DURATION_UNITS.s === 0) return `${ns / DURATION_UNITS.s}s`
  if (ns % DURATION_UNITS.ms === 0) return `${ns / DURATION_UNITS.ms}ms`
  return `${ns}ns`
}

export function parseDurationToNs(text, fallbackNs = 0) {
  if (typeof text === 'number') return text
  const raw = String(text || '').trim()
  if (!raw) return fallbackNs
  if (/^\d+$/.test(raw)) return Number(raw)
  const match = raw.match(/^(\d+)(ns|us|ms|s|m|h)$/)
  if (!match) throw new Error(`非法时长格式: ${raw}，支持 10s / 5m / 3h / 500ms`)
  return Number(match[1]) * DURATION_UNITS[match[2]]
}

export function listToText(arr) {
  return (arr || []).join('\n')
}

export function textToList(text) {
  return String(text || '')
    .split('\n')
    .map((item) => item.trim())
    .filter(Boolean)
}

export function mapToRows(input) {
  const rows = Object.entries(input || {}).map(([key, value]) => ({ key, value: String(value) }))
  return rows.length ? rows : [{ key: '', value: '' }]
}

export function rowsToMap(rows) {
  const out = {}
  for (const row of rows || []) {
    const key = String(row.key || '').trim()
    const value = String(row.value || '').trim()
    if (key) out[key] = value
  }
  return out
}

export function normalizeKeys(keys) {
  const out = []
  const seen = new Set()
  for (const raw of keys || []) {
    const key = String(raw || '').trim()
    if (!key || seen.has(key)) continue
    seen.add(key)
    out.push(key)
  }
  return out
}

export function parseKeysText(text) {
  return normalizeKeys(String(text || '').split('\n').map((line) => line.trim()))
}

function randomAlphaNum(length = 24) {
  const size = Math.max(1, Number(length) || 24)
  const alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789'
  const bytes = new Uint8Array(size)

  if (typeof globalThis.crypto?.getRandomValues === 'function') {
    globalThis.crypto.getRandomValues(bytes)
  } else {
    for (let idx = 0; idx < size; idx += 1) {
      bytes[idx] = Math.floor(Math.random() * 256)
    }
  }

  let out = ''
  for (let idx = 0; idx < size; idx += 1) {
    out += alphabet[bytes[idx] % alphabet.length]
  }
  return out
}

export function generateClientKey() {
  return `sk-jcp-${randomAlphaNum(24)}`
}

export function buildVendorRequestEndpoint(vendorName) {
  const vendor = String(vendorName || '').trim()
  if (!vendor) return ''
  if (typeof window === 'undefined') return `/${vendor}`
  return `${window.location.origin}/${vendor}`
}

export function emptyVendorConfig() {
  return {
    provider: 'generic',
    upstream: { base_url: '' },
    load_balance: 'round_robin',
    upstream_auth: { mode: 'bearer', header: 'Authorization', prefix: 'Bearer ' },
    client_auth: { enabled: false, keys: [] },
    client_headers: { allowlist: [] },
    inject_headers: {},
    path_rewrites: {},
    backoff: { threshold: 3, duration: 10_800_000_000_000 },
    error_policy: {
      auto_disable: {
        invalid_key: true,
        payment_required: true,
        quota_exhausted: true
      },
      cooldown: {
        request_error: { enabled: true, duration: 2_000_000_000 },
        unauthorized: { enabled: true, duration: 1_800_000_000_000 },
        payment_required: { enabled: true, duration: 10_800_000_000_000 },
        forbidden: { enabled: true, duration: 1_800_000_000_000 },
        rate_limit: { enabled: true, duration: 5_000_000_000 },
        server_error: { enabled: true, duration: 2_000_000_000 },
        openai_slow_down: { enabled: true, duration: 900_000_000_000 }
      },
      failover: {
        request_error: true,
        unauthorized: true,
        payment_required: true,
        forbidden: true,
        rate_limit: true,
        server_error: true
      }
    },
    resin: { enabled: false, url: '', platform: 'Default', mode: 'reverse' }
  }
}

export function withVendorDefaults(vendor) {
  const base = emptyVendorConfig()
  const next = clone(vendor || {})
  return {
    provider: next.provider || base.provider,
    upstream: {
      ...base.upstream,
      ...(next.upstream || {})
    },
    load_balance: next.load_balance || base.load_balance,
    upstream_auth: { ...base.upstream_auth, ...(next.upstream_auth || {}) },
    client_auth: {
      ...base.client_auth,
      ...(next.client_auth || {}),
      keys: Array.isArray(next.client_auth?.keys) ? [...next.client_auth.keys] : []
    },
    client_headers: {
      ...base.client_headers,
      ...(next.client_headers || {}),
      allowlist: Array.isArray(next.client_headers?.allowlist) ? [...next.client_headers.allowlist] : []
    },
    inject_headers: { ...base.inject_headers, ...(next.inject_headers || {}) },
    path_rewrites: { ...base.path_rewrites, ...(next.path_rewrites || {}) },
    backoff: { ...base.backoff, ...(next.backoff || {}) },
    error_policy: {
      auto_disable: {
        ...base.error_policy.auto_disable,
        ...(next.error_policy?.auto_disable || {})
      },
      cooldown: {
        request_error: {
          ...base.error_policy.cooldown.request_error,
          ...(next.error_policy?.cooldown?.request_error || {})
        },
        unauthorized: {
          ...base.error_policy.cooldown.unauthorized,
          ...(next.error_policy?.cooldown?.unauthorized || {})
        },
        payment_required: {
          ...base.error_policy.cooldown.payment_required,
          ...(next.error_policy?.cooldown?.payment_required || {})
        },
        forbidden: {
          ...base.error_policy.cooldown.forbidden,
          ...(next.error_policy?.cooldown?.forbidden || {})
        },
        rate_limit: {
          ...base.error_policy.cooldown.rate_limit,
          ...(next.error_policy?.cooldown?.rate_limit || {})
        },
        server_error: {
          ...base.error_policy.cooldown.server_error,
          ...(next.error_policy?.cooldown?.server_error || {})
        },
        openai_slow_down: {
          ...base.error_policy.cooldown.openai_slow_down,
          ...(next.error_policy?.cooldown?.openai_slow_down || {})
        }
      },
      failover: {
        ...base.error_policy.failover,
        ...(next.error_policy?.failover || {})
      }
    },
    resin: { ...base.resin, ...(next.resin || {}) }
  }
}

export function buildNewVendorConfig(baseURL) {
  const cfg = emptyVendorConfig()
  cfg.upstream.base_url = baseURL
  return cfg
}

export function formatClock(ts) {
  if (!ts) return '--'
  return new Date(ts).toLocaleString('zh-CN', { hour12: false })
}

export function formatAgo(ts) {
  if (!ts) return '--'
  const diff = Math.max(0, Math.floor((Date.now() - ts) / 1000))
  if (diff < 10) return '刚刚'
  if (diff < 60) return `${diff} 秒前`
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  return `${Math.floor(diff / 3600)} 小时前`
}

export function tokenPreview(token) {
  if (!token) return '--'
  if (token.length <= 18) return token
  return `${token.slice(0, 9)}...${token.slice(-8)}`
}

export function panelClass(extra = '') {
  return `shell-panel ${extra}`.trim()
}

export function buttonClass(type = 'default') {
  switch (type) {
    case 'primary':
      return 'btn btn-primary'
    case 'danger':
      return 'btn btn-danger'
    case 'ghost':
      return 'btn btn-ghost'
    default:
      return 'btn'
  }
}

export function storageSummary(info) {
  if (!info?.driver) return '--'
  if (info.driver === 'pgsql') return `pgsql · ${info.table || 'default'}`
  return `file · ${info.file_path || '--'}`
}

export function pickName(list, preferred, current) {
  if (preferred && list.includes(preferred)) return preferred
  if (current && list.includes(current)) return current
  return list[0] || ''
}
