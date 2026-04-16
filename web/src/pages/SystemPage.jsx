import { buttonClass, panelClass } from '../app/utils'

export function SystemPage({ busy, systemForm, maskedConfig, onSystemFormChange, onSave, onRefreshPreview }) {
  return (
    <section className="grid gap-4 2xl:grid-cols-[1fr_1fr]">
      <article className={panelClass('p-4')}>
        <h3 className="section-title">系统与管理配置</h3>

        <div className="mt-4 grid gap-4 xl:grid-cols-2">
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

        <p className="mt-3 text-xs text-slate-500">每行一个 CIDR。留空表示不限制来源地址。</p>

        <button className={`mt-4 ${buttonClass('primary')}`} disabled={busy} onClick={onSave}>
          保存系统配置
        </button>
      </article>

      <article className={panelClass('p-4')}>
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <h3 className="section-title">配置预览</h3>
          </div>
          <button className={buttonClass('ghost')} disabled={busy} onClick={onRefreshPreview}>
            刷新预览
          </button>
        </div>
        <textarea className="textarea-base h-[38rem] font-mono text-xs leading-6" readOnly value={JSON.stringify(maskedConfig, null, 2)} />
      </article>
    </section>
  )
}
