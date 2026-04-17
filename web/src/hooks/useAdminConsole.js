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
  clone,
  listToText,
  mapToRows,
  normalizeKeys,
  nsToText,
  parseDurationToNs,
  pickName,
  rowsToMap,
  textToList,
  withVendorDefaults
} from '../app/utils'

function buildErrorPolicyDurationTexts(policy) {
  const cooldown = policy?.cooldown || {}
  return {
    requestError: nsToText(cooldown.request_error?.duration || 0),
    unauthorized: nsToText(cooldown.unauthorized?.duration || 0),
    paymentRequired: nsToText(cooldown.payment_required?.duration || 0),
    forbidden: nsToText(cooldown.forbidden?.duration || 0),
    rateLimit: nsToText(cooldown.rate_limit?.duration || 0),
    serverError: nsToText(cooldown.server_error?.duration || 0),
    openAISlowDown: nsToText(cooldown.openai_slow_down?.duration || 0)
  }
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
  const vendor = String(query.vendor || '').trim()
  const filter = String(query.filter || 'all').trim()
  const keyword = String(query.q || '').trim()

  if (vendor) params.set('vendor', vendor)
  if (filter && filter !== 'all') params.set('filter', filter)
  if (keyword) params.set('q', keyword)
  params.set('page', String(Math.max(1, Number(query.page) || 1)))
  params.set('page_size', String(Math.max(1, Number(query.pageSize) || 50)))

  return `/admin/stats?${params.toString()}`
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
    vendor: '',
    filter: 'all',
    q: '',
    page: 1,
    pageSize: 50
  })

  const [selectedVendor, setSelectedVendor] = useState('')
  const [vendorDraft, setVendorDraft] = useState(null)
  const [vendorBackoffDuration, setVendorBackoffDuration] = useState('3h')
  const [errorPolicyDurations, setErrorPolicyDurations] = useState(buildErrorPolicyDurationTexts(buildNewVendorConfig('').error_policy))
  const [allowlistText, setAllowlistText] = useState('')
  const [injectRows, setInjectRows] = useState([{ key: '', value: '' }])
  const [rewriteRows, setRewriteRows] = useState([{ key: '', value: '' }])

  const [selectedKeyVendor, setSelectedKeyVendor] = useState('')
  const [showSecrets, setShowSecrets] = useState(false)

  const [newVendorForm, setNewVendorForm] = useState({ name: '', baseURL: '' })
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

  const syncVendorDraft = (cfg, vendorName) => {
    const vendor = cfg?.vendors?.[vendorName]
    if (!vendor) {
      setVendorDraft(null)
      setVendorBackoffDuration('3h')
      setErrorPolicyDurations(buildErrorPolicyDurationTexts(buildNewVendorConfig('').error_policy))
      setAllowlistText('')
      setInjectRows([{ key: '', value: '' }])
      setRewriteRows([{ key: '', value: '' }])
      return
    }
    const draft = withVendorDefaults(vendor)
    setVendorDraft(draft)
    setVendorBackoffDuration(nsToText(draft.backoff?.duration || 0))
    setErrorPolicyDurations(buildErrorPolicyDurationTexts(draft.error_policy))
    setAllowlistText(listToText(draft.client_headers?.allowlist || []))
    setInjectRows(mapToRows(draft.inject_headers || {}))
    setRewriteRows(mapToRows(draft.path_rewrites || {}))
  }

  const selectVendor = (vendorName) => {
    setSelectedVendor(vendorName)
    syncVendorDraft(rawConfig, vendorName)
  }

  const selectKeyVendor = (vendorName) => {
    setSelectedKeyVendor(vendorName)
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

    if (!nextQuery.vendor) {
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

  const refreshAll = async (preferredVendor = '', preferredKeyVendor = '') => {
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

      const vendorNames = Object.keys(raw?.vendors || {}).sort()
      const nextVendor = pickName(vendorNames, preferredVendor, selectedVendor)
      setSelectedVendor(nextVendor)
      syncVendorDraft(raw || EMPTY_CONFIG, nextVendor)
      setStatsFilters((prev) => {
        const nextStatsVendor = pickName(vendorNames, preferredVendor || nextVendor, prev.vendor)
        return {
          ...prev,
          vendor: nextStatsVendor,
          page: nextStatsVendor === prev.vendor ? prev.page : 1
        }
      })

      const upstreamVendorNames = (upstreamKeys?.vendors || []).map((item) => item.vendor)
      const nextKeyVendor = pickName(upstreamVendorNames, preferredKeyVendor || nextVendor, selectedKeyVendor)
      setSelectedKeyVendor(nextKeyVendor)

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
    if (!selectedVendor || !vendorDraft) return
    setBusy(true)
    try {
      const next = clone(vendorDraft)
      next.backoff.duration = parseDurationToNs(vendorBackoffDuration, next.backoff.duration)
      next.error_policy.cooldown.request_error.duration = parseDurationToNs(errorPolicyDurations.requestError, next.error_policy.cooldown.request_error.duration)
      next.error_policy.cooldown.unauthorized.duration = parseDurationToNs(errorPolicyDurations.unauthorized, next.error_policy.cooldown.unauthorized.duration)
      next.error_policy.cooldown.payment_required.duration = parseDurationToNs(errorPolicyDurations.paymentRequired, next.error_policy.cooldown.payment_required.duration)
      next.error_policy.cooldown.forbidden.duration = parseDurationToNs(errorPolicyDurations.forbidden, next.error_policy.cooldown.forbidden.duration)
      next.error_policy.cooldown.rate_limit.duration = parseDurationToNs(errorPolicyDurations.rateLimit, next.error_policy.cooldown.rate_limit.duration)
      next.error_policy.cooldown.server_error.duration = parseDurationToNs(errorPolicyDurations.serverError, next.error_policy.cooldown.server_error.duration)
      next.error_policy.cooldown.openai_slow_down.duration = parseDurationToNs(errorPolicyDurations.openAISlowDown, next.error_policy.cooldown.openai_slow_down.duration)
      next.client_headers.allowlist = textToList(allowlistText)
      next.inject_headers = rowsToMap(injectRows)
      next.path_rewrites = rowsToMap(rewriteRows)
      next.client_auth.keys = normalizeKeys(next.client_auth.keys || [])
      if (next.client_auth.keys.length === 0) next.client_auth.enabled = false

      await api('/admin/vendors', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ vendor: selectedVendor, config: next })
      })
      await refreshAll(selectedVendor, selectedKeyVendor || selectedVendor)
      setStatus('success', `供应商 ${selectedVendor} 已保存`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const createVendor = async () => {
    const name = newVendorForm.name.trim()
    const baseURL = newVendorForm.baseURL.trim()
    if (!name) {
      setStatus('warn', '请输入供应商名称')
      return
    }
    if (!baseURL) {
      setStatus('warn', '请输入上游 base_url')
      return
    }
    setBusy(true)
    try {
      await api('/admin/vendors', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ vendor: name, config: buildNewVendorConfig(baseURL) })
      })
      setNewVendorForm({ name: '', baseURL: '' })
      setNav('vendors')
      await refreshAll(name, name)
      setStatus('success', `供应商 ${name} 已创建，可前往上游密钥页面补充配置`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const deleteVendor = async () => {
    if (!selectedVendor) return
    if (!window.confirm(`确认删除供应商 ${selectedVendor} 吗？对应的上游密钥也会一并移除。`)) return
    setBusy(true)
    try {
      await api(`/admin/vendors/${encodeURIComponent(selectedVendor)}`, { method: 'DELETE' })
      await refreshAll('', '')
      setStatus('success', `供应商 ${selectedVendor} 已删除`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const addUpstreamKeys = async (keys) => {
    if (!selectedKeyVendor) {
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
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendor)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keys: nextKeys })
      })
      await refreshAll(selectedVendor, selectedKeyVendor)
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
    if (!selectedKeyVendor || !key) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendor)}/disable`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key, reason: 'manually disabled' })
      })
      await refreshAll(selectedVendor, selectedKeyVendor)
      setStatus('success', `密钥已禁用`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const enableUpstreamKey = async (key) => {
    if (!selectedKeyVendor || !key) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendor)}/enable`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ key })
      })
      await refreshAll(selectedVendor, selectedKeyVendor)
      setStatus('success', `密钥已启用`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
    } finally {
      setBusy(false)
    }
  }

  const deleteUpstreamKey = async (key) => {
    if (!selectedKeyVendor || !key) return
    if (!window.confirm('确认删除这个上游密钥吗？')) return
    setBusy(true)
    try {
      await api(`/admin/upstream-keys/${encodeURIComponent(selectedKeyVendor)}`, {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keys: [key] })
      })
      await refreshAll(selectedVendor, selectedKeyVendor)
      setStatus('success', `密钥已删除`)
    } catch (err) {
      setStatus('error', String(err?.message || err))
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
      await refreshAll(selectedVendor, selectedKeyVendor)
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
      await refreshAll(selectedVendor, selectedKeyVendor)
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

  useEffect(() => {
    if (!token) return
    refreshAll().catch(() => {})
  }, [token])

  useEffect(() => {
    if (!isAuthed || nav !== 'keyHub' || !statsFilters.vendor) return undefined
    loadFilteredStats({}, true, false).catch(() => {})
    return undefined
  }, [isAuthed, nav, statsFilters.vendor, statsFilters.filter, statsFilters.q, statsFilters.page, statsFilters.pageSize])

  useEffect(() => {
    if (!isAuthed || nav !== 'keyHub' || !autoRefreshStats || !statsFilters.vendor) return undefined
    const interval = window.setInterval(() => {
      loadFilteredStats({}, true, false).catch(() => {})
    }, Number(refreshEverySec) * 1000)
    return () => window.clearInterval(interval)
  }, [isAuthed, nav, autoRefreshStats, refreshEverySec, statsFilters.vendor, statsFilters.filter, statsFilters.q, statsFilters.page, statsFilters.pageSize])

  const vendorRows = useMemo(() => {
    const keyCountMap = Object.fromEntries((upstreamKeysData.vendors || []).map((item) => [item.vendor, item.count]))
    return Object.keys(rawConfig.vendors || {})
      .sort()
      .map((name) => {
        const vendor = rawConfig.vendors[name] || {}
        const runtimeKeys = stats.vendors?.[name] || []
        return {
          name,
          upstreamKeys: keyCountMap[name] || 0,
          clientKeys: vendor.client_auth?.keys?.length || 0,
          clientAuthEnabled: !!vendor.client_auth?.enabled,
          resinEnabled: !!vendor.resin?.enabled,
          backoff: runtimeKeys.filter((item) => Number(item.backoff_remaining_seconds || 0) > 0).length,
          inflight: runtimeKeys.reduce((sum, item) => sum + Number(item.inflight || 0), 0)
        }
      })
  }, [rawConfig, upstreamKeysData, stats])

  const overviewMetrics = useMemo(() => {
    let clientKeys = 0
    let resinEnabled = 0
    let clientAuthEnabled = 0
    for (const vendor of Object.values(rawConfig.vendors || {})) {
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
      vendors: Object.keys(rawConfig.vendors || {}).length,
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
      selectedVendor,
      vendorDraft,
      vendorBackoffDuration,
      errorPolicyDurations,
      allowlistText,
      injectRows,
      rewriteRows,
      newVendorForm,
      systemForm,
      setVendorBackoffDuration,
      setErrorPolicyDurations,
      setAllowlistText,
      setInjectRows,
      setRewriteRows,
      setNewVendorForm,
      setSystemForm,
      selectVendor,
      mutateVendorDraft,
      saveVendor,
      createVendor,
      deleteVendor,
      saveSystem,
      saveRaw
    },
    upstream: {
      upstreamKeysData,
      selectedKeyVendor,
      showSecrets,
      setShowSecrets,
      selectKeyVendor,
      addUpstreamKeys,
      disableUpstreamKey,
      enableUpstreamKey,
      deleteUpstreamKey
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
    actions: {
      refreshAll
    }
  }
}
