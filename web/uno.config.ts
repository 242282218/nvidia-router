import { defineConfig, presetUno } from 'unocss'

export default defineConfig({
  presets: [presetUno()],
  shortcuts: {
    'bg-surface': 'bg-[var(--color-surface)]',
    'bg-elevated': 'bg-[var(--color-elevated)]',
    'bg-card': 'bg-[var(--color-surface)]',
    'bg-input': 'bg-[var(--color-sunken)]',
    'border-subtle': 'border border-[var(--color-border)]',
    'border-hover': 'border border-[var(--color-border-strong)]',
    'text-secondary': 'text-[var(--color-text-secondary)]',
    'text-muted': 'text-[var(--color-text-muted)]',
    'text-accent': 'text-[var(--color-accent-bright)]',
    'text-accent-indigo': 'text-[var(--color-info)]',
    'btn-primary': 'inline-flex min-h-11 items-center justify-center rounded-lg bg-[var(--color-accent)] px-4 py-2 text-sm font-semibold text-[var(--color-accent-foreground)] transition-colors duration-200 hover:bg-[var(--color-accent-bright)] active:translate-y-px active:bg-[var(--color-accent)] disabled:cursor-not-allowed disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2',
    'btn-secondary': 'inline-flex min-h-11 items-center justify-center rounded-lg border border-[var(--color-border-strong)] bg-[var(--color-surface)] px-4 py-2 text-sm font-medium text-[var(--color-text-secondary)] transition-colors duration-200 hover:border-white/40 hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] active:translate-y-px active:bg-[var(--color-active)] disabled:cursor-not-allowed disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2',
    'btn-ghost': 'inline-flex min-h-11 items-center justify-center rounded-lg px-3 py-2 text-sm text-[var(--color-text-secondary)] transition-colors duration-200 hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2',
    'btn-danger': 'inline-flex min-h-11 items-center justify-center rounded-lg border border-[var(--color-danger)] bg-transparent px-3 py-2 text-sm font-medium text-[var(--color-danger)] transition-colors duration-200 hover:border-[var(--color-danger)] hover:bg-[var(--color-danger)]/15 active:bg-[var(--color-danger)]/30 disabled:cursor-not-allowed disabled:opacity-40 focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2',
    'input-field': 'w-full rounded-lg border border-[var(--color-border-strong)] bg-[var(--color-sunken)] px-3 py-2.5 text-sm text-[var(--color-text)] placeholder:text-[var(--color-text-subtle)] transition-colors duration-200 focus:border-[var(--color-accent)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]/20 disabled:cursor-not-allowed disabled:opacity-60',
    'card': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)]',
    'card-hover': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] transition-colors duration-200 hover:border-[var(--color-border-strong)] hover:bg-[var(--color-hover)]',
    'page-container': 'min-h-screen bg-[var(--color-canvas)] p-4 sm:p-6 lg:p-8',
    'content-wrapper': 'mx-auto max-w-[1440px]',
    'section-header': 'mb-6 flex flex-wrap items-start justify-between gap-4',
    'page-title': 'text-xl font-semibold tracking-tight text-[var(--color-text)] sm:text-2xl',
    'page-subtitle': 'mt-1 max-w-2xl text-sm text-[var(--color-text-muted)]',
    'badge': 'inline-flex items-center rounded-md border px-2 py-1 text-xs font-medium leading-none',
    'badge-success': 'badge border-[var(--color-success)]/25 bg-[var(--color-success)]/10 text-[var(--color-success)]',
    'badge-warning': 'badge border-[var(--color-warning)]/25 bg-[var(--color-warning)]/10 text-[var(--color-warning)]',
    'badge-danger': 'badge border-[var(--color-danger)]/25 bg-[var(--color-danger)]/10 text-[var(--color-danger)]',
    'badge-muted': 'badge border-white/10 bg-white/5 text-[var(--color-text-secondary)]',
    'badge-info': 'badge border-[var(--color-info)]/25 bg-[var(--color-info)]/10 text-[var(--color-info)]',
    'stat-card': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] p-5 transition-colors duration-200 hover:border-[var(--color-border-strong)]',
    'data-table': 'w-full text-left text-sm',
    'data-table-th': 'bg-[var(--color-sunken)] px-4 py-3 text-left text-[11px] font-semibold uppercase tracking-[0.08em] text-[var(--color-text-muted)]',
    'data-table-td': 'border-t border-[var(--color-border)] px-4 py-3.5 text-[var(--color-text-secondary)]',
    'modal-overlay': 'fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm',
    'modal-panel': 'w-full max-w-2xl rounded-xl border border-[var(--color-border-strong)] bg-[var(--color-elevated)] shadow-[var(--shadow-overlay)]',
    'nav-link': 'flex min-h-11 items-center gap-3 rounded-lg px-3 py-2.5 text-sm text-[var(--color-text-muted)] transition-colors duration-200 hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2',
    'nav-link-active': 'nav-link bg-[var(--color-active)] text-[var(--color-accent-bright)]',
  },
  rules: [
    ['animate-fade-in', { animation: 'fadeIn 0.25s ease-out both' }],
    ['animate-slide-up', { animation: 'slideUp 0.25s ease-out both' }],
    ['animate-scale-in', { animation: 'scaleIn 0.2s ease-out both' }],
  ],
  preflights: [
    {
      getCSS: () => `
        @keyframes fadeIn {
          from { opacity: 0; }
          to { opacity: 1; }
        }
        @keyframes slideUp {
          from { opacity: 0; transform: translateY(6px); }
          to { opacity: 1; transform: translateY(0); }
        }
        @keyframes scaleIn {
          from { opacity: 0; transform: scale(0.98); }
          to { opacity: 1; transform: scale(1); }
        }
        .transition-base { transition: color 0.2s ease, background-color 0.2s ease, border-color 0.2s ease, opacity 0.2s ease; }
        .pulse-dot {
          width: 6px; height: 6px; border-radius: 50%;
          animation: pulse-dot 2s ease-in-out infinite;
        }
        @keyframes pulse-dot {
          0%, 100% { opacity: 0.45; }
          50% { opacity: 1; }
        }
      `,
    },
  ],
})
