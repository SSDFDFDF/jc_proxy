import { useEffect, useMemo, useState } from 'react'

import { buttonClass, panelClass } from '../app/utils'
import { MapRowsEditor } from '../components/MapRowsEditor'

const CUSTOM_OPTION = '__custom__'
const EMPTY_ROWS = [{ key: '', value: '' }]

function uniqueStrings(values) {
  return [...new Set((values || []).map((item) => String(item || '').trim()).filter(Boolean))]
}

function extractModelId(item) {
  if (!item) return ''
  if (typeof item === 'string') return item.trim()
  for (const field of ['id', 'name', 'model', 'display_name']) {
    const value = String(item?.[field] || '').trim()
    if (value) return value
  }
  return ''
}

function extractModelIds(bodyText) {
  if (!String(bodyText || '').trim()) return []

  let parsed
  try {
    parsed = JSON.parse(bodyText)
  } catch (_) {
    return []
  }

  if (Array.isArray(parsed?.data)) {
    return uniqueStrings(parsed.data.map(extractModelId))
  }
  if (Array.isArray(parsed?.models)) {
    return uniqueStrings(parsed.models.map(extractModelId))
  }
  if (Array.isArray(parsed)) {
    return uniqueStrings(parsed.map(extractModelId))
  }
  if (Array.isArray(parsed?.items)) {
    return uniqueStrings(parsed.items.map(extractModelId))
  }
  return []
}

function normalizeHeaderRows(rows) {
  return (rows || [])
    .filter((row) => String(row?.key || '').trim())
    .map((row) => ({
      key: String(row.key || '').trim(),
      value: String(row.value || '')
    }))
}

function fillModelTemplate(value, modelName) {
  const template = String(value || '')
  if (!template.includes('{model}')) return template
  const model = String(modelName || '').trim()
  if (!model) {
    throw new Error('当前端点或请求体使用了 {model} 占位符，请先选择或输入模型名')
  }
  return template.replaceAll('{model}', model)
}

function resultTone(statusCode) {
  return Number(statusCode) >= 400 ? 'pill-err' : 'pill-ok'
}

function ResultBlock({ title, result, models, selectedModel, onPickModel }) {
  if (!result) {
    return (
      <div className="rounded-lg border border-dashed border-[var(--border)] px-4 py-6 text-sm text-[var(--text-muted)]">
        {title}结果会显示在这里。
      </div>
    )
  }

  return (
    <div className="space-y-4 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <strong className="text-sm text-[var(--text-primary)]">{title}</strong>
          <span className={`status-pill ${resultTone(result.status_code)}`}>{result.status_code}</span>
          <span className="text-xs text-[var(--text-muted)]">{result.duration_ms} ms</span>
        </div>
        <div className="text-xs text-[var(--text-muted)]">
          Key: {result.used_key_source === 'none' ? '未注入' : `${result.used_key_source} · ${result.used_key_masked || '--'}`}
        </div>
      </div>

      <div className="grid gap-3 xl:grid-cols-2">
        <label className="field-wrap">
          <span className="field-label">最终请求 URL</span>
          <input className="input-base w-full font-mono text-xs" readOnly value={result.resolved_url || ''} />
        </label>
        <label className="field-wrap">
          <span className="field-label">请求方式</span>
          <input className="input-base w-full font-mono text-xs" readOnly value={result.method || ''} />
        </label>
      </div>

      {models.length > 0 && (
        <div className="space-y-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="field-label">识别到的模型</span>
            {selectedModel && <span className="text-xs text-[var(--text-muted)]">当前测试模型: {selectedModel}</span>}
          </div>
          <div className="flex flex-wrap gap-2">
            {models.map((model) => (
              <button
                key={model}
                className={`rounded-full border px-3 py-1 text-xs transition-colors ${selectedModel === model ? 'border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)]' : 'border-[var(--border)] bg-[var(--bg-elevated)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}
                type="button"
                onClick={() => onPickModel(model)}
              >
                {model}
              </button>
            ))}
          </div>
        </div>
      )}

      <label className="field-wrap">
        <span className="field-label">响应体</span>
        <textarea className="textarea-base h-72 font-mono text-xs leading-6" readOnly value={result.body || ''} />
        {result.truncated && <span className="text-xs text-[var(--warning)]">响应体已截断，只显示前 256 KiB。</span>}
      </label>

      <label className="field-wrap">
        <span className="field-label">响应头</span>
        <textarea className="textarea-base h-40 font-mono text-xs leading-6" readOnly value={JSON.stringify(result.headers || {}, null, 2)} />
      </label>
    </div>
  )
}

export function VendorTestPage({
  busy,
  vendorRows,
  selectedVendor,
  refreshStamp,
  onSelectVendor,
  onRefresh,
  onLoadMeta,
  onRunTest
}) {
  const [meta, setMeta] = useState(null)
  const [metaError, setMetaError] = useState('')

  const [baseURL, setBaseURL] = useState('')
  const [keyMode, setKeyMode] = useState('default')
  const [manualKey, setManualKey] = useState('')
  const [selectedModel, setSelectedModel] = useState('')
  const [headerRows, setHeaderRows] = useState(EMPTY_ROWS)

  const [modelEndpointChoice, setModelEndpointChoice] = useState(CUSTOM_OPTION)
  const [modelEndpoint, setModelEndpoint] = useState('/v1/models')
  const [modelResult, setModelResult] = useState(null)
  const [modelError, setModelError] = useState('')

  const [requestPresetChoice, setRequestPresetChoice] = useState(CUSTOM_OPTION)
  const [requestEndpointChoice, setRequestEndpointChoice] = useState(CUSTOM_OPTION)
  const [requestMethod, setRequestMethod] = useState('POST')
  const [requestEndpoint, setRequestEndpoint] = useState('/v1/chat/completions')
  const [requestBody, setRequestBody] = useState('')
  const [requestResult, setRequestResult] = useState(null)
  const [requestError, setRequestError] = useState('')

  const modelIds = useMemo(() => extractModelIds(modelResult?.body), [modelResult?.body])
  const requestEndpointSuggestions = useMemo(
    () => uniqueStrings((meta?.request_presets || []).map((preset) => preset.endpoint)),
    [meta]
  )

  useEffect(() => {
    if (!selectedVendor) {
      setMeta(null)
      setMetaError('')
      return
    }

    let cancelled = false
    setMetaError('')

    onLoadMeta(selectedVendor, { silent: true, touchBusy: false })
      .then((payload) => {
        if (cancelled) return

        const defaultModelEndpoint = payload?.model_endpoints?.[0] || '/v1/models'
        const firstPreset = payload?.request_presets?.[0] || null

        setMeta(payload || null)
        setBaseURL(payload?.base_url || '')
        setKeyMode('default')
        setManualKey('')
        setSelectedModel('')
        setHeaderRows(EMPTY_ROWS)

        setModelEndpointChoice(defaultModelEndpoint)
        setModelEndpoint(defaultModelEndpoint)
        setModelResult(null)
        setModelError('')

        if (firstPreset) {
          setRequestPresetChoice('0')
          setRequestEndpointChoice(firstPreset.endpoint || CUSTOM_OPTION)
          setRequestMethod(firstPreset.method || 'POST')
          setRequestEndpoint(firstPreset.endpoint || '')
          setRequestBody(firstPreset.body || '')
        } else {
          setRequestPresetChoice(CUSTOM_OPTION)
          setRequestEndpointChoice(CUSTOM_OPTION)
          setRequestMethod('POST')
          setRequestEndpoint('/v1/chat/completions')
          setRequestBody('')
        }
        setRequestResult(null)
        setRequestError('')
      })
      .catch((err) => {
        if (!cancelled) {
          setMeta(null)
          setMetaError(String(err?.message || err))
        }
      })

    return () => {
      cancelled = true
    }
  }, [selectedVendor, refreshStamp])

  const applyRequestPreset = (presetIndex) => {
    const preset = meta?.request_presets?.[presetIndex]
    if (!preset) return
    setRequestPresetChoice(String(presetIndex))
    setRequestMethod(preset.method || 'POST')
    setRequestEndpointChoice(preset.endpoint || CUSTOM_OPTION)
    setRequestEndpoint(preset.endpoint || '')
    setRequestBody(preset.body || '')
  }

  const buildPayload = ({ method, endpoint, body = '' }) => ({
    base_url: baseURL,
    method,
    endpoint,
    body,
    key: (() => {
      if (keyMode !== 'manual') return ''
      const key = String(manualKey || '').trim()
      if (!key) {
        throw new Error('已切换为手动 key，请先输入 key')
      }
      return key
    })(),
    headers: normalizeHeaderRows(headerRows)
  })

  const runModelFetch = async () => {
    if (!selectedVendor) return
    setModelError('')
    try {
      const endpoint = fillModelTemplate(modelEndpoint, selectedModel)
      const result = await onRunTest(selectedVendor, buildPayload({
        method: 'GET',
        endpoint,
        body: ''
      }))
      setModelResult(result)
      const nextModels = extractModelIds(result?.body)
      if (nextModels.length > 0) {
        setSelectedModel((prev) => prev || nextModels[0])
      }
    } catch (err) {
      setModelResult(null)
      setModelError(String(err?.message || err))
    }
  }

  const runRequestTest = async () => {
    if (!selectedVendor) return
    setRequestError('')
    try {
      const endpoint = fillModelTemplate(requestEndpoint, selectedModel)
      const body = fillModelTemplate(requestBody, selectedModel)
      const result = await onRunTest(selectedVendor, buildPayload({
        method: requestMethod,
        endpoint,
        body
      }))
      setRequestResult(result)
    } catch (err) {
      setRequestResult(null)
      setRequestError(String(err?.message || err))
    }
  }

  return (
    <section className="grid gap-5 2xl:grid-cols-[280px_1fr] animate-fade-in">
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
              <span>上游 {row.upstreamKeys} · 回退 {row.backoff}</span>
            </button>
          ))}
        </div>

        <section className="mt-4 rounded-lg border border-[var(--border)] bg-[var(--bg-elevated)] p-3">
          <h3 className="section-title text-sm">说明</h3>
          <p className="mt-2 text-xs leading-6 text-[var(--text-muted)]">
            这里会复用当前供应商的 `base_url`、`path_rewrites`、`inject_headers` 和 `upstream_auth`。未手动输入 key 时，后端会默认选该供应商第一条可用 key。
          </p>
        </section>
      </aside>

      <article className={panelClass('p-5')}>
        {!selectedVendor && (
          <p className="text-sm text-[var(--text-muted)]">
            请先在左侧选择一个供应商。
          </p>
        )}

        {selectedVendor && (
          <div className="space-y-5">
            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border)] pb-4">
              <div className="space-y-1">
                <p className="section-kicker">供应商测试</p>
                <div className="flex flex-wrap items-center gap-2">
                  <h3 className="section-title text-base">{selectedVendor}</h3>
                  <span className="status-pill pill-ok">{meta?.provider || '--'}</span>
                </div>
              </div>
              <div className="text-xs text-[var(--text-muted)]">
                {meta?.default_key_available ? `默认 key: ${meta.default_key_masked}` : '当前没有可用默认 key，可切换为手动输入'}
              </div>
            </div>

            {metaError && <p className="notice notice-error m-0">{metaError}</p>}

            <section className="section-card">
              <div className="section-card-header">
                <div>
                  <h4>连接参数</h4>
                  <p>支持修改供应商 base_url、切换默认 key / 手动 key，并附加额外 Header。</p>
                </div>
              </div>

              <div className="grid gap-4 xl:grid-cols-2">
                <label className="field-wrap">
                  <span className="field-label">供应商 Base URL</span>
                  <input
                    className="input-base w-full"
                    value={baseURL}
                    onChange={(e) => setBaseURL(e.target.value)}
                    placeholder="https://api.openai.com"
                  />
                </label>

                <label className="field-wrap">
                  <span className="field-label">Key 来源</span>
                  <select className="select-base w-full" value={keyMode} onChange={(e) => setKeyMode(e.target.value)}>
                    <option value="default">默认可用 key</option>
                    <option value="manual">手动输入</option>
                  </select>
                </label>

                {keyMode === 'manual' && (
                  <label className="field-wrap xl:col-span-2">
                    <span className="field-label">手动 Key</span>
                    <input
                      className="input-base w-full font-mono"
                      value={manualKey}
                      onChange={(e) => setManualKey(e.target.value)}
                      placeholder="sk-..."
                    />
                  </label>
                )}

                <label className="field-wrap xl:col-span-2">
                  <span className="field-label">测试模型名</span>
                  <input
                    className="input-base w-full"
                    value={selectedModel}
                    onChange={(e) => setSelectedModel(e.target.value)}
                    placeholder="可先拉取模型后点击模型名自动填入，也可手动输入"
                  />
                </label>
              </div>

              <MapRowsEditor
                title="额外 Header"
                rows={headerRows}
                setRows={setHeaderRows}
                keyPlaceholder="Header 名称"
                valuePlaceholder="Header 值"
              />
            </section>

            <div className="grid gap-5 xl:grid-cols-2">
              <section className="section-card">
                <div className="section-card-header">
                  <div>
                    <h4>模型拉取</h4>
                    <p>端点可从建议列表选择，也可以直接改成自定义路径。</p>
                  </div>
                  <button className={buttonClass('primary')} disabled={busy || !meta} onClick={runModelFetch}>
                    拉取模型
                  </button>
                </div>

                <div className="grid gap-4">
                  <label className="field-wrap">
                    <span className="field-label">建议端点</span>
                    <select
                      className="select-base w-full"
                      value={modelEndpointChoice}
                      onChange={(e) => {
                        const value = e.target.value
                        setModelEndpointChoice(value)
                        if (value !== CUSTOM_OPTION) setModelEndpoint(value)
                      }}
                    >
                      {(meta?.model_endpoints || []).map((endpoint) => (
                        <option key={endpoint} value={endpoint}>{endpoint}</option>
                      ))}
                      <option value={CUSTOM_OPTION}>自定义</option>
                    </select>
                  </label>

                  <label className="field-wrap">
                    <span className="field-label">实际端点</span>
                    <input
                      className="input-base w-full font-mono"
                      value={modelEndpoint}
                      onChange={(e) => {
                        setModelEndpoint(e.target.value)
                        setModelEndpointChoice(CUSTOM_OPTION)
                      }}
                      placeholder="/v1/models"
                    />
                  </label>
                </div>

                {modelError && <p className="notice notice-error m-0">{modelError}</p>}
                <ResultBlock
                  title="模型拉取"
                  result={modelResult}
                  models={modelIds}
                  selectedModel={selectedModel}
                  onPickModel={setSelectedModel}
                />
              </section>

              <section className="section-card">
                <div className="section-card-header">
                  <div>
                    <h4>接口测试</h4>
                    <p>预置会填入 provider 常用接口；若使用 <code>{'{model}'}</code> 占位符，会自动替换成上面的模型名。</p>
                  </div>
                  <button className={buttonClass('primary')} disabled={busy || !meta} onClick={runRequestTest}>
                    发送测试
                  </button>
                </div>

                <div className="grid gap-4">
                  <label className="field-wrap">
                    <span className="field-label">接口预置</span>
                    <select
                      className="select-base w-full"
                      value={requestPresetChoice}
                      onChange={(e) => {
                        const value = e.target.value
                        if (value === CUSTOM_OPTION) {
                          setRequestPresetChoice(CUSTOM_OPTION)
                          return
                        }
                        applyRequestPreset(Number(value))
                      }}
                    >
                      {(meta?.request_presets || []).map((preset, index) => (
                        <option key={`${preset.label}-${index}`} value={String(index)}>
                          {preset.label}
                        </option>
                      ))}
                      <option value={CUSTOM_OPTION}>自定义</option>
                    </select>
                  </label>

                  <div className="grid gap-4 xl:grid-cols-[140px_1fr]">
                    <label className="field-wrap">
                      <span className="field-label">方法</span>
                      <select className="select-base w-full" value={requestMethod} onChange={(e) => setRequestMethod(e.target.value)}>
                        <option value="GET">GET</option>
                        <option value="POST">POST</option>
                        <option value="PUT">PUT</option>
                        <option value="PATCH">PATCH</option>
                        <option value="DELETE">DELETE</option>
                      </select>
                    </label>

                    <label className="field-wrap">
                      <span className="field-label">建议端点</span>
                      <select
                        className="select-base w-full"
                        value={requestEndpointChoice}
                        onChange={(e) => {
                          const value = e.target.value
                          setRequestEndpointChoice(value)
                          if (value !== CUSTOM_OPTION) setRequestEndpoint(value)
                        }}
                      >
                        {requestEndpointSuggestions.map((endpoint) => (
                          <option key={endpoint} value={endpoint}>{endpoint}</option>
                        ))}
                        <option value={CUSTOM_OPTION}>自定义</option>
                      </select>
                    </label>
                  </div>

                  <label className="field-wrap">
                    <span className="field-label">实际端点</span>
                    <input
                      className="input-base w-full font-mono"
                      value={requestEndpoint}
                      onChange={(e) => {
                        setRequestEndpoint(e.target.value)
                        setRequestEndpointChoice(CUSTOM_OPTION)
                        setRequestPresetChoice(CUSTOM_OPTION)
                      }}
                      placeholder="/v1/chat/completions"
                    />
                  </label>

                  <label className="field-wrap">
                    <span className="field-label">请求体</span>
                    <textarea
                      className="textarea-base h-72 font-mono text-xs leading-6"
                      value={requestBody}
                      onChange={(e) => {
                        setRequestBody(e.target.value)
                        setRequestPresetChoice(CUSTOM_OPTION)
                      }}
                      placeholder={'{\n  "model": "{model}"\n}'}
                    />
                  </label>
                </div>

                {requestError && <p className="notice notice-error m-0">{requestError}</p>}
                <ResultBlock
                  title="接口测试"
                  result={requestResult}
                  models={[]}
                  selectedModel={selectedModel}
                  onPickModel={setSelectedModel}
                />
              </section>
            </div>
          </div>
        )}
      </article>
    </section>
  )
}
