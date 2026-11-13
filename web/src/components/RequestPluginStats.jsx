import { buttonClass } from '../app/utils'

const statusLabels = {
  changed: '执行成功 · 已改写',
  unchanged: '执行成功 · 无需改写',
  rejected: '插件主动拒绝',
  failed: '执行失败',
  blocked: '执行前拦截',
}

const reasonLabels = {
  execution_failed: '插件执行异常',
  rejected: '输入不符合插件规则',
  timeout: '执行超时',
  canceled: '请求已取消',
  output_too_large: '输出超过大小限制',
  input_too_large: '输入超过大小限制',
  content_encoding: '不支持的 Content-Encoding',
  read_failed: '请求体读取失败',
  invalid_utf8: '请求体不是 UTF-8',
}

export function RequestPluginStats({ stats, onRefresh, disabled }) {
  return (
    <div className="space-y-2 text-xs text-[var(--text-muted)]">
      <div className="flex items-center justify-between gap-3">
        <p>当前运行状态（已保存配置）。成功仅表示插件处理成功，上游结果见 Key 统计。</p>
        <button type="button" className={buttonClass('ghost')} disabled={disabled} onClick={onRefresh}>刷新状态</button>
      </div>
      {stats === undefined ? <p>等待运行统计刷新。</p> : stats.length === 0 ? <p>当前未加载启用的插件。</p> : stats.map((plugin) => {
        const last = plugin.last
        const summary = last?.summary
        return (
          <div key={plugin.name} className="rounded-lg border border-[var(--border)] p-3 space-y-1">
            <p className="font-medium text-[var(--text-primary)]">{plugin.name} · {last ? statusLabels[last.status] || last.status : '已加载 · 尚无执行记录'}</p>
            <p>执行 {plugin.calls} 次 · 改写 {plugin.changed} · 未改写 {plugin.unchanged} · 拒绝 {plugin.rejected} · 失败 {plugin.failed} · 执行前拦截 {plugin.blocked}</p>
            {last && <>
              <p>最近：{new Date(last.at).toLocaleString()} · {last.status === 'blocked' ? '未执行' : `${(last.duration_ns / 1e6).toFixed(3)} ms · ${last.input_bytes} B 输入${['changed', 'unchanged'].includes(last.status) ? ` → ${last.output_bytes} B 输出` : ''}`}</p>
              {last.reason && <p>{reasonLabels[last.reason] || '处理失败'}</p>}
              {summary?.reported && <p>最近处理：删除 reasoning {summary.reasoning_removed} 项 · 删除 ID {summary.ids_removed} 个 · 移除 include {summary.includes_removed} 项 · {summary.store_disabled ? '已设置 store=false' : 'store 已为 false'}</p>}
              {!summary?.reported && ['changed', 'unchanged'].includes(last.status) && <p>此插件未上报字段处理数量。</p>}
            </>}
          </div>
        )
      })}
      <p>按需刷新，不自动轮询。仅保留累计计数和一条最近摘要，不记录正文；重启或修改该插件配置/脚本后重置。尚无记录时，可用匹配的方法和路径发起供应商测试，再刷新查看。</p>
    </div>
  )
}
