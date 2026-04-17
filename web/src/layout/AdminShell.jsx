import { buttonClass, formatClock, panelClass, storageSummary, tokenPreview } from '../app/utils'
import logoImg from '../../logo/logo.png'

const NAV_ICONS = {
  overview: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="3" width="7" height="7" rx="1.5" />
      <rect x="14" y="3" width="7" height="7" rx="1.5" />
      <rect x="3" y="14" width="7" height="7" rx="1.5" />
      <rect x="14" y="14" width="7" height="7" rx="1.5" />
    </svg>
  ),
  key: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4" />
    </svg>
  ),
  vendor: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 2L2 7l10 5 10-5-10-5z" />
      <path d="M2 17l10 5 10-5" />
      <path d="M2 12l10 5 10-5" />
    </svg>
  ),
  stats: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d="M18 20V10" />
      <path d="M12 20V4" />
      <path d="M6 20v-6" />
    </svg>
  ),
  system: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </svg>
  ),
  security: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
    </svg>
  ),
  raw: (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="16 18 22 12 16 6" />
      <polyline points="8 6 2 12 8 18" />
    </svg>
  )
}

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
      {/* Sidebar */}
      <aside className="shell-sidebar">
        <div className="sidebar-header">
          <img src={logoImg} alt="JCProxy" className="sidebar-logo" />
          <h1 className="sidebar-brand-title">JCProxy</h1>
        </div>

        <nav className="sidebar-nav">
          {navItems.map((item) => (
            <button
              key={item.id}
              className={`nav-item ${nav === item.id ? 'nav-item-active' : ''}`}
              onClick={() => onNavChange(item.id)}
            >
              <span className="nav-item-icon">{NAV_ICONS[item.icon] || null}</span>
              <span className="nav-item-label">{item.label}</span>
            </button>
          ))}
        </nav>

        <div className="sidebar-user-footer">
          <div className="flex justify-between items-center">
            <strong>{me.username || username}</strong>
            <span className="sidebar-sync">{formatClock(lastSyncAt)}</span>
          </div>
          <div className="sidebar-footer-actions">
            <button className="sidebar-footer-btn sidebar-footer-btn--sync" disabled={busy} onClick={onRefreshAll}>
              同步配置
            </button>
            <button className="sidebar-footer-btn sidebar-footer-btn--logout" disabled={busy} onClick={onLogout}>
              退出登录
            </button>
          </div>
        </div>
      </aside>

      {/* Main Workspace */}
      <main className="shell-main">
        <header className="header-top">
          <div className="page-title">{currentPageMeta.title}</div>
          
          <div className="header-actions">
            <div className="header-meta">
              <span className="header-meta-item">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{opacity: 0.5}}>
                  <circle cx="12" cy="12" r="10" />
                  <path d="M12 6v6l4 2" />
                </svg>
                <strong>{tokenPreview(token)}</strong>
              </span>
              <span className="header-meta-item">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{opacity: 0.5}}>
                  <ellipse cx="12" cy="5" rx="9" ry="3" />
                  <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3" />
                  <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5" />
                </svg>
                <strong>{storageSummary(upstreamStorage)}</strong>
              </span>
            </div>
          </div>
        </header>

        {notice.text && (
          <div className="px-6 pt-4 pb-0 animate-fade-in">
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
