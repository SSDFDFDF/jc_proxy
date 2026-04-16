import { buttonClass, panelClass } from '../app/utils'

export function LoginPage({ username, password, onUsernameChange, onPasswordChange, onLogin, busy, notice }) {
  return (
    <div className="flex h-screen w-full items-center justify-center bg-[#f8fafc] text-slate-900 px-4">
      <div className="w-full max-w-sm">
        <section className="login-panel rounded-lg border border-slate-200 bg-white p-8">
          <div className="mb-6">
            <h1 className="text-xl font-semibold text-slate-900">jc_proxy 管理后台</h1>
            <p className="mt-1 text-xs text-slate-500">用于维护供应商配置、上游密钥和运行状态。</p>
          </div>

          <div className="space-y-4">
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
            <button className={`${buttonClass('primary')} w-full`} disabled={busy} onClick={onLogin}>
              {busy ? '登录中...' : '登录'}
            </button>
          </div>

          {notice.text && <p className={`mt-4 notice notice-${notice.tone}`}>{notice.text}</p>}
        </section>
      </div>
    </div>
  )
}
