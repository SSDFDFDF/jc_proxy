import { isPerformanceStrategy, performanceDefaults, PERFORMANCE_FIELDS } from '../app/performance'
import { buttonClass } from '../app/utils'

export function PerformanceSettings({ vendor, onMutate, busy }) {
  if (!isPerformanceStrategy(vendor?.load_balance, vendor?.provider)) return null
  const defaults = performanceDefaults(vendor.load_balance)
  const settings = vendor.performance || {}
  const update = (field, raw) => onMutate((draft) => {
    if (!draft.performance) draft.performance = {}
    draft.performance[field.key] = raw === '' ? null : Number(raw) * (field.unit || 1)
  })
  return (
    <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-4 space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div className="text-sm font-medium text-[var(--text-primary)]">Key 动态评分 · {vendor.load_balance}</div>
          <div className="mt-1 text-xs text-[var(--text-muted)]">
            {vendor.load_balance === 'highest_success' ? '成本 = (1 + 并发权重 × 在途数) / 成功率^指数。' : '成本 = 头耗时 × (1 + 并发权重 × 在途数) / 成功率^指数。'}
            成本越低越优先；整包耗时不参与评分。最近 5 次有效尝试采样，保存后热更新。
          </div>
        </div>
        <button className={buttonClass('ghost')} type="button" disabled={busy} onClick={() => onMutate((draft) => { draft.performance = {} })}>
          恢复当前策略默认值
        </button>
      </div>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {PERFORMANCE_FIELDS.filter((field) => !field.latencyOnly || vendor.load_balance !== 'highest_success').map((field) => (
          <label key={field.key} className="field-wrap">
            <span className="field-label">{field.label}</span>
            <input
              className="input-base"
              type="number"
              min={field.min}
              max={field.max}
              step={field.step}
              disabled={busy}
              placeholder={`默认 ${defaults[field.key] / (field.unit || 1)}`}
              value={settings[field.key] == null ? '' : settings[field.key] / (field.unit || 1)}
              onChange={(e) => update(field, e.target.value)}
            />
            <span className="mt-1 text-[11px] text-[var(--text-muted)]">{field.help}</span>
          </label>
        ))}
      </div>
      <p className="text-xs text-[var(--text-muted)]">
        留空使用默认值；切到普通策略时设置保留但不生效。缩短 TTL 可能使已有样本立即过期，不会解除 Key 禁用或错误冷却。
      </p>
    </div>
  )
}
