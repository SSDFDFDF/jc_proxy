import { buttonClass, formatAgo, formatClock, panelClass, tokenPreview } from '../app/utils'

export function SecurityPage({
  busy,
  me,
  username,
  token,
  lastSyncAt,
  maskedConfig,
  newPassword,
  onNewPasswordChange,
  onVerifySession,
  onLogout,
  onRotatePassword
}) {
  return (
    <section className="grid gap-4 2xl:grid-cols-2">
      <article className={panelClass('p-4')}>
        <h3 className="section-title">会话信息</h3>
        <div className="mt-4 space-y-3 text-sm text-slate-700">
          <div className="info-row">
            <span>当前用户</span>
            <strong>{me.username || username}</strong>
          </div>
          <div className="info-row">
            <span>Token 预览</span>
            <strong>{tokenPreview(token)}</strong>
          </div>
          <div className="info-row">
            <span>最近同步</span>
            <strong>{formatClock(lastSyncAt)} · {formatAgo(lastSyncAt)}</strong>
          </div>
          <div className="info-row">
            <span>密码哈希状态</span>
            <strong>{maskedConfig.admin?.password_hash ? '已配置 password_hash' : '未配置 password_hash'}</strong>
          </div>
        </div>
        <div className="mt-4 flex flex-wrap gap-2">
          <button className={buttonClass('ghost')} disabled={busy} onClick={onVerifySession}>
            校验会话
          </button>
          <button className={buttonClass('danger')} disabled={busy} onClick={onLogout}>
            退出当前会话
          </button>
        </div>
      </article>

      <article className={panelClass('p-4')}>
        <h3 className="section-title">管理员密码轮换</h3>
        <div className="mt-4 flex flex-wrap gap-3">
          <input
            type="password"
            className="input-base"
            placeholder="输入新密码"
            value={newPassword}
            onChange={(e) => onNewPasswordChange(e.target.value)}
          />
          <button className={buttonClass('primary')} disabled={busy} onClick={onRotatePassword}>
            轮换密码
          </button>
        </div>
      </article>
    </section>
  )
}
