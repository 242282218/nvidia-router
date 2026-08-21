import { defineConfig, presetUno } from 'unocss'

// 设计语言速查（Warm Paper 2.0）：
// - 控件密度统一 36px（h-9），行内紧凑操作 32px（h-8）；均高于 WCAG 2.2 的 24px 下限。
// - 彩色只出现在状态徽章与品牌 Logo，其余界面由暖白层级 + 暖调近黑强调构成。
// - 任何文本/背景新配对必须先登记进 docs/前端对比度配对表.md（scripts/calc_contrast.py 实算）。
export default defineConfig({
  presets: [presetUno()],
  // UnoCSS 66.x has no built-in pointer-coarse variant; register one so every
  // touch-target rule below actually emits @media (pointer: coarse) CSS.
  variants: [
    {
      name: 'pointer-coarse',
      match(matcher) {
        const prefix = 'pointer-coarse:'
        if (!matcher.startsWith(prefix)) return
        return {
          matcher: matcher.slice(prefix.length),
          handle: (input, next) => next({
            ...input,
            parent: `${input.parent ? `${input.parent} $$ ` : ''}@media (pointer: coarse)`,
          }),
        }
      },
      multiPass: true,
    },
  ],
  shortcuts: [
    /* ── 语义字体阶梯（负字距红线：设计约束硬规则 #6，标题收紧最多到 0） ── */
    {
      'type-display': 'font-[var(--text-display)] text-[var(--color-text)]',
      'type-title': 'font-[var(--text-title)] text-[var(--color-text)]',
      'type-heading': 'font-[var(--text-heading)] text-[var(--color-text)]',
      'type-label': 'font-[var(--text-label)] uppercase tracking-[0.1em] text-[var(--color-text-subtle)]',
    },
    /* ── 基底 ── */
    {
      'bg-surface': 'bg-[var(--color-surface)]',
      'bg-elevated': 'bg-[var(--color-elevated)]',
      'bg-card': 'bg-[var(--color-surface)]',
      'bg-input': 'bg-[var(--color-sunken)]',
      'border-subtle': 'border border-[var(--color-border)]',
      'border-hover': 'border border-[var(--color-border-strong)]',
      'text-secondary': 'text-[var(--color-text-secondary)]',
      'text-muted': 'text-[var(--color-text-muted)]',
      'text-accent': 'text-[var(--color-accent-text)]',
      'text-accent-indigo': 'text-[var(--color-info)]',
    },
    /* ── 按钮：四 variant × 两密度。btn-base 承载形状与动效，variant 只描述颜色。
       pointer-coarse 下触控目标提升到 44px（媒介/web P1#1 + 触控尺寸表硬约束）。 ── */
    {
      'btn-base': 'inline-flex h-9 select-none items-center justify-center gap-2 whitespace-nowrap rounded-[var(--radius-control)] px-3.5 text-sm font-medium transition-[background-color,border-color,box-shadow,color,transform] duration-[var(--duration-micro)] active:translate-y-px disabled:cursor-not-allowed disabled:border disabled:border-[var(--color-disabled-border)] disabled:bg-[var(--color-disabled-background)] disabled:text-[var(--color-disabled-foreground)] disabled:opacity-100 disabled:shadow-none focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2 pointer-coarse:h-11',
      'btn-primary': 'btn-base bg-[var(--color-accent-background)] font-semibold text-[var(--color-accent-foreground)] shadow-[inset_0_1px_0_rgba(255,255,255,0.09),var(--shadow-xs)] hover:bg-[var(--color-accent-background-hover)] hover:shadow-[inset_0_1px_0_rgba(255,255,255,0.09),var(--shadow-sm)] active:bg-[var(--color-accent-background)]',
      'btn-secondary': 'btn-base border border-[var(--color-border-strong)] bg-[var(--color-surface)] text-[var(--color-text-secondary)] shadow-[var(--shadow-xs)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] hover:shadow-[var(--shadow-sm)] active:bg-[var(--color-active)]',
      'btn-ghost': 'btn-base px-3 text-[var(--color-text-secondary)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] active:bg-[var(--color-active)]',
      'btn-danger': 'btn-base border border-[var(--color-danger-text)] bg-transparent text-[var(--color-danger-text)] hover:border-[var(--color-danger-background)] hover:bg-[var(--color-danger-background)] hover:text-[var(--color-danger-foreground)] active:bg-[var(--color-danger-background)]',
      // 行内紧凑操作：表格行、卡片角落
      'btn-sm': 'h-8 rounded-[7px] px-2.5 text-xs pointer-coarse:h-11',
      // 紧凑纯图标操作（36px 见方）；触屏提升到 44px 见方，避免实例级 h-8/w-8
      // 覆盖 shortcut 的媒体查询变体导致触屏目标回退。
      'icon-btn-sm': 'icon-btn h-8 w-8 pointer-coarse:h-11 pointer-coarse:w-11',
      // 纯图标操作（编辑/删除/关闭），36px 见方；触屏 44px 见方
      'icon-btn': 'inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-[var(--radius-control)] text-[var(--color-text-subtle)] transition-[background-color,color,transform] duration-[var(--duration-micro)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] active:translate-y-px active:bg-[var(--color-active)] disabled:cursor-not-allowed disabled:text-[var(--color-disabled-foreground)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2 pointer-coarse:h-11 pointer-coarse:w-11',
    },
    /* ── 表单 ── */
    {
      'input-field': 'h-9 w-full rounded-[var(--radius-control)] border border-[var(--color-border-strong)] bg-[var(--color-sunken)] px-3 text-sm text-[var(--color-text)] placeholder:text-[var(--color-text-subtle)] shadow-[inset_0_1px_2px_rgba(28,25,23,0.04)] transition-[background-color,border-color,box-shadow] duration-[var(--duration-micro)] hover:bg-[var(--color-surface)] focus:border-[var(--color-focus)] focus:bg-[var(--color-surface)] focus:outline-none focus:ring-2 focus:ring-[color-mix(in_srgb,var(--color-focus)_30%,transparent)] disabled:cursor-not-allowed disabled:opacity-60 pointer-coarse:h-11',
      'field-label': 'mb-1.5 block text-sm font-medium text-[var(--color-text-secondary)]',
    },
    /* ── 卡片与面板 ── */
    {
      'card': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] shadow-[var(--shadow-xs)]',
      'card-hover': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] shadow-[var(--shadow-xs)] transition-[border-color,box-shadow,transform] duration-200 hover:border-[var(--color-border-strong)] hover:shadow-[var(--shadow-md)] hover:-translate-y-0.5',
      'panel-inset': 'rounded-[var(--radius-control)] bg-[var(--color-sunken)]',
      'stat-card': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] p-5 shadow-[var(--shadow-xs)] transition-[border-color,box-shadow] duration-[var(--duration-micro)] hover:border-[var(--color-border-strong)] hover:shadow-[var(--shadow-sm)]',
      // Static metric tile (no hover affordance — it is read-only telemetry,
      // not a clickable card). Distinct from stat-card which reacts to hover.
      'metric-card': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] p-4 shadow-[var(--shadow-xs)]',
    },
    /* ── 页面骨架 ── */
    {
      'page-container': 'px-4 py-6 sm:px-6 lg:px-8 lg:py-8',
      'content-wrapper': 'mx-auto max-w-[1280px]',
      'section-header': 'mb-6 flex flex-wrap items-end justify-between gap-x-4 gap-y-3',
      'page-title': 'type-title',
      'page-subtitle': 'mt-1.5 max-w-2xl text-sm text-[var(--color-text-muted)]',
    },
    /* ── 徽章 ── */
    {
      'badge': 'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium leading-none',
      'badge-success': 'badge border-[var(--color-success-background)] bg-[var(--color-success-background)] text-[var(--color-success-foreground)]',
      'badge-warning': 'badge border-[var(--color-warning-background)] bg-[var(--color-warning-background)] text-[var(--color-warning-foreground)]',
      'badge-danger': 'badge border-[var(--color-danger-background)] bg-[var(--color-danger-background)] text-[var(--color-danger-foreground)]',
      'badge-muted': 'badge border-[var(--color-muted-border)] bg-[var(--color-muted-background)] text-[var(--color-muted-foreground)]',
      'badge-info': 'badge border-[var(--color-info-background)] bg-[var(--color-info-background)] text-[var(--color-info-foreground)]',
    },
    /* ── 数据表 ── */
    {
      'data-table': 'w-full text-left text-sm',
      'data-table-th': 'border-b border-[var(--color-border)] px-4 py-3 text-left type-label',
      'data-table-td': 'border-b border-[var(--color-border-subtle)] px-4 py-3 text-[var(--color-text-secondary)]',
      'data-table-row': 'transition-colors duration-[var(--duration-micro)] hover:bg-[var(--color-hover)]',
    },
    /* ── 浮层 ── */
    {
      'modal-overlay': 'fixed inset-0 z-50 flex items-center justify-center bg-[var(--color-overlay)] p-4 backdrop-blur-sm',
      'modal-panel': 'w-full max-w-2xl rounded-[var(--radius-overlay)] border border-[var(--color-border)] bg-[var(--color-elevated)] shadow-[var(--shadow-overlay)]',
    },
    /* ── 导航 ── */
    {
      'nav-group-label': 'px-3 pb-1.5 pt-5 type-label first:pt-1',
      'nav-link': 'flex h-9 items-center gap-2.5 rounded-[var(--radius-control)] px-3 text-sm text-[var(--color-text-muted)] transition-[background-color,color,box-shadow] duration-[var(--duration-micro)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2 pointer-coarse:h-11',
      'nav-link-active': 'nav-link bg-[var(--color-active)] font-medium text-[var(--color-text)] shadow-[var(--shadow-xs)]',
    },
    /* ── 分段切换（时间范围等互斥单选）：共享基线 + 选中/未选中两态。
       触屏下高度提升到 44px，与 nav-link 同一触控组合。 ── */
    {
      'segment-group': 'inline-flex items-center gap-0.5 rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-sunken)] p-1 shadow-[var(--shadow-xs)]',
      'segment-item': 'h-8 rounded-[var(--radius-control)] px-3 text-[13px] font-medium transition-[background-color,color,box-shadow] duration-[var(--duration-micro)] pointer-coarse:h-11',
      'segment-item-active': 'bg-[var(--color-elevated)] text-[var(--color-text)] shadow-[var(--shadow-xs)]',
      'segment-item-idle': 'text-[var(--color-text-muted)] hover:text-[var(--color-text)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2',
    },
  ],
  rules: [
    ['animate-fade-in', { animation: 'fadeIn var(--duration-local) var(--ease-enter) both' }],
    ['animate-slide-up', { animation: 'slideUp var(--duration-local) var(--ease-enter) both' }],
    ['animate-scale-in', { animation: 'scaleIn 0.2s var(--ease-enter) both' }],
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
        .transition-base { transition: color var(--duration-micro) ease, background-color var(--duration-micro) ease, border-color var(--duration-micro) ease, opacity var(--duration-micro) ease; }
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
