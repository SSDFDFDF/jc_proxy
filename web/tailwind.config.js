/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,jsx}'],
  theme: {
    extend: {
      colors: {
        base: '#09090b',
        surface: '#18181b',
        elevated: '#1f1f23',
        'z-hover': '#27272a',
        'z-border': '#2e2e33',
        'z-border-strong': '#3f3f46',
        'z-text': '#fafafa',
        'z-text-secondary': '#a1a1aa',
        'z-text-muted': '#71717a',
        'z-text-faint': '#52525b',
        'z-accent': '#3b82f6',
        'z-accent-hover': '#2563eb',
        'z-success': '#22c55e',
        'z-warning': '#f59e0b',
        'z-danger': '#ef4444'
      },
      fontFamily: {
        sans: ['"Inter"', '"Noto Sans SC"', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', 'ui-monospace', 'SFMono-Regular', 'monospace']
      },
      boxShadow: {
        card: '0 1px 3px rgba(0,0,0,0.3), 0 1px 2px rgba(0,0,0,0.2)',
        glow: '0 0 20px rgba(59,130,246,0.15)'
      },
      borderRadius: {
        DEFAULT: '8px'
      }
    }
  },
  plugins: []
}
