import { buttonClass, normalizeKeys, panelClass } from '../app/utils'
import { KeyTableEditor } from '../components/KeyTableEditor'
import { MapRowsEditor } from '../components/MapRowsEditor'
import { RuntimeKeyStatCard } from '../components/RuntimeKeyStatCard'

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
  selectedVendorStats,
  showSecrets,
  onToggleSecrets,
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
                <button className={buttonClass('ghost')} onClick={onToggleSecrets}>
                  {showSecrets ? '隐藏密钥' : '显示密钥'}
                </button>
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
              <div className="flex items-center justify-between">
                <h3 className="section-title text-sm">客户端密钥</h3>
              </div>
              <textarea
                className="textarea-base h-20"
                placeholder="在此输入客户端密钥，每行一个..."
                value={(vendorDraft.client_auth?.keys || []).join('\n')}
                onChange={(e) => onMutateVendorDraft((draft) => {
                  draft.client_auth.keys = normalizeKeys(e.target.value.split('\n'))
                  if (draft.client_auth.keys.length === 0) {
                    draft.client_auth.enabled = false
                  }
                })}
              />
            </div>

            <div className="space-y-3 pt-4 border-t border-slate-100 mt-6">
              <div className="flex items-center justify-between">
                <h3 className="section-title text-sm">运行态</h3>
              </div>
              <div className="grid gap-3 lg:grid-cols-2">
                {selectedVendorStats.map((item, idx) => (
                  <RuntimeKeyStatCard key={idx} item={item} />
                ))}
                {!selectedVendorStats.length && <p className="text-sm text-slate-400">暂无统计数据</p>}
              </div>
            </div>
          </div>
        )}
      </article>
    </section>
  )
}
