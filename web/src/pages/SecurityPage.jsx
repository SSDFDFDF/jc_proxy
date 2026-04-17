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
    <section className="grid gap-5 2xl:grid-cols-2 animate-fade-in">
      {/* Session Info */}
      <article className={panelClass('p-5')}>
        <h3 className="section-title flex items-center gap-2">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{opacity: 0.6}}>
            <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
          </svg>
          会话信息
        </h3>
        <div className="mt-5 space-y-1">
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
            <strong>
              {maskedConfig.admin?.password_hash ? (
                <span style={{color: 'var(--success)'}}>已配置</span>
              ) : (
                <span style={{color: 'var(--warning)'}}>未配置</span>
              )}
            </strong>
          </div>
        </div>
        <div className="mt-5 flex flex-wrap gap-2">
          <button className={buttonClass('ghost')} disabled={busy} onClick={onVerifySession}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="20 6 9 17 4 12" />
            </svg>
            校验会话
          </button>
          <button className={buttonClass('danger')} disabled={busy} onClick={onLogout}>
            退出当前会话
          </button>
        </div>
      </article>

      {/* Password Rotation */}
      <article className={panelClass('p-5')}>
        <h3 className="section-title flex items-center gap-2">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{opacity: 0.6}}>
            <rect x="3" y="11" width="18" height="11" rx="2" ry="2" /><path d="M7 11V7a5 5 0 0 1 10 0v4" />
          </svg>
          管理员密码轮换
        </h3>
        <p className="mt-2 text-xs text-[var(--text-muted)]">更新管理员密码后，当前会话将保持有效，但下次登录需使用新密码。</p>
        <div className="mt-5 flex flex-wrap gap-3">
          <input
            type="password"
            className="input-base flex-1 min-w-[200px]"
            placeholder="输入新密码"
            value={newPassword}
            onChange={(e) => onNewPasswordChange(e.target.value)}
          />
          <button className={buttonClass('primary')} disabled={busy} onClick={onRotatePassword}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M21 2v6h-6" /><path d="M3 12a9 9 0 0 1 15-6.7L21 8" /><path d="M3 22v-6h6" /><path d="M21 12a9 9 0 0 1-15 6.7L3 16" />
            </svg>
            轮换密码
          </button>
        </div>
      </article>
    </section>
  )
}
