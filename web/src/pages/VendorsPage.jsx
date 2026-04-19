import { useEffect, useState } from 'react'

import { buildVendorRequestEndpoint, buttonClass, clone, generateClientKey, normalizeKeys, panelClass } from '../app/utils'
import { MapRowsEditor } from '../components/MapRowsEditor'

export function VendorsPage({
  busy,
  vendorRows,
  selectedVendor,
  vendorDraft,
  vendorBackoffDuration,
  invalidKeyStatusCodesText,
  invalidKeyKeywordsText,
  responseRuleRows,
  failoverResponseStatusCodesText,
  allowlistText,
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
  onVendorBackoffDurationChange,
  onInvalidKeyStatusCodesTextChange,
  onInvalidKeyKeywordsTextChange,
  setResponseRuleRows,
  onFailoverResponseStatusCodesTextChange,
  onAllowlistTextChange,
  setInjectRows,
  setRewriteRows
}) {
  const requestEndpoint = buildVendorRequestEndpoint(selectedVendor)
  const clientKeys = normalizeKeys(vendorDraft?.client_auth?.keys || [])
  const [clientKeyInputText, setClientKeyInputText] = useState('')

  useEffect(() => {
    setClientKeyInputText('')
  }, [selectedVendor])

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
    const incoming = normalizeKeys(String(clientKeyInputText || '').split(',').map((item) => item.trim()))
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

  return (
    <section className="grid gap-5 2xl:grid-cols-[280px_1fr] animate-fade-in">
      {/* Sidebar */}
      <aside className={panelClass('p-4')}>
        <div className="mb-4 flex items-center justify-between gap-3">
          <h3 className="section-title">供应商</h3>
          <button className={buttonClass('ghost')} disabled={busy} onClick={onRefresh}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 2v6h-6" /><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M3 22v-6h6" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
            </svg>
          </button>
        </div>

        <div className="space-y-2">
          {vendorRows.map((row) => (
            <button
              key={row.name}
              className={`vendor-item ${selectedVendor === row.name ? 'vendor-item-active' : ''}`}
              onClick={() => onSelectVendor(row.name)}
            >
              <strong>{row.name}</strong>
              <span>上游 {row.upstreamKeys} · 客户端 {row.clientKeys}</span>
            </button>
          ))}
        </div>

        {/* Create Vendor */}
        <section className="mt-4 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-3">
          <h3 className="section-title text-sm mb-3">新建供应商</h3>
          <div className="space-y-3">
            <input
              className="input-base w-full"
              placeholder="供应商名称，例如 openai"
              value={newVendorForm.name}
              onChange={(e) => onNewVendorFormChange((prev) => ({ ...prev, name: e.target.value }))}
            />
            <input
              className="input-base w-full"
              placeholder="上游 base_url"
              value={newVendorForm.baseURL}
              onChange={(e) => onNewVendorFormChange((prev) => ({ ...prev, baseURL: e.target.value }))}
            />
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
              <h3 className="section-title text-base">{selectedVendor}</h3>
              <div className="flex flex-wrap gap-2">
                <button className={buttonClass('ghost')} onClick={onOpenUpstreamKeys}>上游密钥 →</button>
                <button className={buttonClass('primary')} disabled={busy} onClick={onSaveVendor}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" /><polyline points="17,21 17,13 7,13 7,21" /><polyline points="7,3 7,8 15,8" />
                  </svg>
                  保存
                </button>
                <button className={buttonClass('danger')} disabled={busy} onClick={onDeleteVendor}>删除</button>
              </div>
            </div>

            <section className="space-y-4 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 className="section-title text-sm">客户端密钥</h3>
                  <p className="mt-1 text-xs text-[var(--text-muted)]">密钥管理前置到这里，支持直接复制、删除、批量导入和自动生成。</p>
                </div>
                <div className="flex flex-wrap items-center gap-2">
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
                </div>
              </div>

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
                  <p className="mt-1 text-xs text-[var(--text-muted)]">逗号分隔客户端密钥，导入时会自动去重。</p>
                  <textarea
                    className="textarea-base mt-3 h-48"
                    placeholder={'sk-jcp-xxxxxxxxxxxxxxxxxxxxxxxx, sk-jcp-yyyyyyyyyyyyyyyyyyyyyyyy'}
                    value={clientKeyInputText}
                    onChange={(e) => setClientKeyInputText(e.target.value)}
                  />
                </aside>
              </div>
            </section>

            {/* Basic Config */}
            <section className="grid gap-4 lg:grid-cols-3">
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
                </select>
              </label>
              <label className="field-wrap">
                <span className="field-label">上游 Base URL</span>
                <input
                  className="input-base"
                  value={vendorDraft.upstream?.base_url || ''}
                  onChange={(e) => onMutateVendorDraft((draft) => { draft.upstream.base_url = e.target.value })}
                />
              </label>
              <label className="field-wrap">
                <span className="field-label">负载均衡策略</span>
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
            </section>

            {/* Auth Config */}
            <section className="grid gap-4 xl:grid-cols-3">
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
            </section>

            {/* Backoff */}
            <section className="grid gap-4 xl:grid-cols-2">
              <label className="field-wrap">
                <span className="field-label">退避阈值</span>
                <input
                  type="number"
                  min="1"
                  className="input-base"
                  value={vendorDraft.backoff?.threshold ?? 3}
                  onChange={(e) => onMutateVendorDraft((draft) => { draft.backoff.threshold = Number(e.target.value || 1) })}
                />
              </label>
              <label className="field-wrap">
                <span className="field-label">退避时长</span>
                <input className="input-base" value={vendorBackoffDuration} onChange={(e) => onVendorBackoffDurationChange(e.target.value)} />
              </label>
            </section>

            {/* Error Policy Section */}
            <hr className="section-divider" />
            <div className="space-y-4">
              <h3 className="section-title text-sm">错误适配</h3>

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
                    <p>按顺序匹配响应码 / 关键字，并设置退避时长与 Retry-After 策略。</p>
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
              </div>
            </div>

            {/* Resin */}
            <section className="grid gap-4 xl:grid-cols-4">
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
            </section>

            {/* Header Allowlist */}
            <hr className="section-divider" />
            <div className="space-y-3">
              <h3 className="section-title text-sm">客户端 Header 白名单</h3>
              <textarea className="textarea-base h-20" value={allowlistText} onChange={(e) => onAllowlistTextChange(e.target.value)} />
            </div>

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
          </div>
        )}
      </article>
    </section>
  )
}
