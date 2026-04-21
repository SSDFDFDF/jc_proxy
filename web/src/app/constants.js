export const STORAGE_TOKEN = 'jc_admin_token'
export const STORAGE_USER = 'jc_admin_user'

export const NAV_ITEMS = [
  { id: 'overview', label: '总览', title: '控制台总览', desc: '供应商、密钥与运行态概况集中查看。', icon: 'overview' },
  { id: 'keyHub', label: '密钥中心', title: '密钥中心', desc: '管理各供应商上游 API 密钥，监控运行状态与异常情况。', icon: 'key' },
  { id: 'vendors', label: '供应商', title: '供应商配置', desc: '维护回源地址、鉴权策略、Header 注入与路径重写。', icon: 'vendor' },
  { id: 'vendorTest', label: '供应商测试', title: '供应商测试', desc: '按供应商 base_url、端点与密钥直接拉取模型并验证接口。', icon: 'probe' },
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

export const CLIENT_HEADER_PRESET_OPTIONS = [
  { value: '', label: '无预置', description: '不启用内置白名单，只使用下方手动补充项。' },
  { value: 'generic_ai', label: 'generic_ai', description: '适合大多数通用 AI / 推理接口，保留常见请求头。' },
  { value: 'openai', label: 'openai', description: '适合 OpenAI 官方接口，保留项目、组织和幂等键等常见头。' },
  { value: 'openai_compatible', label: 'openai_compatible', description: '适合兼容 OpenAI 协议的服务，额外保留常见兼容扩展头。' },
  { value: 'anthropic', label: 'anthropic', description: '适合 Anthropic 接口，保留版本与 beta 相关请求头。' },
  { value: 'gemini', label: 'gemini', description: '适合 Gemini 接口，保留 Google API client 相关头。' }
]

export const CLIENT_HEADER_PRESET_PREVIEWS = {
  generic_ai: ['Accept', 'Accept-Encoding', 'Cache-Control', 'Content-Type', 'Idempotency-Key', 'User-Agent'],
  openai: ['Accept', 'Accept-Encoding', 'Cache-Control', 'Content-Type', 'Idempotency-Key', 'OpenAI-Beta', 'OpenAI-Organization', 'OpenAI-Project', 'User-Agent'],
  openai_compatible: ['Accept', 'Accept-Encoding', 'Cache-Control', 'Content-Type', 'Idempotency-Key', 'OpenAI-Beta', 'OpenAI-Organization', 'OpenAI-Project', 'User-Agent', 'X-Title'],
  anthropic: ['Accept', 'Accept-Encoding', 'Anthropic-Beta', 'Anthropic-Version', 'Content-Type', 'User-Agent'],
  gemini: ['Accept', 'Accept-Encoding', 'Content-Type', 'User-Agent', 'X-Goog-Api-Client']
}

export const DEFAULT_CLIENT_HEADER_DROP_PREVIEW = [
  'Cdn-Loop',
  'CF-Connecting-IP',
  'CF-Ray',
  'Fastly-Client-IP',
  'Forwarded',
  'True-Client-IP',
  'Via',
  'X-Amzn-Trace-Id',
  'X-Forwarded-For',
  'X-Forwarded-Host',
  'X-Forwarded-Port',
  'X-Forwarded-Proto',
  'X-Real-IP',
  'X-Request-Id'
]
