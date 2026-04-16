import { buttonClass, panelClass, storageSummary } from '../app/utils'

export function OverviewPage({ metrics, vendorRows, upstreamStorage, onOpenUpstreamKeys }) {
  return (
    <section className="space-y-4">
      <div className="grid gap-3 md:grid-cols-2 2xl:grid-cols-4">
        <article className={panelClass('stat-card')}>
          <p className="section-kicker">供应商</p>
          <p className="metric-value">{metrics.vendors}</p>
          <p className="metric-hint">当前已配置的供应商数量</p>
        </article>
        <article className={panelClass('stat-card')}>
          <p className="section-kicker">上游密钥</p>
          <p className="metric-value">{metrics.upstreamKeys}</p>
          <p className="metric-hint">已登记的上游 API 密钥总数</p>
        </article>
        <article className={panelClass('stat-card')}>
          <p className="section-kicker">并发中</p>
          <p className="metric-value">{metrics.inflight}</p>
          <p className="metric-hint">正在处理的请求数</p>
        </article>
        <article className={panelClass('stat-card')}>
          <p className="section-kicker">退避中</p>
          <p className="metric-value text-amber-600">{metrics.backoffKeys}</p>
          <p className="metric-hint">当前处于退避状态的密钥</p>
        </article>
      </div>

      <div className="grid gap-4 2xl:grid-cols-[minmax(0,1fr)_320px]">
        <section className={panelClass('p-4')}>
          <div className="mb-3 flex items-center justify-between gap-3">
            <div>
              <h3 className="section-title">供应商状态</h3>
            </div>
            <button className={buttonClass('ghost')} onClick={onOpenUpstreamKeys}>
              管理上游密钥
            </button>
          </div>
          <div className="table-shell">
            <table className="w-full min-w-[680px] text-sm">
              <thead>
                <tr>
                  <th className="px-3 py-3 text-left">供应商</th>
                  <th className="px-3 py-3 text-left">上游密钥</th>
                  <th className="px-3 py-3 text-left">客户端密钥</th>
                  <th className="px-3 py-3 text-left">退避数</th>
                  <th className="px-3 py-3 text-left">并发数</th>
                </tr>
              </thead>
              <tbody>
                {vendorRows.map((row) => (
                  <tr key={row.name}>
                    <td className="px-3 py-3 font-semibold text-slate-900">{row.name}</td>
                    <td className="px-3 py-3">{row.upstreamKeys}</td>
                    <td className="px-3 py-3">{row.clientKeys}</td>
                    <td className={`px-3 py-3 ${row.backoff > 0 ? 'text-amber-600' : 'text-slate-600'}`}>{row.backoff}</td>
                    <td className="px-3 py-3">{row.inflight}</td>
                  </tr>
                ))}
                {!vendorRows.length && (
                  <tr>
                    <td colSpan={5} className="px-3 py-10 text-center text-slate-400">
                      暂无供应商
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </section>

        <section className={panelClass('p-4')}>
          <h3 className="section-title">基础摘要</h3>
          <div className="mt-4 space-y-3 text-sm text-slate-700">
            <div className="info-row">
              <span>Key 存储</span>
              <strong>{upstreamStorage?.driver || '--'}</strong>
            </div>
            <div className="info-row">
              <span>目标位置</span>
              <strong>{storageSummary(upstreamStorage)}</strong>
            </div>
            <div className="info-row">
              <span>客户端密钥</span>
              <strong>{metrics.clientKeys}</strong>
            </div>
            <div className="info-row">
              <span>客户端鉴权</span>
              <strong>{metrics.clientAuthEnabled}</strong>
            </div>
            <div className="info-row">
              <span>Resin</span>
              <strong>{metrics.resinEnabled}</strong>
            </div>
            <div className="info-row">
              <span>预警密钥</span>
              <strong>{metrics.warningKeys}</strong>
            </div>
          </div>
        </section>
      </div>
    </section>
  )
}
