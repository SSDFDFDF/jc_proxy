import { buttonClass } from '../app/utils'

export function RequestPluginsEditor({ plugins = [], onChange, disabled }) {
  const update = (index, patch) => onChange(plugins.map((plugin, i) => i === index ? { ...plugin, ...patch } : plugin))
  const move = (index, offset) => {
    const next = [...plugins]
    ;[next[index], next[index + offset]] = [next[index + offset], next[index]]
    onChange(next)
  }
  const addPlugin = () => {
    let suffix = plugins.length + 1
    while (plugins.some((plugin) => plugin.name === `history-filter-${suffix}`)) suffix += 1
    onChange([...plugins, { name: `history-filter-${suffix}`, builtin: 'codex-history-filter', methods: ['POST'], paths: ['/v1/responses', '/responses'] }])
  }
  return (
    <fieldset disabled={disabled} className="space-y-4">
      <p className="text-xs text-[var(--text-muted)]">按顺序处理请求体，优先使用原生插件。方法和路径留空表示全部匹配；路径使用重写后的路径，支持末尾 *。错误与重试仍由供应商错误策略控制。</p>
      {plugins.map((plugin, index) => {
        const mode = plugin.builtin ? 'builtin' : plugin.script !== undefined ? 'script' : 'source'
        return (
          <div key={index} className="rounded-lg border border-[var(--border)] p-3 space-y-3">
            <div className="flex flex-wrap items-center gap-3">
              <label className="field-inline"><input type="checkbox" checked={plugin.enabled !== false} onChange={(e) => update(index, { enabled: e.target.checked })} />启用</label>
              <input aria-label="插件名称" className="input-base flex-1" placeholder="插件名称（唯一）" value={plugin.name || ''} onChange={(e) => update(index, { name: e.target.value })} />
              <button type="button" className={buttonClass('ghost')} disabled={disabled || index === 0} onClick={() => move(index, -1)}>上移</button>
              <button type="button" className={buttonClass('ghost')} disabled={disabled || index === plugins.length - 1} onClick={() => move(index, 1)}>下移</button>
              <button type="button" className={buttonClass('ghost')} onClick={() => onChange(plugins.filter((_, i) => i !== index))}>移除</button>
            </div>
            <label className="field-wrap">
              <span className="field-label">实现方式</span>
              <select className="select-base" value={mode} onChange={(e) => update(index, {
                builtin: e.target.value === 'builtin' ? 'codex-history-filter' : undefined,
                script: e.target.value === 'script' ? './plugins/codex-history-filter.js' : undefined,
                source: e.target.value === 'source' ? 'function onRequest(request) {\n  return request.body;\n}' : undefined
              })}>
                <option value="builtin">原生 Go 插件（推荐）</option>
                <option value="script">JavaScript 文件</option>
                <option value="source">内联 JavaScript</option>
              </select>
            </label>
            {mode === 'builtin' && <label className="field-wrap"><span className="field-label">原生插件</span><select className="select-base" value={plugin.builtin} onChange={(e) => update(index, { builtin: e.target.value })}><option value="codex-history-filter">历史 reasoning / ID 过滤</option></select></label>}
            {mode === 'script' && <label className="field-wrap"><span className="field-label">服务器上的脚本路径（相对工作目录）</span><input className="input-base font-mono" value={plugin.script} onChange={(e) => update(index, { script: e.target.value })} /></label>}
            {mode === 'source' && <label className="field-wrap"><span className="field-label">脚本：onRequest(request) 返回新 body 字符串，返回 null / undefined 保持原文</span><textarea className="textarea-base h-48 font-mono" spellCheck={false} value={plugin.source || ''} onChange={(e) => update(index, { source: e.target.value })} /></label>}
            <div className="grid gap-3 md:grid-cols-2">
              <label className="field-wrap"><span className="field-label">HTTP 方法（逗号分隔）</span><input className="input-base" placeholder="POST" value={(plugin.methods || []).join(',')} onChange={(e) => update(index, { methods: e.target.value.split(',') })} /></label>
              <label className="field-wrap"><span className="field-label">匹配路径（逗号分隔）</span><input className="input-base" placeholder="/v1/responses,/responses" value={(plugin.paths || []).join(',')} onChange={(e) => update(index, { paths: e.target.value.split(',') })} /></label>
              <label className="field-wrap"><span className="field-label">执行超时（毫秒，默认 100）</span><input type="number" min="0" max="5000" className="input-base" value={plugin.timeout ? plugin.timeout / 1e6 : ''} onChange={(e) => update(index, { timeout: Number(e.target.value) * 1e6 })} /></label>
              <label className="field-wrap"><span className="field-label">请求体上限（KiB，默认 4096）</span><input type="number" min="0" max="65536" className="input-base" value={plugin.max_body_bytes ? plugin.max_body_bytes / 1024 : ''} onChange={(e) => update(index, { max_body_bytes: Number(e.target.value) * 1024 })} /></label>
            </div>
          </div>
        )
      })}
      <button type="button" className={buttonClass('ghost')} disabled={disabled || plugins.length >= 16} onClick={addPlugin}>添加插件</button>
    </fieldset>
  )
}
