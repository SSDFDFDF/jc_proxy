import { useEffect, useMemo, useState } from 'react'

import {
  DEFAULT_SYSTEM_FORM,
  EMPTY_CONFIG,
  EMPTY_UPSTREAM_KEYS,
  NAV_ITEMS,
  STORAGE_TOKEN,
  STORAGE_USER
} from '../app/constants'
import {
  buildNewVendorConfig,
  buildVendorRequestEndpoint,
  clone,
  listToText,
  mapToRows,
  normalizeKeys,
  nsToText,
  parseDurationToNs,
  rowsToMap,
  textToList,
  withVendorDefaults
} from '../app/utils'

function statusCodesToText(codes) {
  return (codes || []).map((code) => String(code)).join(', ')
}

function parseStatusCodesText(text) {
  const out = []
  const seen = new Set()
  for (const part of String(text || '').split(',')) {
    const raw = part.trim()
    if (!raw) continue
    if (!/^\d+$/.test(raw)) {
      throw new Error(`非法响应码: ${raw}`)
    }
    const code = Number(raw)
    if (!seen.has(code)) {
      seen.add(code)
      out.push(code)
    }
  }
  return out
}

function emptyResponseRuleRow() {
  return {
    statusCodesText: '',
    keywordsText: '',
    durationText: '',
    retryAfter: ''
  }
}

function emptyMaskingRuleRow() {
  return {
    statusCodesText: '',
    keywordsText: '',
    statusCodeText: '',
    bodyText: '',
    messageText: '',
    contentTypeText: '',
    retryAfterText: '',
    cooldownText: ''
  }
}

function buildResponseRuleRows(policy) {
  const rows = (policy?.cooldown?.response_rules || []).map((rule) => ({
    statusCodesText: statusCodesToText(rule?.status_codes || []),
    keywordsText: listToText(rule?.keywords || []),
    durationText: nsToText(rule?.duration || 0),
    retryAfter: String(rule?.retry_after || '').trim()
  }))
  return rows.length ? rows : [emptyResponseRuleRow()]
}

function parseResponseRuleRows(rows) {
  const out = []
  for (const [index, row] of (rows || []).entries()) {
    const statusCodesText = String(row?.statusCodesText || '').trim()
    const keywordsText = String(row?.keywordsText || '').trim()
    const durationText = String(row?.durationText || '').trim()
    const retryAfter = String(row?.retryAfter || '').trim()
    if (!statusCodesText && !keywordsText && !durationText && !retryAfter) {
      continue
    }

    const statusCodes = parseStatusCodesText(statusCodesText)
    const keywords = textToList(keywordsText)
    if (!statusCodes.length && !keywords.length) {
      throw new Error(`退避规则第 ${index + 1} 行至少填写响应码或关键字`)
    }

    const duration = parseDurationToNs(durationText, 0)
    if (!duration || duration <= 0) {
      throw new Error(`退避规则第 ${index + 1} 行缺少有效时长`)
    }

    if (retryAfter && !['ignore', 'override', 'max'].includes(retryAfter)) {
      throw new Error(`退避规则第 ${index + 1} 行 Retry-After 仅支持 ignore / override / max`)
    }

    const next = {
      status_codes: statusCodes,
      keywords,
      duration
    }
    if (retryAfter) next.retry_after = retryAfter
    out.push(next)
  }
  return out
}

function buildMaskingRuleRows(policy) {
  const rows = (policy?.masking?.rules || []).map((rule) => ({
    statusCodesText: statusCodesToText(rule?.status_codes || []),
    keywordsText: listToText(rule?.keywords || []),
    statusCodeText: rule?.status_code ? String(rule.status_code) : '',
    bodyText: String(rule?.body || ''),
    messageText: String(rule?.message || ''),
    contentTypeText: String(rule?.content_type || ''),
    retryAfterText: String(rule?.retry_after || '').trim(),
    cooldownText: rule?.cooldown ? nsToText(rule.cooldown) : ''
  }))
  return rows.length ? rows : [emptyMaskingRuleRow()]
}

// parseMaskingRuleRows mirrors the gateway's masking semantics: match by
// status codes and/or keywords, replace with an error status (400-599).
// retry_after accepts empty (preserve), "ignore", or a fixed duration >= 1s.
function parseMaskingRuleRows(rows) {
  const out = []
  for (const [index, row] of (rows || []).entries()) {
    const statusCodesText = String(row?.statusCodesText || '').trim()
    const keywordsText = String(row?.keywordsText || '').trim()
    const statusCodeText = String(row?.statusCodeText || '').trim()
    const bodyText = String(row?.bodyText || '')
    const messageText = String(row?.messageText || '').trim()
    const contentTypeText = String(row?.contentTypeText || '').trim()
    const retryAfterText = String(row?.retryAfterText || '').trim()
    const cooldownText = String(row?.cooldownText || '').trim()
    if (
      !statusCodesText && !keywordsText && !statusCodeText &&
      !bodyText.trim() && !messageText && !contentTypeText &&
      !retryAfterText && !cooldownText
    ) {
      continue
    }

    const statusCodes = parseStatusCodesText(statusCodesText)
    const keywords = textToList(keywordsText)
    if (!statusCodes.length && !keywords.length) {
      throw new Error(`屏蔽规则第 ${index + 1} 行至少填写响应码或关键字`)
    }

    if (!/^\d+$/.test(statusCodeText)) {
      throw new Error(`屏蔽规则第 ${index + 1} 行需填写替换状态码（数字）`)
    }
    const statusCode = Number(statusCodeText)
    if (statusCode < 400 || statusCode > 599) {
      throw new Error(`屏蔽规则第 ${index + 1} 行替换状态码必须在 400-599 之间`)
    }

    let retryAfter = ''
    if (retryAfterText && retryAfterText !== 'ignore' && retryAfterText !== 'preserve') {
      const ns = parseDurationToNs(retryAfterText, 0)
      if (!ns || ns < 1_000_000_000) {
        throw new Error(`屏蔽规则第 ${index + 1} 行 Retry-After 需为 ignore、preserve 或 >= 1s 的时长（如 30s）`)
      }
      retryAfter = retryAfterText
    } else if (retryAfterText === 'ignore' || retryAfterText === 'preserve') {
      retryAfter = retryAfterText
    }

    let cooldown = 0
    if (cooldownText) {
      cooldown = parseDurationToNs(cooldownText, 0)
      if (cooldown < 0) {
        throw new Error(`屏蔽规则第 ${index + 1} 行退避时长不能为负`)
      }
    }

    const next = {
      status_codes: statusCodes,
      keywords,
      status_code: statusCode
    }
    if (bodyText.trim()) next.body = bodyText
    if (messageText) next.message = messageText
    if (contentTypeText) next.content_type = contentTypeText
    if (retryAfter) next.retry_after = retryAfter
    if (cooldown > 0) next.cooldown = cooldown
    out.push(next)
  }
  return out
}

function emptyStatsResult() {
  return {
    vendors: {},
    meta: {
      page: 1,
      page_size: 50,
      total: 0
    }
  }
}

function buildStatsPath(query = {}) {
  const params = new URLSearchParams()
  const vendorID = String(query.vendorID || '').trim()
  const filter = String(query.filter || 'all').trim()
  const keyword = String(query.q || '').trim()

  if (vendorID) params.set('vendor_id', vendorID)
  if (filter && filter !== 'all') params.set('filter', filter)
  if (keyword) params.set('q', keyword)
  params.set('page', String(Math.max(1, Number(query.page) || 1)))
  params.set('page_size', String(Math.max(1, Number(query.pageSize) || 50)))

  return `/admin/stats?${params.toString()}`
}

// vendors is an ordered array of {id, name, ...}: the id is immutable and keys
// every stored reference, the name is a mutable label that doubles as the
// request path segment.
function vendorList(cfg) {
  return Array.isArray(cfg?.vendors) ? cfg.vendors : []
}

function findVendorByID(cfg, vendorID) {
  if (!vendorID) return null
  return vendorList(cfg).find((entry) => entry?.id === vendorID) || null
}

function vendorNameByID(cfg, vendorID) {
  return findVendorByID(cfg, vendorID)?.name || ''
}

function pickVendorID(ids, preferred, current) {
  if (preferred && ids.includes(preferred)) return preferred
  if (current && ids.includes(current)) return current
  return ids[0] || ''
}

export function useAdminConsole() {
  const [token, setToken] = useState(localStorage.getItem(STORAGE_TOKEN) || '')
  const [username, setUsername] = useState(localStorage.getItem(STORAGE_USER) || 'admin')
  const [password, setPassword] = useState('')

  const [busy, setBusy] = useState(false)
  const [nav, setNav] = useState('overview')
  const [notice, setNotice] = useState({ tone: 'info', text: '' })
  const [me, setMe] = useState({ username: '' })
  const [lastSyncAt, setLastSyncAt] = useState(0)

  const [rawConfig, setRawConfig] = useState(EMPTY_CONFIG)
  const [maskedConfig, setMaskedConfig] = useState(EMPTY_CONFIG)
  const [rawConfigText, setRawConfigText] = useState(JSON.stringify(EMPTY_CONFIG, null, 2))
  const [stats, setStats] = useState({ vendors: {} })
  const [statsResult, setStatsResult] = useState(emptyStatsResult())
  const [upstreamKeysData, setUpstreamKeysData] = useState(EMPTY_UPSTREAM_KEYS)

  const [autoRefreshStats, setAutoRefreshStats] = useState(true)
  const [refreshEverySec, setRefreshEverySec] = useState('4')
  const [statsFilters, setStatsFilters] = useState({
    vendorID: '',
    filter: 'all',
    q: '',
    page: 1,
    pageSize: 50
  })

  const [selectedVendorID, setSelectedVendorID] = useState('')
  const [vendorDraft, setVendorDraft] = useState(null)
  const [invalidKeyStatusCodesText, setInvalidKeyStatusCodesText] = useState('')
  const [invalidKeyKeywordsText, setInvalidKeyKeywordsText] = useState('')
  const [responseRuleRows, setResponseRuleRows] = useState(buildResponseRuleRows(buildNewVendorConfig('').error_policy))
  const [maskingRuleRows, setMaskingRuleRows] = useState(buildMaskingRuleRows(buildNewVendorConfig('').error_policy))
  const [failoverResponseStatusCodesText, setFailoverResponseStatusCodesText] = useState('')
  const [aggregateRetryStatusCodesText, setAggregateRetryStatusCodesText] = useState('')
  const [upstreamResponseHeaderTimeoutText, setUpstreamResponseHeaderTimeoutText] = useState('300s')
  const [upstreamBodyTimeoutText, setUpstreamBodyTimeoutText] = useState('5m')
  const [upstreamInterimResponseIntervalText, setUpstreamInterimResponseIntervalText] = useState('30s')
  const [clientHeaderPreset, setClientHeaderPreset] = useState('')
  const [allowlistText, setAllowlistText] = useState('')
  const [dropHeadersText, setDropHeadersText] = useState('')
  const [injectRows, setInjectRows] = useState([{ key: '', value: '' }])
  const [rewriteRows, setRewriteRows] = useState([{ key: '', value: '' }])

  const [selectedKeyVendorID, setSelectedKeyVendorID] = useState('')
  const [showSecrets, setShowSecrets] = useState(false)

  const [newVendorForm, setNewVendorForm] = useState({ name: '', baseURL: '', provider: 'generic' })
  const [renameDraft, setRenameDraft] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [systemForm, setSystemForm] = useState(DEFAULT_SYSTEM_FORM)

  const isAuthed = Boolean(token)
  const currentPageMeta = NAV_ITEMS.find((item) => item.id === nav) || NAV_ITEMS[0]

  const setStatus = (tone, text) => {
    setNotice({ tone, text })
  }

  // Auto-dismiss notices based on type
  // success: 3s, info: 5s, warn: 8s, error: no auto-dismiss
  useEffect(() => {
    if (!notice.text) return
    const delays = { success: 3000, info: 5000, warn: 8000 }
    const delay = delays[notice.tone]
    if (delay) {
      const timer = setTimeout(() => {
        setNotice({ tone: 'info', text: '' })
      }, delay)
      return () => clearTimeout(timer)
    }
  }, [notice])

  const clearSession = (message) => {
    setToken('')
    setMe({ username: '' })
    localStorage.removeItem(STORAGE_TOKEN)
    localStorage.removeItem(STORAGE_USER)
    if (message) setStatus('warn', message)
  }

  const api = async (path, options = {}, extra = {}) => {
    const authEnabled = extra.auth !== false
    const headers = { ...(options.headers || {}) }
    if (authEnabled && token) headers.Authorization = `Bearer ${token}`

    const resp = await fetch(path, { ...options, headers })
    const data = await resp.json().catch(() => ({}))
    if (!resp.ok) {
      const message = data.error || `${resp.status} ${resp.statusText}`
      if (resp.status === 401 && authEnabled) clearSession('会话已过期，请重新登录')
      throw new Error(message)
    }
    return data
  }

  const syncSystemForm = (cfg) => {
    setSystemForm({
      listen: cfg.server?.listen || ':8092',
      readTimeout: nsToText(cfg.server?.read_timeout),
      writeTimeout: nsToText(cfg.server?.write_timeout),
      idleTimeout: nsToText(cfg.server?.idle_timeout),
      shutdownTimeout: nsToText(cfg.server?.shutdown_timeout),
      adminEnabled: !!cfg.admin?.enabled,
      adminUsername: cfg.admin?.username || 'admin',
      adminSessionTTL: nsToText(cfg.admin?.session_ttl),
      auditLogPath: cfg.admin?.audit_log_path || './data/admin_audit.log',
      adminAllowedCIDRsText: listToText(cfg.admin?.allowed_cidrs || []),
      adminTrustedProxyCIDRsText: listToText(cfg.admin?.trusted_proxy_cidrs || [])
    })
  }

  const syncVendorDraft = (cfg, vendorID) => {
    const entry = findVendorByID(cfg, vendorID)
    if (!entry) {
      setVendorDraft(null)
      setRenameDraft('')
      setInvalidKeyStatusCodesText('')
      setInvalidKeyKeywordsText('')
      setResponseRuleRows(buildResponseRuleRows(buildNewVendorConfig('').error_policy))
      setMaskingRuleRows(buildMaskingRuleRows(buildNewVendorConfig('').error_policy))
      setFailoverResponseStatusCodesText('')
      setAggregateRetryStatusCodesText('')
      setUpstreamResponseHeaderTimeoutText('300s')
      setUpstreamBodyTimeoutText('5m')
      setUpstreamInterimResponseIntervalText('30s')
      setClientHeaderPreset('')
      setAllowlistText('')
      setDropHeadersText('')
      setInjectRows([{ key: '', value: '' }])
      setRewriteRows([{ key: '', value: '' }])
      return
    }
    const draft = withVendorDefaults(entry)
    setVendorDraft(draft)
    const currentCodes = draft.error_policy?.auto_disable?.status_codes || []
    const currentKeywords = draft.error_policy?.auto_disable?.keywords || []
    setInvalidKeyStatusCodesText(statusCodesToText(currentCodes))
    setInvalidKeyKeywordsText(listToText(currentKeywords))
    setResponseRuleRows(buildResponseRuleRows(draft.error_policy))
    setMaskingRuleRows(buildMaskingRuleRows(draft.error_policy))
    setFailoverResponseStatusCodesText(statusCodesToText(draft.error_policy?.failover?.response_status_codes || []))
    setAggregateRetryStatusCodesText(statusCodesToText(draft.aggregate?.retry?.status_codes || []))
    setUpstreamResponseHeaderTimeoutText(nsToText(draft.upstream?.response_header_timeout || 0))
    setUpstreamBodyTimeoutText(nsToText(draft.upstream?.body_timeout || 0))
    setUpstreamInterimResponseIntervalText(nsToText(draft.upstream?.interim_response_interval || 0))
    setClientHeaderPreset(String(draft.client_headers?.preset || '').trim())
    setAllowlistText(listToText(draft.client_headers?.allowlist || []))
    setDropHeadersText(listToText(draft.client_headers?.drop || []))
    setInjectRows(mapToRows(draft.inject_headers || {}))
    setRewriteRows(mapToRows(draft.path_rewrites || {}))
  }

  const selectVendor = (vendorID) => {
    setSelectedVendorID(vendorID)
    syncVendorDraft(rawConfig, vendorID)
  }

  const selectKeyVendor = (vendorID) => {
    setSelectedKeyVendorID(vendorID)
  }

  const loadStats = async (silent = false, touchBusy = false) => {
    if (touchBusy) setBusy(true)
    try {
      const data = await api('/admin/stats')
      setStats(data || { vendors: {} })
      setLastSyncAt(Date.now())
      if (!silent) setStatus('success', '运行状态已刷新')
    } catch (err) {
      if (!silent) setStatus('error', String(err?.message || err))
    } finally {
      if (touchBusy) setBusy(false)
    }
  }

  const loadFilteredStats = async (overrides = {}, silent = false, touchBusy = false) => {
    const nextQuery = {
      ...statsFilters,
      ...overrides
    }

    if (!nextQuery.vendorID) {
      setStatsResult(emptyStatsResult())
      return
    }

    if (touchBusy) setBusy(true)
    try {
      const data = await api(buildStatsPath(nextQuery))
      setStatsResult(data || emptyStatsResult())
      setLastSyncAt(Date.now())
      if (!silent) setStatus('success', '运行状态已刷新')
    } catch (err) {
      if (!silent) setStatus('error', String(err?.message || err))
    } finally {
      if (touchBusy) setBusy(false)
    }
  }

  const refreshAll = async (preferredVendorID = '', preferredKeyVendorID = '') => {
    setBusy(true)
    try {
      const [nextMe, raw, masked, upstreamKeys, runtimeStats] = await Promise.all([
        api('/admin/me'),
        api('/admin/config/raw'),
        api('/admin/config'),
        api('/admin/upstream-keys'),
        api('/admin/stats')
      ])

      setMe(nextMe || { username: '' })
      setRawConfig(raw || EMPTY_CONFIG)
      setMaskedConfig(masked || EMPTY_CONFIG)
      setRawConfigText(JSON.stringify(raw || EMPTY_CONFIG, null, 2))
      setUpstreamKeysData(upstreamKeys || EMPTY_UPSTREAM_KEYS)
      setStats(runtimeStats || { vendors: {} })
      syncSystemForm(raw || EMPTY_CONFIG)

      const vendorIDs = vendorList(raw).map((entry) => entry.id)
      const nextVendorID = pickVendorID(vendorIDs, preferredVendorID, selectedVendorID)
      setSelectedVendorID(nextVendorID)
      syncVendorDraft(raw || EMPTY_CONFIG, nextVendorID)
      setStatsFilters((prev) => {
        const nextStatsVendorID = pickVendorID(vendorIDs, preferredVendorID || nextVendorID, prev.vendorID)
        return {
          ...prev,
          vendorID: nextStatsVendorID,
          page: nextStatsVendorID === prev.vendorID ? prev.page : 1
        }
      })

      // Key partitions can outlive their config entry (orphans), so the picker
      // is driven by the key store's own vendor list.
      const upstreamVendorIDs = (upstreamKeys?.vendors || []).map((item) => item.vendor_id)
      const nextKeyVendorID = pickVendorID(upstreamVendorIDs, preferredKeyVendorID || nextVendorID, selectedKeyVendorID)
      setSelectedKeyVendorID(nextKeyVendorID)

      setLastSyncAt(Date.now())
      setStatus('success', '管理数据已同步')
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const login = async () => {
    setBusy(true)
    try {
      const payload = await api(
        '/admin/login',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ username, password })
        },
        { auth: false }
      )
      localStorage.setItem(STORAGE_TOKEN, payload.token)
      localStorage.setItem(STORAGE_USER, payload.username || username)
      setToken(payload.token)
      setStatus('success', '登录成功')
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const logout = async () => {
    setBusy(true)
    try {
      if (token) await api('/admin/logout', { method: 'POST' })
    } catch (_) {
      // ignore
    } finally {
      clearSession('已退出登录')
      setBusy(false)
    }
  }

  const mutateVendorDraft = (mutator) => {
    setVendorDraft((prev) => {
      const next = withVendorDefaults(prev || buildNewVendorConfig(''))
      mutator(next)
      return next
    })
  }

  const saveVendor = async () => {
    if (!selectedVendorID || !vendorDraft) return
    setBusy(true)
    try {
      const next = clone(vendorDraft)
      next.upstream.response_header_timeout = parseDurationToNs(upstreamResponseHeaderTimeoutText, next.upstream.response_header_timeout)
      next.upstream.body_timeout = parseDurationToNs(upstreamBodyTimeoutText, next.upstream.body_timeout)
      next.upstream.interim_response_interval = parseDurationToNs(upstreamInterimResponseIntervalText, next.upstream.interim_response_interval)
      next.error_policy.auto_disable = {
        enabled: vendorDraft.error_policy?.auto_disable?.enabled !== false,
        status_codes: parseStatusCodesText(invalidKeyStatusCodesText),
        keywords: textToList(invalidKeyKeywordsText)
      }
      next.error_policy.cooldown.response_rules = parseResponseRuleRows(responseRuleRows)
      next.error_policy.masking = {
        enabled: next.error_policy?.masking?.enabled ?? true,
        rules: parseMaskingRuleRows(maskingRuleRows)
      }
      next.error_policy.failover.response_status_codes = parseStatusCodesText(failoverResponseStatusCodesText)
      if (next.provider === 'aggregate') {
        if (!next.aggregate) next.aggregate = { children: [] }
        if (!next.aggregate.retry) next.aggregate.retry = {}
        next.aggregate.retry.status_codes = parseStatusCodesText(aggregateRetryStatusCodesText)
      }
      next.client_headers.preset = String(clientHeaderPreset || '').trim()
      next.client_headers.allowlist = textToList(allowlistText)
      next.client_headers.drop = textToList(dropHeadersText)
      next.inject_headers = rowsToMap(injectRows)
      next.path_rewrites = rowsToMap(rewriteRows)
      next.client_auth.keys = normalizeKeys(next.client_auth.keys || [])
      if (next.client_auth.keys.length === 0) next.client_auth.enabled = false

      // Addressed by immutable id, and the payload carries config only: the
      // display/route name is changed through renameVendor.
      await api(`/admin/vendors/${encodeURIComponent(selectedVendorID)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config: next })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID || selectedVendorID)
      setStatus('success', `供应商 ${selectedVendorName || selectedVendorID} 已保存`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const createVendor = async () => {
    const name = newVendorForm.name.trim()
    const baseURL = newVendorForm.baseURL.trim()
    const provider = newVendorForm.provider || 'generic'
    if (!name) {
      setStatus('warn', '请输入供应商名称')
      return
    }
    if (provider !== 'aggregate' && !baseURL) {
      setStatus('warn', '请输入上游 base_url')
      return
    }
    setBusy(true)
    try {
      const created = await api('/admin/vendors', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, config: buildNewVendorConfig(baseURL, provider) })
      })
      const createdID = created?.vendor_id || ''
      setNewVendorForm({ name: '', baseURL: '', provider: 'generic' })
      setNav('vendors')
      await refreshAll(createdID, createdID)
      if (provider === 'aggregate') {
        setStatus('success', `聚合供应商 ${name} 已创建`)
      } else {
        setStatus('success', `供应商 ${name} 已创建，可前往上游密钥页面补充配置`)
      }
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  // Renaming only changes the display/route name. Every stored reference keys
  // off the immutable id, so keys, statistics and aggregate topology stay put;
  // the one visible effect is that clients must call the new path segment.
  const renameVendor = async () => {
    const nextName = String(renameDraft || '').trim()
    if (!selectedVendorID) return
    if (!nextName) {
      setStatus('warn', '请输入新的供应商名称')
      return
    }
    if (nextName === selectedVendorName) {
      setStatus('info', '名称未变化')
      return
    }
    const oldEndpoint = buildVendorRequestEndpoint(selectedVendorName)
    const nextEndpoint = buildVendorRequestEndpoint(nextName)
    const confirmed = window.confirm(
      `确认把供应商 ${selectedVendorName} 改名为 ${nextName} 吗？\n\n` +
        `调用地址会从\n  ${oldEndpoint}\n变为\n  ${nextEndpoint}\n\n` +
        '旧地址会立即返回 404，请同步更新客户端配置。上游密钥、运行统计和聚合关系不受影响。'
    )
    if (!confirmed) return
    setBusy(true)
    try {
      await api(`/admin/vendors/${encodeURIComponent(selectedVendorID)}/rename`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: nextName })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID || selectedVendorID)
      setStatus('success', `供应商已改名为 ${nextName}，请更新客户端调用地址`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const deleteVendor = async () => {
    if (!selectedVendorID) return
    if (!window.confirm(`确认删除供应商 ${selectedVendorName || selectedVendorID} 吗？对应的上游密钥也会一并移除。`)) return
    setBusy(true)
    try {
      await api(`/admin/vendors/${encodeURIComponent(selectedVendorID)}`, { method: 'DELETE' })
      await refreshAll('', '')
      setStatus('success', `供应商 ${selectedVendorName || selectedVendorID} 已删除`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const addUpstreamKeys = async (keys) => {
    if (!selectedKeyVendorID) {
      setStatus('warn', '请先选择供应商')
      return false
    }
    const nextKeys = normalizeKeys(Array.isArray(keys) ? keys : [])
    if (!nextKeys.length) {
      setStatus('warn', '请输入至少一条有效密钥')
      return false
    }
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendorID)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keys: nextKeys })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', nextKeys.length === 1 ? '密钥已添加' : `已添加 ${nextKeys.length} 条密钥`)
      return true
    } catch (err) {
      setStatus('error', String(err?.message || err))
      return false
    } finally {
      setBusy(false)
    }
  }

  const disableUpstreamKey = async (key) => {
    if (!selectedKeyVendorID || !key) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendorID)}/disable`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key, reason: 'manually disabled' })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', `密钥已禁用`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const disableUpstreamKeys = async (keys) => {
    const nextKeys = normalizeKeys(Array.isArray(keys) ? keys : [])
    if (!selectedKeyVendorID || !nextKeys.length) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendorID)}/disable`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keys: nextKeys, reason: 'manually disabled' })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', `已禁用 ${nextKeys.length} 条密钥`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const enableUpstreamKey = async (key) => {
    if (!selectedKeyVendorID || !key) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendorID)}/enable`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', `密钥已启用`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const enableUpstreamKeys = async (keys) => {
    const nextKeys = normalizeKeys(Array.isArray(keys) ? keys : [])
    if (!selectedKeyVendorID || !nextKeys.length) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendorID)}/enable`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keys: nextKeys })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', `已启用 ${nextKeys.length} 条密钥`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const recoverUpstreamKey = async (key) => {
    if (!selectedKeyVendorID || !key) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendorID)}/recover`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', `密钥已恢复`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const recoverUpstreamKeys = async (keys) => {
    const nextKeys = normalizeKeys(Array.isArray(keys) ? keys : [])
    if (!selectedKeyVendorID || !nextKeys.length) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendorID)}/recover`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keys: nextKeys })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', `已恢复 ${nextKeys.length} 条密钥`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const deleteUpstreamKey = async (key) => {
    if (!selectedKeyVendorID || !key) return
    if (!window.confirm('确认删除这个上游密钥吗？')) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendorID)}`, {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keys: [key] })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', `密钥已删除`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const deleteUpstreamKeys = async (keys) => {
    if (!selectedKeyVendorID || !keys?.length) return
    if (!window.confirm(`确认删除这 ${keys.length} 个上游密钥吗？`)) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendorID)}`, {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keys })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', `已删除 ${keys.length} 条密钥`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const setUpstreamKeyRemark = async (key, remark) => {
    if (!selectedKeyVendorID || !key) return false
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendorID)}/remark`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key, remark: String(remark || '').trim() })
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', '密钥备注已保存')
      return true
    } catch (err) {
      setStatus('error', String(err?.message || err))
      return false
    } finally {
      setBusy(false)
    }
  }

  // Export every upstream key (all vendors) as a self-contained JSON backup.
  // The backup is intentionally pure-data: it stores the immutable vendor id, the
  // current display name (for human readers), and each key's plaintext plus
  // remark/status/disable_reason. Runtime counters and timestamps are excluded
  // because they are meaningless on another deployment.
  const exportUpstreamKeysBackup = async () => {
    setBusy(true)
    try {
      // Re-fetch the freshest data so the backup reflects the current store,
      // not whatever the UI happens to have cached.
      const fresh = await api('/admin/upstream-keys')
      const freshConfig = await api('/admin/config/raw')
      const vendors = fresh?.vendors || []
      const items = fresh?.items || {}
      const nameByID = new Map()
      for (const entry of vendorList(freshConfig)) {
        if (entry?.id) nameByID.set(entry.id, entry.name || entry.id)
      }
      const exportedVendors = []
      for (const summary of vendors) {
        const vendorID = summary.vendor_id
        const records = items[vendorID] || []
        if (!records.length) continue
        exportedVendors.push({
          vendor_id: vendorID,
          vendor_name: nameByID.get(vendorID) || summary.vendor || vendorID,
          keys: records.map((record) => ({
            key: record.key,
            remark: record.remark || '',
            status: record.status || 'active',
            disable_reason: record.disable_reason || ''
          }))
        })
      }
      const payload = {
        schema: 'jc_proxy.upstream_keys.backup',
        version: 1,
        exported_at: new Date().toISOString(),
        storage: fresh?.storage || null,
        vendors: exportedVendors
      }
      const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const stamp = new Date().toISOString().replace(/[:.]/g, '-').replace('T', '_').replace('Z', '')
      const a = document.createElement('a')
      a.href = url
      a.download = `jc_proxy_upstream_keys_backup_${stamp}.json`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
      const total = exportedVendors.reduce((sum, item) => sum + item.keys.length, 0)
      setStatus('success', `已导出 ${exportedVendors.length} 个供应商、共 ${total} 条密钥`)
      return true
    } catch (err) {
      setStatus('error', String(err?.message || err))
      return false
    } finally {
      setBusy(false)
    }
  }

  // Import a backup produced by exportUpstreamKeysBackup. Only vendors that
  // still exist in the target config are touched; keys for unknown vendors are
  // skipped and reported. For each surviving vendor we add the keys that do not
  // exist yet, then restore their remark / status so a backup taken before a
  // key was disabled is faithfully re-applied. Returns a structured report so
  // the caller can show a per-vendor breakdown in the UI.
  const importUpstreamKeysBackup = async (backup) => {
    const rawBackup = backup || {}
    const backupVendors = Array.isArray(rawBackup.vendors) ? rawBackup.vendors : []
    if (!backupVendors.length) {
      setStatus('warn', '备份文件中没有可导入的供应商密钥')
      return { ok: false, skippedVendors: [], vendors: [], totalAdded: 0, totalSkipped: 0 }
    }
    // Resolve the live vendor set from the freshest config, not the cached
    // state, so vendors created after the last refresh are recognised too.
    let liveConfig
    setBusy(true)
    try {
      liveConfig = await api('/admin/config/raw')
    } catch (err) {
      setStatus('error', String(err?.message || err))
      setBusy(false)
      return { ok: false, skippedVendors: [], vendors: [], totalAdded: 0, totalSkipped: 0 }
    }
    const liveVendorIDs = new Set(vendorList(liveConfig).map((entry) => entry?.id).filter(Boolean))
    const report = {
      ok: true,
      skippedVendors: [],
      vendors: [],
      totalAdded: 0,
      totalSkipped: 0
    }
    try {
      for (const vendorBlock of backupVendors) {
        const vendorID = String(vendorBlock.vendor_id || '').trim()
        const backupKeys = Array.isArray(vendorBlock.keys) ? vendorBlock.keys : []
        if (!vendorID || !backupKeys.length) continue
        if (!liveVendorIDs.has(vendorID)) {
          report.skippedVendors.push({
            vendor_id: vendorID,
            vendor_name: vendorBlock.vendor_name || vendorID,
            count: backupKeys.length,
            reason: '供应商不存在'
          })
          report.totalSkipped += backupKeys.length
          continue
        }
        // 1) Add every key from the backup. The server silently skips keys
        //    that already exist, so duplicates are safe.
        const keysToAdd = normalizeKeys(backupKeys.map((item) => String(item?.key || '').trim()))
        let added = 0
        if (keysToAdd.length) {
          try {
            await api(`/admin/upstream-keys/${encodeURIComponent(vendorID)}`, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ keys: keysToAdd })
            })
            added = keysToAdd.length
          } catch (err) {
            // If the bulk add fails, surface it but keep going so other
            // vendors in the backup are not lost.
            report.vendors.push({
              vendor_id: vendorID,
              added: 0,
              remarks: 0,
              disabled: 0,
              enabled: 0,
              failed: String(err?.message || err)
            })
            report.totalSkipped += keysToAdd.length
            continue
          }
        }
        // 2) Restore remark and disabled_manual status. We deliberately do
        //    not re-create disabled_auto, because that status is set by the
        //    runtime in response to live traffic and should not be faked.
        let remarksApplied = 0
        let disabledApplied = 0
        let enabledApplied = 0
        for (const entry of backupKeys) {
          const key = String(entry?.key || '').trim()
          if (!key) continue
          const remark = String(entry?.remark || '').trim()
          const status = String(entry?.status || 'active').trim()
          if (remark) {
            try {
              await api(`/admin/upstream-keys/${encodeURIComponent(vendorID)}/remark`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ key, remark })
              })
              remarksApplied += 1
            } catch (err) {
              // Remark failures are non-fatal; the key is already added.
            }
          }
          if (status === 'disabled_manual') {
            const reason = String(entry?.disable_reason || 'imported from backup').trim()
            try {
              await api(`/admin/upstream-keys/${encodeURIComponent(vendorID)}/disable`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ keys: [key], reason })
              })
              disabledApplied += 1
            } catch (err) {
              // ignore: key stays active, which is a safe superset
            }
          } else if (status === 'active') {
            try {
              await api(`/admin/upstream-keys/${encodeURIComponent(vendorID)}/enable`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ keys: [key] })
              })
              enabledApplied += 1
            } catch (err) {
              // ignore: freshly added keys are already active
            }
          }
        }
        report.vendors.push({
          vendor_id: vendorID,
          added,
          remarks: remarksApplied,
          disabled: disabledApplied,
          enabled: enabledApplied,
          failed: ''
        })
        report.totalAdded += added
      }
      await refreshAll(selectedVendorID, selectedKeyVendorID || selectedVendorID)
      const parts = []
      parts.push(`已导入 ${report.totalAdded} 条密钥`)
      if (report.totalSkipped > 0) parts.push(`跳过 ${report.totalSkipped} 条（供应商不存在）`)
      if (report.vendors.length) parts.push(`覆盖 ${report.vendors.length} 个供应商`)
      setStatus('success', parts.join(' · '))
      return report
    } catch (err) {
      setStatus('error', String(err?.message || err))
      report.ok = false
      report.fatalError = String(err?.message || err)
      return report
    } finally {
      setBusy(false)
    }
  }

  const saveSystem = async () => {
    setBusy(true)
    try {
      const next = clone(rawConfig)
      next.server.listen = systemForm.listen.trim()
      next.server.read_timeout = parseDurationToNs(systemForm.readTimeout, next.server.read_timeout)
      next.server.write_timeout = parseDurationToNs(systemForm.writeTimeout, next.server.write_timeout)
      next.server.idle_timeout = parseDurationToNs(systemForm.idleTimeout, next.server.idle_timeout)
      next.server.shutdown_timeout = parseDurationToNs(systemForm.shutdownTimeout, next.server.shutdown_timeout)
      next.admin.enabled = !!systemForm.adminEnabled
      next.admin.username = systemForm.adminUsername.trim()
      next.admin.session_ttl = parseDurationToNs(systemForm.adminSessionTTL, next.admin.session_ttl)
      next.admin.audit_log_path = systemForm.auditLogPath.trim()
      next.admin.allowed_cidrs = textToList(systemForm.adminAllowedCIDRsText)
      next.admin.trusted_proxy_cidrs = textToList(systemForm.adminTrustedProxyCIDRsText)

      await api('/admin/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(next)
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', '系统配置已更新')
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const saveRaw = async () => {
    setBusy(true)
    try {
      const next = JSON.parse(rawConfigText)
      await api('/admin/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(next)
      })
      await refreshAll(selectedVendorID, selectedKeyVendorID)
      setStatus('success', '高级 JSON 已保存')
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const rotatePassword = async () => {
    const next = newPassword.trim()
    if (!next) {
      setStatus('warn', '请输入新密码')
      return
    }
    setBusy(true)
    try {
      await api('/admin/password', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: next })
      })
      setNewPassword('')
      clearSession('管理员密码已轮换，请使用新密码重新登录')
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const verifySession = async () => {
    setBusy(true)
    try {
      const payload = await api('/admin/me')
      setMe(payload || { username: '' })
      setStatus('success', '会话状态正常')
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const loadVendorTestMeta = async (vendorID, options = {}) => {
    const id = String(vendorID || '').trim()
    if (!id) return null
    const { silent = true, touchBusy = false } = options
    if (touchBusy) setBusy(true)
    try {
      const data = await api(`/admin/vendors/${encodeURIComponent(id)}/test-meta`)
      if (!silent) setStatus('success', '已加载测试配置')
      return data
    } catch (err) {
      if (!silent) setStatus('error', String(err?.message || err))
      throw err
    } finally {
      if (touchBusy) setBusy(false)
    }
  }

  const runVendorTest = async (vendorID, payload, options = {}) => {
    const id = String(vendorID || '').trim()
    if (!id) return null
    const { silent = false, touchBusy = true } = options
    if (touchBusy) setBusy(true)
    try {
      const data = await api(`/admin/vendors/${encodeURIComponent(id)}/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload || {})
      })
      if (!silent) setStatus('success', '测试请求已完成')
      return data
    } catch (err) {
      if (!silent) setStatus('error', String(err?.message || err))
      throw err
    } finally {
      if (touchBusy) setBusy(false)
    }
  }

  useEffect(() => {
    if (!token) return
    refreshAll().catch(() => {})
  }, [token])

  useEffect(() => {
    if (!isAuthed || nav !== 'keyHub') return undefined
    loadStats(true, false).catch(() => {})
    return undefined
  }, [isAuthed, nav])

  useEffect(() => {
    if (!isAuthed || nav !== 'keyHub' || !autoRefreshStats) return undefined
    const interval = window.setInterval(() => {
      loadStats(true, false).catch(() => {})
    }, Number(refreshEverySec) * 1000)
    return () => window.clearInterval(interval)
  }, [isAuthed, nav, autoRefreshStats, refreshEverySec])

  const selectedVendorName = useMemo(() => vendorNameByID(rawConfig, selectedVendorID), [rawConfig, selectedVendorID])

  const vendorRows = useMemo(() => {
    const keySummaryMap = Object.fromEntries((upstreamKeysData.vendors || []).map((item) => [item.vendor_id, item]))
    return vendorList(rawConfig)
      .map((entry) => {
        const runtimeKeys = stats.vendors?.[entry.id] || []
        const keySummary = keySummaryMap[entry.id] || {}
        return {
          id: entry.id,
          name: entry.name,
          provider: entry.provider || 'generic',
          upstreamKeys: keySummary.count || 0,
          activeUpstreamKeys: keySummary.active_count || 0,
          disabledUpstreamKeys: keySummary.disabled_count || 0,
          clientKeys: entry.client_auth?.keys?.length || 0,
          clientAuthEnabled: !!entry.client_auth?.enabled,
          resinEnabled: !!entry.resin?.enabled,
          aggregateChildCount: entry.aggregate?.children?.length || 0,
          backoff: runtimeKeys.filter((item) => Number(item.backoff_remaining_seconds || 0) > 0).length,
          inflight: runtimeKeys.reduce((sum, item) => sum + Number(item.inflight || 0), 0)
        }
      })
      .sort((a, b) => a.name.localeCompare(b.name))
  }, [rawConfig, upstreamKeysData, stats])

  const overviewMetrics = useMemo(() => {
    let clientKeys = 0
    let resinEnabled = 0
    let clientAuthEnabled = 0
    for (const vendor of vendorList(rawConfig)) {
      clientKeys += vendor.client_auth?.keys?.length || 0
      if (vendor.resin?.enabled) resinEnabled += 1
      if (vendor.client_auth?.enabled) clientAuthEnabled += 1
    }

    let inflight = 0
    let backoffKeys = 0
    let warningKeys = 0
    for (const keys of Object.values(stats.vendors || {})) {
      for (const key of keys || []) {
        inflight += Number(key.inflight || 0)
        if (Number(key.backoff_remaining_seconds || 0) > 0) backoffKeys += 1
        if (Number(key.failures || 0) > 0) warningKeys += 1
      }
    }

    const upstreamKeys = (upstreamKeysData.vendors || []).reduce((sum, item) => sum + Number(item.count || 0), 0)

    return {
      vendors: vendorList(rawConfig).length,
      upstreamKeys,
      clientKeys,
      resinEnabled,
      clientAuthEnabled,
      inflight,
      backoffKeys,
      warningKeys
    }
  }, [rawConfig, upstreamKeysData, stats])

  return {
    auth: {
      token,
      username,
      password,
      busy,
      notice,
      me,
      lastSyncAt,
      isAuthed,
      setUsername,
      setPassword,
      login,
      logout,
      verifySession
    },
    shell: {
      nav,
      setNav,
      navItems: NAV_ITEMS,
      currentPageMeta
    },
    overview: {
      vendorRows,
      overviewMetrics
    },
    config: {
      rawConfig,
      maskedConfig,
      rawConfigText,
      setRawConfigText,
      selectedVendorID,
      selectedVendorName,
      renameDraft,
      setRenameDraft,
      vendorDraft,
      invalidKeyStatusCodesText,
      invalidKeyKeywordsText,
      responseRuleRows,
      maskingRuleRows,
      failoverResponseStatusCodesText,
      aggregateRetryStatusCodesText,
      upstreamResponseHeaderTimeoutText,
      upstreamBodyTimeoutText,
      upstreamInterimResponseIntervalText,
      clientHeaderPreset,
      allowlistText,
      dropHeadersText,
      injectRows,
      rewriteRows,
      newVendorForm,
      systemForm,
      setInvalidKeyStatusCodesText,
      setInvalidKeyKeywordsText,
      setResponseRuleRows,
      setMaskingRuleRows,
      setFailoverResponseStatusCodesText,
      setAggregateRetryStatusCodesText,
      setUpstreamResponseHeaderTimeoutText,
      setUpstreamBodyTimeoutText,
      setUpstreamInterimResponseIntervalText,
      setClientHeaderPreset,
      setAllowlistText,
      setDropHeadersText,
      setInjectRows,
      setRewriteRows,
      setNewVendorForm,
      setSystemForm,
      selectVendor,
      mutateVendorDraft,
      saveVendor,
      createVendor,
      renameVendor,
      deleteVendor,
      saveSystem,
      saveRaw
    },
    upstream: {
      upstreamKeysData,
      selectedKeyVendorID,
      selectedKeyVendorName: vendorNameByID(rawConfig, selectedKeyVendorID),
      showSecrets,
      setShowSecrets,
      selectKeyVendor,
      addUpstreamKeys,
      disableUpstreamKey,
      disableUpstreamKeys,
      enableUpstreamKey,
      enableUpstreamKeys,
      recoverUpstreamKey,
      recoverUpstreamKeys,
      deleteUpstreamKey,
      deleteUpstreamKeys,
      setUpstreamKeyRemark,
      exportUpstreamKeysBackup,
      importUpstreamKeysBackup
    },
    statsView: {
      stats,
      statsResult,
      statsFilters,
      autoRefreshStats,
      refreshEverySec,
      setStatsFilters,
      setAutoRefreshStats,
      setRefreshEverySec,
      loadStats,
      loadFilteredStats
    },
    security: {
      newPassword,
      setNewPassword,
      rotatePassword
    },
    vendorTest: {
      loadVendorTestMeta,
      runVendorTest
    },
    actions: {
      refreshAll
    }
  }
}
