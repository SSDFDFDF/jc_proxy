import { buttonClass, panelClass, storageSummary } from '../app/utils'

export function OverviewPage({ metrics, vendorRows, upstreamStorage, onOpenUpstreamKeys }) {
  return (
    <section className="space-y-5 animate-fade-in">
      {/* Metric Cards */}
      <div className="grid gap-4 md:grid-cols-2 2xl:grid-cols-4">
        <article className={panelClass('stat-card')}>
          <p className="section-kicker">供应商</p>
          <p className="metric-value">{metrics.vendors}</p>
          <p className="metric-hint">已配置的供应商数量</p>
        </article>
        <article className={panelClass('stat-card')}>
          <p className="section-kicker">上游密钥</p>
          <p className="metric-value">{metrics.upstreamKeys}</p>
          <p className="metric-hint">已登记的上游 API 密钥</p>
        </article>
        <article className={panelClass('stat-card')}>
          <p className="section-kicker">并发中</p>
          <p className="metric-value">{metrics.inflight}</p>
          <p className="metric-hint">正在处理的请求数</p>
        </article>
        <article className={panelClass('stat-card')}>
          <p className="section-kicker">退避中</p>
          <p className="metric-value" style={{ color: 'var(--warning)' }}>{metrics.backoffKeys}</p>
          <p className="metric-hint">处于退避状态的密钥</p>
        </article>
      </div>

      {/* Main Content */}
      <div className="grid gap-5 2xl:grid-cols-[minmax(0,1fr)_340px]">
        {/* Vendor Status Table */}
        <section className={panelClass('p-5')}>
          <div className="mb-4 flex items-center justify-between gap-3">
            <h3 className="section-title">供应商状态</h3>
            <button className={buttonClass('ghost')} onClick={onOpenUpstreamKeys}>
              管理上游密钥 →
            </button>
          </div>
          <div className="table-shell">
            <table className="w-full min-w-[680px] text-sm">
              <thead>
                <tr>
                  <th>供应商</th>
                  <th>上游密钥</th>
                  <th>客户端密钥</th>
                  <th>退避数</th>
                  <th>并发数</th>
                </tr>
              </thead>
              <tbody>
                {vendorRows.map((row) => (
                  <tr key={row.name}>
                    <td className="font-semibold text-[var(--text-primary)]">{row.name}</td>
                    <td className="font-mono">{row.upstreamKeys}</td>
                    <td className="font-mono">{row.clientKeys}</td>
                    <td className="font-mono" style={{ color: row.backoff > 0 ? 'var(--warning)' : undefined }}>
                      {row.backoff}
                    </td>
                    <td className="font-mono">{row.inflight}</td>
                  </tr>
                ))}
                {!vendorRows.length && (
                  <tr>
                    <td colSpan={5} className="px-3 py-12 text-center text-[var(--text-faint)]">
                      暂无供应商
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </section>

        {/* Summary Panel */}
        <section className={panelClass('p-5')}>
          <h3 className="section-title">基础摘要</h3>
          <div className="mt-5 space-y-1">
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
              <strong style={{ color: Number(metrics.warningKeys) > 0 ? 'var(--warning)' : undefined }}>
                {metrics.warningKeys}
              </strong>
            </div>
          </div>
        </section>
      </div>
    </section>
  )
}
