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
  return (arr || []).join(', ')
}

export function textToList(text) {
  return String(text || '')
    .split(/[\r\n,，]+/)
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
  return normalizeKeys(String(text || '').split(/[\r\n,，]+/).map((item) => item.trim()))
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

export function recommendedClientHeaderPreset(provider) {
  switch (String(provider || '').trim()) {
    case 'openai':
      return 'openai'
    case 'anthropic':
      return 'anthropic'
    case 'gemini':
      return 'gemini'
    case 'azure_openai':
    case 'deepseek':
      return 'openai_compatible'
    case 'generic':
    default:
      return 'generic_ai'
  }
}

export function emptyVendorConfig() {
  return {
    provider: 'generic',
    upstream: {
      base_url: '',
      response_header_timeout: 120_000_000_000,
      body_timeout: 300_000_000_000
    },
    load_balance: 'round_robin',
    upstream_auth: { mode: 'bearer', header: 'Authorization', prefix: 'Bearer ' },
    client_auth: { enabled: false, keys: [] },
    client_headers: { preset: '', allowlist: [], drop: [] },
    inject_headers: {},
    path_rewrites: {},
    error_policy: {
      auto_disable: {
        invalid_key: true,
        invalid_key_status_codes: [],
        invalid_key_keywords: []
      },
      cooldown: {
        response_rules: []
      },
      failover: {
        request_error: true,
        response_status_codes: []
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
      allowlist: Array.isArray(next.client_headers?.allowlist) ? [...next.client_headers.allowlist] : [],
      drop: Array.isArray(next.client_headers?.drop) ? [...next.client_headers.drop] : []
    },
    inject_headers: { ...base.inject_headers, ...(next.inject_headers || {}) },
    path_rewrites: { ...base.path_rewrites, ...(next.path_rewrites || {}) },
    error_policy: {
      auto_disable: {
        invalid_key: next.error_policy?.auto_disable?.invalid_key ?? base.error_policy.auto_disable.invalid_key,
        invalid_key_status_codes: Array.isArray(next.error_policy?.auto_disable?.invalid_key_status_codes)
          ? [...next.error_policy.auto_disable.invalid_key_status_codes]
          : [...base.error_policy.auto_disable.invalid_key_status_codes],
        invalid_key_keywords: Array.isArray(next.error_policy?.auto_disable?.invalid_key_keywords)
          ? [...next.error_policy.auto_disable.invalid_key_keywords]
          : [...base.error_policy.auto_disable.invalid_key_keywords]
      },
      cooldown: {
        response_rules: Array.isArray(next.error_policy?.cooldown?.response_rules)
          ? next.error_policy.cooldown.response_rules.map((rule) => ({
              ...rule,
              status_codes: Array.isArray(rule?.status_codes) ? [...rule.status_codes] : [],
              keywords: Array.isArray(rule?.keywords) ? [...rule.keywords] : []
            }))
          : [...base.error_policy.cooldown.response_rules]
      },
      failover: {
        request_error: next.error_policy?.failover?.request_error ?? base.error_policy.failover.request_error,
        response_status_codes: Array.isArray(next.error_policy?.failover?.response_status_codes)
          ? [...next.error_policy.failover.response_status_codes]
          : [...base.error_policy.failover.response_status_codes]
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
