import { useEffect, useState } from 'react'

import { CLIENT_HEADER_PRESET_OPTIONS, CLIENT_HEADER_PRESET_PREVIEWS, DEFAULT_CLIENT_HEADER_DROP_PREVIEW } from '../app/constants'
import { buildVendorRequestEndpoint, buttonClass, clone, generateClientKey, normalizeKeys, panelClass, parseKeysText, recommendedClientHeaderPreset } from '../app/utils'
import { MapRowsEditor } from '../components/MapRowsEditor'

const DEFAULT_EXPANDED_SECTIONS = {
  clientKeys: true,
  upstreamConfig: true,
  errorPolicy: true,
  resin: false,
  clientHeaders: true,
  requestTransforms: false
}

function countTextItems(text) {
  return String(text || '')
    .split(/[\r\n,，]+/)
    .map((item) => item.trim())
    .filter(Boolean)
    .length
}

function countFilledRows(rows, fields = ['key', 'value']) {
  return (rows || []).filter((row) => fields.some((field) => String(row?.[field] || '').trim())).length
}

function countConfiguredResponseRules(rows) {
  return (rows || []).filter((row) => (
    String(row?.statusCodesText || '').trim() ||
    String(row?.keywordsText || '').trim() ||
    String(row?.durationText || '').trim() ||
    String(row?.retryAfter || '').trim()
  )).length
}

function CollapsibleSection({
  title,
  description,
  summary,
  actions,
  open,
  onToggle,
  children
}) {
  return (
    <section className="section-card">
      <div className="section-card-header gap-3">
        <button className="flex min-w-0 flex-1 items-start gap-3 text-left" type="button" onClick={onToggle} aria-expanded={open}>
          <span className={`mt-0.5 inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md border border-[var(--border)] bg-[var(--bg-surface)] text-[var(--text-secondary)] transition-transform ${open ? 'rotate-180' : ''}`}>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="6 9 12 15 18 9" />
            </svg>
          </span>
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h4>{title}</h4>
              {summary && <span className="rounded-full border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 text-[10px] font-medium text-[var(--text-muted)]">{summary}</span>}
            </div>
            {description && <p>{description}</p>}
          </div>
        </button>
        <div className="flex flex-wrap items-center gap-2">
          {actions}
          <button className={buttonClass('ghost')} type="button" onClick={onToggle}>
            {open ? '收起' : '展开'}
          </button>
        </div>
      </div>
      {open && <div className="space-y-4">{children}</div>}
    </section>
  )
}

export function VendorsPage({
  busy,
  vendorRows,
  selectedVendor,
  vendorDraft,
  upstreamKeysData,
  runtimeStats,
  invalidKeyStatusCodesText,
  invalidKeyKeywordsText,
  responseRuleRows,
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
  onSelectVendor,
  onRefresh,
  onNewVendorFormChange,
  onCreateVendor,
  onOpenUpstreamKeys,
  onSaveVendor,
  onDeleteVendor,
  onMutateVendorDraft,
  onInvalidKeyStatusCodesTextChange,
  onInvalidKeyKeywordsTextChange,
  setResponseRuleRows,
  onFailoverResponseStatusCodesTextChange,
  onAggregateRetryStatusCodesTextChange,
  onUpstreamResponseHeaderTimeoutTextChange,
  onUpstreamBodyTimeoutTextChange,
  onUpstreamInterimResponseIntervalTextChange,
  onClientHeaderPresetChange,
  onAllowlistTextChange,
  onDropHeadersTextChange,
  setInjectRows,
  setRewriteRows
}) {
  const requestEndpoint = buildVendorRequestEndpoint(selectedVendor)
  const clientKeys = normalizeKeys(vendorDraft?.client_auth?.keys || [])
  const selectedPresetOption = CLIENT_HEADER_PRESET_OPTIONS.find((option) => option.value === clientHeaderPreset) || CLIENT_HEADER_PRESET_OPTIONS[0]
  const presetPreviewHeaders = CLIENT_HEADER_PRESET_PREVIEWS[clientHeaderPreset] || []
  const [clientKeyInputText, setClientKeyInputText] = useState('')
  const [expandedSections, setExpandedSections] = useState(DEFAULT_EXPANDED_SECTIONS)
  const [vendorSearchQuery, setVendorSearchQuery] = useState('')
  const [keyPicker, setKeyPicker] = useState(null)
  const allowlistCount = countTextItems(allowlistText)
  const dropHeadersCount = countTextItems(dropHeadersText)
  const responseRuleCount = countConfiguredResponseRules(responseRuleRows)
  const injectHeaderCount = countFilledRows(injectRows, ['key', 'value'])
  const rewriteRuleCount = countFilledRows(rewriteRows, ['key', 'value'])

  const filteredVendorRows = (vendorRows || []).filter(row => 
    row.name.toLowerCase().includes(vendorSearchQuery.toLowerCase()) || 
    (row.provider && row.provider.toLowerCase().includes(vendorSearchQuery.toLowerCase()))
  )
  const aggregateVendorRows = filteredVendorRows.filter((row) => row.provider === 'aggregate')
  const independentVendorRows = filteredVendorRows.filter((row) => row.provider !== 'aggregate')
  const aggregateVendorCount = (vendorRows || []).filter((row) => row.provider === 'aggregate').length
  const independentVendorCount = (vendorRows || []).length - aggregateVendorCount

  useEffect(() => {
    setClientKeyInputText('')
    setKeyPicker(null)
  }, [selectedVendor])

  const toggleSection = (sectionKey) => {
    setExpandedSections((prev) => ({ ...prev, [sectionKey]: !prev[sectionKey] }))
  }

  const updateClientKeys = (keys) => {
    const nextKeys = normalizeKeys(keys)
    onMutateVendorDraft((draft) => {
      const hadKeys = normalizeKeys(draft.client_auth.keys || []).length > 0
      draft.client_auth.keys = nextKeys
      if (!nextKeys.length) {
        draft.client_auth.enabled = false
      } else if (!hadKeys) {
        draft.client_auth.enabled = true
      }
    })
  }

  const handleGenerateClientKey = () => {
    const nextKey = generateClientKey()
    onMutateVendorDraft((draft) => {
      draft.client_auth.keys = normalizeKeys([...(draft.client_auth.keys || []), nextKey])
      draft.client_auth.enabled = true
    })
  }

  const copyRequestEndpoint = async () => {
    if (!requestEndpoint || typeof navigator?.clipboard?.writeText !== 'function') return
    try {
      await navigator.clipboard.writeText(requestEndpoint)
    } catch (_) {
      // ignore clipboard failures in unsupported contexts
    }
  }

  const copyClientKey = async (key) => {
    if (!key || typeof navigator?.clipboard?.writeText !== 'function') return
    try {
      await navigator.clipboard.writeText(key)
    } catch (_) {
      // ignore clipboard failures in unsupported contexts
    }
  }

  const removeClientKey = (targetKey) => {
    updateClientKeys(clientKeys.filter((key) => key !== targetKey))
  }

  const importClientKeys = () => {
    const incoming = parseKeysText(clientKeyInputText)
    if (!incoming.length) return
    updateClientKeys([...(clientKeys || []), ...incoming])
    setClientKeyInputText('')
  }

  const updateResponseRuleRow = (index, patch) => {
    setResponseRuleRows((prev) => {
      const next = clone(prev || [])
      next[index] = { ...next[index], ...patch }
      return next
    })
  }

  const addResponseRuleRow = () => {
    setResponseRuleRows((prev) => [...(prev || []), { statusCodesText: '', keywordsText: '', durationText: '', retryAfter: '' }])
  }

  const removeResponseRuleRow = (index) => {
    setResponseRuleRows((prev) => {
      const next = (prev || []).filter((_, idx) => idx !== index)
      return next.length ? next : [{ statusCodesText: '', keywordsText: '', durationText: '', retryAfter: '' }]
    })
  }

  const restoreDefaultHeaderPolicy = () => {
    const nextPreset = recommendedClientHeaderPreset(vendorDraft?.provider)
    onMutateVendorDraft((draft) => {
      draft.client_headers.preset = nextPreset
      draft.client_headers.allowlist = []
      draft.client_headers.drop = []
    })
    onClientHeaderPresetChange(nextPreset)
    onAllowlistTextChange('')
    onDropHeadersTextChange('')
  }

  const isAggregate = vendorDraft?.provider === 'aggregate'
  const aggregateChildren = vendorDraft?.aggregate?.children || []
  const aggregateRetry = vendorDraft?.aggregate?.retry || {}
  const vendorRowByName = new Map((vendorRows || []).map((row) => [row.name, row]))
  const runtimeKeyByVendor = new Map(Object.entries(runtimeStats?.vendors || {}).map(([vendor, items]) => {
    const keys = new Map()
    for (const item of items || []) {
      if (item.key_id) keys.set(item.key_id, item)
      if (item.key_masked) keys.set(item.key_masked, item)
    }
    return [vendor, keys]
  }))

  const aggregateKeyState = (vendor, item) => {
    const runtime = runtimeKeyByVendor.get(vendor)?.get(item.key_id) || runtimeKeyByVendor.get(vendor)?.get(item.masked) || {}
    const status = String(runtime.status || item.status || 'active')
    const backoff = Number(runtime.backoff_remaining_seconds || 0)
    return {
      available: status === 'active' && backoff <= 0,
      status,
      backoff
    }
  }

  const aggregateChildHealth = (child) => {
    const row = vendorRowByName.get(child?.vendor)
    if (!child?.vendor || !row) {
      return { tone: 'muted', label: '未选择' }
    }
    const selectedIDs = Array.isArray(child.key_ids) ? child.key_ids : []
    if (selectedIDs.length > 0) {
      const selectedSet = new Set(selectedIDs)
      const selectedStates = (upstreamKeysData?.items?.[child.vendor] || [])
        .filter((item) => selectedSet.has(item.key_id))
        .map((item) => aggregateKeyState(child.vendor, item))
      const available = selectedStates.filter((state) => state.available).length
      const backingOff = selectedStates.some((state) => state.status === 'active' && state.backoff > 0)
      return {
        tone: available === 0 ? (backingOff ? 'warn' : 'disabled') : (available < selectedIDs.length ? 'warn' : 'ok'),
        label: `可用 ${available}/${selectedIDs.length}`
      }
    }
    if (Number(row.upstreamKeys || 0) === 0) {
      return { tone: 'warn', label: '无密钥' }
    }
    if (Number(row.activeUpstreamKeys || 0) === 0 && Number(row.disabledUpstreamKeys || 0) > 0) {
      return { tone: 'disabled', label: '已禁用' }
    }
    if (Number(row.backoff || 0) > 0) {
      return { tone: 'warn', label: `退避 ${row.backoff}` }
    }
    return { tone: 'ok', label: `启用 ${row.activeUpstreamKeys || 0}` }
  }

  const addAggregateChild = () => {
    onMutateVendorDraft((draft) => {
      if (!draft.aggregate) draft.aggregate = { children: [] }
      if (!draft.aggregate.children) draft.aggregate.children = []
      draft.aggregate.children.push({ vendor: '', weight: 1, priority: 0, key_ids: [] })
    })
  }

  const removeAggregateChild = (index) => {
    onMutateVendorDraft((draft) => {
      if (draft.aggregate?.children) draft.aggregate.children.splice(index, 1)
    })
  }

  const updateAggregateChild = (index, field, value) => {
    onMutateVendorDraft((draft) => {
      if (draft.aggregate?.children?.[index]) draft.aggregate.children[index][field] = value
    })
  }

  const openAggregateKeyPicker = (childIndex, child) => {
    setKeyPicker({
      childIndex,
      vendor: child.vendor,
      query: '',
      page: 1,
      selected: Array.isArray(child.key_ids) ? [...child.key_ids] : []
    })
  }

  const togglePickerKey = (keyID) => {
    setKeyPicker((current) => {
      if (!current) return current
      const selected = new Set(current.selected)
      if (selected.has(keyID)) selected.delete(keyID)
      else selected.add(keyID)
      return { ...current, selected: Array.from(selected) }
    })
  }

  const pickerAllItems = keyPicker ? (upstreamKeysData?.items?.[keyPicker.vendor] || []) : []
  const pickerQuery = keyPicker?.query.trim().toLowerCase() || ''
  const pickerFilteredItems = pickerAllItems.filter((item) => !pickerQuery || [item.masked, item.remark, item.key_id, item.status]
    .filter(Boolean)
    .join(' ')
    .toLowerCase()
    .includes(pickerQuery))
  const pickerPageSize = 40
  const pickerTotalPages = Math.max(1, Math.ceil(pickerFilteredItems.length / pickerPageSize))
  const pickerCurrentPage = Math.min(keyPicker?.page || 1, pickerTotalPages)
  const pickerPageItems = pickerFilteredItems.slice((pickerCurrentPage - 1) * pickerPageSize, pickerCurrentPage * pickerPageSize)
  const pickerKnownIDs = new Set(pickerAllItems.map((item) => item.key_id))
  const pickerMissingSelectedCount = (keyPicker?.selected || []).filter((keyID) => !pickerKnownIDs.has(keyID)).length

  const selectPickerResults = () => {
    setKeyPicker((current) => {
      if (!current) return current
      const selected = new Set(current.selected)
      for (const item of pickerFilteredItems) selected.add(item.key_id)
      return { ...current, selected: Array.from(selected) }
    })
  }

  const saveAggregateKeyPicker = () => {
    if (!keyPicker) return
    const child = aggregateChildren[keyPicker.childIndex]
    if (child?.vendor === keyPicker.vendor) {
      updateAggregateChild(keyPicker.childIndex, 'key_ids', keyPicker.selected)
    }
    setKeyPicker(null)
  }

  const updateAggregateRetry = (field, value) => {
    onMutateVendorDraft((draft) => {
      if (!draft.aggregate) draft.aggregate = { children: [] }
      if (!draft.aggregate.retry) draft.aggregate.retry = {}
      draft.aggregate.retry[field] = value
    })
  }

  const renderVendorItem = (row) => {
    const aggregate = row.provider === 'aggregate'
    const state = aggregate
      ? (Number(row.aggregateChildCount || 0) > 0 ? 'ready' : 'warning')
      : (Number(row.activeUpstreamKeys || 0) > 0 ? 'ready' : (Number(row.upstreamKeys || 0) > 0 ? 'disabled' : 'warning'))
    return (
      <button
        key={row.name}
        className={`vendor-item vendor-item-${aggregate ? 'aggregate' : 'independent'} ${selectedVendor === row.name ? 'vendor-item-active' : ''}`}
        onClick={() => onSelectVendor(row.name)}
      >
        <span className="vendor-item-heading">
          <i className={`vendor-state-dot vendor-state-dot-${state}`} aria-hidden="true" />
          <strong>{row.name}</strong>
          {!aggregate && <em>{row.provider}</em>}
        </span>
        <span>
          {aggregate
            ? `${row.aggregateChildCount || 0} 个子供应商`
            : `Key ${row.activeUpstreamKeys || 0}/${row.upstreamKeys || 0} · 客户端 ${row.clientKeys || 0}`}
        </span>
      </button>
    )
  }

  return (
    <section className="grid gap-5 xl:grid-cols-[300px_minmax(0,1fr)] animate-fade-in">
      {/* Sidebar */}
      <aside className={panelClass('p-4')}>
        <div className="mb-4 flex items-center justify-between gap-3">
          <div>
            <h3 className="section-title">供应商</h3>
            <div className="mt-1 flex items-center gap-2 text-[10px] text-[var(--text-muted)]">
              <span>聚合 {aggregateVendorCount}</span>
              <span className="text-[var(--border-strong)]">/</span>
              <span>独立 {independentVendorCount}</span>
            </div>
          </div>
          <button className={buttonClass('ghost')} disabled={busy} onClick={onRefresh}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 2v6h-6" /><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M3 22v-6h6" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
            </svg>
          </button>
        </div>

        <div className="mb-3">
          <input
            type="text"
            className="input-base w-full text-sm"
            placeholder="搜索供应商..."
            value={vendorSearchQuery}
            onChange={(e) => setVendorSearchQuery(e.target.value)}
          />
        </div>

        <div className="vendor-list max-h-[420px] overflow-y-auto pr-1 custom-scrollbar xl:max-h-[calc(100vh-310px)]">
          {(aggregateVendorRows.length > 0 || !vendorSearchQuery) && (
            <section className="vendor-list-group vendor-list-group-aggregate">
              <div className="vendor-list-group-header">
                <span><i aria-hidden="true" />聚合供应商</span>
                <strong>{aggregateVendorRows.length}</strong>
              </div>
              <div className="space-y-2">
                {aggregateVendorRows.map(renderVendorItem)}
                {aggregateVendorRows.length === 0 && <p className="vendor-list-empty">暂无聚合供应商</p>}
              </div>
            </section>
          )}

          {(independentVendorRows.length > 0 || !vendorSearchQuery) && (
            <section className="vendor-list-group vendor-list-group-independent">
              <div className="vendor-list-group-header">
                <span><i aria-hidden="true" />独立供应商</span>
                <strong>{independentVendorRows.length}</strong>
              </div>
              <div className="space-y-2">
                {independentVendorRows.map(renderVendorItem)}
                {independentVendorRows.length === 0 && <p className="vendor-list-empty">暂无独立供应商</p>}
              </div>
            </section>
          )}
          {filteredVendorRows.length === 0 && (
            <div className="text-center text-xs text-[var(--text-muted)] py-4">未找到匹配的供应商</div>
          )}
        </div>

        {/* Create Vendor */}
        <section className="mt-4 border-t border-[var(--border)] pt-4">
          <h3 className="section-title text-sm mb-3">新建供应商</h3>
          <div className="space-y-3">
            <div className="vendor-type-segment" role="group" aria-label="供应商类型">
              <button
                type="button"
                className={newVendorForm.provider !== 'aggregate' ? 'active' : ''}
                onClick={() => onNewVendorFormChange((prev) => ({ ...prev, provider: 'generic' }))}
              >
                独立
              </button>
              <button
                type="button"
                className={newVendorForm.provider === 'aggregate' ? 'active aggregate' : ''}
                onClick={() => onNewVendorFormChange((prev) => ({ ...prev, provider: 'aggregate' }))}
              >
                聚合
              </button>
            </div>
            <input
              className="input-base w-full"
              placeholder="供应商名称，例如 openai"
              value={newVendorForm.name}
              onChange={(e) => onNewVendorFormChange((prev) => ({ ...prev, name: e.target.value }))}
            />
            {newVendorForm.provider !== 'aggregate' && (
              <input
                className="input-base w-full"
                placeholder="上游 base_url"
                value={newVendorForm.baseURL}
                onChange={(e) => onNewVendorFormChange((prev) => ({ ...prev, baseURL: e.target.value }))}
              />
            )}
            <button className={`${buttonClass('primary')} w-full`} disabled={busy} onClick={onCreateVendor}>
              创建供应商
            </button>
          </div>
        </section>
      </aside>

      {/* Main Config Area */}
      <article className={panelClass('p-5')}>
        {!vendorDraft && <p className="text-sm text-[var(--text-muted)]">请先在左侧选择一个供应商。</p>}
        {vendorDraft && (
          <div className="space-y-5">
            {/* Vendor Header */}
            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-4">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <h3 className="section-title text-base">{selectedVendor}</h3>
                  <span className={`vendor-kind-badge ${isAggregate ? 'vendor-kind-badge-aggregate' : ''}`}>
                    {isAggregate ? '聚合供应商' : '独立供应商'}
                  </span>
                </div>
                <p className="mt-1 text-xs text-[var(--text-muted)]">
                  {isAggregate
                    ? `${aggregateChildren.length} 个子供应商参与路由`
                    : `${vendorDraft.provider || 'generic'} · ${vendorRowByName.get(selectedVendor)?.activeUpstreamKeys || 0} 个可用 Key`}
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                {!isAggregate && <button className={buttonClass('ghost')} onClick={onOpenUpstreamKeys}>上游密钥 →</button>}
                <button className={buttonClass('primary')} disabled={busy} onClick={onSaveVendor}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" /><polyline points="17,21 17,13 7,13 7,21" /><polyline points="7,3 7,8 15,8" />
                  </svg>
                  保存
                </button>
                <button className={buttonClass('danger')} disabled={busy} onClick={onDeleteVendor}>删除</button>
              </div>
            </div>

            <CollapsibleSection
              title="客户端密钥"
              description="密钥管理前置到这里，支持复制、删除、批量导入和自动生成。"
              summary={`${vendorDraft.client_auth?.enabled ? '鉴权开启' : '鉴权关闭'} · ${clientKeys.length} 条`}
              open={expandedSections.clientKeys}
              onToggle={() => toggleSection('clientKeys')}
              actions={(
                <>
                  <div className="field-inline">
                    <span className="field-label">启用鉴权</span>
                    <select
                      className="select-base"
                      value={vendorDraft.client_auth?.enabled ? 'true' : 'false'}
                      onChange={(e) => onMutateVendorDraft((draft) => { draft.client_auth.enabled = e.target.value === 'true' })}
                    >
                      <option value="false">关闭</option>
                      <option value="true">开启</option>
                    </select>
                  </div>
                  <button className={buttonClass('ghost')} type="button" onClick={handleGenerateClientKey}>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <path d="M12 2v4m0 12v4M4.93 4.93l2.83 2.83m8.49 8.49l2.83 2.83M2 12h4m12 0h4M4.93 19.07l2.83-2.83m8.49-8.49l2.83-2.83" />
                    </svg>
                    自动生成
                  </button>
                  <button className={buttonClass('primary')} type="button" disabled={!clientKeyInputText.trim()} onClick={importClientKeys}>
                    导入密钥
                  </button>
                </>
              )}
            >
              <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-3">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <div className="text-sm font-medium text-[var(--text-primary)]">客户端请求端点</div>
                    <div className="mt-1 text-xs text-[var(--text-muted)]">客户端可通过 `Authorization: Bearer sk-jcp-...` 或 `X-API-Key` 调用。</div>
                  </div>
                  <button className="text-xs font-medium text-[var(--accent)] hover:text-blue-400 transition-colors" type="button" onClick={copyRequestEndpoint}>
                    复制
                  </button>
                </div>
                <div className="mt-3 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 font-mono text-xs text-[var(--text-secondary)] break-all">
                  {requestEndpoint || '--'}
                </div>
              </div>

              <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_320px]">
                <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-3">
                  <div className="mb-3 flex items-center justify-between gap-3">
                    <div className="text-sm font-medium text-[var(--text-primary)]">现有密钥</div>
                    <div className="text-xs text-[var(--text-muted)]">共 {clientKeys.length} 条</div>
                  </div>

                  <div className="space-y-2">
                    {!clientKeys.length && (
                      <div className="rounded-lg border border-dashed border-[var(--border)] px-3 py-6 text-center text-xs text-[var(--text-muted)]">
                        暂无客户端密钥，可以先自动生成一条，或在右侧批量导入。
                      </div>
                    )}

                    {clientKeys.map((key, index) => (
                      <div key={key} className="grid gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-3 md:grid-cols-[40px_minmax(0,1fr)_auto_auto] md:items-center">
                        <div className="text-xs text-[var(--text-faint)]">{index + 1}</div>
                        <div className="font-mono text-xs text-[var(--text-primary)] break-all">{key}</div>
                        <button className={buttonClass('ghost')} type="button" onClick={() => copyClientKey(key)}>
                          复制
                        </button>
                        <button className={buttonClass('danger')} type="button" onClick={() => removeClientKey(key)}>
                          删除
                        </button>
                      </div>
                    ))}
                  </div>
                </div>

                <aside className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-3">
                  <div className="text-sm font-medium text-[var(--text-primary)]">批量导入</div>
                  <p className="mt-1 text-xs text-[var(--text-muted)]">支持换行或逗号分隔，导入时会自动去重。</p>
                  <textarea
                    className="textarea-base mt-3 h-48"
                    placeholder={'sk-jcp-xxxxxxxxxxxxxxxxxxxxxxxx\nsk-jcp-yyyyyyyyyyyyyyyyyyyyyyyy'}
                    value={clientKeyInputText}
                    onChange={(e) => setClientKeyInputText(e.target.value)}
                  />
                </aside>
              </div>
            </CollapsibleSection>

            <CollapsibleSection
              title="上游配置"
              description="基础参数、鉴权方式和负载均衡统一收敛到这里。"
              summary={`${vendorDraft.provider || 'generic'} · ${vendorDraft.load_balance || 'round_robin'}`}
              open={expandedSections.upstreamConfig}
              onToggle={() => toggleSection('upstreamConfig')}
            >
              <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-4 space-y-4">
                <div>
                  <div className="text-sm font-medium text-[var(--text-primary)]">基础配置</div>
                  <div className="mt-1 text-xs text-[var(--text-muted)]">{isAggregate ? '聚合路由策略与参数。' : 'Provider、上游地址和超时设置。'}</div>
                </div>
                <div className="grid gap-4 lg:grid-cols-5">
                  <label className="field-wrap">
                    <span className="field-label">Provider</span>
                    <select
                      className="select-base"
                      value={vendorDraft.provider || 'generic'}
                      onChange={(e) => onMutateVendorDraft((draft) => { draft.provider = e.target.value })}
                    >
                      <option value="generic">generic</option>
                      <option value="openai">openai</option>
                      <option value="anthropic">anthropic</option>
                      <option value="gemini">gemini</option>
                      <option value="deepseek">deepseek</option>
                      <option value="azure_openai">azure_openai</option>
                      <option value="aggregate">aggregate (聚合)</option>
                    </select>
                  </label>
                  {!isAggregate && (
                    <label className="field-wrap">
                      <span className="field-label">上游 Base URL</span>
                      <input
                        className="input-base"
                        value={vendorDraft.upstream?.base_url || ''}
                        onChange={(e) => onMutateVendorDraft((draft) => { draft.upstream.base_url = e.target.value })}
                      />
                    </label>
                  )}
                  <label className="field-wrap">
                    <span className="field-label">{isAggregate ? '聚合分发策略' : '负载均衡策略'}</span>
                    <select
                      className="select-base"
                      value={vendorDraft.load_balance || 'round_robin'}
                      onChange={(e) => onMutateVendorDraft((draft) => { draft.load_balance = e.target.value })}
                    >
                      <option value="round_robin">round_robin</option>
                      <option value="random">random</option>
                      <option value="least_used">least_used</option>
                      <option value="least_requests">least_requests</option>
                    </select>
                  </label>
                  {isAggregate && (
                    <>
                      <label className="field-wrap">
                        <span className="field-label">启用重试</span>
                        <select
                          className="select-base"
                          value={aggregateRetry.enabled === false ? 'false' : 'true'}
                          onChange={(e) => updateAggregateRetry('enabled', e.target.value === 'true')}
                        >
                          <option value="true">开启</option>
                          <option value="false">关闭</option>
                        </select>
                      </label>
                      <label className="field-wrap">
                        <span className="field-label">最大尝试次数</span>
                        <input
                          className="input-base"
                          type="number"
                          min="1"
                          max="5"
                          placeholder="默认 2"
                          value={aggregateRetry.max_attempts || ''}
                          onChange={(e) => {
                            const v = parseInt(e.target.value, 10)
                            updateAggregateRetry('max_attempts', v > 0 ? v : 0)
                          }}
                        />
                      </label>
                      <label className="field-wrap lg:col-span-2">
                        <span className="field-label">额外重试响应码</span>
                        <input
                          className="input-base"
                          placeholder="逗号分隔，例如：408, 529"
                          value={aggregateRetryStatusCodesText}
                          onChange={(e) => onAggregateRetryStatusCodesTextChange(e.target.value)}
                        />
                      </label>
                    </>
                  )}
                  {!isAggregate && (
                    <>
                      <label className="field-wrap">
                        <span className="field-label">上游首包超时</span>
                        <input
                          className="input-base"
                          placeholder="例如 15s / 30s / 2m"
                          value={upstreamResponseHeaderTimeoutText}
                          onChange={(e) => onUpstreamResponseHeaderTimeoutTextChange(e.target.value)}
                        />
                      </label>
                      <label className="field-wrap">
                        <span className="field-label">上游 Body 超时</span>
                        <input
                          className="input-base"
                          placeholder="例如 30s / 5m / 1h"
                          value={upstreamBodyTimeoutText}
                          onChange={(e) => onUpstreamBodyTimeoutTextChange(e.target.value)}
                        />
                      </label>
                      <label className="field-wrap">
                        <span className="field-label">102 保活间隔</span>
                        <input
                          className="input-base"
                          placeholder="例如 0s / 30s / 1m"
                          value={upstreamInterimResponseIntervalText}
                          onChange={(e) => onUpstreamInterimResponseIntervalTextChange(e.target.value)}
                        />
                      </label>
                    </>
                  )}
                </div>
                {isAggregate && (
                  <div className="grid gap-3 md:grid-cols-3">
                    {[
                      ['rate_limit', '429 限流重试'],
                      ['server_error', '5xx 错误重试'],
                      ['network_error', '网络错误重试']
                    ].map(([field, label]) => (
                      <label key={field} className="flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] px-3 py-2 text-sm text-[var(--text-secondary)]">
                        <input
                          type="checkbox"
                          className="rounded border-[var(--border)] bg-transparent text-[var(--accent)]"
                          checked={aggregateRetry[field] !== false}
                          onChange={(e) => updateAggregateRetry(field, e.target.checked)}
                        />
                        {label}
                      </label>
                    ))}
                  </div>
                )}
              </div>

              {isAggregate && (
                <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-4 space-y-4">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <div className="text-sm font-medium text-[var(--text-primary)]">子供应商列表</div>
                      <div className="mt-1 text-xs text-[var(--text-muted)]">优先级越小越先用；同优先级按权重分配，高优先级不可用时切到下一层。</div>
                    </div>
                    <button className={buttonClass('ghost')} onClick={addAggregateChild}>
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
                      </svg>
                      添加子项
                    </button>
                  </div>
                  <div className="space-y-3">
                    {!aggregateChildren.length && (
                      <div className="rounded-lg border border-dashed border-[var(--border)] px-3 py-6 text-center text-xs text-[var(--text-muted)]">
                        暂无子供应商，请添加。
                      </div>
                    )}
                    {aggregateChildren.map((child, index) => {
                      const health = aggregateChildHealth(child)
                      return (
                        <div key={index} className={`aggregate-child-row ${health.tone === 'disabled' ? 'aggregate-child-row-disabled' : ''}`}>
                          <div className="aggregate-child-row-header">
                            <div className="flex min-w-0 items-center gap-2">
                              <span className="font-mono text-xs text-[var(--text-faint)]">#{index + 1}</span>
                              <span className={`aggregate-child-badge aggregate-child-badge-${health.tone}`}>
                                {health.label}
                              </span>
                            </div>
                            <button className={buttonClass('danger')} type="button" onClick={() => removeAggregateChild(index)}>移除</button>
                          </div>
                          <div className="aggregate-child-fields">
                            <label className="field-wrap mb-0">
                              <span className="field-label">子供应商名称</span>
                              <select
                                className="select-base w-full"
                                value={child.vendor || ''}
                                onChange={(e) => {
                                  updateAggregateChild(index, 'vendor', e.target.value)
                                  updateAggregateChild(index, 'key_ids', [])
                                }}
                              >
                                <option value="" disabled>-- 请选择已有供应商 --</option>
                                {vendorRows
                                  .filter((v) => v.name !== selectedVendor && v.provider !== 'aggregate')
                                  .map((v) => (
                                    <option key={v.name} value={v.name}>
                                      {v.name} ({v.provider})
                                    </option>
                                  ))}
                              </select>
                            </label>
                            <label className="field-wrap mb-0">
                              <span className="field-label">权重</span>
                              <input
                                className="input-base w-full"
                                type="number"
                                min="1"
                                value={child.weight || 1}
                                onChange={(e) => updateAggregateChild(index, 'weight', parseInt(e.target.value, 10) || 1)}
                              />
                            </label>
                            <label className="field-wrap mb-0">
                              <span className="field-label">优先级</span>
                              <input
                                className="input-base w-full"
                                type="number"
                                min="0"
                                value={child.priority || 0}
                                onChange={(e) => updateAggregateChild(index, 'priority', parseInt(e.target.value, 10) || 0)}
                              />
                            </label>
                          </div>
                          {child.vendor && (
                            <div className="mt-3 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-3">
                              <div className="flex flex-wrap items-center justify-between gap-2">
                                <div>
                                  <div className="text-xs font-medium text-[var(--text-secondary)]">指定 Key 清单</div>
                                  <div className="mt-1 text-[10px] text-[var(--text-muted)]">
                                    {child.key_ids?.length ? `已指定 ${child.key_ids.length} 个 Key` : '全部可用 Key'}
                                  </div>
                                </div>
                                <button className={buttonClass('ghost')} type="button" onClick={() => openAggregateKeyPicker(index, child)}>
                                  选择 Key
                                </button>
                              </div>
                            </div>
                          )}
                        </div>
                      )
                    })}
                  </div>
                </div>
              )}

              {!isAggregate && (
                <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-4 space-y-4">
                  <div>
                    <div className="text-sm font-medium text-[var(--text-primary)]">上游鉴权</div>
                    <div className="mt-1 text-xs text-[var(--text-muted)]">配置转发到上游时使用的鉴权头和前缀。</div>
                  </div>
                  <div className="grid gap-4 xl:grid-cols-3">
                    <label className="field-wrap">
                      <span className="field-label">上游鉴权模式</span>
                      <select
                        className="select-base"
                        value={vendorDraft.upstream_auth?.mode || 'bearer'}
                        onChange={(e) => onMutateVendorDraft((draft) => { draft.upstream_auth.mode = e.target.value })}
                      >
                        <option value="bearer">bearer</option>
                        <option value="header">header</option>
                        <option value="passthrough">passthrough</option>
                      </select>
                    </label>
                    <label className="field-wrap">
                      <span className="field-label">鉴权 Header</span>
                      <input
                        className="input-base"
                        value={vendorDraft.upstream_auth?.header || ''}
                        onChange={(e) => onMutateVendorDraft((draft) => { draft.upstream_auth.header = e.target.value })}
                      />
                    </label>
                    <label className="field-wrap">
                      <span className="field-label">鉴权前缀</span>
                      <input
                        className="input-base"
                        value={vendorDraft.upstream_auth?.prefix || ''}
                        onChange={(e) => onMutateVendorDraft((draft) => { draft.upstream_auth.prefix = e.target.value })}
                      />
                    </label>
                  </div>
                </div>
              )}
            </CollapsibleSection>

            {!isAggregate && (<>
            <CollapsibleSection
              title="错误适配"
              description="无效密钥检测、退避规则和请求切换统一配置。"
              summary={`${responseRuleCount} 条退避规则`}
              open={expandedSections.errorPolicy}
              onToggle={() => toggleSection('errorPolicy')}
            >
              <div className="section-card">
                <div className="section-card-header">
                  <div>
                    <h4>无效密钥检测</h4>
                    <p>匹配到指定响应码或关键字时，自动禁用上游密钥。</p>
                  </div>
                  <div className="field-inline">
                    <span className="field-label">自动禁用</span>
                    <select
                      className="select-base"
                      value={vendorDraft.error_policy?.auto_disable?.invalid_key ? 'true' : 'false'}
                      onChange={(e) => onMutateVendorDraft((draft) => { draft.error_policy.auto_disable.invalid_key = e.target.value === 'true' })}
                    >
                      <option value="true">开启</option>
                      <option value="false">关闭</option>
                    </select>
                  </div>
                </div>
                <div className="section-card-body xl:grid-cols-2 xl:grid">
                  <label className="field-wrap">
                    <span className="field-label">触发响应码</span>
                    <input
                      className="input-base"
                      placeholder="逗号分隔，例如：401, 403"
                      value={invalidKeyStatusCodesText}
                      onChange={(e) => onInvalidKeyStatusCodesTextChange(e.target.value)}
                    />
                  </label>
                  <label className="field-wrap">
                    <span className="field-label">关键字</span>
                    <input
                      className="input-base"
                      placeholder="逗号分隔，例如：incorrect_api_key, key revoked"
                      value={invalidKeyKeywordsText}
                      onChange={(e) => onInvalidKeyKeywordsTextChange(e.target.value)}
                    />
                  </label>
                </div>
              </div>

              <div className="section-card">
                <div className="section-card-header">
                  <div>
                    <h4>退避规则</h4>
                    <p>按顺序匹配响应码 / 关键字，并设置基础退避时长与 Retry-After 策略。连续命中会按该时长指数放大。</p>
                  </div>
                  <div className="field-inline">
                    <span className="field-label">无默认退避</span>
                    <select
                      className="select-base"
                      value={vendorDraft?.error_policy?.cooldown?.no_default_backoff ? 'true' : 'false'}
                      onChange={(e) => onMutateVendorDraft((draft) => { draft.error_policy.cooldown.no_default_backoff = e.target.value === 'true' })}
                    >
                      <option value="false">关闭</option>
                      <option value="true">开启</option>
                    </select>
                  </div>
                  <button className={buttonClass('ghost')} onClick={addResponseRuleRow}>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
                    </svg>
                    新增规则
                  </button>
                </div>

                <div className="space-y-3">
                  {responseRuleRows.map((row, index) => (
                    <div key={index} className="section-card" style={{ background: 'var(--bg-surface)' }}>
                      <div className="flex items-center justify-between">
                        <span className="text-xs font-medium text-[var(--text-muted)]">规则 {index + 1}</span>
                        <button className={buttonClass('danger')} onClick={() => removeResponseRuleRow(index)}>删除</button>
                      </div>
                      <div className="grid gap-3 xl:grid-cols-2">
                        <label className="field-wrap">
                          <span className="field-label">响应码</span>
                          <input
                            className="input-base"
                            placeholder="逗号分隔，例如：429, 500, 502, 503"
                            value={row.statusCodesText}
                            onChange={(e) => updateResponseRuleRow(index, { statusCodesText: e.target.value })}
                          />
                        </label>
                        <label className="field-wrap">
                          <span className="field-label">关键字</span>
                          <input
                            className="input-base"
                            placeholder="逗号分隔，可留空，例如：slow down"
                            value={row.keywordsText}
                            onChange={(e) => updateResponseRuleRow(index, { keywordsText: e.target.value })}
                          />
                        </label>
                      </div>
                      <div className="grid gap-3 xl:grid-cols-2">
                        <label className="field-wrap">
                          <span className="field-label">退避时长</span>
                          <input
                            className="input-base"
                            placeholder="例如 5s / 30m / 3h"
                            value={row.durationText}
                            onChange={(e) => updateResponseRuleRow(index, { durationText: e.target.value })}
                          />
                        </label>
                        <label className="field-wrap">
                          <span className="field-label">Retry-After</span>
                          <select
                            className="select-base"
                            value={row.retryAfter || ''}
                            onChange={(e) => updateResponseRuleRow(index, { retryAfter: e.target.value })}
                          >
                            <option value="">默认</option>
                            <option value="ignore">ignore</option>
                            <option value="override">override</option>
                            <option value="max">max</option>
                          </select>
                        </label>
                      </div>
                    </div>
                  ))}
                </div>
              </div>

              <div className="section-card">
                <div className="section-card-header">
                  <div>
                    <h4>请求切换</h4>
                    <p>上游返回指定响应码时，自动切换到下一个上游。</p>
                  </div>
                  <div className="field-inline">
                    <span className="field-label">请求错误切换</span>
                    <select
                      className="select-base"
                      value={vendorDraft.error_policy?.failover?.request_error ? 'true' : 'false'}
                      onChange={(e) => onMutateVendorDraft((draft) => { draft.error_policy.failover.request_error = e.target.value === 'true' })}
                    >
                      <option value="true">开启</option>
                      <option value="false">关闭</option>
                    </select>
                  </div>
                </div>
                <label className="field-wrap">
                  <span className="field-label">响应码切换</span>
                  <input
                    className="input-base"
                    placeholder="逗号分隔，例如：401, 429, 500, 502, 503"
                    value={failoverResponseStatusCodesText}
                    onChange={(e) => onFailoverResponseStatusCodesTextChange(e.target.value)}
                  />
                </label>
                <label className="field-wrap">
                  <span className="field-label">最大切号次数</span>
                  <input
                    className="input-base"
                    type="number"
                    min="1"
                    max="20"
                    placeholder="默认 5"
                    value={vendorDraft.error_policy?.failover?.max_attempts || ''}
                    onChange={(e) => {
                      const v = parseInt(e.target.value, 10)
                      onMutateVendorDraft((draft) => { draft.error_policy.failover.max_attempts = v > 0 ? v : 0 })
                    }}
                  />
                </label>
              </div>
            </CollapsibleSection>

            <CollapsibleSection
              title="Resin"
              description="可选的 Resin 转发配置，默认不启用。"
              summary={vendorDraft.resin?.enabled ? `${vendorDraft.resin?.platform || 'Default'} · 已启用` : '已关闭'}
              open={expandedSections.resin}
              onToggle={() => toggleSection('resin')}
            >
              <div className="grid gap-4 xl:grid-cols-4">
                <label className="field-wrap">
                  <span className="field-label">启用 Resin</span>
                  <select
                    className="select-base"
                    value={vendorDraft.resin?.enabled ? 'true' : 'false'}
                    onChange={(e) => onMutateVendorDraft((draft) => { draft.resin.enabled = e.target.value === 'true' })}
                  >
                    <option value="false">false</option>
                    <option value="true">true</option>
                  </select>
                </label>
                <label className="field-wrap xl:col-span-2">
                  <span className="field-label">Resin 地址</span>
                  <input
                    className="input-base"
                    value={vendorDraft.resin?.url || ''}
                    onChange={(e) => onMutateVendorDraft((draft) => { draft.resin.url = e.target.value })}
                  />
                </label>
                <label className="field-wrap">
                  <span className="field-label">Resin 平台</span>
                  <input
                    className="input-base"
                    value={vendorDraft.resin?.platform || ''}
                    onChange={(e) => onMutateVendorDraft((draft) => { draft.resin.platform = e.target.value })}
                  />
                </label>
              </div>
            </CollapsibleSection>

            <CollapsibleSection
              title="客户端 Header 策略"
              description="统一管理白名单预置、补充放行与额外清理规则。"
              summary={`${selectedPresetOption.label} · 放行 ${allowlistCount} · 清理 ${dropHeadersCount}`}
              open={expandedSections.clientHeaders}
              onToggle={() => toggleSection('clientHeaders')}
              actions={(
                <>
                  <div className="field-inline">
                    <span className="field-label">白名单预置</span>
                    <select className="select-base" value={clientHeaderPreset} onChange={(e) => onClientHeaderPresetChange(e.target.value)}>
                      {CLIENT_HEADER_PRESET_OPTIONS.map((option) => (
                        <option key={option.value || 'none'} value={option.value}>{option.label}</option>
                      ))}
                    </select>
                  </div>
                  <button className={buttonClass('ghost')} type="button" disabled={busy} onClick={restoreDefaultHeaderPolicy}>
                    恢复默认策略
                  </button>
                </>
              )}
            >
              <label className="field-wrap">
                <span className="field-label">白名单补充</span>
                <textarea
                  className="textarea-base h-24"
                  placeholder={'逗号或换行分隔，例如：\nOpenAI-Project\nIdempotency-Key'}
                  value={allowlistText}
                  onChange={(e) => onAllowlistTextChange(e.target.value)}
                />
              </label>

              <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-3 space-y-3">
                <div>
                  <div className="text-sm font-medium text-[var(--text-primary)]">预置说明</div>
                  <div className="mt-1 text-xs text-[var(--text-muted)]">{selectedPresetOption.description}</div>
                </div>
                {!!presetPreviewHeaders.length && (
                  <div>
                    <div className="text-xs font-medium text-[var(--text-secondary)]">当前预置默认放行</div>
                    <div className="mt-2 flex flex-wrap gap-2">
                      {presetPreviewHeaders.map((header) => (
                        <span key={header} className="rounded-full border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-1 font-mono text-xs text-[var(--text-secondary)]">
                          {header}
                        </span>
                      ))}
                    </div>
                  </div>
                )}
                <div>
                  <div className="text-xs font-medium text-[var(--text-secondary)]">系统默认清理</div>
                  <div className="mt-2 flex flex-wrap gap-2">
                    {DEFAULT_CLIENT_HEADER_DROP_PREVIEW.map((header) => (
                      <span key={header} className="rounded-full border border-[var(--border)] bg-[var(--bg-elevated)] px-2 py-1 font-mono text-xs text-[var(--text-muted)]">
                        {header}
                      </span>
                    ))}
                  </div>
                </div>
              </div>

              <label className="field-wrap">
                <span className="field-label">额外清理 Header</span>
                <span className="text-xs text-[var(--text-muted)]">默认已内置常见代理 / CDN Header 清理；这里可以继续追加自定义项。</span>
                <textarea
                  className="textarea-base h-24"
                  placeholder={'逗号或换行分隔，例如：\nX-Custom-Trace\nX-Debug-Token'}
                  value={dropHeadersText}
                  onChange={(e) => onDropHeadersTextChange(e.target.value)}
                />
              </label>
            </CollapsibleSection>

            <CollapsibleSection
              title="请求改写"
              description="注入 Header 与路径重写归到同一类，便于按需折叠。"
              summary={`注入 ${injectHeaderCount} · 重写 ${rewriteRuleCount}`}
              open={expandedSections.requestTransforms}
              onToggle={() => toggleSection('requestTransforms')}
            >
              <MapRowsEditor
                title="注入 Header"
                rows={injectRows}
                setRows={setInjectRows}
                keyPlaceholder="Header 名称"
                valuePlaceholder="Header 值"
              />

              <MapRowsEditor
                title="路径重写"
                rows={rewriteRows}
                setRows={setRewriteRows}
                keyPlaceholder="/from/path 或 /from/*"
                valuePlaceholder="/to/path 或 /to/*"
              />
            </CollapsibleSection>
            </>)}
          </div>
        )}
      </article>
      {keyPicker && (
        <div className="modal-overlay animate-fade-in">
          <div className="modal-panel animate-slide-in max-w-2xl">
            <div className="modal-header">
              <div>
                <h3>选择 {keyPicker.vendor} 的 Key</h3>
                <p>保存后，该子供应商只会在所选 Key 内进行轮询和故障切换。</p>
              </div>
              <button className="modal-close" type="button" aria-label="关闭" onClick={() => setKeyPicker(null)}>✕</button>
            </div>
            <div className="modal-body">
              <div className="flex flex-wrap items-center gap-2">
                <input
                  className="input-base min-w-[220px] flex-1"
                  value={keyPicker.query}
                  autoFocus
                  placeholder="搜索 Key、备注或状态"
                  onChange={(e) => setKeyPicker((current) => ({ ...current, query: e.target.value, page: 1 }))}
                />
                <button className={buttonClass('ghost')} type="button" disabled={!pickerFilteredItems.length} onClick={selectPickerResults}>全选当前结果</button>
                <button className={buttonClass('ghost')} type="button" disabled={!keyPicker.selected.length} onClick={() => setKeyPicker((current) => ({ ...current, selected: [] }))}>清除限制</button>
              </div>
              <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-[var(--text-muted)]">
                <span>已选 {keyPicker.selected.length} 个；未选择表示使用全部可用 Key。</span>
                {pickerMissingSelectedCount > 0 && <span className="text-[var(--warning)]">{pickerMissingSelectedCount} 个已选 Key 不存在</span>}
              </div>
              <div className="grid gap-2 sm:grid-cols-2">
                {pickerPageItems.map((item) => {
                  const state = aggregateKeyState(keyPicker.vendor, item)
                  const stateLabel = state.backoff > 0
                    ? `退避 ${state.backoff}s`
                    : (state.status === 'active' ? '可用' : (state.status === 'disabled_manual' ? '手动禁用' : '自动禁用'))
                  return (
                    <label key={item.key_id || item.key} className={`flex cursor-pointer items-start gap-2 rounded border px-2.5 py-2 ${state.available ? 'border-[var(--border)] hover:bg-[var(--bg-hover)]' : 'border-[var(--border-strong)] bg-[var(--bg-elevated)] opacity-75'}`}>
                      <input
                        type="checkbox"
                        className="mt-0.5 rounded border-[var(--border)] bg-transparent text-[var(--accent)]"
                        checked={keyPicker.selected.includes(item.key_id)}
                        onChange={() => togglePickerKey(item.key_id)}
                      />
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center justify-between gap-2">
                          <span className="truncate font-mono text-[11px] text-[var(--text-secondary)]">{item.masked}</span>
                          <span className={`shrink-0 text-[10px] ${state.available ? 'text-[var(--success)]' : 'text-[var(--warning)]'}`}>{stateLabel}</span>
                        </span>
                        <span className="mt-0.5 block truncate text-[10px] text-[var(--text-muted)]">{item.remark || '无备注'}</span>
                      </span>
                    </label>
                  )
                })}
                {!pickerPageItems.length && (
                  <div className="py-8 text-center text-xs text-[var(--text-muted)] sm:col-span-2">没有匹配的 Key。</div>
                )}
              </div>
              {pickerTotalPages > 1 && (
                <div className="flex items-center justify-center gap-2">
                  <button className="pagination-btn" type="button" disabled={pickerCurrentPage <= 1} onClick={() => setKeyPicker((current) => ({ ...current, page: current.page - 1 }))}>上一页</button>
                  <span className="text-xs text-[var(--text-muted)]">{pickerCurrentPage} / {pickerTotalPages}</span>
                  <button className="pagination-btn" type="button" disabled={pickerCurrentPage >= pickerTotalPages} onClick={() => setKeyPicker((current) => ({ ...current, page: current.page + 1 }))}>下一页</button>
                </div>
              )}
            </div>
            <div className="modal-footer">
              <button className={buttonClass()} type="button" onClick={() => setKeyPicker(null)}>取消</button>
              <button className={buttonClass('primary')} type="button" onClick={saveAggregateKeyPicker}>保存</button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
