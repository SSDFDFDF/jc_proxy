export const USER_AGENT_PRESETS = [
  {
    id: 'default',
    label: '默认 (JCProxy-Admin-Test)',
    shortLabel: '默认',
    value: ''
  },
  {
    id: 'claudecode_cli',
    label: 'Claude Code (claude-cli)',
    shortLabel: 'Claude Code (cli)',
    value: 'claude-cli/0.2.29 (external, cli)'
  },
  {
    id: 'claudecode',
    label: 'Claude Code (claude-code)',
    shortLabel: 'Claude Code',
    value: 'claude-code/0.2.29 (cli)'
  },
  {
    id: 'codex',
    label: 'OpenAI Codex (codex_cli_rs)',
    shortLabel: 'Codex',
    value: 'codex_cli_rs/0.125.0 (x86_64; linux) xterm-256color'
  },
  {
    id: 'cursor',
    label: 'Cursor',
    shortLabel: 'Cursor',
    value: 'Cursor/0.45.0'
  },
  {
    id: 'copilot',
    label: 'GitHub Copilot',
    shortLabel: 'Copilot',
    value: 'GitHubCopilot/1.250.0'
  },
  {
    id: 'cline',
    label: 'Cline',
    shortLabel: 'Cline',
    value: 'Cline/3.0.0'
  },
  {
    id: 'aider',
    label: 'Aider',
    shortLabel: 'Aider',
    value: 'aider/0.70.0'
  },
  {
    id: 'windsurf',
    label: 'Windsurf',
    shortLabel: 'Windsurf',
    value: 'Windsurf/1.0.0'
  },
  {
    id: 'continue',
    label: 'Continue',
    shortLabel: 'Continue',
    value: 'Continue/0.8.0'
  },
  {
    id: 'openai_sdk',
    label: 'OpenAI Python SDK',
    shortLabel: 'OpenAI SDK',
    value: 'OpenAI/Python 1.60.0'
  },
  {
    id: 'anthropic_sdk',
    label: 'Anthropic TypeScript SDK',
    shortLabel: 'Anthropic SDK',
    value: 'anthropic-typescript/0.38.0'
  }
]

export function normalizeHeaderRows(rows) {
  return (rows || [])
    .filter((row) => String(row?.key || '').trim())
    .map((row) => ({
      key: String(row.key || '').trim(),
      value: String(row.value || '')
    }))
}

export function applyUserAgentToHeaders(headers, userAgent) {
  const rows = [...(headers || [])]
  const ua = String(userAgent || '').trim()
  if (!ua) {
    return normalizeHeaderRows(rows)
  }

  const existingIdx = rows.findIndex(
    (row) => String(row?.key || '').trim().toLowerCase() === 'user-agent'
  )
  if (existingIdx >= 0) {
    rows[existingIdx] = {
      key: rows[existingIdx].key || 'User-Agent',
      value: ua
    }
  } else {
    rows.push({ key: 'User-Agent', value: ua })
  }
  return normalizeHeaderRows(rows)
}

export function matchUserAgentPreset(uaValue, customOption = '__custom__') {
  const trimmed = String(uaValue || '').trim()
  if (!trimmed) {
    return 'default'
  }
  const match = USER_AGENT_PRESETS.find((p) => p.value && p.value === trimmed)
  return match ? match.id : customOption
}
