import { buildVendorRequestEndpoint, buttonClass, generateClientKey, normalizeKeys, panelClass } from '../app/utils'
import { KeyTableEditor } from '../components/KeyTableEditor'
import { MapRowsEditor } from '../components/MapRowsEditor'

export function VendorsPage({
  busy,
  vendorRows,
  selectedVendor,
  vendorDraft,
  vendorBackoffDuration,
  errorPolicyDurations,
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
  onErrorPolicyDurationsChange,
  onAllowlistTextChange,
  setInjectRows,
  setRewriteRows
}) {
  const cooldownFields = [
    { key: 'requestError', configKey: 'request_error', label: '请求错误' },
    { key: 'unauthorized', configKey: 'unauthorized', label: '401 未授权' },
    { key: 'paymentRequired', configKey: 'payment_required', label: '402 计费/额度' },
    { key: 'forbidden', configKey: 'forbidden', label: '403 禁止访问' },
    { key: 'rateLimit', configKey: 'rate_limit', label: '429 限流' },
    { key: 'serverError', configKey: 'server_error', label: '5xx/529 服务异常' },
    { key: 'openAISlowDown', configKey: 'openai_slow_down', label: 'OpenAI slow down' }
  ]

  const failoverFields = [
    { key: 'request_error', label: '请求错误切换' },
    { key: 'unauthorized', label: '401 切换' },
    { key: 'payment_required', label: '402 切换' },
    { key: 'forbidden', label: '403 切换' },
    { key: 'rate_limit', label: '429 切换' },
    { key: 'server_error', label: '5xx/529 切换' }
  ]

  const requestEndpoint = buildVendorRequestEndpoint(selectedVendor)
  const clientKeys = normalizeKeys(vendorDraft?.client_auth?.keys || [])

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

            {/* Backoff & Client Auth */}
            <section className="grid gap-4 xl:grid-cols-3">
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
              <label className="field-wrap">
                <span className="field-label">启用客户端鉴权</span>
                <select
                  className="select-base"
                  value={vendorDraft.client_auth?.enabled ? 'true' : 'false'}
                  onChange={(e) => onMutateVendorDraft((draft) => { draft.client_auth.enabled = e.target.value === 'true' })}
                >
                  <option value="false">false</option>
                  <option value="true">true</option>
                </select>
              </label>
            </section>

            {/* Error Policy Section */}
            <hr className="section-divider" />
            <div className="space-y-4">
              <h3 className="section-title text-sm">错误适配</h3>

              <section className="grid gap-4 xl:grid-cols-3">
                <label className="field-wrap">
                  <span className="field-label">无效密钥自动禁用</span>
                  <select
                    className="select-base"
                    value={vendorDraft.error_policy?.auto_disable?.invalid_key ? 'true' : 'false'}
                    onChange={(e) => onMutateVendorDraft((draft) => { draft.error_policy.auto_disable.invalid_key = e.target.value === 'true' })}
                  >
                    <option value="true">true</option>
                    <option value="false">false</option>
                  </select>
                </label>
                <label className="field-wrap">
                  <span className="field-label">402 自动禁用</span>
                  <select
                    className="select-base"
                    value={vendorDraft.error_policy?.auto_disable?.payment_required ? 'true' : 'false'}
                    onChange={(e) => onMutateVendorDraft((draft) => { draft.error_policy.auto_disable.payment_required = e.target.value === 'true' })}
                  >
                    <option value="true">true</option>
                    <option value="false">false</option>
                  </select>
                </label>
                <label className="field-wrap">
                  <span className="field-label">额度耗尽自动禁用</span>
                  <select
                    className="select-base"
                    value={vendorDraft.error_policy?.auto_disable?.quota_exhausted ? 'true' : 'false'}
                    onChange={(e) => onMutateVendorDraft((draft) => { draft.error_policy.auto_disable.quota_exhausted = e.target.value === 'true' })}
                  >
                    <option value="true">true</option>
                    <option value="false">false</option>
                  </select>
                </label>
              </section>

              {/* Cooldown Fields */}
              <section className="grid gap-3 xl:grid-cols-2">
                {cooldownFields.map((field) => (
                  <div key={field.key} className="grid gap-3 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-3 md:grid-cols-[160px_1fr]">
                    <label className="field-wrap">
                      <span className="field-label">{field.label}</span>
                      <select
                        className="select-base"
                        value={vendorDraft.error_policy?.cooldown?.[field.configKey]?.enabled ? 'true' : 'false'}
                        onChange={(e) => onMutateVendorDraft((draft) => { draft.error_policy.cooldown[field.configKey].enabled = e.target.value === 'true' })}
                      >
                        <option value="true">true</option>
                        <option value="false">false</option>
                      </select>
                    </label>
                    <label className="field-wrap">
                      <span className="field-label">{field.label}退避时长</span>
                      <input
                        className="input-base"
                        value={errorPolicyDurations?.[field.key] || '0s'}
                        onChange={(e) => onErrorPolicyDurationsChange((prev) => ({ ...prev, [field.key]: e.target.value }))}
                      />
                    </label>
                  </div>
                ))}
              </section>

              {/* Failover Fields */}
              <section className="grid gap-4 xl:grid-cols-3">
                {failoverFields.map((field) => (
                  <label key={field.key} className="field-wrap">
                    <span className="field-label">{field.label}</span>
                    <select
                      className="select-base"
                      value={vendorDraft.error_policy?.failover?.[field.key] ? 'true' : 'false'}
                      onChange={(e) => onMutateVendorDraft((draft) => { draft.error_policy.failover[field.key] = e.target.value === 'true' })}
                    >
                      <option value="true">true</option>
                      <option value="false">false</option>
                    </select>
                  </label>
                ))}
              </section>
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

            {/* Client Keys Section */}
            <hr className="section-divider" />
            <div className="space-y-4">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 className="section-title text-sm">客户端密钥</h3>
                  <p className="mt-1 text-xs text-[var(--text-muted)]">支持手动导入或自动生成 sk-jcp-... 格式密钥。</p>
                </div>
                <button className={buttonClass('ghost')} type="button" onClick={handleGenerateClientKey}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 2v4m0 12v4M4.93 4.93l2.83 2.83m8.49 8.49l2.83 2.83M2 12h4m12 0h4M4.93 19.07l2.83-2.83m8.49-8.49l2.83-2.83" />
                  </svg>
                  自动生成
                </button>
              </div>

              <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_340px]">
                <KeyTableEditor
                  title="客户端密钥"
                  keys={clientKeys}
                  onChange={updateClientKeys}
                  showSecrets
                  scopeKey={selectedVendor}
                  toneClass="text-[var(--text-primary)]"
                />

                <aside className="rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-4">
                  <div className="flex items-center justify-between gap-3">
                    <h4 className="text-sm font-medium text-[var(--text-primary)]">客户端请求端点</h4>
                    <button className="text-xs font-medium text-[var(--accent)] hover:text-blue-400 transition-colors" type="button" onClick={copyRequestEndpoint}>
                      复制
                    </button>
                  </div>

                  <div className="mt-3 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2 font-mono text-xs text-[var(--text-secondary)] break-all">
                    {requestEndpoint || '--'}
                  </div>

                  <div className="mt-3 space-y-2 text-xs text-[var(--text-muted)]">
                    <p>示例请求:</p>
                    <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2 font-mono text-[11px] text-[var(--text-secondary)] break-all">
                      {requestEndpoint ? `${requestEndpoint}/v1/chat/completions` : '--'}
                    </div>
                    <p>客户端可通过 Authorization: Bearer sk-jcp-... 或 X-API-Key 进行鉴权。</p>
                  </div>
                </aside>
              </div>
            </div>
          </div>
        )}
      </article>
    </section>
  )
}
