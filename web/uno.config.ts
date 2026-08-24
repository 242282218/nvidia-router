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
       pointer-coarse 下触控目标提升到 44px（媒介/web P1#1 + 触控尺寸表硬约束）。
       扁平化：无阴影/内高光，层级靠填充色与描边。 ── */
    {
      'btn-base': 'inline-flex h-9 select-none items-center justify-center gap-2 whitespace-nowrap rounded-[var(--radius-control)] px-3.5 text-sm font-medium transition-[background-color,border-color,color,transform] duration-[var(--duration-micro)] active:translate-y-px disabled:cursor-not-allowed disabled:border disabled:border-[var(--color-disabled-border)] disabled:bg-[var(--color-disabled-background)] disabled:text-[var(--color-disabled-foreground)] disabled:opacity-100 focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2 pointer-coarse:h-11',
      'btn-primary': 'btn-base bg-[var(--color-accent-background)] font-semibold text-[var(--color-accent-foreground)] hover:bg-[var(--color-accent-background-hover)] active:bg-[var(--color-accent-background)]',
      'btn-secondary': 'btn-base border border-[var(--color-border-strong)] bg-[var(--color-surface)] text-[var(--color-text-secondary)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] active:bg-[var(--color-active)]',
      'btn-ghost': 'btn-base px-3 text-[var(--color-text-secondary)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] active:bg-[var(--color-active)]',
      'btn-danger': 'btn-base border border-[var(--color-danger-text)] bg-transparent text-[var(--color-danger-text)] hover:border-[var(--color-danger-background)] hover:bg-[var(--color-danger-background)] hover:text-[var(--color-danger-foreground)] active:bg-[var(--color-danger-background)]',
      // 行内紧凑操作：表格行、卡片角落
      'btn-sm': 'h-8 rounded-[var(--radius-control)] px-2.5 text-xs pointer-coarse:h-11',
      // 紧凑纯图标操作（36px 见方）；触屏提升到 44px 见方，避免实例级 h-8/w-8
      // 覆盖 shortcut 的媒体查询变体导致触屏目标回退。
      'icon-btn-sm': 'icon-btn h-8 w-8 pointer-coarse:h-11 pointer-coarse:w-11',
      // 纯图标操作（编辑/删除/关闭），36px 见方；触屏 44px 见方
      'icon-btn': 'inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-[var(--radius-control)] text-[var(--color-text-subtle)] transition-[background-color,color,transform] duration-[var(--duration-micro)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] active:translate-y-px active:bg-[var(--color-active)] disabled:cursor-not-allowed disabled:text-[var(--color-disabled-foreground)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2 pointer-coarse:h-11 pointer-coarse:w-11',
    },
    /* ── 表单 ── */
    {
      'input-field': 'h-9 w-full rounded-[var(--radius-control)] border border-[var(--color-border-strong)] bg-[var(--color-sunken)] px-3 text-sm text-[var(--color-text)] placeholder:text-[var(--color-text-subtle)] transition-[background-color,border-color] duration-[var(--duration-micro)] hover:bg-[var(--color-surface)] focus:border-[var(--color-focus)] focus:bg-[var(--color-surface)] disabled:cursor-not-allowed disabled:opacity-60 pointer-coarse:h-11',
      'field-label': 'mb-1.5 block text-sm font-medium text-[var(--color-text-secondary)]',
      /* 复选框：原生控件忽略 background/border/text 等属性，只有 accent-color
         真正生效，所以这里是唯一权威配方——实例不要再各自拼一套。
         16px 是视觉尺寸；命中区靠 checkbox-hit（裸控件）或外层 label 的
         min-h 撑到 24px 下限。 */
      'checkbox-control': 'h-4 w-4 shrink-0 cursor-pointer accent-[var(--color-accent)] disabled:cursor-not-allowed',
      /* 无文字标签的裸复选框（表格选择列）：包一层把命中区补到 24px。 */
      'checkbox-hit': 'inline-flex h-6 w-6 cursor-pointer items-center justify-center pointer-coarse:h-11 pointer-coarse:w-11',
      /* 带文字的复选框行：文字只有 12px 时行高不足 24px，需要显式下限。 */
      'checkbox-row': 'flex min-h-6 cursor-pointer items-center gap-2 pointer-coarse:min-h-11',
    },
    /* ── 卡片与面板（扁平：无阴影，层级靠底色亮度差 + 描边） ── */
    {
      'card': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)]',
      'card-hover': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] transition-colors duration-200 hover:border-[var(--color-border-strong)]',
      'panel-inset': 'rounded-[var(--radius-control)] bg-[var(--color-sunken)]',
      'stat-card': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] p-6 transition-colors duration-[var(--duration-micro)] hover:border-[var(--color-border-strong)]',
      // Static metric tile (no hover affordance — it is read-only telemetry,
      // not a clickable card). Distinct from stat-card which reacts to hover.
      'metric-card': 'rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-surface)] p-5',
    },
    /* ── 页面骨架 ── */
    {
      'page-container': 'px-4 py-6 sm:px-6 lg:px-8 lg:py-8',
      'content-wrapper': 'mx-auto max-w-[1280px]',
      'section-header': 'mb-6 flex flex-wrap items-end justify-between gap-x-4 gap-y-3',
      'page-title': 'type-title',
      'page-subtitle': 'mt-2 max-w-2xl text-sm text-[var(--color-text-muted)]',
    },
    /* ── 徽章 ── */
    {
      'badge': 'inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border px-2.5 py-1 text-xs font-medium leading-none',
      'badge-success': 'badge border-[var(--color-success-background)] bg-[var(--color-success-background)] text-[var(--color-success-foreground)]',
      'badge-warning': 'badge border-[var(--color-warning-background)] bg-[var(--color-warning-background)] text-[var(--color-warning-foreground)]',
      'badge-danger': 'badge border-[var(--color-danger-background)] bg-[var(--color-danger-background)] text-[var(--color-danger-foreground)]',
      'badge-muted': 'badge border-[var(--color-muted-border)] bg-[var(--color-muted-background)] text-[var(--color-muted-foreground)]',
      'badge-info': 'badge border-[var(--color-info-background)] bg-[var(--color-info-background)] text-[var(--color-info-foreground)]',
    },
    /* ── 数据表 ── */
    {
      // border-collapse 不只是省掉 UA 默认 border-spacing:2px 带来的额外宽度
      //（9 列表格凭空多 20px，足以在 1440 下逼出横向滚动条），也让行分隔用的
      // 1px border-b 连成整线，而不是每格之间断开。
      'data-table': 'w-full border-collapse text-left text-sm',
      'data-table-th': 'border-b border-[var(--color-border)] px-4 py-3 text-left type-label whitespace-nowrap',
      'data-table-td': 'border-b border-[var(--color-border-subtle)] px-4 py-3 text-[var(--color-text-secondary)]',
      // Warm Restraint：hover 高亮压到 40% 透明，行扫过只留一丝暖意
      'data-table-row': 'transition-colors duration-[var(--duration-micro)] hover:bg-[color-mix(in_srgb,var(--color-hover)_40%,transparent)]',
    },
    /* ── 浮层（扁平：scrim 无 blur，面板靠描边与 raised 底色区分） ── */
    {
      'modal-overlay': 'fixed inset-0 z-[var(--z-modal)] flex min-h-dvh items-start justify-center overflow-y-auto bg-[var(--color-overlay)] p-4 sm:items-center',
      'modal-panel': 'w-full max-h-[calc(100dvh-2rem)] max-w-2xl overflow-hidden rounded-[var(--radius-overlay)] border border-[var(--color-border)] bg-[var(--color-elevated)]',
      // UiMenu 菜单项：36px 行高、左对齐，与控件圆角基线一致。
      'menu-item': 'flex h-9 w-full items-center gap-2.5 whitespace-nowrap rounded-[var(--radius-control)] px-3 text-left text-sm text-[var(--color-text-secondary)] transition-colors duration-[var(--duration-micro)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] disabled:cursor-not-allowed disabled:text-[var(--color-disabled-foreground)] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[var(--color-focus)]',
    },
    /* ── 导航 ── */
    {
      'nav-group-label': 'px-3 pb-1.5 pt-5 type-label first:pt-1',
      'nav-link': 'flex h-9 items-center gap-2.5 rounded-[var(--radius-control)] px-3 text-sm text-[var(--color-text-muted)] transition-[background-color,color] duration-[var(--duration-micro)] hover:bg-[var(--color-hover)] hover:text-[var(--color-text)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2 pointer-coarse:h-11',
      'nav-link-active': 'nav-link bg-[var(--color-active)] font-medium text-[var(--color-text)]',
    },
    /* ── 分段切换（时间范围等互斥单选）：共享基线 + 选中/未选中两态。
       触屏下高度提升到 44px，与 nav-link 同一触控组合。 ── */
    {
      'segment-group': 'inline-flex items-center gap-0.5 rounded-[var(--radius-panel)] border border-[var(--color-border)] bg-[var(--color-sunken)] p-1',
      'segment-item': 'h-8 rounded-[var(--radius-control)] px-3 text-[13px] font-medium transition-[background-color,color] duration-[var(--duration-micro)] pointer-coarse:h-11',
      'segment-item-active': 'bg-[var(--color-elevated)] text-[var(--color-text)]',
      'segment-item-idle': 'text-[var(--color-text-muted)] hover:text-[var(--color-text)] focus-visible:outline-2 focus-visible:outline-[var(--color-focus)] focus-visible:outline-offset-2',
    },
  ],
  rules: [
    /* ── 语义字体阶梯（负字距红线：设计约束硬规则 #6，标题收紧最多到 0）。
       Warm Restraint v4：display 收紧 -0.02em 专供 KPI 主数字，title 微松 +0.01em；
       type-label 维持既有 uppercase + 0.1em（眉题效果已达标，不再收紧）。

       这四条必须是 rule 而不是 shortcut：--text-* 是 font **简写**值
       （weight size/line-height family），而 UnoCSS 的 `font-[...]` 编译成
       font-family。把简写赋给 font-family 是无效声明，会被整条丢弃，标题
       于是一路回落到浏览器默认（h1 30px/700、h2 22.5px/700，眉题 15px），
       四层阶梯从未真正生效。这里显式输出 font 简写。 ── */
    ['type-display', { font: 'var(--text-display)', 'letter-spacing': 'var(--tracking-display)', color: 'var(--color-text)' }],
    ['type-title', { font: 'var(--text-title)', 'letter-spacing': 'var(--tracking-title)', color: 'var(--color-text)' }],
    ['type-heading', { font: 'var(--text-heading)', color: 'var(--color-text)' }],
    ['type-label', {
      font: 'var(--text-label)',
      'letter-spacing': '0.1em',
      'text-transform': 'uppercase',
      color: 'var(--color-text-subtle)',
    }],
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
