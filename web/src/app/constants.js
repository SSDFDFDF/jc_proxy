export const STORAGE_TOKEN = 'jc_admin_token'
export const STORAGE_USER = 'jc_admin_user'

export const NAV_ITEMS = [
  { id: 'overview', label: '总览', title: '控制台总览', desc: '供应商、密钥与运行态概况集中查看。', icon: 'overview' },
  { id: 'keyHub', label: '密钥中心', title: '密钥中心', desc: '管理各供应商上游 API 密钥，监控运行状态与异常情况。', icon: 'key' },
  { id: 'vendors', label: '供应商', title: '供应商配置', desc: '维护回源地址、鉴权策略、Header 注入与路径重写。', icon: 'vendor' },
  { id: 'system', label: '系统设置', title: '系统设置', desc: '服务端口、超时、管理员与审计配置。', icon: 'system' },
  { id: 'security', label: '安全中心', title: '安全中心', desc: '会话校验、密码轮换与管理员安全操作。', icon: 'security' },
  { id: 'raw', label: '高级 JSON', title: '高级 JSON', desc: '直接编辑当前运行配置的完整 JSON 结构。', icon: 'raw' }
]

export const EMPTY_CONFIG = {
  server: {
    listen: ':8092',
    read_timeout: 30_000_000_000,
    write_timeout: 0,
    idle_timeout: 90_000_000_000,
    shutdown_timeout: 10_000_000_000
  },
  admin: {
    enabled: false,
    username: 'admin',
    password: '******',
    password_hash: '******',
    session_ttl: 43_200_000_000_000,
    audit_log_path: './data/admin_audit.log',
    allowed_cidrs: [],
    trusted_proxy_cidrs: []
  },
  vendors: {}
}

export const EMPTY_UPSTREAM_KEYS = {
  storage: { driver: 'file', file_path: './data/upstream_keys.json' },
  vendors: [],
  items: {}
}

export const DEFAULT_SYSTEM_FORM = {
  listen: ':8092',
  readTimeout: '30s',
  writeTimeout: '0s',
  idleTimeout: '90s',
  shutdownTimeout: '10s',
  adminEnabled: false,
  adminUsername: 'admin',
  adminSessionTTL: '12h',
  auditLogPath: './data/admin_audit.log',
  adminAllowedCIDRsText: '',
  adminTrustedProxyCIDRsText: ''
}

export const DURATION_UNITS = {
  ns: 1,
  us: 1e3,
  ms: 1e6,
  s: 1e9,
  m: 60e9,
  h: 3600e9
}
