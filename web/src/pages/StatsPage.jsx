import { buttonClass, panelClass } from '../app/utils'
import { RuntimeKeyStatCard } from '../components/RuntimeKeyStatCard'

export function StatsPage({
  busy,
  stats,
  autoRefreshStats,
  refreshEverySec,
  onToggleAutoRefresh,
  onRefreshEverySecChange,
  onRefresh
}) {
  return (
    <section className="space-y-4">
      <article className={panelClass('p-4')}>
        <div className="flex flex-wrap items-end gap-3">
          <button className={buttonClass('ghost')} disabled={busy} onClick={onRefresh}>
            立即刷新
          </button>
          <button className={autoRefreshStats ? buttonClass('primary') : buttonClass()} onClick={onToggleAutoRefresh}>
            自动刷新: {autoRefreshStats ? '开' : '关'}
          </button>
          <label className="field-wrap w-40">
            <span className="field-label">刷新间隔</span>
            <select className="select-base" value={refreshEverySec} onChange={(e) => onRefreshEverySecChange(e.target.value)}>
              <option value="2">2 秒</option>
              <option value="4">4 秒</option>
              <option value="8">8 秒</option>
              <option value="15">15 秒</option>
            </select>
          </label>
        </div>
      </article>

      <div className="grid gap-3 lg:grid-cols-2 2xl:grid-cols-3">
        {Object.entries(stats.vendors || {}).map(([vendorName, keys]) => {
          const backoffCount = (keys || []).filter((item) => Number(item.backoff_remaining_seconds || 0) > 0).length
          return (
            <article key={vendorName} className={panelClass('p-4')}>
              <div className="flex items-center justify-between gap-3">
                <div>
                  <h3 className="section-title">{vendorName}</h3>
                </div>
                <span className={`status-pill ${backoffCount > 0 ? 'pill-err' : 'pill-ok'}`}>退避 {backoffCount}</span>
              </div>

              <div className="mt-4 space-y-3">
                {(keys || []).map((item, index) => (
                  <RuntimeKeyStatCard key={index} item={item} />
                ))}
                {!(keys || []).length && <p className="text-sm text-slate-400">暂无密钥统计</p>}
              </div>
            </article>
          )
        })}
      </div>
    </section>
  )
}
