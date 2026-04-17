import { buttonClass } from '../app/utils'
import logoImg from '../../logo/logo.png'

export function LoginPage({ username, password, onUsernameChange, onPasswordChange, onLogin, busy, notice }) {
  return (
    <div className="login-bg">
      <div className="w-full max-w-sm">
        <section className="login-panel p-8">
          <div className="mb-8 text-center">
            <div className="mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-[var(--bg-elevated)] border border-[var(--border)]">
              <img src={logoImg} alt="JCProxy" className="h-10 w-auto" />
            </div>
            <h1 className="text-xl font-bold text-[var(--text-primary)]">JCProxy Console</h1>
          </div>

          <div className="space-y-5">
            <label className="field-wrap">
              <span className="field-label">用户名</span>
              <input className="input-base" placeholder="admin" value={username} onChange={(e) => onUsernameChange(e.target.value)} />
            </label>
            <label className="field-wrap">
              <span className="field-label">密码</span>
              <input
                type="password"
                className="input-base"
                placeholder="输入管理员密码"
                value={password}
                onChange={(e) => onPasswordChange(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && onLogin()}
              />
            </label>
            <button className={`${buttonClass('primary')} w-full h-10`} disabled={busy} onClick={onLogin}>
              {busy ? (
                <span className="flex items-center gap-2">
                  <svg className="animate-spin h-4 w-4" viewBox="0 0 24 24" fill="none">
                    <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" strokeDasharray="60" strokeDashoffset="20" strokeLinecap="round" />
                  </svg>
                  登录中...
                </span>
              ) : '登录'}
            </button>
          </div>

          {notice.text && <p className={`mt-5 notice notice-${notice.tone} animate-fade-in`}>{notice.text}</p>}
        </section>

        <p className="mt-4 text-center text-[11px] text-[var(--text-faint)]">
          JCProxy · API Gateway Console
        </p>
      </div>
    </div>
  )
}
