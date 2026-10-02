import { formatSampleSeconds } from '../app/keyStats'

// Color the two numbers like a typical LLM request/response display:
// <3s green, 3–10s amber, ≥10s red; a missing sample stays faint.
const timingTone = (milliseconds, samples) => {
  const value = Number(milliseconds)
  if (!(Number(samples) > 0) || !Number.isFinite(value) || value < 0) return 'text-[var(--text-faint)]'
  if (value < 3000) return 'text-[var(--success)]'
  if (value < 10000) return 'text-[var(--warning)]'
  return 'text-[var(--danger)]'
}

export function KeyRecentTiming({ stats = {} }) {
  const count = Number(stats.recent_requests || 0)
  const success = Number(stats.recent_success_count || 0)
  const summary = count > 0
    ? `近${count}次均值 · 成功 ${Math.round((success / count) * 100)}% · 样本 ${Number(stats.header_samples || 0)}/${Number(stats.response_samples || 0)}`
    : '近5次均值 · 暂无样本'
  return (
    <div
      className="key-recent-timing flex items-baseline gap-1"
      title={`首包/整包 · ${summary}。成功样本：首包为最终响应头（非首token），整包计到EOF、含下游背压；上游失败统一按999s/999s计入均值，不是真实耗时。客户端取消/下游中断仅计入累计请求，不进入本窗口。颜色：<3s 绿，3-10s 黄，≥10s 红。`}
    >
      <span className="text-[10px] text-[var(--text-faint)]">首包/整包</span>
      <span className={`font-mono tabular-nums ${timingTone(stats.avg_header_ms, stats.header_samples)}`}>{formatSampleSeconds(stats.avg_header_ms, stats.header_samples)}</span>
      <span className="text-[var(--text-faint)]">/</span>
      <span className={`font-mono tabular-nums ${timingTone(stats.avg_response_ms, stats.response_samples)}`}>{formatSampleSeconds(stats.avg_response_ms, stats.response_samples)}</span>
    </div>
  )
}
