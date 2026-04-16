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
    <section className="grid gap-4 2xl:grid-cols-[280px_1fr]">
      <aside className={panelClass('p-4')}>
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <h3 className="section-title">供应商</h3>
          </div>
          <button className={buttonClass('ghost')} disabled={busy} onClick={onRefresh}>
            刷新
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

        <section className="mt-4 rounded-md border border-slate-200 bg-slate-50 p-3">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="section-title">新建</h3>
          </div>
          <div className="space-y-3">
            <input
              className="input-base"
              placeholder="供应商名称，例如 openai"
              value={newVendorForm.name}
              onChange={(e) => onNewVendorFormChange((prev) => ({ ...prev, name: e.target.value }))}
            />
            <input
              className="input-base"
              placeholder="上游 base_url"
              value={newVendorForm.baseURL}
              onChange={(e) => onNewVendorFormChange((prev) => ({ ...prev, baseURL: e.target.value }))}
            />
            <button className={buttonClass('primary')} disabled={busy} onClick={onCreateVendor}>
              创建供应商
            </button>
          </div>
        </section>
      </aside>

      <article className={panelClass('p-4')}>
        {!vendorDraft && <p className="text-sm text-slate-500">请先在左侧选择一个供应商。</p>}
        {vendorDraft && (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <h3 className="section-title">{selectedVendor}</h3>
              </div>
              <div className="flex flex-wrap gap-2">
                <button className={buttonClass('ghost')} onClick={onOpenUpstreamKeys}>
                  上游密钥
                </button>
                <button className={buttonClass('primary')} disabled={busy} onClick={onSaveVendor}>
                  保存供应商
                </button>
                <button className={buttonClass('danger')} disabled={busy} onClick={onDeleteVendor}>
                  删除供应商
                </button>
              </div>
            </div>

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

            <div className="space-y-3 pt-4 border-t border-slate-100 mt-6">
              <div className="flex items-center justify-between">
                <h3 className="section-title text-sm">错误适配</h3>
              </div>

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

              <section className="grid gap-4 xl:grid-cols-2">
                {cooldownFields.map((field) => (
                  <div key={field.key} className="grid gap-4 rounded-md border border-slate-200 p-3 md:grid-cols-[160px_1fr]">
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

            <div className="space-y-3 pt-4 border-t border-slate-100 mt-6">
              <div className="flex items-center justify-between">
                <h3 className="section-title text-sm">客户端 Header 白名单</h3>
              </div>
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

            <div className="space-y-3 pt-4 border-t border-slate-100 mt-6">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 className="section-title text-sm">客户端密钥</h3>
                  <p className="mt-1 text-xs text-slate-500">支持手动导入，也支持自动生成 `sk-jcp-...` 格式密钥。</p>
                </div>
                <button className={buttonClass('ghost')} type="button" onClick={handleGenerateClientKey}>
                  自动生成
                </button>
              </div>

              <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_320px]">
                <KeyTableEditor
                  title="客户端密钥"
                  keys={clientKeys}
                  onChange={updateClientKeys}
                  showSecrets
                  scopeKey={selectedVendor}
                  toneClass="text-slate-800"
                />

                <aside className="rounded-md border border-slate-200 bg-slate-50 p-4">
                  <div className="flex items-center justify-between gap-3">
                    <h4 className="text-sm font-medium text-slate-800">客户端请求端点</h4>
                    <button className="text-xs font-medium text-blue-600 hover:text-blue-700" type="button" onClick={copyRequestEndpoint}>
                      复制
                    </button>
                  </div>

                  <div className="mt-3 rounded-md border border-slate-200 bg-white px-3 py-2 font-mono text-xs text-slate-700 break-all">
                    {requestEndpoint || '--'}
                  </div>

                  <div className="mt-3 space-y-2 text-xs text-slate-500">
                    <p>示例请求:</p>
                    <div className="rounded-md border border-slate-200 bg-white px-3 py-2 font-mono text-[11px] text-slate-700 break-all">
                      {requestEndpoint ? `${requestEndpoint}/v1/chat/completions` : '--'}
                    </div>
                    <p>客户端可通过 `Authorization: Bearer sk-jcp-...` 或 `X-API-Key` 进行鉴权。</p>
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
