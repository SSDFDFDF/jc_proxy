import { buttonClass, panelClass } from '../app/utils'

export function SystemPage({ busy, systemForm, maskedConfig, onSystemFormChange, onSave, onRefreshPreview }) {
  return (
    <section className="grid gap-5 2xl:grid-cols-[1fr_1fr] animate-fade-in">
      <article className={panelClass('p-5')}>
        <h3 className="section-title">系统与管理配置</h3>

        <div className="mt-5 grid gap-4 xl:grid-cols-2">
          <label className="field-wrap">
            <span className="field-label">监听地址</span>
            <input className="input-base" value={systemForm.listen} onChange={(e) => onSystemFormChange((prev) => ({ ...prev, listen: e.target.value }))} />
          </label>
          <label className="field-wrap">
            <span className="field-label">读取超时</span>
            <input className="input-base" value={systemForm.readTimeout} onChange={(e) => onSystemFormChange((prev) => ({ ...prev, readTimeout: e.target.value }))} />
          </label>
          <label className="field-wrap">
            <span className="field-label">写入超时</span>
            <input className="input-base" value={systemForm.writeTimeout} onChange={(e) => onSystemFormChange((prev) => ({ ...prev, writeTimeout: e.target.value }))} />
          </label>
          <label className="field-wrap">
            <span className="field-label">空闲超时</span>
            <input className="input-base" value={systemForm.idleTimeout} onChange={(e) => onSystemFormChange((prev) => ({ ...prev, idleTimeout: e.target.value }))} />
          </label>
          <label className="field-wrap">
            <span className="field-label">关闭等待</span>
            <input className="input-base" value={systemForm.shutdownTimeout} onChange={(e) => onSystemFormChange((prev) => ({ ...prev, shutdownTimeout: e.target.value }))} />
          </label>
          <label className="field-wrap">
            <span className="field-label">管理员会话 TTL</span>
            <input className="input-base" value={systemForm.adminSessionTTL} onChange={(e) => onSystemFormChange((prev) => ({ ...prev, adminSessionTTL: e.target.value }))} />
          </label>
          <label className="field-wrap">
            <span className="field-label">管理员用户名</span>
            <input className="input-base" value={systemForm.adminUsername} onChange={(e) => onSystemFormChange((prev) => ({ ...prev, adminUsername: e.target.value }))} />
          </label>
          <label className="field-wrap">
            <span className="field-label">审计日志路径</span>
            <input className="input-base" value={systemForm.auditLogPath} onChange={(e) => onSystemFormChange((prev) => ({ ...prev, auditLogPath: e.target.value }))} />
          </label>
          <label className="field-wrap xl:col-span-2">
            <span className="field-label">管理面允许来源 CIDR</span>
            <textarea
              className="textarea-base h-24"
              value={systemForm.adminAllowedCIDRsText}
              onChange={(e) => onSystemFormChange((prev) => ({ ...prev, adminAllowedCIDRsText: e.target.value }))}
            />
          </label>
          <label className="field-wrap xl:col-span-2">
            <span className="field-label">受信代理 CIDR</span>
            <textarea
              className="textarea-base h-24"
              value={systemForm.adminTrustedProxyCIDRsText}
              onChange={(e) => onSystemFormChange((prev) => ({ ...prev, adminTrustedProxyCIDRsText: e.target.value }))}
            />
          </label>
          <label className="field-wrap">
            <span className="field-label">启用后台</span>
            <select
              className="select-base"
              value={systemForm.adminEnabled ? 'true' : 'false'}
              onChange={(e) => onSystemFormChange((prev) => ({ ...prev, adminEnabled: e.target.value === 'true' }))}
            >
              <option value="true">true</option>
              <option value="false">false</option>
            </select>
          </label>
        </div>

        <p className="mt-4 text-xs text-[var(--text-muted)] leading-relaxed">
          每行一个 CIDR。若服务部署在反向代理后，请把 LB / 网关网段填到"受信代理 CIDR"，系统才会按 X-Forwarded-For 识别真实来源。
        </p>

        <button className={`mt-5 ${buttonClass('primary')}`} disabled={busy} onClick={onSave}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" /><polyline points="17,21 17,13 7,13 7,21" /><polyline points="7,3 7,8 15,8" />
          </svg>
          保存系统配置
        </button>
      </article>

      <article className={panelClass('p-5')}>
        <div className="mb-4 flex items-center justify-between gap-3">
          <h3 className="section-title">配置预览</h3>
          <button className={buttonClass('ghost')} disabled={busy} onClick={onRefreshPreview}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 2v6h-6" /><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M3 22v-6h6" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
            </svg>
            刷新
          </button>
        </div>
        <textarea
          className="textarea-base h-[38rem] font-mono text-xs leading-6"
          readOnly
          value={JSON.stringify(maskedConfig, null, 2)}
          style={{ background: 'var(--bg-base)', color: 'var(--text-secondary)' }}
        />
      </article>
    </section>
  )
}
