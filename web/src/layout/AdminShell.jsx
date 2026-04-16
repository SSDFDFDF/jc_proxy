import { buttonClass, formatClock, panelClass, storageSummary, tokenPreview } from '../app/utils'

export function AdminShell({
  navItems,
  nav,
  onNavChange,
  me,
  username,
  lastSyncAt,
  notice,
  currentPageMeta,
  token,
  upstreamStorage,
  busy,
  onRefreshAll,
  onRefreshStats,
  onLogout,
  children
}) {
  return (
    <div className="shell-layout">
      {/* Sidebar Focus Area */}
      <aside className="shell-sidebar">
        <div className="sidebar-header">
          <h1 className="sidebar-brand-title">jc_proxy Console</h1>
        </div>

        <nav className="sidebar-nav">
          {navItems.map((item) => (
            <button
              key={item.id}
              className={`nav-item ${nav === item.id ? 'nav-item-active' : ''}`}
              onClick={() => onNavChange(item.id)}
            >
              <span className="nav-item-label">{item.label}</span>
            </button>
          ))}
        </nav>

        <div className="sidebar-user-footer">
          <div className="flex justify-between items-center mb-1">
            <strong>{me.username || username}</strong>
            <span className="sidebar-sync">{formatClock(lastSyncAt)}</span>
          </div>
          <div className="flex gap-2">
            <button className="text-[12px] text-blue-600 hover:text-blue-700 disabled:opacity-50" disabled={busy} onClick={onRefreshAll}>
              同步配置
            </button>
            <button className="text-[12px] text-red-600 hover:text-red-700 disabled:opacity-50" disabled={busy} onClick={onLogout}>
              退出登录
            </button>
          </div>
        </div>
      </aside>

      {/* Main Workspace Area */}
      <main className="shell-main">
        {/* Top Header replacing Page Hero */}
        <header className="header-top">
          <div className="page-title">{currentPageMeta.title}</div>
          
          <div className="header-actions">
            <div className="header-meta">
              <span className="header-meta-item">
                会话: <strong>{tokenPreview(token)}</strong>
              </span>
              <span className="header-meta-item">
                存储: <strong>{storageSummary(upstreamStorage)}</strong>
              </span>
            </div>
          </div>
        </header>

        {notice.text && (
          <div className="px-5 pt-4 pb-0">
            <p className={`notice notice-${notice.tone} m-0`}>{notice.text}</p>
          </div>
        )}

        <div className="shell-content">
          {children}
        </div>
      </main>
    </div>
  )
}
